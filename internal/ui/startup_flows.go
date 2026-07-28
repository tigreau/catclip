package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

func resolveStartupModifierArgs(resolver *discovery.Resolver, currentArgs, currentScopeTargets, currentScopeExplicitTargets []string) ([]string, bool, error) {
	choice, err := chooseStartupModifier(currentArgs)
	if err != nil {
		return nil, false, err
	}
	if err := startupValidateModifierChoice(currentArgs, choice); err != nil {
		return nil, false, err
	}

	finalArgs := trimTrailingModifierPlaceholders(append([]string(nil), currentArgs...))
	return resolveStartupModifierChoice(resolver, finalArgs, currentScopeTargets, currentScopeExplicitTargets, choice)
}

func resolveStartupModifierChoice(resolver *discovery.Resolver, finalArgs, currentScopeTargets, currentScopeExplicitTargets []string, choice StartupModifierChoice) ([]string, bool, error) {
	switch choice.Mode {
	case startupModifierModeFinish:
		return finalArgs, true, nil
	case startupModifierModeThen:
		return append(finalArgs, "--then"), true, nil
	case startupModifierModeInclude:
		for {
			args, _, _, includeUsedFzf, err := resolveStartupScopeInputs(resolver, nil, []string{""}, currentScopeTargets, currentScopeExplicitTargets)
			if err != nil {
				return nil, true, err
			}
			candidate := append(finalArgs, args...)
			includePaths := extractIncludePathsFromPickerArgs(args)
			resolved, narrowUsedFzf, narrowErr := maybeNarrowConfirmForResolver(resolver, candidate, includePaths, currentScopeExplicitTargets)
			if errors.Is(narrowErr, errNarrowConfirmBack) {
				continue
			}
			if narrowErr != nil {
				return nil, true, narrowErr
			}
			return resolved, true || includeUsedFzf || narrowUsedFzf, nil
		}
	case startupModifierModeOnly:
		args, onlyUsedFzf, err := resolveStartupScopeFileSetArgs(finalArgs, "--only", "only> ")
		return args, true || onlyUsedFzf, err
	case startupModifierModeExclude:
		args, excludeUsedFzf, err := resolveStartupScopeFileSetArgs(finalArgs, "--exclude", "exclude> ")
		return args, true || excludeUsedFzf, err
	case startupModifierModeRecent:
		args, err := resolveStartupRecentArgs(finalArgs)
		return args, true, err
	case startupModifierModeSize:
		args, usedFzf, err := resolveStartupSizeArgs(finalArgs)
		if err != nil {
			return nil, true || usedFzf, err
		}
		return args, true || usedFzf, nil
	case startupModifierModeDepth:
		args, usedFzf, err := resolveStartupDepthArgs(finalArgs)
		if err != nil {
			return nil, true || usedFzf, err
		}
		return args, true || usedFzf, nil
	case startupModifierModeLines:
		args, usedFzf, err := resolveStartupLinesArgs(finalArgs)
		if err != nil {
			return nil, true || usedFzf, err
		}
		return args, true || usedFzf, nil
	case startupModifierModeContains:
		args, containsUsedFzf, err := resolveStartupContentArgs(finalArgs, "--contains")
		if err != nil {
			return nil, true || containsUsedFzf, err
		}
		return args, true || containsUsedFzf, nil
	case startupModifierModeNotContains:
		args, ncUsedFzf, err := resolveStartupContentArgs(finalArgs, "--not-contains")
		if err != nil {
			return nil, true || ncUsedFzf, err
		}
		return args, true || ncUsedFzf, nil
	case startupModifierModeSnippet:
		args, containsUsedFzf, err := resolveStartupContentArgs(finalArgs, "--snippet")
		if err != nil {
			return nil, true || containsUsedFzf, err
		}
		return args, true || containsUsedFzf, nil
	case startupModifierModeGit:
		diffPreview := strings.HasSuffix(choice.Args[0], "-diff")
		args, gitUsedFzf, err := resolveStartupGitScopeArgs(resolver, append(finalArgs, choice.Args[0]), startupGitStagePrompt(choice.Args[0]), nil, true, diffPreview)
		if err != nil {
			return nil, true || gitUsedFzf, err
		}
		return args, true || gitUsedFzf, nil
	case startupModifierModeFlags:
		return append(finalArgs, choice.Args...), true, nil
	default:
		return nil, false, discovery.ErrSelectionCancelled
	}
}

func resolveStartupRecentArgs(currentArgs []string) ([]string, error) {
	return resolveStartupRecentPickerArgs(currentArgs, "")
}

func resolveStartupRecentArgsWithEscHint(currentArgs []string, escHint string) ([]string, error) {
	return resolveStartupRecentPickerArgsWithEscHint(currentArgs, "", escHint)
}

func resolveStartupContentArgs(currentArgs []string, flag string) ([]string, bool, error) {
	return resolveStartupContentArgsWithEscHint(currentArgs, flag, "")
}

