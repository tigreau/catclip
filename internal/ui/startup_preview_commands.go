package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

func startupFileSetPickerHeader(flag string) string {
	return startupFileSetPickerHeaderWithEscHint(flag, "")
}

func startupFileSetPickerHeaderWithEscHint(flag, escHint string) string {
	controls := fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  %s", platform.MultiSelectToggleAllKey(), startupEscLabel(escHint))
	switch flag {
	case "--exclude":
		return discovery.PickerHeader(
			"Remove files whose paths match.",
			"Type a path pattern.",
			controls,
		)
	case "--changed", "--changed-diff":
		firstLine := "Pick git-changed files."
		if flag == "--changed-diff" {
			firstLine = "Pick diffs for git-changed files."
		}
		return discovery.PickerHeader(
			firstLine,
			"Type a path to narrow the list.",
			controls,
		)
	case "--staged", "--staged-diff":
		firstLine := "Pick git-staged files."
		if flag == "--staged-diff" {
			firstLine = "Pick diffs for git-staged files."
		}
		return discovery.PickerHeader(
			firstLine,
			"Type a path to narrow the list.",
			controls,
		)
	case "--unstaged", "--unstaged-diff":
		firstLine := "Pick git-unstaged files."
		if flag == "--unstaged-diff" {
			firstLine = "Pick diffs for git-unstaged files."
		}
		return discovery.PickerHeader(
			firstLine,
			"Type a path to narrow the list.",
			controls,
		)
	case "--untracked":
		return discovery.PickerHeader(
			"Pick untracked files.",
			"Type a path to narrow the list.",
			controls,
		)
	default:
		return discovery.PickerHeader(
			"Keep only files whose paths match.",
			"Type a path pattern.",
			controls,
		)
	}
}

func startupScopeFileSetPaths(currentArgs []string) ([]string, error) {
	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(view.Entries))
	relPaths := make([]string, 0, len(view.Entries))
	for _, entry := range view.Entries {
		if entry.RelPath == "" {
			continue
		}
		if _, ok := seen[entry.RelPath]; ok {
			continue
		}
		seen[entry.RelPath] = struct{}{}
		relPaths = append(relPaths, entry.RelPath)
	}
	sort.Strings(relPaths)
	return relPaths, nil
}

func startupFileSetRows(flag string, relPaths []string) []startupFileSetRow {
	rows := make([]startupFileSetRow, 0, len(relPaths)+8)
	if allRow := startupAllFileSetRow(flag); allRow != nil {
		rows = append(rows, *allRow)
	}
	if flag == "--only" || flag == "--exclude" {
		// fzf's current bottom-prompt layout renders earlier input rows closest
		// to the prompt. Prepend synthetic pattern rows here so they stay near
		// the prompt at the bottom instead of floating to the top of the window.
		rows = append(rows, startupExtensionPatternRows(relPaths)...)
	}
	rows = append(rows, startupFilePathRows(relPaths)...)
	return rows
}

func startupFileSetPreviewCommand(currentArgs []string, flag string, diffPreview bool) string {
	if diffPreview {
		return discovery.FzfDiffFilePreviewCommand(currentArgs)
	}
	if activeDiffFlag := currentScopeDiffPreviewFlag(currentArgs); activeDiffFlag != "" {
		return discovery.FzfDiffFilePreviewCommand(currentArgs)
	}
	// Non-diff file-set previews require a checkpoint so fzf can hand the
	// selected rows to one bounded file argument. If checkpoint setup fails,
	// omit the preview instead of falling back to expanding every marked path
	// into argv and risking E2BIG.
	return ""
}

