package ui

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/discovery"
)

const (
	dynamicFileSetInferenceThreshold     = 2
	largeFileSetSubtreeFastPathThreshold = 128
)

type interactiveFileSetSelectedValue struct {
	raw         string
	normalized  string
	matcher     discovery.StageValueMatcher
	isExactFile bool
}

type dynamicFileSetPatternCandidate struct {
	pattern         string
	selectedMatches int
	literalChars    int
	wildcardCount   int
	covered         []string
}

// normalizeInteractiveFileSetStageValues removes redundant exact file values
// from an interactive file-set stage when another selected value already
// covers that same file under the current scope's path-pattern semantics.
//
// This intentionally stays stage-local. It does not rewrite across repeated
// stages or across --then.
func normalizeInteractiveFileSetStageValues(currentArgs []string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if symbolic, ok, err := normalizeSymbolicInteractiveFileSetValues(values); err != nil {
		return nil, err
	} else if ok {
		return symbolic, nil
	}

	relPaths, err := startupScopeFileSetPaths(currentArgs)
	if err != nil {
		return nil, err
	}
	if len(relPaths) == 0 {
		return dedupeInteractiveFileSetValues(values), nil
	}
	return normalizeInteractiveFileSetStageValuesForPaths(relPaths, values)
}

// normalizeSymbolicInteractiveFileSetValues handles the common picker result
// where every selected row is already a glob such as "*.c". Exact-file and
// subtree inference cannot shorten an all-symbolic selection, so loading and
// indexing the complete current scope would only reproduce these values.
//
// Unix permits wildcard characters in literal filenames. A cheap lstat keeps
// such a selected file on the canonical normalization path instead of
// reinterpreting it as a pattern.
func normalizeSymbolicInteractiveFileSetValues(values []string) ([]string, bool, error) {
	deduped := dedupeInteractiveFileSetValues(values)
	for _, value := range deduped {
		normalized := strings.ReplaceAll(value, "\\", "/")
		if !strings.ContainsAny(normalized, "*?[") {
			return nil, false, nil
		}
		if _, err := discovery.ClassifyStageValue(value); err != nil {
			return nil, false, err
		}
		// Windows forbids wildcard characters in filenames, and Lstat reports
		// ERROR_INVALID_NAME rather than a not-exist error for these values.
		// The literal-wildcard safeguard is therefore both unnecessary and
		// misleading there: every successfully classified wildcard row is
		// symbolic by construction.
		if runtime.GOOS == "windows" {
			continue
		}
		if _, err := os.Lstat(filepath.FromSlash(normalized)); err == nil {
			return nil, false, nil
		} else if !os.IsNotExist(err) {
			// Permission and transient filesystem errors are not proof that the
			// value is symbolic. Let canonical scope normalization decide.
			return nil, false, nil
		}
	}
	return deduped, true, nil
}

func normalizeInteractiveFileSetStageValuesForPaths(relPaths, values []string) ([]string, error) {
	exactFiles := make(map[string]struct{}, len(relPaths))
	for _, relPath := range relPaths {
		normalized := normalizeRelPath(relPath)
		if normalized == "" {
			continue
		}
		exactFiles[normalized] = struct{}{}
	}

	selected := make([]interactiveFileSetSelectedValue, 0, len(values))
	seenRaw := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seenRaw[value]; ok {
			continue
		}
		seenRaw[value] = struct{}{}

		matcher, err := discovery.ClassifyStageValue(value)
		if err != nil {
			return nil, err
		}
		normalized := normalizeRelPath(value)
		_, isExactFile := exactFiles[normalized]
		selected = append(selected, interactiveFileSetSelectedValue{
			raw:         value,
			normalized:  normalized,
			matcher:     matcher,
			isExactFile: isExactFile,
		})
	}

	nonExactSelections := make([]interactiveFileSetSelectedValue, 0, len(selected))
	selectedExactSet := make(map[string]struct{}, len(selected))
	for _, value := range selected {
		if value.isExactFile {
			selectedExactSet[value.normalized] = struct{}{}
			continue
		}
		nonExactSelections = append(nonExactSelections, value)
	}

	nonExact := make([]string, 0, len(nonExactSelections))
	exact := make([]string, 0, len(selectedExactSet))
	for _, value := range selected {
		if value.isExactFile {
			if matchesAnyInteractiveFileSetSelection(value.normalized, nonExactSelections) {
				continue
			}
			exact = append(exact, value.raw)
			continue
		}
		nonExact = append(nonExact, value.raw)
	}

	subtrees, subtreeRemaining, err := inferCompleteSelectedSubtrees(exact, relPaths)
	if err != nil {
		return nil, err
	}

	var inferred, remainingExact []string
	if len(exact) >= largeFileSetSubtreeFastPathThreshold && len(subtrees)+len(subtreeRemaining) == 1 {
		// One complete directory selector is already the shortest possible
		// non-empty representation. Avoid basename inference entirely: on a
		// dependency tree this is the confirmation-time fast path that prevents
		// thousands of exact selections from delaying the output picker.
		inferred, remainingExact = subtrees, subtreeRemaining
	} else {
		selectedRelPaths := selectedStageRelPathSet(selectedExactSet, nonExactSelections, relPaths)
		inferred, remainingExact, err = inferDynamicFileSetPatterns(exact, relPaths, selectedRelPaths)
		if err != nil {
			return nil, err
		}
		if len(subtrees)+len(subtreeRemaining) < len(inferred)+len(remainingExact) {
			inferred = subtrees
			remainingExact = subtreeRemaining
		}
	}
	nonExact, err = removeNonExactValuesCoveredByInferredPatterns(nonExact, relPaths, inferred)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(nonExact)+len(inferred)+len(remainingExact))
	out = append(out, nonExact...)
	out = append(out, inferred...)
	out = append(out, remainingExact...)
	return out, nil
}