func resolveStartupContentArgsWithEscHint(currentArgs []string, flag string, escHint string) ([]string, bool, error) {
	finishBench := platform.InternalBenchSpan("ui.content_picker.resolve",
		"flag", flag,
		"argc", platform.InternalBenchInt(len(currentArgs)),
	)
	if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, flag); err != nil {
		finishBench("err", platform.InternalBenchError(err), "used_fzf", "false")
		return nil, false, err
	}
	finishChooseBench := platform.InternalBenchSpan("ui.content_picker.choose_matches",
		"flag", flag,
	)
	result, err := discovery.ChooseContentMatchesWithFzfAndEscHint("", currentArgs, flag, escHint)
	finishChooseBench(
		"err", platform.InternalBenchError(err),
		"matches", platform.InternalBenchInt(len(result.Matches)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err), "used_fzf", "false")
		return nil, false, err
	}
	if strings.TrimSpace(result.Query) == "" {
		finishBench("err", "false", "used_fzf", "false", "cancelled", "true")
		return nil, false, discovery.ErrSelectionCancelled
	}

	finalArgs := append([]string(nil), currentArgs...)
	finalArgs = append(finalArgs, flag, result.Query)

	// matchPaths is the set of files matching the query in the current scope; it
	// drives the --only coverage check below. For --snippet we compute the match
	// set ONCE here and reuse it for both the boundary preview and the coverage
	// check, instead of the boundary flow re-scanning the same pattern 2-3x. The
	// reused set is equivalent to contentMatchPathsForArgs, gated by
	// TestSnippetBoundaryMatchSetEquivalence.
	var matchPaths []string
	matchPathsReady := false

	if flag == "--snippet" {
		finishScopeBench := platform.InternalBenchSpan("ui.snippet_picker.scope_match",
			"selected", platform.InternalBenchInt(len(result.Matches)),
		)
		view, matched, scanErr := snippetBoundaryScopeMatch(currentArgs, result.Query)
		finishScopeBench(
			"err", platform.InternalBenchError(scanErr),
			"entries", platform.InternalBenchInt(len(view.Entries)),
			"matched", platform.InternalBenchInt(len(matched)),
			"bad_pattern", platform.InternalBenchCancelled(scanErr, search.ErrRipgrepBadPattern),
		)
		previewCommand := ""
		cleanupPreview := func() {}
		if scanErr == nil {
			finishPreviewBench := platform.InternalBenchSpan("ui.snippet_picker.build_boundary_preview",
				"matched", platform.InternalBenchInt(len(matched)),
				"selected", platform.InternalBenchInt(len(result.Matches)),
			)
			previewCommand, cleanupPreview = buildSnippetBoundaryPreviewCommand(view, result.Query, result.Matches, matched)
			finishPreviewBench("preview", platform.InternalBenchBool(previewCommand != ""))
			matchPaths = entryRelPaths(matched)
			matchPathsReady = true
		}
		defer cleanupPreview()
		finishBoundaryBench := platform.InternalBenchSpan("ui.snippet_picker.choose_boundary",
			"preview", platform.InternalBenchBool(previewCommand != ""),
		)
		boundary, err := chooseSnippetBoundaryWithFzfAndEscHint(previewCommand, escHint)
		finishBoundaryBench("err", platform.InternalBenchError(err))
		if err != nil {
			finishBench("err", platform.InternalBenchError(err), "used_fzf", "true")
			return nil, false, err
		}
		if boundary.SnippetContextSet {
			finalArgs = append(finalArgs, strconv.Itoa(boundary.SnippetContextLines))
		}
	}

	if contentMatchSelectionIncludesAllRow(result.Matches) {
		finishBench("err", "false", "used_fzf", "true", "selected", "all")
		return finalArgs, true, nil
	}
	if !matchPathsReady {
		finishPathsBench := platform.InternalBenchSpan("ui.content_picker.match_paths_for_args",
			"flag", flag,
			"selected", platform.InternalBenchInt(len(result.Matches)),
		)
		matchPaths, err = contentMatchPathsForArgs(currentArgs, flag, result.Query)
		finishPathsBench(
			"err", platform.InternalBenchError(err),
			"paths", platform.InternalBenchInt(len(matchPaths)),
		)
		if err != nil {
			finishBench("err", platform.InternalBenchError(err), "used_fzf", "true")
			return nil, false, err
		}
	}
	if startupStageSelectionCoversAll(result.Matches, matchPaths) {
		finishBench("err", "false", "used_fzf", "true", "selected", "covers_all")
		return finalArgs, true, nil
	}
	if len(result.Matches) > 0 {
		finalArgs = append(finalArgs, "--only")
		finalArgs = append(finalArgs, result.Matches...)
	}
	finishBench(
		"err", "false",
		"used_fzf", "true",
		"selected", platform.InternalBenchInt(len(result.Matches)),
	)
	return finalArgs, true, nil
}

// snippetBoundaryScopeMatch resolves the current scope and finds the files
// matching pattern — the single content scan the boundary flow needs. Its
// result feeds both the boundary preview and the --only coverage check, which
// previously re-ran the same scan up to three times. The matched paths are
// equivalent to contentMatchPathsForArgs (gated by
// TestSnippetBoundaryMatchSetEquivalence).
func snippetBoundaryScopeMatch(currentArgs []string, pattern string) (resolvedScopeView, []discovery.Entry, error) {
	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil {
		return resolvedScopeView{}, nil, err
	}
	if len(view.Entries) == 0 {
		return view, nil, nil
	}
	matched, err := discovery.FilterEntriesByContent(discovery.EnsureEntryAbsPaths(view.Entries, view.Invocation.WorkingDir), pattern)
	if err != nil {
		return view, nil, err
	}
	return view, matched, nil
}

