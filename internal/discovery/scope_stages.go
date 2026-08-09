package discovery

import (
	"path"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

type stageValueMatchKind string

const (
	stageValueMatchGlob     stageValueMatchKind = "glob"
	stageValueMatchSubtree  stageValueMatchKind = "subtree"
	stageValueMatchAnchored stageValueMatchKind = "anchored"
	stageValueMatchBare     stageValueMatchKind = "bare"
)

type StageValueMatcher struct {
	kind    stageValueMatchKind
	value   string
	glob    compiledGlob
	dirOnly bool
	hasGlob bool
}

// stageContext bundles the inputs every per-stage applier needs. The
// pipeline owns it; appliers consume it read-only. Carrying the scope
// and the specific stage (not just stage values) lets appliers see
// scope-wide info — e.g., the no-ignore applier inspects s.Targets — and
// keeps each applier's signature uniform so the dispatch table stays
// flat.
type stageContext struct {
	Resolver *Resolver
	GitCtx   git.Context
	Scope    command.ExecutionScope
	Stage    command.Stage
}

// stageApplier is the per-stage transformer. Each kind of scope stage
// (no-ignore, only, exclude, recent, depth, contains, snippet, the git
// selection family, diff) gets one applier registered in
// stageApplierTable. The pipeline calls them in scope.Stages order;
// returning an empty entry slice short-circuits the rest of the chain.
//
// All appliers must be pure functions of (ctx, entries) — no hidden
// state, no globals, no side effects beyond what's visible in the
// returned slice. This preserves the args-as-state property that future
// undo support depends on.
type stageApplier func(ctx stageContext, entries []Entry) ([]Entry, error)

// stageApplierTable maps each command.StageKind to its applier. The table
// is the only place that knows the kind→applier correspondence;
// applyScopeStages just looks up and dispatches. To add a new stage:
// add the kind to scope_stage.go, add an applier function below, and
// register it here. Tests in scope_stages_test.go enforce coverage.
var stageApplierTable = map[command.StageKind]stageApplier{
	command.StageNoIgnore:     applyNoIgnoreStageCase,
	command.StageOnly:         applyOnlyStageCase,
	command.StageExclude:      applyExcludeStageCase,
	command.StageRecent:       ApplyRecentStageCase,
	command.StageSize:         ApplySizeStageCase,
	command.StageDepth:        ApplyDepthStageCase,
	command.StageContains:     applyContentStageCase,
	command.StageNotContains:  applyNotContentStageCase,
	command.StageSnippet:      applyContentStageCase,
	command.StageChanged:      applyChangedStageCase,
	command.StageStaged:       applyStagedStageCase,
	command.StageUnstaged:     applyUnstagedStageCase,
	command.StageUntracked:    applyUntrackedStageCase,
	command.StageChangedDiff:  applyChangedStageCase,
	command.StageStagedDiff:   applyStagedStageCase,
	command.StageUnstagedDiff: applyUnstagedStageCase,
	command.StageDiff:         applyDiffStageCase,
	command.StagePaths:        applyPathsStageCase,
	command.StageLines:        applyLinesStageCase,
}

func applyScopeStages(resolver *Resolver, gitCtx git.Context, s command.ExecutionScope, entries []Entry) ([]Entry, error) {
	entries, _, err := applyScopeStagesWithDiagnostics(resolver, gitCtx, s, entries, -1, platform.Palette{})
	return entries, err
}

func applyScopeStagesWithDiagnostics(resolver *Resolver, gitCtx git.Context, s command.ExecutionScope, entries []Entry, scopeIndex int, colors platform.Palette) ([]Entry, []Diagnostic, error) {
	if len(entries) == 0 {
		return entries, nil, nil
	}
	var diagnostics []Diagnostic
	for _, stage := range s.Stages {
		applier, ok := stageApplierTable[stage.Kind]
		if !ok {
			// Unknown kind = no-op. Preserves the original switch's
			// default behavior so additions to the kind enum can land
			// without breaking older runs.
			continue
		}
		deadValues := deadTrailingSlashGlobStageValues(stage)
		if scopeIndex >= 0 {
			for _, value := range deadValues {
				diagnostics = append(diagnostics, Diagnostic{
					Message:    trailingSlashGlobStageWarning(stage.Kind, value, scopeIndex, colors),
					ScopeIndex: scopeIndex,
				})
			}
		}
		ctx := stageContext{Resolver: resolver, GitCtx: gitCtx, Scope: s, Stage: stage}
		next, err := applier(ctx, entries)
		if err != nil {
			return nil, nil, err
		}
		entries = next
		if len(entries) == 0 {
			if stage.Kind == command.StageOnly && len(deadValues) > 0 && len(deadValues) == len(stage.Values) && len(diagnostics) > 0 {
				diagnostics[len(diagnostics)-len(deadValues)].ExplainsEmptyResult = true
			}
			return entries, diagnostics, nil
		}
	}
	return entries, diagnostics, nil
}

func deadTrailingSlashGlobStageValues(stage command.Stage) []string {
	if stage.ExactValues || (stage.Kind != command.StageOnly && stage.Kind != command.StageExclude) {
		return nil
	}
	var dead []string
	for _, value := range stage.Values {
		normalized := strings.ReplaceAll(value, "\\", "/")
		if hasGlobChars(normalized) && strings.HasSuffix(normalized, "/") {
			dead = append(dead, value)
		}
	}
	return dead
}

func applyNoIgnoreStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	return applyNoIgnoreStage(ctx.Resolver, ctx.Scope, entries)
}

func applyOnlyStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	if ctx.Stage.ExactValues {
		return FilterEntriesByExactStagePaths(entries, ctx.Stage.Values, true), nil
	}
	return filterEntriesByStagePatterns(entries, ctx.Stage.Values, true)
}

func applyExcludeStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	if ctx.Stage.ExactValues {
		return FilterEntriesByExactStagePaths(entries, ctx.Stage.Values, false), nil
	}
	return filterEntriesByStagePatterns(entries, ctx.Stage.Values, false)
}

func ApplyRecentStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	return ApplyRecentStage(entries, ctx.Resolver.Cfg.WorkingDir, ctx.Stage.Limit)
}

func ApplySizeStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	return ApplySizeStage(entries, ctx.Resolver.Cfg.WorkingDir, ctx.Stage.Nums)
}

func ApplyDepthStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	if ctx.Stage.Limit == nil {
		return entries, nil
	}
	return ApplyDepthStage(entries, *ctx.Stage.Limit)
}

// applyContentStageCase serves both --contains and --snippet. The two
// stages share matching semantics (rg-driven regex over file content);
// the difference between them is downstream — only the emit shape
// changes. Keeping a single applier here removes the bug surface of
// "added logic to --contains but forgot to mirror it on --snippet."
func applyContentStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	if len(ctx.Stage.Values) == 0 {
		return entries, nil
	}
	entries = EnsureEntryAbsPaths(entries, ctx.Resolver.Cfg.WorkingDir)
	if ctx.Stage.Kind == command.StageSnippet {
		// One-pass membership + line-number pinning; the output build
		// consumes Entry.SnippetMatchLines instead of re-running rg.
		return FilterEntriesBySnippetContent(entries, ctx.Stage.Values[0])
	}
	return FilterEntriesByContent(entries, ctx.Stage.Values[0])
}

// applyNotContentStageCase is the --not-contains prune step: drops
// entries whose contents match the regex. One Stage per --not-contains
// occurrence; applied in argv order alongside --contains stages.
func applyNotContentStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	if len(ctx.Stage.Values) == 0 {
		return entries, nil
	}
	entries = EnsureEntryAbsPaths(entries, ctx.Resolver.Cfg.WorkingDir)
	return FilterEntriesByNotContent(entries, ctx.Stage.Values[0])
}

// applyChangedStageCase serves command.StageChanged and command.StageChangedDiff;
// the *Diff variant only changes the emit shape downstream, not the
// selected file set.
func applyChangedStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	if !ctx.GitCtx.Enabled {
		return entries, nil
	}
	return FilterChangedEntries(ctx.GitCtx, command.ExecutionScope{Changed: true}, entries)
}

func applyStagedStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	if !ctx.GitCtx.Enabled {
		return entries, nil
	}
	return FilterChangedEntries(ctx.GitCtx, command.ExecutionScope{Changed: true, Staged: true}, entries)
}

func applyUnstagedStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	if !ctx.GitCtx.Enabled {
		return entries, nil
	}
	return FilterChangedEntries(ctx.GitCtx, command.ExecutionScope{Changed: true, Unstaged: true}, entries)
}

