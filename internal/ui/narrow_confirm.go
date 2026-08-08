package ui

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

// appendOnlyForNarrow inserts a single `--only <pattern>...` flag with
// all narrow patterns as values (OR-union semantics) at the end of the
// current scope. Multi-VALUE — never multi-FLAG — because the narrow
// intent is "keep files matching pattern-A OR pattern-B OR ...",
// and catclip's multi-flag `--only` is AND-intersect. With two
// disjoint ignored subtrees like `docs/policy/*` and
// `docs/versions/*`, multi-flag would demand a file match BOTH and
// return zero results — the bug reported 2026-07-02.
//
// Used by maybeNarrowConfirm to translate
//
//	`catclip . --include docs/policy docs/versions`
//
// into
//
//	`catclip . --include docs/policy docs/versions --only "docs/policy/*" "docs/versions/*"`
//
// when the user picks "Keep only ignored." The original argv stays
// verbatim; only the new `--only` flag is appended.
//
// The single-pattern case reduces to `--only "docs/*"` — one flag,
// one value, no ambiguity.
//
// The include picker only fires for the last scope, so insertion is at
// end of argv. If a future flow opens the include picker for an earlier
// scope, this helper needs a nextThenBoundary lookahead.
func appendOnlyForNarrow(args []string, onlyPatterns []string) []string {
	if len(onlyPatterns) == 0 {
		return args
	}
	out := make([]string, 0, len(args)+1+len(onlyPatterns))
	out = append(out, args...)
	out = append(out, "--only")
	out = append(out, onlyPatterns...)
	return out
}

// onlyPatternsForIncludes computes the `--only` patterns for the narrow
// rewrite. Per the v0.6.4 plan:
//   - Literal directory include `--include docs` → "docs/*"
//   - Literal file include `--include docs/x.md` → "docs/x.md"
//   - `--no-ignore` → the broadest replay-safe directory patterns,
//     with exact file paths where a directory pattern would retain visible
//     siblings
//
// File vs directory is resolved by statting the path relative to
// workingDir. Stat errors fall through to the "/*" recursive form,
// which is conservatively wide (matches the file form's intent plus
// any descendants if there happen to be some).
func onlyPatternsForIncludes(workingDir string, includePaths []string, noIgnore bool, allEntries, ignoredEntries []discovery.Entry) []string {
	if noIgnore {
		return onlyPatternsForNoIgnore(allEntries, ignoredEntries)
	}
	patterns := make([]string, 0, len(includePaths))
	for _, inc := range includePaths {
		rel := normalizeRelPath(inc)
		if rel == "" || rel == "." {
			continue
		}
		if isFileInclude(workingDir, rel) {
			patterns = append(patterns, rel)
		} else {
			patterns = append(patterns, rel+"/*")
		}
	}
	return patterns
}

// onlyPatternsReplayExactlyIgnored verifies generated argv against the same
// matcher used by the real --only stage. This catches syntax-dependent
// widening such as a root-level file value (`debug.log`) floating by basename
// to a visible `src/debug.log`.
func onlyPatternsReplayExactlyIgnored(allEntries, ignoredEntries []discovery.Entry, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	matchers := make([]discovery.StageValueMatcher, 0, len(patterns))
	for _, pattern := range patterns {
		matcher, err := discovery.ClassifyStageValue(pattern)
		if err != nil {
			return false
		}
		matchers = append(matchers, matcher)
	}

	want := make(map[string]struct{}, len(ignoredEntries))
	for _, entry := range ignoredEntries {
		want[normalizeRelPath(entry.RelPath)] = struct{}{}
	}
	got := make(map[string]struct{}, len(want))
	for _, entry := range allEntries {
		rel := normalizeRelPath(entry.RelPath)
		for _, matcher := range matchers {
			if discovery.MatchesStageValue(rel, matcher) {
				got[rel] = struct{}{}
				break
			}
		}
	}
	if len(got) != len(want) {
		return false
	}
	for rel := range want {
		if _, ok := got[rel]; !ok {
			return false
		}
	}
	return true
}

const maxNarrowReplayCommandBytes = 16 * 1024

