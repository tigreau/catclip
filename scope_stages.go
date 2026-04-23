package catclip

import (
	"io"
	"path"
	"strings"
)

type stageValueMatchKind string

const (
	stageValueMatchGlob     stageValueMatchKind = "glob"
	stageValueMatchSubtree  stageValueMatchKind = "subtree"
	stageValueMatchAnchored stageValueMatchKind = "anchored"
	stageValueMatchBare     stageValueMatchKind = "bare"
)

type stageValueMatcher struct {
	kind    stageValueMatchKind
	value   string
	glob    compiledGlob
	dirOnly bool
	hasGlob bool
}

func applyScopeStages(resolver *scopeResolver, gitCtx gitContext, s executionScope, entries []fileEntry) ([]fileEntry, error) {
	if len(entries) == 0 {
		return entries, nil
	}
	for _, stage := range s.Stages {
		var err error
		switch stage.Kind {
		case scopeStageInclude:
			entries, err = applyIncludeStage(resolver, s, entries, stage.Values, stage.ExactValues)
		case scopeStageOnly:
			if stage.ExactValues {
				entries = filterEntriesByExactStagePaths(entries, stage.Values, true)
				continue
			}
			entries, err = filterEntriesByStagePatterns(entries, stage.Values, true)
		case scopeStageExclude:
			if stage.ExactValues {
				entries = filterEntriesByExactStagePaths(entries, stage.Values, false)
				continue
			}
			entries, err = filterEntriesByStagePatterns(entries, stage.Values, false)
		case scopeStageRecent:
			entries, err = applyRecentStage(entries, resolver.cfg.WorkingDir, stage.Limit)
		case scopeStageDepth:
			if stage.Limit == nil {
				continue
			}
			entries, err = applyDepthStage(entries, *stage.Limit)
		case scopeStageContains:
			if len(stage.Values) == 0 {
				continue
			}
			entries = ensureEntryAbsPaths(entries, resolver.cfg.WorkingDir)
			entries, err = filterEntriesByContent(entries, stage.Values[0])
		case scopeStageSnippet:
			if len(stage.Values) == 0 {
				continue
			}
			entries = ensureEntryAbsPaths(entries, resolver.cfg.WorkingDir)
			entries, err = filterEntriesByContent(entries, stage.Values[0])
		case scopeStageChanged:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, executionScope{Changed: true}, entries)
		case scopeStageStaged:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, executionScope{Changed: true, Staged: true}, entries)
		case scopeStageUnstaged:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, executionScope{Changed: true, Unstaged: true}, entries)
		case scopeStageUntracked:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, executionScope{Changed: true, Untracked: true}, entries)
		case scopeStageChangedDiff:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, executionScope{Changed: true}, entries)
		case scopeStageStagedDiff:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, executionScope{Changed: true, Staged: true}, entries)
		case scopeStageUnstagedDiff:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, executionScope{Changed: true, Unstaged: true}, entries)
		case scopeStageDiff:
			// Output-shape modifiers do not change the selected file set.
		}
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return entries, nil
		}
	}
	return entries, nil
}

func applyIncludeStage(resolver *scopeResolver, s executionScope, entries []fileEntry, targets []string, exactValues bool) ([]fileEntry, error) {
	if len(targets) == 0 {
		return entries, nil
	}

	out := append([]fileEntry(nil), entries...)
	for _, target := range targets {
		if target == "*" || includeTargetActsAsAuthorizationOnly(s, target) {
			continue
		}
		scopedTargets := scopeIncludeTarget(s.Targets, target)
		for _, scopedTarget := range scopedTargets {
			if exactValues {
				included, err := resolveExactIncludeStageTarget(resolver, scopedTarget)
				if err != nil {
					return nil, err
				}
				out = append(out, included...)
				continue
			}
			included, _, _, _, err := resolver.resolveAndDiscoverTarget(0, scopedTarget, io.Discard, colorPalette{})
			if err != nil {
				return nil, err
			}
			out = append(out, included...)
		}
	}
	return dedupeEntriesByPathPreserveOrder(out), nil
}

// scopeIncludeTarget resolves an include target relative to the scope targets.
// Returns the list of concrete paths to resolve (one per scope target that
// could contain the include target). When any scope target is "." (root), the
// include target is returned as-is since the entire project is in scope.
func scopeIncludeTarget(scopeTargets []string, includeTarget string) []string {
	includeTarget = normalizeRelPath(includeTarget)
	if includeTarget == "" || includeTarget == "." {
		return nil
	}

	// If any scope target is root, the include target is in scope as-is.
	for _, target := range scopeTargets {
		target = normalizeRelPath(target)
		if target == "" || target == "." {
			return []string{includeTarget}
		}
	}

	// Anchored include target (contains "/"): check if it falls under any
	// scope target prefix, or is an ancestor of any scope target (authorizing
	// discovery of the scope target itself).
	if strings.Contains(includeTarget, "/") {
		for _, target := range scopeTargets {
			target = normalizeRelPath(target)
			if target == "" {
				continue
			}
			if includeTarget == target || strings.HasPrefix(includeTarget, target+"/") || strings.HasPrefix(target, includeTarget+"/") {
				return []string{includeTarget}
			}
		}
		return nil
	}

	// Bare include target (no "/"): resolve relative to each scope target.
	out := make([]string, 0, len(scopeTargets))
	for _, target := range scopeTargets {
		target = normalizeRelPath(target)
		if target == "" {
			continue
		}
		out = append(out, target+"/"+includeTarget)
	}
	return out
}

