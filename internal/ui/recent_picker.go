package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

const (
	recentPickerSortAllToken      = "all"
	recentPickerPreviewAgeWidth   = 10
	recentPickerPreviewDataPrefix = "catclip-recent-preview-"
)

type recentPreviewEntry struct {
	RelPath string
	ModTime time.Time
}

func resolveStartupRecentPickerArgs(currentArgs []string, query string) ([]string, error) {
	return resolveStartupRecentPickerArgsWithEscHint(currentArgs, query, "")
}

func resolveStartupRecentPickerArgsWithEscHint(currentArgs []string, query string, escHint string) ([]string, error) {
	entries, err := startupRecentPickerEntries(currentArgs)
	if err != nil {
		return nil, err
	}

	dataPath, err := writeRecentPreviewData(entries)
	if err != nil {
		return nil, err
	}
	defer os.Remove(dataPath)

	result, err := chooseRecentWithFzf(query, startupRecentPickerLines(entries), dataPath, escHint)
	if err != nil {
		return nil, err
	}
	return applyStartupRecentSelection(currentArgs, result)
}

func startupRecentPickerEntries(currentArgs []string) ([]discovery.Entry, error) {
	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil {
		return nil, err
	}
	if len(view.Scopes) == 0 {
		return nil, nil
	}
	entries, ok, err := retainedScopeViewEntriesWithReadyMetadata(view)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("internal error: recent picker scope is missing retained metadata")
	}
	return discovery.ApplyRecentStage(entries, view.Invocation.WorkingDir, nil)
}

func startupRecentPickerLines(entries []discovery.Entry) []string {
	return startupRecentPickerLinesAt(entries, time.Now())
}

func startupRecentPickerLinesAt(entries []discovery.Entry, now time.Time) []string {
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, strings.Join([]string{"[sort all by recent]", recentPickerSortAllToken, ""}, "\t"))
	if len(entries) == 0 {
		return lines
	}

	for i := range entries {
		value := strconv.Itoa(i + 1)
		lines = append(lines, strings.Join([]string{value, value, recentPickerCutoffLabel(entries[i], now)}, "\t"))
	}
	return lines
}

func chooseRecentWithFzf(query string, lines []string, dataPath string, escHint ...string) (picker.Result, error) {
	hint := ""
	if len(escHint) > 0 {
		hint = escHint[0]
	}
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return picker.Result{}, err
	}

	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          strings.TrimSpace(query),
		Prompt:         "recent> ",
		WithNth:        "1,3",
		Nth:            "1",
		Header:         recentPickerHeaderWithEscHint(hint),
		PreviewCommand: recentPickerPreviewCommand(dataPath),
		NoSort:         true,
		Exact:          true,
		Lines:          lines,
	}))
	if err == nil {
		return result, nil
	}
	if err == picker.ErrSelectionCancelled {
		return picker.Result{}, discovery.ErrSelectionCancelled
	}
	return picker.Result{}, err
}

func applyStartupRecentSelection(currentArgs []string, result picker.Result) ([]string, error) {
	finalArgs := append(append([]string(nil), currentArgs...), "--recent")

	selection := recentPickerSortAllToken
	if len(result.Matches) > 0 {
		selection = strings.TrimSpace(result.Matches[0])
	}
	if selection == "" || selection == recentPickerSortAllToken {
		return finalArgs, nil
	}

	limit, err := cli.ParseRecentLimitToken(selection)
	if err != nil {
		return nil, discovery.ErrSelectionCancelled
	}
	return append(finalArgs, strconv.Itoa(limit)), nil
}

func recentPickerHeaderWithEscHint(escHint string) string {
	return discovery.PickerHeader(
		"Pick recent files.",
		"Type a number to choose how many to keep.",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint)),
	)
}

func recentPickerCutoffLabel(entry discovery.Entry, now time.Time) string {
	return "up to " + formatFinderModifiedLabel(now, entry.ModTime)
}

// formatFinderModifiedLabel, sameCalendarDay, and formatRecentAge moved to
// date_format.go (the shared date/time formatting tool).

