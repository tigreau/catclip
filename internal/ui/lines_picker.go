package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

// linesPickerEOFToken is the sentinel emitted by the end-line picker to
// represent the "to end of file" choice. It lives in column 1 (the search /
// returned field) of the TSV row; the preview command's inline shell case
// dispatches on this exact string to render the open-ended form.
const linesPickerEOFToken = "EOF"

type startupLinesPickerSession struct {
	CheckpointPath string
	Cleanup        func()
	MaxLines       int
}

// resolveStartupLinesArgs runs the two-stage lines picker (start line, then
// end line) and returns currentArgs extended with the chosen --lines values.
//
// Stage flow:
//   - Esc on the start picker returns discovery.ErrSelectionCancelled and the lines
//     picker exits to its caller (the modifier menu).
//   - Esc on the end picker reopens the start picker so the user can revise
//     the opening bound. This is the only two-stage backtrack in catclip
//     today; the loop here is intra-picker.
//
// Returns currentArgs+["--lines", "START"] for the open-ended choice and
// currentArgs+["--lines", "START", "END"] otherwise.
func resolveStartupLinesArgs(currentArgs []string) ([]string, bool, error) {
	return resolveStartupLinesArgsWithEscHint(currentArgs, "")
}

func resolveStartupLinesArgsWithEscHint(currentArgs []string, escHint string) ([]string, bool, error) {
	session, directArgs, err := prepareStartupLinesPickerSession(currentArgs)
	if err != nil {
		return nil, false, err
	}
	if directArgs != nil {
		return directArgs, false, nil
	}
	defer session.Cleanup()

	for {
		start, err := chooseStartupStartLineWithEscHint(session.CheckpointPath, session.MaxLines, escHint)
		if err != nil {
			return nil, true, err
		}
		end, isOpenEnd, err := chooseStartupEndLine(session.CheckpointPath, start, session.MaxLines)
		if errors.Is(err, discovery.ErrSelectionCancelled) {
			// Two-stage backtrack: reopen the start picker.
			continue
		}
		if err != nil {
			return nil, true, err
		}
		args := append(append([]string(nil), currentArgs...), "--lines", strconv.Itoa(start))
		if !isOpenEnd {
			args = append(args, strconv.Itoa(end))
		}
		return args, true, nil
	}
}

func prepareStartupLinesPickerSession(currentArgs []string) (startupLinesPickerSession, []string, error) {
	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil {
		return startupLinesPickerSession{}, nil, err
	}
	entries := discovery.EnsureEntryAbsPaths(view.Entries, view.Invocation.WorkingDir)
	if len(entries) == 0 {
		return startupLinesPickerSession{}, nil, discovery.ErrSelectionCancelled
	}
	entries = discovery.FillEntrySizes(view.Invocation.WorkingDir, entries)
	maxLines, err := maxLinesForSizedEntries(entries)
	if err != nil {
		return startupLinesPickerSession{}, nil, err
	}
	if maxLines <= 0 {
		return startupLinesPickerSession{}, nil, discovery.ErrSelectionCancelled
	}
	if maxLines == 1 {
		// Single-line files in scope. A one-row picker is friction without
		// value; emit --lines 1 directly.
		return startupLinesPickerSession{}, append(append([]string(nil), currentArgs...), "--lines", "1"), nil
	}

	checkpointPath, cleanup, err := writeLinesPickerCheckpoint(view, entries)
	if err != nil {
		return startupLinesPickerSession{}, nil, err
	}
	return startupLinesPickerSession{CheckpointPath: checkpointPath, Cleanup: cleanup, MaxLines: maxLines}, nil, nil
}