// inferCompleteSelectedSubtrees replaces exact files with directory selectors
// only when every scope file below that directory was selected. It is the
// directory-shaped counterpart to basename pattern inference and prevents a
// large heterogeneous tree (node_modules is the canonical case) from becoming
// thousands of argv values.
func inferCompleteSelectedSubtrees(selectedExact, scopeFiles []string) ([]string, []string, error) {
	if len(selectedExact) < dynamicFileSetInferenceThreshold || len(scopeFiles) == 0 {
		return nil, append([]string(nil), selectedExact...), nil
	}

	scopeCounts := make(map[string]int)
	selectedCounts := make(map[string]int)
	countAncestors := func(counts map[string]int, relPath string) {
		for dir := path.Dir(normalizeRelPath(relPath)); dir != "." && dir != "" && dir != "/"; dir = path.Dir(dir) {
			counts[dir]++
		}
	}
	for _, relPath := range scopeFiles {
		countAncestors(scopeCounts, relPath)
	}
	for _, relPath := range selectedExact {
		countAncestors(selectedCounts, relPath)
	}

	candidates := make([]string, 0, len(selectedCounts))
	for dir, selectedCount := range selectedCounts {
		if selectedCount < dynamicFileSetInferenceThreshold || selectedCount != scopeCounts[dir] {
			continue
		}
		if strings.ContainsAny(dir, "*?[") {
			continue
		}
		candidates = append(candidates, dir)
	}
	sort.Slice(candidates, func(i, j int) bool {
		leftDepth := strings.Count(candidates[i], "/")
		rightDepth := strings.Count(candidates[j], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return candidates[i] < candidates[j]
	})

	selectedDirs := make(map[string]struct{}, len(candidates))
	selectors := make([]string, 0, len(candidates))
	for _, dir := range candidates {
		if directoryHasSelectedAncestor(dir, selectedDirs) {
			continue
		}

		selector := dir + "/"
		if !strings.Contains(dir, "/") {
			// A one-segment trailing-slash selector floats to same-named nested
			// directories. Use the anchored glob form for an exact root subtree;
			// Catclip filter '*' crosses folders, so this still covers descendants.
			selector = dir + "/*"
		}
		if _, err := discovery.ClassifyStageValue(selector); err != nil {
			return nil, nil, err
		}
		selectedDirs[dir] = struct{}{}
		selectors = append(selectors, selector)
	}

	remaining := make([]string, 0, len(selectedExact))
	seen := make(map[string]struct{}, len(selectedExact))
	for _, relPath := range selectedExact {
		normalized := normalizeRelPath(relPath)
		if pathHasSelectedAncestor(normalized, selectedDirs) {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		remaining = append(remaining, relPath)
	}
	return selectors, remaining, nil
}

func directoryHasSelectedAncestor(dir string, selectedDirs map[string]struct{}) bool {
	for ancestor := path.Dir(dir); ancestor != "." && ancestor != "" && ancestor != "/"; ancestor = path.Dir(ancestor) {
		if _, ok := selectedDirs[ancestor]; ok {
			return true
		}
	}
	return false
}

func pathHasSelectedAncestor(relPath string, selectedDirs map[string]struct{}) bool {
	for dir := path.Dir(relPath); dir != "." && dir != "" && dir != "/"; dir = path.Dir(dir) {
		if _, ok := selectedDirs[dir]; ok {
			return true
		}
	}
	return false
}

func matchesAnyInteractiveFileSetSelection(relPath string, values []interactiveFileSetSelectedValue) bool {
	for _, value := range values {
		if discovery.MatchesStageValue(relPath, value.matcher) {
			return true
		}
	}
	return false
}

func selectedStageRelPathSet(selectedExact map[string]struct{}, nonExact []interactiveFileSetSelectedValue, relPaths []string) map[string]struct{} {
	if (len(selectedExact) == 0 && len(nonExact) == 0) || len(relPaths) == 0 {
		return nil
	}
	out := make(map[string]struct{})
	for _, relPath := range relPaths {
		normalized := normalizeRelPath(relPath)
		if normalized == "" {
			continue
		}
		if _, ok := selectedExact[normalized]; ok {
			out[normalized] = struct{}{}
			continue
		}
		if matchesAnyInteractiveFileSetSelection(normalized, nonExact) {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func dedupeInteractiveFileSetValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func inferDynamicFileSetPatterns(selectedExact, scopeFiles []string, selectedRelPaths map[string]struct{}) ([]string, []string, error) {
	if len(selectedExact) < dynamicFileSetInferenceThreshold || len(scopeFiles) == 0 {
		return nil, append([]string(nil), selectedExact...), nil
	}

	selectedSet := make(map[string]struct{}, len(selectedExact))
	for _, relPath := range selectedExact {
		normalized := normalizeRelPath(relPath)
		if normalized != "" {
			selectedSet[normalized] = struct{}{}
		}
	}
	if len(selectedSet) < dynamicFileSetInferenceThreshold {
		return nil, append([]string(nil), selectedExact...), nil
	}
	if len(selectedRelPaths) == 0 {
		selectedRelPaths = selectedSet
	}

	candidateSet := make(map[string]struct{})
	for _, relPath := range selectedExact {
		for _, pattern := range dynamicPatternCandidatesForBasename(path.Base(normalizeRelPath(relPath))) {
			candidateSet[pattern] = struct{}{}
		}
	}
	if len(candidateSet) == 0 {
		return nil, append([]string(nil), selectedExact...), nil
	}

	// Split candidates into prefix ("P*") and suffix ("*S") forms. Every
	// candidate has exactly one "*" at one end (appendDynamicPatternCandidate
	// guarantees this), so matching is HasPrefix/HasSuffix, not regex. We probe
	// each file's own substrings against these sets, so the per-file cost is
	// O(basename length) regardless of how many candidates exist — that is what
	// lets us drop the old candidate-count cap and still collapse very large
	// selections at 50k+ scope into globs (see
	// docs/versions/v0.5.5/reports/ACTIVE_PLAN_dynamic_pattern_inference_testing.md).
	prefixCandidates := make(map[string]struct{}, len(candidateSet))
	suffixCandidates := make(map[string]struct{}, len(candidateSet))
	maxPrefixLen := 0
	for pattern := range candidateSet {
		switch {
		case strings.HasSuffix(pattern, "*"):
			p := pattern[:len(pattern)-1]
			prefixCandidates[p] = struct{}{}
			if len(p) > maxPrefixLen {
				maxPrefixLen = len(p)
			}
		case strings.HasPrefix(pattern, "*"):
			suffixCandidates[pattern[1:]] = struct{}{}
		}
	}

	// patternsForFile reports the candidate patterns a file matches, reproducing
	// the compiled-glob semantics exactly (`*` -> `.*`, anchored, matched against
	// basename OR relpath): a prefix candidate "P*" matches if P prefixes the
	// basename or the relpath; a suffix candidate "*S" matches if S suffixes the
	// basename (equivalent to the relpath suffix since S has no slash). Buffers
	// are reused across calls; the caller consumes the result before the next
	// call.
	seen := make(map[string]struct{}, 16)
	matchBuf := make([]string, 0, 16)
	patternsForFile := func(rel string) []string {
		clear(seen)
		matchBuf = matchBuf[:0]
		base := path.Base(rel)
		add := func(pattern string) {
			if _, ok := seen[pattern]; ok {
				return
			}
			seen[pattern] = struct{}{}
			matchBuf = append(matchBuf, pattern)
		}
		for i := 1; i <= len(base); i++ {
			if _, ok := prefixCandidates[base[:i]]; ok {
				add(base[:i] + "*")
			}
		}
		for i := 1; i <= len(rel) && i <= maxPrefixLen; i++ {
			if _, ok := prefixCandidates[rel[:i]]; ok {
				add(rel[:i] + "*")
			}
		}
		for i := 0; i < len(base); i++ {
			if _, ok := suffixCandidates[base[i:]]; ok {
				add("*" + base[i:])
			}
		}
		return matchBuf
	}

	// One pass over the scope tallies, per candidate, how many scope files match
	// and how many of those are selected (mirrors the old per-candidate loop:
	// selectedMatches counts scope files that are selected, so covered ⊆ scope).
	type matchCounts struct {
		scope    int
		selected int
		covered  []string
	}
	stats := make(map[string]*matchCounts, len(candidateSet))
	for _, relPath := range scopeFiles {
		normalized := normalizeRelPath(relPath)
		if normalized == "" {
			continue
		}
		_, isSelected := selectedRelPaths[normalized]
		for _, pattern := range patternsForFile(normalized) {
			c := stats[pattern]
			if c == nil {
				c = &matchCounts{}
				stats[pattern] = c
			}
			c.scope++
			if isSelected {
				c.selected++
				c.covered = append(c.covered, normalized)
			}
		}
	}

	// A candidate is valid when it matches no unselected file (scope == selected)
	// and characterizes at least the threshold of the selection.
	valid := make([]dynamicFileSetPatternCandidate, 0, len(stats))
	for pattern, c := range stats {
		if c.scope != c.selected || c.selected < dynamicFileSetInferenceThreshold {
			continue
		}
		valid = append(valid, dynamicFileSetPatternCandidate{
			pattern:         pattern,
			selectedMatches: c.selected,
			literalChars:    dynamicPatternLiteralCharCount(pattern),
			wildcardCount:   dynamicPatternWildcardCount(pattern),
			covered:         c.covered,
		})
	}
	if len(valid) == 0 {
		return nil, append([]string(nil), selectedExact...), nil
	}

	coveredByInferred := make(map[string]struct{}, len(selectedRelPaths))
	var inferred []string

	// If a single clean pattern covers the entire selection, emit the broadest
	// such pattern (fewest literal characters). This is the chosen tie rule:
	// selecting every Go file yields `*.go`, not a longer prefix.
	totalSelected := len(selectedRelPaths)
	best := -1
	for i := range valid {
		if valid[i].selectedMatches != totalSelected {
			continue
		}
		if best == -1 || dynamicFileSetPatternCandidateBroader(valid[i], valid[best]) {
			best = i
		}
	}
	if best >= 0 {
		inferred = []string{valid[best].pattern}
		for _, relPath := range valid[best].covered {
			coveredByInferred[relPath] = struct{}{}
		}
	} else {
		// No single full cover: keep the most-specific-first greedy assembly so
		// multi-family selections stay precise.
		sort.Slice(valid, func(i, j int) bool {
			return dynamicFileSetPatternCandidateLess(valid[i], valid[j])
		})
		uncovered := make(map[string]struct{}, len(selectedRelPaths))
		for relPath := range selectedRelPaths {
			uncovered[relPath] = struct{}{}
		}
		for _, candidate := range valid {
			coversUncovered := false
			for _, relPath := range candidate.covered {
				if _, ok := uncovered[relPath]; ok {
					coversUncovered = true
					break
				}
			}
			if !coversUncovered {
				continue
			}
			inferred = append(inferred, candidate.pattern)
			for _, relPath := range candidate.covered {
				delete(uncovered, relPath)
				coveredByInferred[relPath] = struct{}{}
			}
		}
	}
	if len(inferred) == 0 {
		return nil, append([]string(nil), selectedExact...), nil
	}

	remaining := make([]string, 0, len(selectedExact))
	seenRemaining := make(map[string]struct{}, len(selectedExact))
	for _, relPath := range selectedExact {
		normalized := normalizeRelPath(relPath)
		if _, ok := coveredByInferred[normalized]; ok {
			continue
		}
		if _, ok := seenRemaining[normalized]; ok {
			continue
		}
		seenRemaining[normalized] = struct{}{}
		remaining = append(remaining, relPath)
	}
	return inferred, remaining, nil
}

func removeNonExactValuesCoveredByInferredPatterns(nonExact, scopeFiles, inferred []string) ([]string, error) {
	if len(nonExact) == 0 || len(inferred) == 0 {
		return nonExact, nil
	}
	inferredMatchers := make([]discovery.StageValueMatcher, 0, len(inferred))
	for _, pattern := range inferred {
		matcher, err := discovery.ClassifyStageValue(pattern)
		if err != nil {
			return nil, err
		}
		inferredMatchers = append(inferredMatchers, matcher)
	}

	out := make([]string, 0, len(nonExact))
	for _, value := range nonExact {
		matcher, err := discovery.ClassifyStageValue(value)
		if err != nil {
			return nil, err
		}
		matchedAny := false
		allCovered := true
		for _, relPath := range scopeFiles {
			normalized := normalizeRelPath(relPath)
			if normalized == "" || !discovery.MatchesStageValue(normalized, matcher) {
				continue
			}
			matchedAny = true
			if !discovery.MatchesStageValues(normalized, inferredMatchers) {
				allCovered = false
				break
			}
		}
		if matchedAny && allCovered {
			continue
		}
		out = append(out, value)
	}
	return out, nil
}

func dynamicFileSetPatternCandidateLess(a, b dynamicFileSetPatternCandidate) bool {
	if a.selectedMatches != b.selectedMatches {
		return a.selectedMatches > b.selectedMatches
	}
	if a.literalChars != b.literalChars {
		return a.literalChars > b.literalChars
	}
	if a.wildcardCount != b.wildcardCount {
		return a.wildcardCount < b.wildcardCount
	}
	return a.pattern < b.pattern
}

// dynamicFileSetPatternCandidateBroader reports whether a is the broader of two
// full-cover candidates: fewest literal characters wins (e.g. `*.go` over
// `auth_*`, and `auth*` over `auth_*`), then fewest wildcards, then lexical.
func dynamicFileSetPatternCandidateBroader(a, b dynamicFileSetPatternCandidate) bool {
	if a.literalChars != b.literalChars {
		return a.literalChars < b.literalChars
	}
	if a.wildcardCount != b.wildcardCount {
		return a.wildcardCount < b.wildcardCount
	}
	return a.pattern < b.pattern
}

func dynamicPatternCandidatesForBasename(basename string) []string {
	if basename == "" || basename == "." || basename == "/" {
		return nil
	}
	out := make([]string, 0, 8)
	seen := make(map[string]struct{})
	for i := 0; i < len(basename); i++ {
		if !dynamicPatternBoundaryAt(basename, i) {
			continue
		}
		suffix := basename[i:]
		out = appendDynamicPatternCandidate(out, seen, "*"+suffix)
	}
	for i := 1; i < len(basename); i++ {
		if !dynamicPatternBoundaryAt(basename, i) {
			continue
		}
		prefix := basename[:i]
		out = appendDynamicPatternCandidate(out, seen, prefix+"*")
		if dynamicPatternDelimiter(basename[i]) {
			out = appendDynamicPatternCandidate(out, seen, basename[:i+1]+"*")
		}
	}
	return out
}

func appendDynamicPatternCandidate(out []string, seen map[string]struct{}, pattern string) []string {
	if pattern == "" || pattern == "*" || strings.Count(pattern, "*") != 1 {
		return out
	}
	if strings.ContainsAny(strings.ReplaceAll(pattern, "*", ""), "?[") {
		return out
	}
	if _, ok := seen[pattern]; ok {
		return out
	}
	seen[pattern] = struct{}{}
	return append(out, pattern)
}

func dynamicPatternBoundaryAt(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	ch := s[i]
	if dynamicPatternDelimiter(ch) {
		return true
	}
	if i == 0 {
		return false
	}
	prev := s[i-1]
	next := byte(0)
	if i+1 < len(s) {
		next = s[i+1]
	}
	if isASCIIUpper(ch) {
		return isASCIILower(prev) || isASCIIDigit(prev) || (isASCIIUpper(prev) && isASCIILower(next))
	}
	if isASCIIDigit(ch) {
		return isASCIILower(prev)
	}
	if isASCIILetter(ch) {
		return isASCIIDigit(prev)
	}
	return false
}

func dynamicPatternDelimiter(ch byte) bool {
	return ch == '.' || ch == '_' || ch == '-'
}

func dynamicPatternLiteralCharCount(pattern string) int {
	return len(strings.ReplaceAll(pattern, "*", ""))
}

func dynamicPatternWildcardCount(pattern string) int {
	count := 0
	for _, r := range pattern {
		switch r {
		case '*', '?', '[':
			count++
		}
	}
	return count
}

func isASCIILetter(ch byte) bool {
	return isASCIILower(ch) || isASCIIUpper(ch)
}

func isASCIILower(ch byte) bool {
	return ch >= 'a' && ch <= 'z'
}

func isASCIIUpper(ch byte) bool {
	return ch >= 'A' && ch <= 'Z'
}

func isASCIIDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}