func entryRelPaths(entries []discovery.Entry) []string {
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.RelPath)
	}
	return paths
}

func chooseSnippetBoundaryWithFzfAndEscHint(previewCommand, escHint string) (startupSnippetBoundaryChoice, error) {
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return startupSnippetBoundaryChoice{}, err
	}
	lines, index := startupSnippetBoundaryChoiceLines()
	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Prompt:         "snippet mode> ",
		WithNth:        "1,3",
		Nth:            "1",
		Header:         snippetBoundaryPickerHeaderWithEscHint(escHint),
		NoSort:         true,
		PreviewCommand: previewCommand,
		Lines:          lines,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return startupSnippetBoundaryChoice{}, discovery.ErrSelectionCancelled
	}
	if err != nil {
		return startupSnippetBoundaryChoice{}, err
	}
	if len(result.Matches) == 0 {
		return startupSnippetBoundaryChoice{}, discovery.ErrSelectionCancelled
	}
	choice, ok := index[result.Matches[0]]
	if !ok {
		return startupSnippetBoundaryChoice{}, discovery.ErrSelectionCancelled
	}
	return choice, nil
}

// buildSnippetBoundaryPreviewCommand builds the boundary-picker preview from an
// already-resolved view and the already-scanned matched entries (the single
// scan from snippetBoundaryScopeMatch). It does no content scanning of its own.
func buildSnippetBoundaryPreviewCommand(view resolvedScopeView, pattern string, selected []string, matched []discovery.Entry) (string, func()) {
	noop := func() {}
	if strings.TrimSpace(pattern) == "" || len(view.Entries) == 0 || len(matched) == 0 {
		return "", noop
	}
	onlyValues := snippetBoundaryPreviewOnlyValues(selected, entryRelPaths(matched))
	cmd, tmpdir := buildSnippetBoundaryPreviewForScope(view, pattern, matched, onlyValues)
	if cmd == "" {
		return "", noop
	}
	return cmd, func() {
		_ = os.RemoveAll(tmpdir)
	}
}

// snippetBoundaryPreviewOnlyValues decides whether the preview should narrow to
// the user's explicit file selection. It compares the selection against the
// already-computed matchPaths (no scan); when the selection covers the whole
// match set it returns nil (preview the full set).
func snippetBoundaryPreviewOnlyValues(selected []string, matchPaths []string) []string {
	if contentMatchSelectionIncludesAllRow(selected) {
		return nil
	}
	values := make([]string, 0, len(selected))
	for _, value := range selected {
		relPath := normalizeRelPath(value)
		if relPath == "" || relPath == contentMatchAllMatchesLabel {
			continue
		}
		values = append(values, relPath)
	}
	if len(values) == 0 {
		return nil
	}
	if startupStageSelectionCoversAll(values, matchPaths) {
		return nil
	}
	return values
}

// buildSnippetBoundaryPreviewForScope sets up the lazy `snippet mode>` preview:
// it runs the one width-independent rg pass (output.BatchSnippetMatches) for match
// lines, serializes the source once, and returns a per-focus preview command.
// fzf renders only the focused boundary on demand — no eager pre-render of all
// 8 widths, which was ~1.3 s of blocking work before the picker painted on the
// 195k corpus. The per-focus handler STREAMS the focused width's raw snippet
// output (same shape as the `--lines` preview), so this path stays independent
// of tree-document rendering.
func buildSnippetBoundaryPreviewForScope(view resolvedScopeView, pattern string, matched []discovery.Entry, onlyValues []string) (cmd string, tmpdir string) {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return "", ""
	}

	source, err := buildSnippetBoundarySource(view, pattern, matched, onlyValues)
	if err != nil || len(source.Entries) == 0 {
		return "", ""
	}

	tmpdir, err = os.MkdirTemp("", "catclip-snippet-boundary-*")
	if err != nil {
		return "", ""
	}
	sourcePath := filepath.Join(tmpdir, "source.json")
	if err := writeSnippetBoundarySource(sourcePath, source); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", ""
	}

	// Trivial-value property (depth-picker pattern): the boundary key {2} is a
	// bare token fzf substitutes per focus; the hazardous source path is a fixed
	// discovery.ShellQuoteArg-quoted argument catclip controls.
	parts := []string{discovery.ShellQuoteArg(self), "--quiet", "--internal-snippet-boundary-preview",
		"--internal-boundary-source", discovery.ShellQuoteArg(sourcePath), "--internal-boundary-key", "{2}"}
	return strings.Join(parts, " "), tmpdir
}

func snippetBoundaryPreviewMatchedEntries(view resolvedScopeView, pattern string, onlyValues []string) ([]discovery.Entry, error) {
	entries := append([]discovery.Entry(nil), view.Entries...)
	entries = discovery.EnsureEntryAbsPaths(entries, view.Invocation.WorkingDir)
	entries, err := discovery.FilterEntriesByContent(entries, pattern)
	if err != nil {
		return nil, err
	}
	if len(onlyValues) > 0 {
		entries = discovery.FilterEntriesByExactStagePaths(entries, onlyValues, true)
	}
	return entries, nil
}

