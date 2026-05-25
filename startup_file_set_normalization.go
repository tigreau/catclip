package catclip

import (
	"path"
	"sort"
	"strings"
)

const dynamicFileSetInferenceThreshold = 2

type interactiveFileSetSelectedValue struct {
	raw         string
	normalized  string
	matcher     stageValueMatcher
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

	relPaths, err := startupScopeFileSetPaths(currentArgs)
	if err != nil {
		return nil, err
	}
	if len(relPaths) == 0 {
		return dedupeInteractiveFileSetValues(values), nil
	}

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

		matcher, err := classifyStageValue(value)
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

	survivors := make([]interactiveFileSetSelectedValue, 0, len(selected))
	for i, value := range selected {
		if value.isExactFile && interactiveExactFileCoveredByOtherSelection(selected, i) {
			continue
		}
		survivors = append(survivors, value)
	}
	selectedRelPaths := selectedStageRelPathSet(selected, relPaths)

	nonExact := make([]string, 0, len(survivors))
	exact := make([]string, 0, len(survivors))
	for _, value := range survivors {
		if value.isExactFile {
			exact = append(exact, value.raw)
			continue
		}
		nonExact = append(nonExact, value.raw)
	}

	inferred, remainingExact, err := inferDynamicFileSetPatterns(exact, relPaths, selectedRelPaths)
	if err != nil {
		return nil, err
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

func interactiveExactFileCoveredByOtherSelection(values []interactiveFileSetSelectedValue, current int) bool {
	target := values[current]
	if !target.isExactFile || target.normalized == "" {
		return false
	}
	for i, other := range values {
		if i == current {
			continue
		}
		if matchesStageValue(target.normalized, other.matcher) {
			return true
		}
	}
	return false
}

func selectedStageRelPathSet(selected []interactiveFileSetSelectedValue, relPaths []string) map[string]struct{} {
	if len(selected) == 0 || len(relPaths) == 0 {
		return nil
	}
	out := make(map[string]struct{})
	for _, relPath := range relPaths {
		normalized := normalizeRelPath(relPath)
		if normalized == "" {
			continue
		}
		for _, value := range selected {
			if matchesStageValue(normalized, value.matcher) {
				out[normalized] = struct{}{}
				break
			}
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
	inferredMatchers := make([]stageValueMatcher, 0, len(inferred))
	for _, pattern := range inferred {
		matcher, err := classifyStageValue(pattern)
		if err != nil {
			return nil, err
		}
		inferredMatchers = append(inferredMatchers, matcher)
	}

	out := make([]string, 0, len(nonExact))
	for _, value := range nonExact {
		matcher, err := classifyStageValue(value)
		if err != nil {
			return nil, err
		}
		matchedAny := false
		allCovered := true
		for _, relPath := range scopeFiles {
			normalized := normalizeRelPath(relPath)
			if normalized == "" || !matchesStageValue(normalized, matcher) {
				continue
			}
			matchedAny = true
			if !matchesStageValues(normalized, inferredMatchers) {
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