// onlyPatternsForNoIgnore builds replay-safe selectors for the ignored files
// admitted by `--no-ignore`. A broad root pattern is used only when that root
// contains no visible file; otherwise exact file paths keep visible siblings
// out of the "Keep only ignored" result.
func onlyPatternsForNoIgnore(allEntries, ignoredEntries []discovery.Entry) []string {
	ignoredSet := make(map[string]struct{}, len(ignoredEntries))
	for _, entry := range ignoredEntries {
		ignoredSet[normalizeRelPath(entry.RelPath)] = struct{}{}
	}

	visibleDirs := make(map[string]struct{})
	visibleEntries := make([]discovery.Entry, 0, len(allEntries)-len(ignoredEntries))
	for _, entry := range allEntries {
		rel := normalizeRelPath(entry.RelPath)
		if _, ignored := ignoredSet[rel]; ignored {
			continue
		}
		visibleEntries = append(visibleEntries, entry)
		for dir := path.Dir(rel); dir != "." && dir != ""; dir = path.Dir(dir) {
			visibleDirs[dir] = struct{}{}
		}
	}

	patterns := make([]string, 0, len(ignoredEntries))
	seen := make(map[string]struct{}, len(ignoredEntries))
	for _, entry := range ignoredEntries {
		rel := normalizeRelPath(entry.RelPath)
		if rel == "" || rel == "." {
			continue
		}

		candidate := rel
		ancestors := make([]string, 0, 8)
		for dir := path.Dir(rel); dir != "." && dir != ""; dir = path.Dir(dir) {
			ancestors = append(ancestors, dir)
		}
		for i := len(ancestors) - 1; i >= 0; i-- {
			if _, containsVisible := visibleDirs[ancestors[i]]; !containsVisible {
				candidate = ancestors[i] + "/*"
				break
			}
		}

		// Catclip would interpret metacharacters as a glob rather than the
		// literal filesystem name. There is no lossless argv spelling for
		// that path, so skip the optional confirmation instead of promising
		// a replay command that selects the wrong files.
		if strings.ContainsAny(candidate, "*?[") && !strings.HasSuffix(candidate, "/*") {
			return nil
		}
		if strings.HasSuffix(candidate, "/*") && strings.ContainsAny(strings.TrimSuffix(candidate, "/*"), "*?[") {
			return nil
		}
		matcher, err := discovery.ClassifyStageValue(candidate)
		if err != nil {
			return nil
		}
		for _, visible := range visibleEntries {
			if discovery.MatchesStageValue(normalizeRelPath(visible.RelPath), matcher) {
				return nil
			}
		}
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		patterns = append(patterns, candidate)
	}
	return patterns
}

func narrowReplayCommandBytes(args, patterns []string) int {
	total := 0
	for _, arg := range args {
		// Include separators and conservative quoting overhead. The cap is
		// deliberately below PowerShell/CreateProcess limits.
		total += len(arg) + 3
	}
	for _, pattern := range patterns {
		total += len(pattern) + 3
	}
	return total
}