// buildSnippetBoundarySource runs the one width-independent rg pass and assembles
// the context-independent source the streaming boundary preview replays per focus:
// the matched files in emission order, each with its match lines. Match lines and
// bodies do not depend on the context width — only the range slicing does — so this
// is computed ONCE at picker open and the per-focus handler slices the focused width
// out of it.
//
// The stamping/dedup order mirrors output.BuildPlanForResolvedScopes exactly so the
// streamed per-width output is byte-identical to what `--snippet PATTERN N` copies
// (verified by TestSnippetBoundaryStreamMatchesCommit and the corpus no-drop test).
func buildSnippetBoundarySource(view resolvedScopeView, pattern string, matchedEntries []discovery.Entry, onlyValues []string) (snippetBoundarySource, error) {
	// Stamp once as snippet mode so output.BatchSnippetMatches sees the pattern. The
	// boundary choice is irrelevant here: match lines are context-independent.
	scope := snippetBoundaryPreviewScope(view.Scope, pattern, startupSnippetBoundaryChoice{})
	entries := append([]discovery.Entry(nil), matchedEntries...)
	if len(onlyValues) > 0 {
		entries = discovery.FilterEntriesByExactStagePaths(entries, onlyValues, true)
	}
	discovery.StampEntriesWithScopeOutputMode(entries, command.EntryModeSnippet, scope)
	entries = discovery.EnsureEntryAbsPaths(entries, view.Invocation.WorkingDir)
	// Match output.BuildPlanForResolvedScopes's emission order exactly:
	// ordering stages preserve evaluated order, otherwise dedupe sorts by
	// relative path.
	if output.ExecutionScopesPreserveEvaluatedOrder([]command.ExecutionScope{scope}) {
		entries = discovery.DedupeEntriesByPathPreserveOrder(entries)
	} else {
		entries = discovery.DedupeEntriesByPath(entries)
	}

	matchCache, err := output.BatchSnippetMatches(entries)
	if err != nil {
		return snippetBoundarySource{}, err
	}
	source := snippetBoundarySource{Pattern: pattern}
	for _, e := range entries {
		if e.AbsPath == "" {
			continue
		}
		lines := matchCache.Lookup(pattern, e.AbsPath)
		if len(lines) == 0 {
			continue
		}
		source.Entries = append(source.Entries, snippetBoundarySourceEntry{RelPath: e.RelPath, AbsPath: e.AbsPath, Lines: lines})
	}
	return source, nil
}

func snippetBoundaryPreviewScope(base command.ExecutionScope, pattern string, choice startupSnippetBoundaryChoice) command.ExecutionScope {
	scope := base
	scope.Snippet = true
	scope.SnippetPattern = pattern
	scope.SnippetContextSet = choice.SnippetContextSet
	scope.SnippetContextLines = choice.SnippetContextLines
	scope.Stages = append([]command.Stage(nil), base.Stages...)
	scope.Stages = append(scope.Stages, command.Stage{
		Kind:   command.StageSnippet,
		Values: []string{pattern},
	})
	return scope
}

func resolveStartupScopeFileSetArgs(currentArgs []string, flag, prompt string) ([]string, bool, error) {
	return resolveStartupScopeFileSetArgsWithQuery(currentArgs, flag, prompt, "")
}

func resolveStartupScopeFileSetArgsWithQuery(currentArgs []string, flag, prompt, query string) ([]string, bool, error) {
	return resolveStartupScopeFileSetArgsWithQueryAndEscHint(currentArgs, flag, prompt, query, "")
}

func resolveStartupScopeFileSetArgsWithQueryAndEscHint(currentArgs []string, flag, prompt, query string, escHint string) ([]string, bool, error) {
	var values []string
	if query != "" {
		values = []string{query}
	}
	stageValues, usedFzf, err := resolveStartupModifierStageValuesWithEscHint(currentArgs, flag, prompt, values, query == "", "", escHint)
	if err != nil {
		return nil, false, err
	}
	stageValues, err = normalizeInteractiveFileSetStageValues(currentArgs, stageValues)
	if err != nil {
		return nil, false, err
	}
	finalArgs := append([]string(nil), currentArgs...)
	finalArgs = append(finalArgs, flag)
	finalArgs = append(finalArgs, stageValues...)
	return finalArgs, usedFzf, nil
}

func resolveStartupModifierStage(resolver *discovery.Resolver, currentArgs, currentScopeTargets, currentScopeExplicitTargets []string, choiceArgs, remaining []string, allowInteractiveCompletion bool) ([]string, []string, bool, int, error) {
	return resolveStartupModifierStageWithEscHint(resolver, currentArgs, currentScopeTargets, currentScopeExplicitTargets, choiceArgs, remaining, allowInteractiveCompletion, "")
}