func recentPickerPreviewCommand(dataPath string) string {
	if strings.TrimSpace(dataPath) == "" {
		return ""
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{
		discovery.ShellQuoteArg(self),
		"--quiet",
		"--internal-recent-preview",
		"--internal-recent-data", discovery.ShellQuoteArg(dataPath),
		"--internal-recent-selection", "{2}",
	}
	return strings.Join(parts, " ")
}

func writeRecentPreviewData(entries []discovery.Entry) (string, error) {
	f, err := os.CreateTemp("", recentPickerPreviewDataPrefix+"*.tsv")
	if err != nil {
		return "", err
	}

	path := f.Name()
	w := bufio.NewWriter(f)
	for _, entry := range entries {
		if _, err := fmt.Fprintf(w, "%d\t%s\n", entry.ModTime.UnixNano(), entry.RelPath); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return "", err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

type recentPreviewConfig struct {
	DataPath string
	Selected string
}

func RecentPreviewConfigFromParsedCommand(cfg command.Parsed) recentPreviewConfig {
	return recentPreviewConfig{
		DataPath: cfg.RecentData,
		Selected: cfg.RecentSelect,
	}
}

func RunInternalRecentPreview(cfg recentPreviewConfig, stdout io.Writer) error {
	finishBench := platform.InternalBenchSpan("ui.internal.recent_preview",
		"selected", cfg.Selected,
	)
	finishReadBench := platform.InternalBenchSpan("ui.internal.recent_preview.read_data")
	entries, err := readRecentPreviewData(cfg.DataPath)
	finishReadBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(entries)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	finishRenderBench := platform.InternalBenchSpan("ui.internal.recent_preview.render",
		"entries", platform.InternalBenchInt(len(entries)),
	)
	rendered := renderRecentPreview(entries, cfg.Selected, time.Now())
	finishRenderBench("bytes", platform.InternalBenchInt(len(rendered)))
	_, err = io.WriteString(stdout, rendered)
	finishBench("err", platform.InternalBenchError(err))
	return err
}

func readRecentPreviewData(path string) ([]recentPreviewEntry, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	entries := make([]recentPreviewEntry, 0, 32)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		nanos, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, err
		}
		entries = append(entries, recentPreviewEntry{
			RelPath: parts[1],
			ModTime: time.Unix(0, nanos),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func renderRecentPreview(entries []recentPreviewEntry, selection string, now time.Time) string {
	selection = strings.TrimSpace(selection)

	requestedLimit, sortAll, valid := resolveRecentPreviewSelection(selection, len(entries))
	var b strings.Builder

	if !valid {
		b.WriteString("Select [sort all by recent] or a numeric recent limit.\n")
		b.WriteString("Type to filter the numeric choices, then press Enter on the row you want.")
		return b.String()
	}

	if len(entries) == 0 {
		b.WriteString("No files matched in the current scope.\n")
		if sortAll {
			b.WriteString("Enter still adds bare --recent, but the scope is empty.")
		} else {
			fmt.Fprintf(&b, "Enter would apply --recent %d, but there are no files to rank.", requestedLimit)
		}
		return b.String()
	}

	displayCount := len(entries)
	heading := "Sort all files by recent"
	if !sortAll {
		displayCount = requestedLimit
		if displayCount > len(entries) {
			displayCount = len(entries)
		}
		heading = fmt.Sprintf("Top %d recent files", displayCount)
	}

	rankWidth := len(strconv.Itoa(len(entries)))
	previewRows := displayCount

	b.WriteString(heading)
	b.WriteString("\n\n")
	for i := 0; i < previewRows; i++ {
		fmt.Fprintf(&b, "%*d  %-*s  %s\n", rankWidth, i+1, recentPickerPreviewAgeWidth, formatRecentAge(now, entries[i].ModTime), entries[i].RelPath)
	}
	if previewRows == 0 {
		b.WriteString("(no files)\n")
	}

	b.WriteString("\n")
	switch {
	case sortAll && len(entries) > previewRows:
		fmt.Fprintf(&b, "Showing the top %d of %d files. Enter keeps the whole scope newest-first.", previewRows, len(entries))
	case sortAll:
		fmt.Fprintf(&b, "%d files in the current scope. Enter keeps them all newest-first.", len(entries))
	case displayCount > previewRows:
		fmt.Fprintf(&b, "Showing the first %d of %d selected files. Enter applies --recent %d.", previewRows, displayCount, requestedLimit)
	default:
		fmt.Fprintf(&b, "%d of %d files selected. Enter applies --recent %d.", displayCount, len(entries), requestedLimit)
	}
	return strings.TrimRight(b.String(), "\n")
}

func resolveRecentPreviewSelection(selection string, total int) (limit int, sortAll bool, valid bool) {
	switch strings.TrimSpace(selection) {
	case "", recentPickerSortAllToken:
		return total, true, true
	default:
		limit, err := strconv.Atoi(strings.TrimSpace(selection))
		if err != nil || limit <= 0 {
			return total, true, true
		}
		return limit, false, true
	}
}