func applyUntrackedStageCase(ctx stageContext, entries []Entry) ([]Entry, error) {
	if !ctx.GitCtx.Enabled {
		return entries, nil
	}
	return FilterChangedEntries(ctx.GitCtx, command.ExecutionScope{Changed: true, Untracked: true}, entries)
}

// applyDiffStageCase, applyPathsStageCase, applyLinesStageCase are no-ops:
// these are output-shape modifiers, not file-set filters. They appear in
// scope.Stages for completeness so canonicalScopeArgs round-trips, but
// the file set they "apply to" is whatever the upstream stages produced.
func applyDiffStageCase(_ stageContext, entries []Entry) ([]Entry, error) {
	return entries, nil
}

func applyPathsStageCase(_ stageContext, entries []Entry) ([]Entry, error) {
	return entries, nil
}

func applyLinesStageCase(_ stageContext, entries []Entry) ([]Entry, error) {
	return entries, nil
}

// applyNoIgnoreStage expands the already-resolved scope targets during
// checkpoint replay. Initial discovery sees --no-ignore authorization before it
// resolves targets, but a prediscovered checkpoint contains only the earlier
// visible set and must recover ignored entries when the no-ignore stage is
// replayed. Resolve each target independently so `src --no-ignore` cannot
// widen to ignored files elsewhere in cwd.
func applyNoIgnoreStage(resolver *Resolver, s command.ExecutionScope, entries []Entry) ([]Entry, error) {
	targets := s.Targets
	if len(targets) == 0 {
		targets = []string{"."}
	}
	out := append([]Entry(nil), entries...)
	for _, target := range targets {
		target = normalizeRelPath(target)
		if target == "" {
			target = "."
		}
		if resolver.noIgnoreTargetWalked(target) {
			continue
		}

		var included []Entry
		if hasGlobChars(target) {
			var err error
			included, _, _, _, err = resolver.resolveGlobTarget(-1, target, platform.Palette{})
			if err != nil {
				return nil, err
			}
		} else {
			var handled bool
			var err error
			included, handled, _, err = resolver.resolveExactTarget(target, false, platform.Palette{})
			if err != nil {
				return nil, err
			}
			if !handled {
				continue
			}
		}
		out = append(out, included...)
	}
	return DedupeEntriesByPathPreserveOrder(out), nil
}

func FilterEntriesByExactStagePaths(entries []Entry, paths []string, keepMatches bool) []Entry {
	wanted := make(map[string]struct{}, len(paths))
	for _, value := range paths {
		normalized := normalizeRelPath(value)
		if normalized == "" {
			continue
		}
		wanted[normalized] = struct{}{}
	}

	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		_, matched := wanted[normalizeRelPath(entry.RelPath)]
		if keepMatches == matched {
			out = append(out, entry)
		}
	}
	return out
}

func filterEntriesByStagePatterns(entries []Entry, patterns []string, keepMatches bool) ([]Entry, error) {
	if len(patterns) == 0 {
		return entries, nil
	}

	matchers := make([]StageValueMatcher, 0, len(patterns))
	for _, pattern := range patterns {
		matcher, err := ClassifyStageValue(pattern)
		if err != nil {
			return nil, newUsageError("Error: invalid pattern %q.", pattern)
		}
		matchers = append(matchers, matcher)
	}

	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		matched := MatchesStageValues(entry.RelPath, matchers)
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

func ClassifyStageValue(value string) (StageValueMatcher, error) {
	if hasGlobChars(value) {
		normalized := normalizeGlobStageValue(value)
		re, err := compileGlob(normalized)
		if err != nil {
			return StageValueMatcher{}, err
		}
		return StageValueMatcher{
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
		return StageValueMatcher{kind: stageValueMatchSubtree, value: normalized, dirOnly: true}, nil
	case strings.Contains(normalized, "/"):
		return StageValueMatcher{kind: stageValueMatchAnchored, value: normalized}, nil
	default:
		return StageValueMatcher{kind: stageValueMatchBare, value: normalized}, nil
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

func MatchesStageValues(relPath string, matchers []StageValueMatcher) bool {
	for _, matcher := range matchers {
		if MatchesStageValue(relPath, matcher) {
			return true
		}
	}
	return false
}

func MatchesStageValue(relPath string, matcher StageValueMatcher) bool {
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