func resolveStartupModifierStageWithEscHint(resolver *discovery.Resolver, currentArgs, currentScopeTargets, currentScopeExplicitTargets []string, choiceArgs, remaining []string, allowInteractiveCompletion bool, escHint string) ([]string, []string, bool, int, error) {
	if len(choiceArgs) == 0 {
		return nil, nil, false, 0, discovery.ErrSelectionCancelled
	}
	flag := choiceArgs[0]

	switch flag {
	case "--include":
		currentIncludedTargets, err := startupCurrentScopeIncludedTargetPaths(currentArgs)
		if err != nil {
			return nil, nil, false, 0, err
		}
		values, consumed := startupStageValues(remaining)
		if len(values) == 0 {
			if !allowInteractiveCompletion {
				return nil, nil, false, 0, cli.RequiredStageValueError(flag)
			}
			for {
				resolvedArgs, resolvedTargets, _, usedFzf, err := resolveStartupScopeInputs(resolver, nil, []string{""}, currentScopeTargets, currentScopeExplicitTargets)
				if err != nil {
					return nil, nil, false, 0, err
				}
				finalArgs := append(append([]string(nil), currentArgs...), resolvedArgs...)
				// v0.6.4 narrow-confirm: if the just-resolved include is subset of
				// the scope target, offer "keep / narrow" before returning.
				includePaths := extractIncludePathsFromPickerArgs(resolvedArgs)
				narrowed, narrowUsedFzf, narrowErr := maybeNarrowConfirmForResolver(resolver, finalArgs, includePaths, currentScopeExplicitTargets)
				if errors.Is(narrowErr, errNarrowConfirmBack) {
					continue
				}
				if narrowErr != nil {
					return nil, nil, false, 0, narrowErr
				}
				return narrowed, append(append([]string(nil), currentScopeTargets...), resolvedTargets...), usedFzf || narrowUsedFzf, 0, nil
			}
		}
		if err := cli.ValidateIncludeValues(values); err != nil {
			return nil, nil, false, 0, err
		}
		exactStageValues, unresolvedValues, err := resolver.ResolveExactIgnoredIncludeTargets(values, currentScopeExplicitTargets)
		if err != nil {
			return nil, nil, false, 0, err
		}
		resolvedGroups := make([][]string, len(unresolvedValues))
		resolveFrom := 0
		stageUsedFzf := false
	resolveIncludes:
		for {
			stageValues := append([]string(nil), exactStageValues...)
			selectedPaths := append(append([]string(nil), currentIncludedTargets...), exactStageValues...)
			for i := 0; i < resolveFrom; i++ {
				stageValues = append(stageValues, resolvedGroups[i]...)
				selectedPaths = append(selectedPaths, resolvedGroups[i]...)
			}
			for resolveFrom < len(unresolvedValues) {
				value := unresolvedValues[resolveFrom]
				covered, err := resolver.InteractiveIgnoredQueryCoveredBySelection(value, selectedPaths, currentScopeExplicitTargets, currentScopeExplicitTargets)
				if err != nil {
					return nil, nil, false, 0, err
				}
				if covered {
					resolvedGroups[resolveFrom] = nil
					resolveFrom++
					continue
				}
				resolved, err := resolver.ResolveInteractiveIncludeTargets(value, selectedPaths, currentScopeExplicitTargets, currentScopeExplicitTargets)
				if err != nil {
					if errors.Is(err, discovery.ErrSelectionCancelled) {
						back := previousResolvedIncludeGroup(resolvedGroups, resolveFrom)
						if back >= 0 {
							clearResolvedIncludeGroupsFrom(resolvedGroups, back)
							resolveFrom = back
							continue resolveIncludes
						}
					}
					return nil, nil, false, 0, err
				}
				if len(resolved) == 0 {
					back := previousResolvedIncludeGroup(resolvedGroups, resolveFrom)
					if back >= 0 {
						clearResolvedIncludeGroupsFrom(resolvedGroups, back)
						resolveFrom = back
						continue resolveIncludes
					}
					return nil, nil, false, 0, discovery.ErrSelectionCancelled
				}
				resolvedGroups[resolveFrom] = append([]string(nil), resolved...)
				stageValues = append(stageValues, resolved...)
				selectedPaths = append(selectedPaths, resolved...)
				stageUsedFzf = true
				resolveFrom++
			}
			finalArgs := append(append([]string(nil), currentArgs...), flag)
			finalArgs = append(finalArgs, stageValues...)
			if stageUsedFzf {
				narrowed, _, narrowErr := maybeNarrowConfirmForResolver(resolver, finalArgs, stageValues, currentScopeExplicitTargets)
				if errors.Is(narrowErr, errNarrowConfirmBack) {
					back := previousResolvedIncludeGroup(resolvedGroups, len(resolvedGroups))
					if back < 0 {
						return nil, nil, false, 0, narrowErr
					}
					clearResolvedIncludeGroupsFrom(resolvedGroups, back)
					resolveFrom = back
					continue
				}
				if narrowErr != nil {
					return nil, nil, false, 0, narrowErr
				}
				return narrowed, append(append([]string(nil), currentScopeTargets...), stageValues...), true, consumed, nil
			}
			return finalArgs, append(append([]string(nil), currentScopeTargets...), stageValues...), stageUsedFzf, consumed, nil
		}
	case "--only", "--exclude":
		values, consumed := startupStageValues(remaining)
		if len(values) == 0 && !allowInteractiveCompletion {
			return nil, nil, false, 0, cli.RequiredStageValueError(flag)
		}
		stageValues, usedFzf, err := resolveStartupModifierStageValuesWithEscHint(currentArgs, flag, startupStagePrompt(flag), values, len(values) == 0, "", escHint)
		if err != nil {
			return nil, nil, false, 0, err
		}
		stageValues, err = normalizeInteractiveFileSetStageValues(currentArgs, stageValues)
		if err != nil {
			return nil, nil, false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		finalArgs = append(finalArgs, stageValues...)
		return finalArgs, append([]string(nil), currentScopeTargets...), usedFzf, consumed, nil
	case "--recent":
		if len(remaining) == 0 || cli.IsModifierBoundaryToken(remaining[0]) {
			if !allowInteractiveCompletion {
				finalArgs := append(append([]string(nil), currentArgs...), flag)
				return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
			}
			args, err := resolveStartupRecentArgsWithEscHint(currentArgs, escHint)
			return args, append([]string(nil), currentScopeTargets...), true, 0, err
		}
		limit, err := cli.ParseRecentLimitToken(remaining[0])
		if err != nil {
			return nil, nil, false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, strconv.Itoa(limit))
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--size":
		if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, "--size"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if len(remaining) == 0 || (allowInteractiveCompletion && startupRemainingIsBarePlaceholderChain(remaining)) {
			if !allowInteractiveCompletion {
				finalArgs := append(append([]string(nil), currentArgs...), flag)
				return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
			}
			args, usedFzf, err := resolveStartupSizeArgsWithEscHint(currentArgs, escHint)
			return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
		}
		nums, consumed, err := startupSizeBoundsFromRemaining(remaining)
		if err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		for _, n := range nums {
			finalArgs = append(finalArgs, strconv.Itoa(n))
		}
		return finalArgs, append([]string(nil), currentScopeTargets...), false, consumed, nil
	case "--depth":
		if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, "--depth"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if len(remaining) == 0 || (allowInteractiveCompletion && startupRemainingIsBarePlaceholderChain(remaining)) {
			if !allowInteractiveCompletion {
				return nil, append([]string(nil), currentScopeTargets...), false, 0, cli.RequiredStageValueError("--depth")
			}
			args, usedFzf, err := resolveStartupDepthArgsWithEscHint(currentArgs, escHint)
			return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
		}
		depth, err := validateStartupDepthValue(currentArgs, remaining[0])
		if err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, strconv.Itoa(depth))
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--paths":
		if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, "--paths"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
	case "--lines":
		if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, "--lines"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if len(remaining) == 0 || (allowInteractiveCompletion && startupRemainingIsBarePlaceholderChain(remaining)) {
			if allowInteractiveCompletion {
				args, usedFzf, err := resolveStartupLinesArgsWithEscHint(currentArgs, escHint)
				return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
			}
			// Bare --lines without numerics in non-interactive mode keeps
			// today's "emit numbered full file" behavior.
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		consumed := 0
		for consumed < len(remaining) {
			if _, err := strconv.Atoi(remaining[consumed]); err != nil {
				break
			}
			finalArgs = append(finalArgs, remaining[consumed])
			consumed++
		}
		return finalArgs, append([]string(nil), currentScopeTargets...), false, consumed, nil
	case "--contains":
		if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, "--contains"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if len(remaining) == 0 || (allowInteractiveCompletion && startupRemainingIsBarePlaceholderChain(remaining)) {
			if !allowInteractiveCompletion {
				return nil, append([]string(nil), currentScopeTargets...), false, 0, cli.ContainsMissingPatternError(currentArgs, len(currentArgs))
			}
			args, usedFzf, err := resolveStartupContentArgsWithEscHint(currentArgs, "--contains", escHint)
			return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, remaining[0])
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--not-contains":
		if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, "--not-contains"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if len(remaining) == 0 || (allowInteractiveCompletion && startupRemainingIsBarePlaceholderChain(remaining)) {
			if !allowInteractiveCompletion {
				return nil, append([]string(nil), currentScopeTargets...), false, 0, cli.NotContainsMissingPatternError(currentArgs, len(currentArgs))
			}
			args, usedFzf, err := resolveStartupContentArgsWithEscHint(currentArgs, "--not-contains", escHint)
			return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, remaining[0])
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--snippet":
		if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, "--snippet"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if len(remaining) == 0 || (allowInteractiveCompletion && startupRemainingIsBarePlaceholderChain(remaining)) {
			if !allowInteractiveCompletion {
				return nil, append([]string(nil), currentScopeTargets...), false, 0, cli.RequiredStageValueError("--snippet")
			}
			args, usedFzf, err := resolveStartupContentArgsWithEscHint(currentArgs, "--snippet", escHint)
			return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, remaining[0])
		consumed := 1
		if len(remaining) > 1 {
			if n, err := strconv.Atoi(remaining[1]); err == nil {
				if n < 0 || n > snippetContextMax {
					return nil, append([]string(nil), currentScopeTargets...), false, 0, newUsageError("Error: --snippet context must be between 0 and %d (got %d).\n  Use: --snippet 'REGEX' N for N lines around each match (0 = matching line only).", snippetContextMax, n)
				}
				finalArgs = append(finalArgs, strconv.Itoa(n))
				consumed = 2
			}
		}
		return finalArgs, append([]string(nil), currentScopeTargets...), false, consumed, nil
	case "--changed", "--staged", "--unstaged", "--untracked":
		if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, flag); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if err := startupValidateGitStageArgs(currentArgs, flag, choiceArgs, remaining); err != nil {
			return nil, nil, false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		if len(remaining) > 0 && !cli.IsModifierBoundaryToken(remaining[0]) {
			return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
		}
		if !resolver.GitCtx.Enabled {
			return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
		}
		diffPreview := false
		argsAfterStage, usedFzf, err := resolveStartupGitScopeArgsWithEscHint(resolver, finalArgs, startupGitStagePrompt(flag), nil, true, diffPreview, escHint)
		if err != nil {
			return nil, nil, false, 0, err
		}
		return argsAfterStage, append([]string(nil), currentScopeTargets...), usedFzf, 0, nil
	case "--changed-diff", "--staged-diff", "--unstaged-diff":
		if err := cli.ValidateCurrentScopeFlagAddition(currentArgs, flag); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		if len(remaining) > 0 && !cli.IsModifierBoundaryToken(remaining[0]) {
			return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
		}
		if !resolver.GitCtx.Enabled {
			return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
		}
		argsAfterStage, usedFzf, err := resolveStartupGitScopeArgsWithEscHint(resolver, finalArgs, startupGitStagePrompt(flag), nil, true, true, escHint)
		if err != nil {
			return nil, nil, false, 0, err
		}
		return argsAfterStage, append([]string(nil), currentScopeTargets...), usedFzf, 0, nil
	default:
		return nil, nil, false, 0, discovery.ErrSelectionCancelled
	}
}

func previousResolvedIncludeGroup(groups [][]string, before int) int {
	if before > len(groups) {
		before = len(groups)
	}
	for i := before - 1; i >= 0; i-- {
		if len(groups[i]) > 0 {
			return i
		}
	}
	return -1
}

func clearResolvedIncludeGroupsFrom(groups [][]string, start int) {
	for i := start; i < len(groups); i++ {
		groups[i] = nil
	}
}

func startupRemainingIsBarePlaceholderChain(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		if token != "--" {
			return false
		}
	}
	return true
}

