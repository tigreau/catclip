package ui

import (
	"errors"
	"fmt"
	"os"
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
// disjoint ignored subtrees like `docs/policy/**` and
// `docs/versions/**`, multi-flag would demand a file match BOTH and
// return zero results — the bug reported 2026-07-02.
//
// Used by maybeNarrowConfirm to translate
//
//	`catclip . --include docs/policy docs/versions`
//
// into
//
//	`catclip . --include docs/policy docs/versions --only "docs/policy/**" "docs/versions/**"`
//
// when the user picks "Keep only ignored." The original argv stays
// verbatim; only the new `--only` flag is appended.
//
// The single-pattern case reduces to `--only "docs/**"` — one flag,
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
//   - Literal directory include `--include docs` → "docs/**"
//   - Literal file include `--include docs/x.md` → "docs/x.md"
//   - Wildcard `--include *` → one "<root>/**" per root in
//     topLevelRootsForNarrow(ignoredEntries)
//
// File vs directory is resolved by statting the path relative to
// workingDir. Stat errors fall through to the "/**" recursive form,
// which is conservatively wide (matches the file form's intent plus
// any descendants if there happen to be some).
func onlyPatternsForIncludes(workingDir string, includePaths []string, ignoredEntries []discovery.Entry) []string {
	if includesContainWildcard(includePaths) {
		roots := topLevelRootsForNarrow(ignoredEntries)
		patterns := make([]string, 0, len(roots))
		for _, root := range roots {
			patterns = append(patterns, root+"/**")
		}
		return patterns
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
			patterns = append(patterns, rel+"/**")
		}
	}
	return patterns
}

// isFileInclude reports whether the include path resolves to a regular
// file (not a directory). Stat errors return false so the caller
// defaults to the "/**" recursive form for ambiguous cases.
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
// Wildcard `*` is treated as subset-by-construction: the include picker
// (chooseIgnoredTargetMatches) already filters its candidates through
// filterIgnoredTargetsByScopeTargets before returning `*`, so any
// wildcard return is guaranteed to resolve to paths under the scope.
func allIncludesAreSubsetOfTargets(includePaths, scopeTargets []string) bool {
	if len(includePaths) == 0 || len(scopeTargets) == 0 {
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
		if inc == "*" {
			continue
		}
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

// topLevelRootsForNarrow extracts the distinct top-level path segments
// from a slice of entries, in stable first-seen order. Used to compute
// the narrow argv targets when the user picked `--include *` and we
// have to translate "every ignored file" into a small set of concrete
// roots.
func topLevelRootsForNarrow(entries []discovery.Entry) []string {
	seen := make(map[string]struct{}, len(entries))
	roots := make([]string, 0, 8)
	for _, e := range entries {
		rel := normalizeRelPath(e.RelPath)
		if rel == "" || rel == "." {
			continue
		}
		first := rel
		if i := strings.Index(rel, "/"); i >= 0 {
			first = rel[:i]
		}
		if _, dup := seen[first]; dup {
			continue
		}
		seen[first] = struct{}{}
		roots = append(roots, first)
	}
	return roots
}

// includesContainWildcard reports whether `*` (the select-all sentinel
// returned by the include picker) appears in the include list.
func includesContainWildcard(includes []string) bool {
	for _, inc := range includes {
		if inc == "*" {
			return true
		}
	}
	return false
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

// maybeNarrowConfirm runs the v0.6.4 narrow-confirm screen if its
// preconditions are met. It returns either:
//   - the input candidate args (user picked "Keep all", Esc'd out, OR
//     preconditions not met → no screen fired / no narrowing chosen), or
//   - the rewritten narrow args (user picked "Keep only ignored").
//
// Esc inside the screen is treated as "Keep all" rather than cascading
// the cancel through the include picker + modifier menu. See the v0.6.4
// resolved-plan doc for the rationale.
//
// Discovery runs at most once (for the keep set); the narrow set is
// derived by partition. See
// docs/versions/v0.6.4/reports/RESOLVED_PLAN_include_narrow_confirm.md
// "Avoiding re-discovery."
func maybeNarrowConfirm(candidate []string, includePaths, scopeExplicitTargets []string) ([]string, bool, error) {
	if len(includePaths) == 0 {
		return candidate, false, nil
	}
	if !allIncludesAreSubsetOfTargets(includePaths, scopeExplicitTargets) {
		return candidate, false, nil
	}

	// v0.6.4: `command.RewriteDeepIncludeScope`'s auto-`--only`
	// synthesis was deleted; the walker's per-entry `targetIncluded`
	// check now does the deep-include narrowing at walk time. The
	// neutralize helper that used to reshape the candidate here is
	// therefore gone — EvaluateScope sees the user's argv as-typed.
	// Keep the variable name for readability of the row-pick branch
	// below.
	broaderCandidate := candidate

	view, err := resolvedCurrentScopeViewForArgs(broaderCandidate)
	if err != nil {
		// EvaluateScope failed → can't render previews; return without
		// firing the screen rather than block the include flow.
		return candidate, false, nil
	}
	allEntries, ignoredEntries := discovery.PartitionIgnoredByIncludes(view.Entries, includePaths)
	if len(ignoredEntries) == 0 || len(ignoredEntries) == len(allEntries) {
		// Either the include authorized nothing new, or the entire scope
		// is already only the include-authorized set → narrow row would be
		// identical to keep row. Skip the screen.
		return candidate, false, nil
	}

	onlyPatterns := onlyPatternsForIncludes(view.Invocation.WorkingDir, includePaths, ignoredEntries)
	if len(onlyPatterns) == 0 {
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
		// Esc = "don't narrow" — equivalent to picking the Keep all row.
		// Avoids cascading the cancel through the include picker + modifier
		// menu, which would force the user to re-open both. Honest semantic:
		// Esc means "leave what I typed alone."
		return broaderCandidate, true, nil
	}
	if err != nil {
		return candidate, true, err
	}
	if len(result.Matches) == 0 {
		return broaderCandidate, true, nil
	}
	// picker.parseMatches returns column 2 (the second tab-separated
	// field) when present — i.e. the checkpoint path, not the label.
	// Compare against the keep checkpoint path to identify the row.
	picked := result.Matches[0]
	if picked == keepPath {
		// "Keep all" uses the broader form (no deep-include auto-`--only`)
		// so the user gets the full original scope plus the include's
		// authorization, not the parser's silent narrowing.
		return broaderCandidate, true, nil
	}
	return appendOnlyForNarrow(broaderCandidate, onlyPatterns), true, nil
}