func includeTargetActsAsAuthorizationOnly(s executionScope, includeTarget string) bool {
	return includeTargetActsAsAuthorizationOnlyForTargets(s.Targets, includeTarget)
}

func includeTargetActsAsAuthorizationOnlyForTargets(targets []string, includeTarget string) bool {
	includeTarget = normalizeRelPath(includeTarget)
	if includeTarget == "" || includeTarget == "." {
		return false
	}
	for _, target := range targets {
		target = normalizeRelPath(target)
		if target == "" || target == "." {
			continue
		}
		if target == includeTarget || strings.HasPrefix(target, includeTarget+"/") {
			return true
		}
	}
	return false
}

func resolveExactIncludeStageTarget(resolver *scopeResolver, target string) ([]fileEntry, error) {
	normalized := normalizeRelPath(target)
	if normalized == "" {
		return nil, nil
	}
	included, handled, _, err := resolver.resolveExactTarget(normalized, false, colorPalette{})
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, nil
	}
	return included, nil
}

func filterEntriesByExactStagePaths(entries []fileEntry, paths []string, keepMatches bool) []fileEntry {
	wanted := make(map[string]struct{}, len(paths))
	for _, value := range paths {
		normalized := normalizeRelPath(value)
		if normalized == "" {
			continue
		}
		wanted[normalized] = struct{}{}
	}

	out := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		_, matched := wanted[normalizeRelPath(entry.RelPath)]
		if keepMatches == matched {
			out = append(out, entry)
		}
	}
	return out
}

func filterEntriesByStagePatterns(entries []fileEntry, patterns []string, keepMatches bool) ([]fileEntry, error) {
	if len(patterns) == 0 {
		return entries, nil
	}

	matchers := make([]stageValueMatcher, 0, len(patterns))
	for _, pattern := range patterns {
		matcher, err := classifyStageValue(pattern)
		if err != nil {
			return nil, newUsageError("Error: invalid pattern %q.", pattern)
		}
		matchers = append(matchers, matcher)
	}

	out := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		matched := matchesStageValues(entry.RelPath, matchers)
		if keepMatches {
			if matched {
				out = append(out, entry)
			}
			continue
		}
		if !matched {
			out = append(out, entry)
		}
	}
	return out, nil
}

func classifyStageValue(value string) (stageValueMatcher, error) {
	if hasGlobChars(value) {
		normalized := normalizeGlobStageValue(value)
		re, err := compileGlob(normalized)
		if err != nil {
			return stageValueMatcher{}, err
		}
		return stageValueMatcher{
			kind:    stageValueMatchGlob,
			value:   normalized,
			hasGlob: true,
			glob: compiledGlob{
				raw: value,
				re:  re,
			},
		}, nil
	}

	normalized, dirOnly := normalizeStageValue(value)
	switch {
	case dirOnly:
		return stageValueMatcher{kind: stageValueMatchSubtree, value: normalized, dirOnly: true}, nil
	case strings.Contains(normalized, "/"):
		return stageValueMatcher{kind: stageValueMatchAnchored, value: normalized}, nil
	default:
		return stageValueMatcher{kind: stageValueMatchBare, value: normalized}, nil
	}
}

func normalizeStageValue(value string) (string, bool) {
	value = strings.ReplaceAll(value, "\\", "/")
	dirOnly := strings.HasSuffix(value, "/")
	normalized := normalizeRelPath(value)
	if normalized == "" {
		return "", dirOnly
	}
	return normalized, dirOnly
}

func normalizeGlobStageValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	for strings.Contains(value, "//") {
		value = strings.ReplaceAll(value, "//", "/")
	}
	return value
}

func matchesStageValues(relPath string, matchers []stageValueMatcher) bool {
	for _, matcher := range matchers {
		if matchesStageValue(relPath, matcher) {
			return true
		}
	}
	return false
}

func matchesStageValue(relPath string, matcher stageValueMatcher) bool {
	basename := path.Base(relPath)
	switch matcher.kind {
	case stageValueMatchGlob:
		if matcher.glob.re.MatchString(basename) || matcher.glob.re.MatchString(relPath) {
			return true
		}
	case stageValueMatchSubtree:
		return matchesStageSubtree(relPath, matcher.value)
	case stageValueMatchAnchored:
		return relPath == matcher.value || strings.HasPrefix(relPath, matcher.value+"/")
	case stageValueMatchBare:
		if basename == matcher.value {
			return true
		}
		return relPathHasDirSegment(relPath, matcher.value)
	}
	return false
}

func matchesStageSubtree(relPath, subtree string) bool {
	if subtree == "." {
		return relPath != "" && relPath != "."
	}
	prefix := subtree + "/"
	if strings.Contains(subtree, "/") {
		return strings.HasPrefix(relPath, prefix)
	}
	return strings.HasPrefix(relPath, prefix) || strings.Contains(relPath, "/"+prefix)
}

func relPathHasDirSegment(relPath, want string) bool {
	if want == "" || want == "." {
		return false
	}

	dir := path.Dir(relPath)
	if dir == "." || dir == "" {
		return false
	}
	for _, segment := range strings.Split(dir, "/") {
		if segment == want {
			return true
		}
	}
	return false
}