func startupValidateGitStageArgs(currentArgs []string, flag string, choiceArgs, remaining []string) error {
	if flag != "--untracked" {
		return nil
	}

	hasDiff := currentScopeHasFlag(currentArgs, "--changed-diff") ||
		currentScopeHasFlag(currentArgs, "--staged-diff") ||
		currentScopeHasFlag(currentArgs, "--unstaged-diff") ||
		slices.Contains(choiceArgs[1:], "--changed-diff") ||
		slices.Contains(choiceArgs[1:], "--staged-diff") ||
		slices.Contains(choiceArgs[1:], "--unstaged-diff") ||
		(len(remaining) > 0 && (remaining[0] == "--changed-diff" || remaining[0] == "--staged-diff" || remaining[0] == "--unstaged-diff"))
	if !hasDiff {
		return nil
	}

	if currentScopeHasFlag(currentArgs, "--changed") ||
		currentScopeHasFlag(currentArgs, "--staged") ||
		currentScopeHasFlag(currentArgs, "--unstaged") {
		return nil
	}

	return cli.UntrackedDiffError()
}

func resolveStartupGitScopeArgs(resolver *discovery.Resolver, currentArgs []string, prompt string, values []string, allowInteractiveEmpty bool, diffPreview bool) ([]string, bool, error) {
	return resolveStartupGitScopeArgsWithEscHint(resolver, currentArgs, prompt, values, allowInteractiveEmpty, diffPreview, "")
}