// writeLinesPickerCheckpoint writes a prediscovered SCC checkpoint file
// for the current scope's resolved entries. The preview commands in both
// stages of the lines picker read this file via --internal-prediscovered.
//
// The returned cleanup removes the entire tmpdir; it is safe to call
// multiple times.
func writeLinesPickerCheckpoint(view resolvedScopeView, entries []discovery.Entry) (string, func(), error) {
	tmpdir, err := os.MkdirTemp("", "catclip-lines-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpdir) }
	checkpointPath := filepath.Join(tmpdir, "scope.json")

	statuses := map[string]string{}
	if view.GitContext.Enabled {
		statuses, err = git.StatusMapForPathspecs(view.GitContext, discovery.GitStatusPathspecsForEntries(view.GitContext, entries))
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	if err := discovery.WriteCheckpoint(checkpointPath, view.Invocation.WorkingDir, discovery.CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    entries,
	}); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return checkpointPath, cleanup, nil
}

func chooseStartupStartLine(checkpointPath string, maxLines int) (int, error) {
	return chooseStartupStartLineWithEscHint(checkpointPath, maxLines, "")
}

func chooseStartupStartLineWithEscHint(checkpointPath string, maxLines int, escHint string) (int, error) {
	previewCmd := buildLinesPickerStartPreviewCommand(checkpointPath)
	lines := startupLinePickerLines(1, maxLines)
	selected, err := chooseLineWithFzf("start-line> ", linesPickerStartHeaderWithEscHint(escHint), lines, previewCmd)
	if err != nil {
		return 0, err
	}
	n, err := parseLinesPickerToken(selected)
	if err != nil {
		return 0, err
	}
	if n < 1 || n > maxLines {
		return 0, discovery.ErrSelectionCancelled
	}
	return n, nil
}

func chooseStartupEndLine(checkpointPath string, startLine, maxLines int) (int, bool, error) {
	return chooseStartupEndLineWithEscHint(checkpointPath, startLine, maxLines, "")
}

func chooseStartupEndLineWithEscHint(checkpointPath string, startLine, maxLines int, escHint string) (int, bool, error) {
	previewCmd := buildLinesPickerEndPreviewCommand(checkpointPath, startLine)
	lines := startupLineEndPickerLines(startLine, maxLines)
	selected, err := chooseLineWithFzf("end-line> ", linesPickerEndHeaderWithEscHint(escHint), lines, previewCmd)
	if err != nil {
		return 0, false, err
	}
	if linesPickerSelectionIsEOF(selected) {
		return 0, true, nil
	}
	n, err := parseLinesPickerToken(selected)
	if err != nil {
		return 0, false, err
	}
	if n < startLine || n > maxLines {
		return 0, false, discovery.ErrSelectionCancelled
	}
	return n, false, nil
}

func startupLinePickerLines(startInclusive, endInclusive int) []string {
	if endInclusive < startInclusive {
		return nil
	}
	out := make([]string, 0, endInclusive-startInclusive+1)
	for n := startInclusive; n <= endInclusive; n++ {
		value := strconv.Itoa(n)
		label := "Line " + value
		out = append(out, strings.Join([]string{value, value, label}, "\t"))
	}
	return out
}

func startupLineEndPickerLines(startLine, maxLines int) []string {
	if maxLines < startLine {
		return nil
	}
	rows := make([]string, 0, maxLines-startLine+2)
	// EOF row: column 1 (search/return) is the sentinel; column 2 (preview
	// substitution into {2}) is the literal EOF — the shell case in the
	// preview command dispatches on it. Column 3 is the human label.
	rows = append(rows, strings.Join([]string{linesPickerEOFToken, linesPickerEOFToken, "[to end of file]"}, "\t"))
	rows = append(rows, startupLinePickerLines(startLine, maxLines)...)
	return rows
}

func parseLinesPickerToken(selected string) (int, error) {
	value := selected
	if tab := strings.IndexByte(selected, '\t'); tab >= 0 {
		value = selected[:tab]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, discovery.ErrSelectionCancelled
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, discovery.ErrSelectionCancelled
	}
	return n, nil
}

func linesPickerSelectionIsEOF(selected string) bool {
	value := selected
	if tab := strings.IndexByte(selected, '\t'); tab >= 0 {
		value = selected[:tab]
	}
	return strings.TrimSpace(value) == linesPickerEOFToken
}

func chooseLineWithFzf(prompt, header string, lines []string, previewCommand string) (string, error) {
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return "", err
	}
	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Prompt:         prompt,
		WithNth:        "1,3",
		Nth:            "1",
		Header:         header,
		PreviewCommand: previewCommand,
		NoSort:         true,
		Exact:          true,
		Lines:          lines,
	}))
	if err == nil {
		if len(result.Matches) == 0 {
			return "", discovery.ErrSelectionCancelled
		}
		return strings.TrimSpace(result.Matches[0]), nil
	}
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return "", discovery.ErrSelectionCancelled
	}
	return "", err
}

func linesPickerStartHeader() string {
	return linesPickerStartHeaderWithEscHint("")
}

func linesPickerStartHeaderWithEscHint(escHint string) string {
	return discovery.PickerHeader(
		"Pick the start line.",
		"Hover a line to preview from there to EOF.",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint)),
	)
}

func linesPickerEndHeader() string {
	return linesPickerEndHeaderWithEscHint("")
}

func linesPickerEndHeaderWithEscHint(escHint string) string {
	controls := "[Up/Down] move  [Enter] confirm  [Esc] back to start"
	if escHint != "" {
		controls = fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint))
	}
	return discovery.PickerHeader(
		"Pick the end line.",
		"[to end of file] keeps the slice open-ended.",
		controls,
	)
}

// buildLinesPickerStartPreviewCommand wires the start-line picker's preview
// pane to the SCC prediscovered checkpoint, routing through the
// --internal-lines-preview emit path. {2} resolves to the hovered line
// number; catclip applies --lines {2} (open-ended) to the already-resolved
// entry set and emits actual file content. The preview pane is byte-
// faithful to what the chosen slice will paste.
//
// We do NOT use the tree-document renderer here: tree payloads carry
// metadata-only entries, not file bodies. The lines preview's whole purpose is
// to show the bodies, so we display the raw emit directly.
func buildLinesPickerStartPreviewCommand(checkpointPath string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}
	parts := []string{
		discovery.ShellQuoteArg(self),
		"--quiet",
		"--internal-prediscovered", discovery.ShellQuoteArg(checkpointPath),
		"--internal-lines-preview",
		"--lines", "{2}",
	}
	return strings.Join(parts, " ")
}

// buildLinesPickerEndPreviewCommand wires the end-line picker.
// It directly passes {2} to --lines. The CLI argument parser accepts
// the EOF sentinel and treats it identically to an open-ended slice.
// Output is raw emit text; see start helper above.
func buildLinesPickerEndPreviewCommand(checkpointPath string, startLine int) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}
	start := strconv.Itoa(startLine)
	parts := []string{
		discovery.ShellQuoteArg(self),
		"--quiet",
		"--internal-prediscovered", discovery.ShellQuoteArg(checkpointPath),
		"--internal-lines-preview",
		"--lines", start, "{2}",
	}
	return strings.Join(parts, " ")
}