// startupCheckpointFileSetPreviewCommand is the SCC path for free-form file-set
// picker previews. If a short-lived checkpoint cannot be written, the picker
// remains usable without a live preview; it must not fall back to an
// unbounded selected-path argument list.
func startupCheckpointFileSetPreviewCommand(currentArgs []string, flag string, diffPreview bool) (string, func()) {
	benchEnabled := platform.InternalBenchEnabled()
	finishBench := func(...string) {}
	if benchEnabled {
		finishBench = platform.InternalBenchSpan("ui.startup.file_set_preview.prepare",
			"flag", flag,
			"argc", platform.InternalBenchInt(len(currentArgs)),
			"diff", platform.InternalBenchBool(diffPreview),
		)
	}
	finish := func(fields ...string) {
		if benchEnabled {
			finishBench(fields...)
		}
	}
	fallback := func() string {
		return startupFileSetPreviewCommand(currentArgs, flag, diffPreview)
	}
	if diffPreview || currentScopeDiffPreviewFlag(currentArgs) != "" {
		cmd := fallback()
		if benchEnabled {
			finish("route", "diff-fallback", "preview", platform.InternalBenchBool(cmd != ""))
		}
		return cmd, func() {}
	}
	switch flag {
	case "--only", "--exclude":
	case "--changed", "--staged", "--unstaged", "--untracked":
	default:
		cmd := fallback()
		if benchEnabled {
			finish("route", "unsupported-fallback", "preview", platform.InternalBenchBool(cmd != ""))
		}
		return cmd, func() {}
	}

	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil || len(view.Entries) == 0 {
		cmd := fallback()
		if benchEnabled {
			finish(
				"route", "scope-fallback",
				"err", platform.InternalBenchError(err),
				"entries", platform.InternalBenchInt(len(view.Entries)),
				"preview", platform.InternalBenchBool(cmd != ""),
			)
		}
		return cmd, func() {}
	}
	previewFlag := flag
	switch flag {
	case "--changed", "--staged", "--unstaged", "--untracked":
		previewFlag = "--only"
	}
	cmd, tmpdir := buildFileSetCheckpointPreview(currentArgs, view, previewFlag)
	if cmd == "" {
		cmd = fallback()
		if benchEnabled {
			finish(
				"route", "checkpoint-fallback",
				"entries", platform.InternalBenchInt(len(view.Entries)),
				"preview", platform.InternalBenchBool(cmd != ""),
			)
		}
		return cmd, func() {}
	}
	if benchEnabled {
		finish(
			"route", "checkpoint",
			"entries", platform.InternalBenchInt(len(view.Entries)),
			"preview", "true",
		)
	}
	return cmd, func() {
		_ = os.RemoveAll(tmpdir)
	}
}

func buildFileSetCheckpointPreview(currentArgs []string, view resolvedScopeView, previewFlag string) (cmd string, tmpdir string) {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return "", ""
	}
	buildCommand := func(path string) string {
		parts := []string{
			discovery.ShellQuoteArg(self), "--quiet", "--internal-tree-preview",
			"--internal-prediscovered", discovery.ShellQuoteArg(path),
		}
		if previewFlag != "" {
			parts = append(parts,
				"--internal-file-set-selection", "{+f}",
				"--internal-file-set-stage", strings.TrimPrefix(previewFlag, "--"),
			)
		}
		parts = append(parts,
			"--internal-tree-target", "{3}",
			"--internal-tree-kind", "{4}",
			"--internal-tree-state", "{5}",
		)
		return strings.Join(parts, " ")
	}
	if currentArgs != nil {
		if cachedPath, ok := scopeViewMemoCheckpointPath(currentArgs); ok {
			return buildCommand(cachedPath), ""
		}
	}
	statuses := map[string]string{}
	if view.GitContext.Enabled {
		statuses, err = git.StatusMapForPathspecs(view.GitContext, discovery.GitStatusPathspecsForEntries(view.GitContext, view.Entries))
		if err != nil {
			return "", ""
		}
	}
	if currentArgs != nil {
		cachedPath, owned, cacheErr := scopeViewMemoCheckpoint(currentArgs, view, statuses)
		if owned {
			if cacheErr != nil {
				return "", ""
			}
			return buildCommand(cachedPath), ""
		}
	}
	tmpdir, err = os.MkdirTemp("", "catclip-scc-*")
	if err != nil {
		return "", ""
	}
	checkpointPath := filepath.Join(tmpdir, "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, view.Invocation.WorkingDir, discovery.CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    view.Entries,
	}); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", ""
	}

	return buildCommand(checkpointPath), tmpdir
}