func resolveStartupGitScopeArgsWithEscHint(resolver *discovery.Resolver, currentArgs []string, prompt string, values []string, allowInteractiveEmpty bool, diffPreview bool, escHint string) ([]string, bool, error) {
	if !resolver.GitCtx.Enabled {
		return append([]string(nil), currentArgs...), false, nil
	}

	stageFlag := "--changed"
	for i := len(currentArgs) - 1; i >= 0; i-- {
		switch currentArgs[i] {
		case "--changed", "--staged", "--unstaged", "--untracked",
			"--changed-diff", "--staged-diff", "--unstaged-diff":
			stageFlag = currentArgs[i]
			i = -1
		case "--then":
			i = -1
		}
	}

	previewCommand := ""
	if diffPreview {
		previewCommand = startupFileSetPreviewCommand(currentArgs, stageFlag, diffPreview)
	}
	stageValues, usedFzf, err := resolveStartupModifierStageValuesWithEscHint(currentArgs, stageFlag, prompt, values, allowInteractiveEmpty, previewCommand, escHint)
	if err != nil {
		return nil, false, err
	}
	if len(stageValues) == 0 {
		return append([]string(nil), currentArgs...), usedFzf, nil
	}
	if startupStageSelectionIncludesAllRow(stageValues, stageFlag) {
		return append([]string(nil), currentArgs...), usedFzf, nil
	}

	relPaths, err := startupScopeFileSetPaths(currentArgs)
	if err != nil {
		return nil, false, err
	}
	if startupStageSelectionCoversAll(stageValues, relPaths) {
		return append([]string(nil), currentArgs...), usedFzf, nil
	}

	finalArgs := append([]string(nil), currentArgs...)
	finalArgs = append(finalArgs, "--only")
	finalArgs = append(finalArgs, stageValues...)
	return finalArgs, usedFzf, nil
}