// isFileInclude reports whether the include path resolves to a regular
// file (not a directory). Stat errors return false so the caller
// defaults to the "/*" recursive form for ambiguous cases.
func isFileInclude(workingDir, rel string) bool {
	info, err := os.Stat(filepath.Join(workingDir, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// allIncludesAreSubsetOfTargets reports whether every supplied include
// path is a descendant of (or equal to) at least one of the scope's
// explicit target paths. This is the "includes ⊆ targets" invariant the
// narrow-confirm screen requires before firing.
//
// `--no-ignore` is subset-by-construction because execution bounds it to the
// current scope targets.
func allIncludesAreSubsetOfTargets(includePaths, scopeTargets []string, noIgnore bool) bool {
	if len(scopeTargets) == 0 || (len(includePaths) == 0 && !noIgnore) {
		return false
	}
	normalizedTargets := make([]string, 0, len(scopeTargets))
	rootCovers := false
	for _, t := range scopeTargets {
		n := normalizeRelPath(t)
		if n == "" || n == "." {
			rootCovers = true
			continue
		}
		normalizedTargets = append(normalizedTargets, n)
	}
	for _, inc := range includePaths {
		n := normalizeRelPath(inc)
		if n == "" || n == "." {
			continue
		}
		if rootCovers {
			continue
		}
		covered := false
		for _, t := range normalizedTargets {
			if n == t || strings.HasPrefix(n, t+"/") {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// extractIncludePathsFromPickerArgs pulls just the include values from the
// argv slice the include picker returns. The picker's normal output is
// `["--include", v1, v2, …]`, but the v0.5.7 basename+include flow can
// rewrite it to multi-modifier shapes like
// `["--include", "docs", "--only", "docs/architecture"]` when the user
// picks a descendant of a gitignored ancestor — so we stop at the first
// modifier boundary instead of taking all of args[1:].
// Returns nil for any unexpected shape so callers fall through silently.
func extractIncludePathsFromPickerArgs(args []string) []string {
	if len(args) < 2 || args[0] != "--include" {
		return nil
	}
	out := make([]string, 0, len(args)-1)
	for _, v := range args[1:] {
		if cli.IsModifierBoundaryToken(v) {
			break
		}
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// narrowConfirmHeader returns the fzf header for the two-row confirm
// screen. Three lines (catclip's PickerHeader convention).
func narrowConfirmHeader() string {
	return discovery.PickerHeader(
		"--include authorized a subtree that fits entirely inside the current target.",
		"Pick the file set you want.",
		"[Up/Down] move  [Enter] confirm  [Esc] back",
	)
}

var errNarrowConfirmBack = errors.New("narrow confirmation: back")

// maybeNarrowConfirm runs the v0.6.4 narrow-confirm screen if its
// preconditions are met. It returns either:
//   - the input candidate args (user picked "Keep all" OR
//     preconditions not met → no screen fired / no narrowing chosen), or
//   - the rewritten narrow args (user picked "Keep only ignored").
//
// Esc returns errNarrowConfirmBack. The include flow catches that sentinel and
// reopens the immediately preceding include picker.
//
// Discovery runs at most once (for the keep set); the narrow set is
// derived by partition. See
// docs/versions/v0.6.4/reports/RESOLVED_PLAN_include_narrow_confirm.md
// "Avoiding re-discovery."
func maybeNarrowConfirm(candidate []string, includePaths, scopeExplicitTargets []string) ([]string, bool, error) {
	return maybeNarrowConfirmForEvaluation(candidate, candidate, includePaths, scopeExplicitTargets)
}

// maybeNarrowConfirmForResolver evaluates the candidate with global discovery
// policy that the startup resolver already learned from the complete raw argv.
// This matters when a global flag appears after --include: the partially built
// candidate must keep its original order, but the narrow preview must still see
// the same file universe as the include picker and final command.
func maybeNarrowConfirmForResolver(resolver *discovery.Resolver, candidate []string, includePaths, scopeExplicitTargets []string) ([]string, bool, error) {
	evaluationArgs := candidate
	if resolver != nil && resolver.WithBinaries && !argsContain(evaluationArgs, "--with-binaries") {
		evaluationArgs = append(cloneStringSlice(evaluationArgs), "--with-binaries")
	}
	return maybeNarrowConfirmForEvaluation(candidate, evaluationArgs, includePaths, scopeExplicitTargets)
}

func maybeNarrowConfirmForEvaluation(candidate, evaluationArgs []string, includePaths, scopeExplicitTargets []string) ([]string, bool, error) {
	noIgnore := currentScopeHasFlag(candidate, "--no-ignore")
	if len(includePaths) == 0 && !noIgnore {
		return candidate, false, nil
	}
	if !allIncludesAreSubsetOfTargets(includePaths, scopeExplicitTargets, noIgnore) {
		return candidate, false, nil
	}

	view, err := resolvedCurrentScopeViewForArgs(evaluationArgs)
	if err != nil {
		// EvaluateScope failed → can't render previews; return without
		// firing the screen rather than block the include flow.
		return candidate, false, nil
	}
	allEntries, ignoredEntries := discovery.PartitionIgnoredByIncludes(view.Entries, includePaths, noIgnore)
	if len(ignoredEntries) == 0 || len(ignoredEntries) == len(allEntries) {
		// Either the include authorized nothing new, or the entire scope
		// is already only the include-authorized set → narrow row would be
		// identical to keep row. Skip the screen.
		return candidate, false, nil
	}

	onlyPatterns := onlyPatternsForIncludes(view.Invocation.WorkingDir, includePaths, noIgnore, allEntries, ignoredEntries)
	if len(onlyPatterns) == 0 {
		return candidate, false, nil
	}
	if narrowReplayCommandBytes(candidate, append([]string{"--only"}, onlyPatterns...)) > maxNarrowReplayCommandBytes {
		return candidate, false, nil
	}
	if !onlyPatternsReplayExactlyIgnored(allEntries, ignoredEntries, onlyPatterns) {
		return candidate, false, nil
	}

	tmpdir, err := os.MkdirTemp("", "catclip-narrow-*")
	if err != nil {
		return candidate, false, nil
	}
	defer os.RemoveAll(tmpdir)

	statuses := map[string]string{}
	if view.GitContext.Enabled {
		statuses, err = git.StatusMapForPathspecs(view.GitContext, discovery.GitStatusPathspecsForEntries(view.GitContext, allEntries))
		if err != nil {
			return candidate, false, nil
		}
	}

	keepPath := filepath.Join(tmpdir, "keep.json")
	narrowPath := filepath.Join(tmpdir, "narrow.json")
	if err := discovery.WriteCheckpoint(keepPath, view.Invocation.WorkingDir, discovery.CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    allEntries,
	}); err != nil {
		return candidate, false, nil
	}
	if err := discovery.WriteCheckpoint(narrowPath, view.Invocation.WorkingDir, discovery.CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    ignoredEntries,
	}); err != nil {
		return candidate, false, nil
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return candidate, false, nil
	}
	previewCmd := fmt.Sprintf("%s --quiet --internal-tree-preview --internal-prediscovered {2}",
		discovery.ShellQuoteArg(self))

	lines := []string{
		"Keep all current files\t" + keepPath,
		"Keep only ignored files\t" + narrowPath,
	}

	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return candidate, false, err
	}
	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Prompt:         "narrow> ",
		WithNth:        "1",
		Nth:            "1",
		Header:         narrowConfirmHeader(),
		PreviewCommand: previewCmd,
		NoSort:         true,
		Exact:          true,
		Lines:          lines,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return candidate, true, errNarrowConfirmBack
	}
	if err != nil {
		return candidate, true, err
	}
	// picker.parseMatches returns column 2 (the second tab-separated
	// field) when present — i.e. the checkpoint path, not the label.
	// Compare against the keep checkpoint path to identify the row.
	picked := result.Matches[0]
	if picked == keepPath {
		return candidate, true, nil
	}
	return appendOnlyForNarrow(candidate, onlyPatterns), true, nil
}