// startupModifierCurrentScopePreviewCommand prepares the static modifier-menu
// preview as a retained state checkpoint. Only checkpoint preparation blocks
// opening fzf; the child decodes and renders the tree after the menu is live.
// A same-state revisit and later dynamic pickers reuse the same checkpoint.
func startupModifierCurrentScopePreviewCommand(currentArgs []string, state startupCurrentScopeState, view resolvedScopeView) (cmd string, tmpdir string) {
	if !state.Known || state.Empty || len(state.Scopes) == 0 {
		return "", ""
	}
	if len(view.Entries) == 0 {
		// State says non-empty scope but the view has no entries: a race or a
		// stale view; skip the preview rather than write an empty checkpoint.
		return "", ""
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return "", ""
	}
	buildCommand := func(checkpointPath string) string {
		parts := []string{
			discovery.ShellQuoteArg(self), "--quiet", "--internal-tree-preview",
			"--internal-prediscovered", discovery.ShellQuoteArg(checkpointPath),
		}
		parts = append(parts, command.CanonicalScopeArgs(state.Scopes[len(state.Scopes)-1])...)
		return strings.Join(parts, " ")
	}
	buildTargetInventoryCommand := func(inventoryPath string) string {
		parts := []string{
			discovery.ShellQuoteArg(self), "--quiet", "--internal-tree-preview",
			"--internal-target-inventory", discovery.ShellQuoteArg(inventoryPath),
		}
		if view.Invocation.WithBinaries {
			parts = append(parts, "--with-binaries")
		}
		parts = append(parts, command.CanonicalScopeArgs(state.Scopes[len(state.Scopes)-1])...)
		return strings.Join(parts, " ")
	}

	// The first stage-free filter screen can consume the target picker's sealed
	// compact projection directly. This avoids materializing an equivalent JSON
	// checkpoint before picker.Run. Derived states and dynamic pickers continue
	// through the general retained checkpoint path below.
	currentScope := state.Scopes[len(state.Scopes)-1]
	if currentArgs != nil && len(currentScope.Stages) == 0 &&
		!currentScope.Paths && currentScope.OutputMode() == command.EntryModeFull {
		if inventoryPath, owned, inventoryErr := scopeViewMemoTargetPreviewInventory(currentArgs); owned && inventoryErr == nil {
			return buildTargetInventoryCommand(inventoryPath), ""
		}
	}

	if currentArgs != nil {
		if checkpointPath, ok := scopeViewMemoCheckpointPath(currentArgs); ok {
			return buildCommand(checkpointPath), ""
		}
	}

	statuses := map[string]string{}
	if view.GitContext.Enabled {
		finishStatusBench := platform.InternalBenchSpan("ui.startup.modifier_picker.git_status_map",
			"entries", platform.InternalBenchInt(len(view.Entries)),
		)
		if state.GitStatusMap != nil {
			statuses = state.GitStatusMap
			finishStatusBench("err", "false", "statuses", platform.InternalBenchInt(len(statuses)), "cached", "true")
		} else {
			statuses, err = git.StatusMapForPathspecs(view.GitContext, discovery.GitStatusPathspecsForEntries(view.GitContext, view.Entries))
			finishStatusBench(
				"err", platform.InternalBenchError(err),
				"statuses", platform.InternalBenchInt(len(statuses)),
				"cached", "false",
			)
			if err != nil {
				return "", ""
			}
		}
	}
	if currentArgs != nil {
		checkpointPath, owned, cacheErr := scopeViewMemoCheckpoint(currentArgs, view, statuses)
		if owned {
			if cacheErr != nil {
				return "", ""
			}
			return buildCommand(checkpointPath), ""
		}
	}

	tmpdir, err = os.MkdirTemp("", "catclip-modifier-*")
	if err != nil {
		return "", ""
	}
	checkpointPath := filepath.Join(tmpdir, "scope.json")
	finishWriteBench := platform.InternalBenchSpan("ui.startup.modifier_picker.write_checkpoint",
		"entries", platform.InternalBenchInt(len(view.Entries)),
	)
	err = discovery.WriteCheckpoint(checkpointPath, view.Invocation.WorkingDir, discovery.CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    view.Entries,
		NoIgnore:   view.Scope.NoIgnore,
	})
	finishWriteBench("err", platform.InternalBenchError(err))
	if err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", ""
	}
	return buildCommand(checkpointPath), tmpdir
}