func startupGitStagePrompt(flag string) string {
	switch flag {
	case "--staged", "--staged-diff":
		return "staged> "
	case "--unstaged", "--unstaged-diff":
		return "unstaged> "
	case "--untracked":
		return "untracked> "
	default:
		return "changed> "
	}
}

func startupStageSelectionCoversAll(selected, candidates []string) bool {
	if len(selected) == 0 || len(candidates) == 0 {
		return false
	}

	seenSelected := make(map[string]struct{}, len(selected))
	for _, value := range selected {
		seenSelected[normalizeRelPath(value)] = struct{}{}
	}
	if len(seenSelected) != len(candidates) {
		return false
	}
	for _, candidate := range candidates {
		if _, ok := seenSelected[normalizeRelPath(candidate)]; !ok {
			return false
		}
	}
	return true
}

func resolveStartupModifierStageValuesWithEscHint(currentArgs []string, flag, prompt string, values []string, allowInteractiveEmpty bool, previewCommand string, escHint string) ([]string, bool, error) {
	cleanupPreview := func() {}
	defer func() {
		cleanupPreview()
	}()
	previewReady := previewCommand != ""
	ensurePreviewCommand := func() string {
		if previewReady {
			return previewCommand
		}
		previewReady = true
		previewCommand, cleanupPreview = startupCheckpointFileSetPreviewCommand(currentArgs, flag, false)
		return previewCommand
	}
	if len(values) == 0 {
		if !allowInteractiveEmpty {
			return nil, false, discovery.ErrSelectionCancelled
		}
		relPaths, err := startupScopeFileSetPaths(currentArgs)
		if err != nil {
			return nil, false, err
		}
		if len(relPaths) == 0 {
			return nil, false, discovery.ErrSelectionCancelled
		}
		selected, err := chooseManyStartupFileSetRowsWithFzf("", prompt, startupFileSetPickerHeaderWithEscHint(flag, escHint), ensurePreviewCommand(), startupFileSetRows(flag, relPaths))
		if err != nil {
			return nil, false, err
		}
		return selected, true, nil
	}

	resolvedValues := make([]string, 0, len(values))
	usedFzf := false
	var rows []startupFileSetRow
	for _, value := range values {
		keepLiteral, err := startupFileSetValueShouldStayLiteral(currentArgs, flag, value)
		if err != nil {
			return nil, false, err
		}
		if keepLiteral {
			resolvedValues = append(resolvedValues, value)
			continue
		}
		if rows == nil {
			relPaths, err := startupScopeFileSetPaths(currentArgs)
			if err != nil {
				return nil, false, err
			}
			rows = startupFileSetRows(flag, relPaths)
		}
		selected, err := chooseManyStartupFileSetRowsWithFzf(value, prompt, startupFileSetPickerHeaderWithEscHint(flag, escHint), ensurePreviewCommand(), rows)
		if err != nil {
			return nil, false, err
		}
		resolvedValues = append(resolvedValues, selected...)
		usedFzf = true
	}

	return resolvedValues, usedFzf, nil
}

func startupFileSetValueShouldStayLiteral(currentArgs []string, flag, value string) (bool, error) {
	if startupLooksLikeLiteralFileSetPattern(value) {
		return true, nil
	}
	if startupFileSetQueryMatchesExistingPath(currentArgs, value) {
		return true, nil
	}
	relPaths, err := startupScopeFileSetPaths(currentArgs)
	if err != nil {
		return false, err
	}
	for _, row := range startupFileSetRows(flag, relPaths) {
		if row.Kind == startupFileSetRowAll {
			continue
		}
		if row.Value == value {
			return true, nil
		}
	}
	return false, nil
}

func startupLooksLikeLiteralFileSetPattern(value string) bool {
	normalized := strings.ReplaceAll(value, "\\", "/")
	return strings.ContainsAny(normalized, "*?[") || strings.HasSuffix(normalized, "/")
}

func startupFileSetQueryMatchesExistingPath(currentArgs []string, value string) bool {
	cfg, err := cli.ParseArgsAllowImplicitDot(currentArgs)
	if err != nil {
		return false
	}
	trimmed := strings.TrimSuffix(value, "/")
	normalized := normalizeRelPath(trimmed)
	if normalized == "" || normalized == "." {
		return false
	}
	info, err := os.Stat(filepath.Join(cfg.WorkingDir, filepath.FromSlash(normalized)))
	if err != nil {
		return false
	}
	if strings.HasSuffix(value, "/") {
		return info.IsDir()
	}
	return true
}

func startupStageValues(tokens []string) ([]string, int) {
	values := make([]string, 0, len(tokens))
	i := 0
	for i < len(tokens) {
		if cli.IsModifierBoundaryToken(tokens[i]) {
			break
		}
		values = append(values, tokens[i])
		i++
	}
	return values, i
}

func startupStagePrompt(flag string) string {
	if flag == "--exclude" {
		return "exclude> "
	}
	return "only> "
}
