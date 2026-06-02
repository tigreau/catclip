package catclip

import (
	"io"
	"os"
	"path"
	"path/filepath"
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

// stageContext bundles the inputs every per-stage applier needs. The
// pipeline owns it; appliers consume it read-only. Carrying the scope
// and the specific stage (not just stage values) lets appliers see
// scope-wide info — e.g., the include applier inspects s.Targets — and
// keeps each applier's signature uniform so the dispatch table stays
// flat.
type stageContext struct {
	Resolver *scopeResolver
	GitCtx   gitContext
	Scope    executionScope
	Stage    scopeStage
}

// stageApplier is the per-stage transformer. Each kind of scope stage
// (include, only, exclude, recent, depth, contains, snippet, the git
// selection family, diff) gets one applier registered in
// stageApplierTable. The pipeline calls them in scope.Stages order;
// returning an empty entry slice short-circuits the rest of the chain.
//
// All appliers must be pure functions of (ctx, entries) — no hidden
// state, no globals, no side effects beyond what's visible in the
// returned slice. This preserves the args-as-state property that future
// undo support depends on.
type stageApplier func(ctx stageContext, entries []fileEntry) ([]fileEntry, error)

// stageApplierTable maps each scopeStageKind to its applier. The table
// is the only place that knows the kind→applier correspondence;
// applyScopeStages just looks up and dispatches. To add a new stage:
// add the kind to scope_stage.go, add an applier function below, and
// register it here. Tests in scope_stages_test.go enforce coverage.
var stageApplierTable = map[scopeStageKind]stageApplier{
	scopeStageInclude:      applyIncludeStageCase,
	scopeStageOnly:         applyOnlyStageCase,
	scopeStageExclude:      applyExcludeStageCase,
	scopeStageRecent:       applyRecentStageCase,
	scopeStageDepth:        applyDepthStageCase,
	scopeStageContains:     applyContentStageCase,
	scopeStageSnippet:      applyContentStageCase,
	scopeStageChanged:      applyChangedStageCase,
	scopeStageStaged:       applyStagedStageCase,
	scopeStageUnstaged:     applyUnstagedStageCase,
	scopeStageUntracked:    applyUntrackedStageCase,
	scopeStageChangedDiff:  applyChangedStageCase,
	scopeStageStagedDiff:   applyStagedStageCase,
	scopeStageUnstagedDiff: applyUnstagedStageCase,
	scopeStageDiff:         applyDiffStageCase,
	scopeStagePaths:        applyPathsStageCase,
	scopeStageLines:        applyLinesStageCase,
}

func applyScopeStages(resolver *scopeResolver, gitCtx gitContext, s executionScope, entries []fileEntry) ([]fileEntry, error) {
	if len(entries) == 0 {
		return entries, nil
	}
	for _, stage := range s.Stages {
		applier, ok := stageApplierTable[stage.Kind]
		if !ok {
			// Unknown kind = no-op. Preserves the original switch's
			// default behavior so additions to the kind enum can land
			// without breaking older runs.
			continue
		}
		ctx := stageContext{Resolver: resolver, GitCtx: gitCtx, Scope: s, Stage: stage}
		next, err := applier(ctx, entries)
		if err != nil {
			return nil, err
		}
		entries = next
		if len(entries) == 0 {
			return entries, nil
		}
	}
	return entries, nil
}

func applyIncludeStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	return applyIncludeStage(ctx.Resolver, ctx.Scope, entries, ctx.Stage.Values, ctx.Stage.ExactValues)
}

func applyOnlyStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	if ctx.Stage.ExactValues {
		return filterEntriesByExactStagePaths(entries, ctx.Stage.Values, true), nil
	}
	return filterEntriesByStagePatterns(entries, ctx.Stage.Values, true)
}

func applyExcludeStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	if ctx.Stage.ExactValues {
		return filterEntriesByExactStagePaths(entries, ctx.Stage.Values, false), nil
	}
	return filterEntriesByStagePatterns(entries, ctx.Stage.Values, false)
}

func applyRecentStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	return applyRecentStage(entries, ctx.Resolver.cfg.WorkingDir, ctx.Stage.Limit)
}

func applyDepthStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	if ctx.Stage.Limit == nil {
		return entries, nil
	}
	return applyDepthStage(entries, *ctx.Stage.Limit)
}

// applyContentStageCase serves both --contains and --snippet. The two
// stages share matching semantics (rg-driven regex over file content);
// the difference between them is downstream — only the emit shape
// changes. Keeping a single applier here removes the bug surface of
// "added logic to --contains but forgot to mirror it on --snippet."
func applyContentStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	if len(ctx.Stage.Values) == 0 {
		return entries, nil
	}
	entries = ensureEntryAbsPaths(entries, ctx.Resolver.cfg.WorkingDir)
	return filterEntriesByContent(entries, ctx.Stage.Values[0])
}

// applyChangedStageCase serves scopeStageChanged and scopeStageChangedDiff;
// the *Diff variant only changes the emit shape downstream, not the
// selected file set.
func applyChangedStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	if !ctx.GitCtx.Enabled {
		return entries, nil
	}
	return filterChangedEntries(ctx.GitCtx, executionScope{Changed: true}, entries)
}

func applyStagedStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	if !ctx.GitCtx.Enabled {
		return entries, nil
	}
	return filterChangedEntries(ctx.GitCtx, executionScope{Changed: true, Staged: true}, entries)
}

func applyUnstagedStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	if !ctx.GitCtx.Enabled {
		return entries, nil
	}
	return filterChangedEntries(ctx.GitCtx, executionScope{Changed: true, Unstaged: true}, entries)
}

func applyUntrackedStageCase(ctx stageContext, entries []fileEntry) ([]fileEntry, error) {
	if !ctx.GitCtx.Enabled {
		return entries, nil
	}
	return filterChangedEntries(ctx.GitCtx, executionScope{Changed: true, Untracked: true}, entries)
}

// applyDiffStageCase, applyPathsStageCase, applyLinesStageCase are no-ops:
// these are output-shape modifiers, not file-set filters. They appear in
// scope.Stages for completeness so canonicalScopeArgs round-trips, but
// the file set they "apply to" is whatever the upstream stages produced.
func applyDiffStageCase(_ stageContext, entries []fileEntry) ([]fileEntry, error) {
	return entries, nil
}

func applyPathsStageCase(_ stageContext, entries []fileEntry) ([]fileEntry, error) {
	return entries, nil
}

func applyLinesStageCase(_ stageContext, entries []fileEntry) ([]fileEntry, error) {
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
		scopedTargets := scopeIncludeTarget(resolver.cfg.WorkingDir, s.Targets, target)
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
//
// For the bare-include-target branch (no slash), the result is `target/include`
// only when `target` is actually a directory on disk. If the scope target is a
// file (e.g. `agent.md`), combining yields a nonsense path; in that case the
// include's authorization still applies globally (via buildIncludedTargetSet)
// and the basename-include lookup picks it up — see
// ACTIVE_BUG_basename_target_ignores_include_subtree.md.
func scopeIncludeTarget(workingDir string, scopeTargets []string, includeTarget string) []string {
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

	// Bare include target (no "/"): combine `target/include` only when `target`
	// stats as a directory. For file-shaped scope targets the combined path
	// would be invalid; the include's global authorization still applies via
	// buildIncludedTargetSet and the basename-include probe finds the file
	// inside the authorized subtree.
	out := make([]string, 0, len(scopeTargets))
	for _, target := range scopeTargets {
		target = normalizeRelPath(target)
		if target == "" {
			continue
		}
		if !scopeTargetIsDir(workingDir, target) {
			continue
		}
		out = append(out, target+"/"+includeTarget)
	}
	return out
}

func scopeTargetIsDir(workingDir, relTarget string) bool {
	info, err := os.Stat(filepath.Join(workingDir, filepath.FromSlash(relTarget)))
	return err == nil && info.IsDir()
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
