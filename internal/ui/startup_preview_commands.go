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
	previewFlag := flag
	switch flag {
	case "--changed", "--staged", "--unstaged", "--untracked":
		// Git file pickers narrow the already-selected git set. Preview should
		// mirror the final lowering to `--only`, not append a bogus value to the
		// git selector itself.
		previewFlag = "--only"
	}
	return discovery.FzfFileSetPreviewCommand(currentArgs, previewFlag)
}

// startupCheckpointFileSetPreviewCommand is the SCC path for free-form file-set
// picker previews. It falls back to startupFileSetPreviewCommand only when a
// short-lived checkpoint cannot be written.
func startupCheckpointFileSetPreviewCommand(currentArgs []string, flag string, diffPreview bool) (string, func()) {
	fallback := func() string {
		return startupFileSetPreviewCommand(currentArgs, flag, diffPreview)
	}
	if diffPreview || currentScopeDiffPreviewFlag(currentArgs) != "" {
		return fallback(), func() {}
	}
	switch flag {
	case "--only", "--exclude":
	case "--changed", "--staged", "--unstaged", "--untracked":
	default:
		return fallback(), func() {}
	}

	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil || len(view.Entries) == 0 {
		return fallback(), func() {}
	}
	previewFlag := flag
	switch flag {
	case "--changed", "--staged", "--unstaged", "--untracked":
		previewFlag = "--only"
	}
	cmd, tmpdir := buildFileSetCheckpointPreview(view, previewFlag)
	if cmd == "" {
		return fallback(), func() {}
	}
	return cmd, func() {
		_ = os.RemoveAll(tmpdir)
	}
}

func buildFileSetCheckpointPreview(view resolvedScopeView, previewFlag string) (cmd string, tmpdir string) {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return "", ""
	}
	tmpdir, err = os.MkdirTemp("", "catclip-scc-*")
	if err != nil {
		return "", ""
	}
	checkpointPath := filepath.Join(tmpdir, "scope.json")
	statuses := map[string]string{}
	if view.GitContext.Enabled {
		statuses, err = git.StatusMapForPathspecs(view.GitContext, discovery.GitStatusPathspecsForEntries(view.GitContext, view.Entries))
		if err != nil {
			_ = os.RemoveAll(tmpdir)
			return "", ""
		}
	}
	if err := discovery.WriteCheckpoint(checkpointPath, view.Invocation.WorkingDir, discovery.CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    view.Entries,
	}); err != nil {
		_ = os.RemoveAll(tmpdir)
		return "", ""
	}

	parts := []string{discovery.ShellQuoteArg(self), "--quiet", "--internal-tree-preview", "--internal-prediscovered", discovery.ShellQuoteArg(checkpointPath)}
	if previewFlag != "" {
		parts = append(parts, previewFlag, "{+2}")
	}
	parts = append(parts,
		"--internal-tree-target", "{3}",
		"--internal-tree-kind", "{4}",
		"--internal-tree-state", "{5}",
	)
	return strings.Join(parts, " "), tmpdir
}

// startupModifierCurrentScopePreviewCommand is a static start:preview for the
// modifier menu. It is pinned once when the menu opens and is not a per-keystroke
// free-form refinement, so it is an explicit SCC exemption.
//
// Returns (cmd, tmpdir). The caller must os.RemoveAll(tmpdir) after fzf exits
// (or immediately when cmd is ""). The checkpoint hand-off matters: without it
// the child runs ~1.3s of search.rg.text_files re-discovery on every catclip
// startup, which the Windows latency trace exposed in v0.6.0
// (see docs/versions/v0.6.1/reports/ACTIVE_NOTE_windows_interactive_latency_findings.md
// Finding 1). All other picker previews use --internal-prediscovered; this one
// shipped without it.
func startupModifierCurrentScopePreviewCommand(state startupCurrentScopeState, view resolvedScopeView) (cmd string, tmpdir string) {
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

	tmpdir, err = os.MkdirTemp("", "catclip-modifier-*")
	if err != nil {
		return "", ""
	}
	checkpointPath := filepath.Join(tmpdir, "scope.json")
	statuses := map[string]string{}
	if view.GitContext.Enabled {
		if state.GitStatusMap != nil {
			// Reuse the porcelain map already collected by
			// startupCurrentScopeStateForArgs (single git invocation
			// per parent flow). Trace label kept for continuity but
			// records the cache hit explicitly.
			finishStatusBench := platform.InternalBenchSpan("ui.startup.modifier_picker.git_status_map",
				"entries", platform.InternalBenchInt(len(view.Entries)),
			)
			statuses = state.GitStatusMap
			finishStatusBench(
				"err", "false",
				"statuses", platform.InternalBenchInt(len(statuses)),
				"cached", "true",
			)
		} else {
			finishStatusBench := platform.InternalBenchSpan("ui.startup.modifier_picker.git_status_map",
				"entries", platform.InternalBenchInt(len(view.Entries)),
			)
			statuses, err = git.StatusMapForPathspecs(view.GitContext, discovery.GitStatusPathspecsForEntries(view.GitContext, view.Entries))
			finishStatusBench(
				"err", platform.InternalBenchError(err),
				"statuses", platform.InternalBenchInt(len(statuses)),
				"cached", "false",
			)
			if err != nil {
				_ = os.RemoveAll(tmpdir)
				return "", ""
			}
		}
	}
	finishWriteBench := platform.InternalBenchSpan("ui.startup.modifier_picker.write_checkpoint",
		"entries", platform.InternalBenchInt(len(view.Entries)),
	)
	werr := discovery.WriteCheckpoint(checkpointPath, view.Invocation.WorkingDir, discovery.CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    view.Entries,
	})
	finishWriteBench("err", platform.InternalBenchError(werr))
	if werr != nil {
		_ = os.RemoveAll(tmpdir)
		return "", ""
	}

	// Modifier menu pins one static preview for the current scope (no
	// per-bucket / per-target args, unlike the file-set picker), so just the
	// prediscovered checkpoint flag plus the canonical scope tail.
	parts := []string{discovery.ShellQuoteArg(self), "--quiet", "--internal-tree-preview", "--internal-prediscovered", discovery.ShellQuoteArg(checkpointPath)}
	scopeArgs := command.CanonicalScopeArgs(state.Scopes[len(state.Scopes)-1])
	parts = append(parts, scopeArgs...)
	return strings.Join(parts, " "), tmpdir
}
