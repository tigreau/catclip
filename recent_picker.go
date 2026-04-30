package catclip

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/picker"
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
	entries, err := startupRecentPickerEntries(currentArgs)
	if err != nil {
		return nil, err
	}

	dataPath, err := writeRecentPreviewData(entries)
	if err != nil {
		return nil, err
	}
	defer os.Remove(dataPath)

	result, err := chooseRecentWithFzf(query, startupRecentPickerLines(entries), dataPath)
	if err != nil {
		return nil, err
	}
	return applyStartupRecentSelection(currentArgs, result)
}

func startupRecentPickerEntries(currentArgs []string) ([]fileEntry, error) {
	cfg, err := parseArgsAllowImplicitDot(currentArgs)
	if err != nil {
		return nil, err
	}
	scopeSpecs := configCommandScopes(cfg)
	if len(scopeSpecs) == 0 {
		return nil, nil
	}

	scopeIndex := len(scopeSpecs) - 1
	currentScope := executionScopeFromCommandScopeSpec(scopeSpecs[scopeIndex])

	gitCtx := detectGitContext(cfg.WorkingDir)
	baseRules, err := loadIgnoreRules()
	if err != nil {
		return nil, err
	}

	entries, _, _, _, err := evaluateScope(cfg, gitCtx, scopeIndex, currentScope, baseRules, io.Discard, colorPalette{})
	if err != nil {
		return nil, err
	}
	return applyRecentStage(entries, cfg.WorkingDir, nil)
}

func startupRecentPickerLines(entries []fileEntry) []string {
	return startupRecentPickerLinesAt(entries, time.Now())
}

func startupRecentPickerLinesAt(entries []fileEntry, now time.Time) []string {
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

func chooseRecentWithFzf(query string, lines []string, dataPath string) (picker.Result, error) {
	bin, err := fuzzyResolverBinary()
	if err != nil {
		return picker.Result{}, err
	}

	stopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          strings.TrimSpace(query),
		Prompt:         "recent> ",
		WithNth:        "1,3",
		Nth:            "1",
		Header:         recentPickerHeader(),
		PreviewCommand: recentPickerPreviewCommand(dataPath),
		NoSort:         true,
		Exact:          true,
		Lines:          lines,
	}))
	if err == nil {
		return result, nil
	}
	if err == picker.ErrSelectionCancelled {
		return picker.Result{}, errSelectionCancelled
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

	limit, err := parseRecentLimitToken(selection)
	if err != nil {
		return nil, errSelectionCancelled
	}
	return append(finalArgs, strconv.Itoa(limit)), nil
}

func recentPickerHeader() string {
	return pickerHeader(
		"Pick recent files.",
		"Type a number to choose how many to keep.",
		"[Up/Down] move  [Enter] confirm  [Esc] cancel",
	)
}

func recentPickerCutoffLabel(entry fileEntry, now time.Time) string {
	return "up to " + formatFinderModifiedLabel(now, entry.ModTime)
}

func formatFinderModifiedLabel(now, modTime time.Time) string {
	if modTime.IsZero() {
		return "unknown date"
	}

	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	modTime = modTime.In(loc)

	switch {
	case sameCalendarDay(now, modTime):
		return "Today at " + modTime.Format("3:04 PM")
	case sameCalendarDay(now.AddDate(0, 0, -1), modTime):
		return "Yesterday at " + modTime.Format("3:04 PM")
	case now.Year() == modTime.Year() && now.Sub(modTime) < 7*24*time.Hour:
		return modTime.Format("Monday at 3:04 PM")
	case now.Year() == modTime.Year():
		return modTime.Format("Jan 2 at 3:04 PM")
	default:
		return modTime.Format("Jan 2, 2006 at 3:04 PM")
	}
}

func sameCalendarDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func recentPickerPreviewCommand(dataPath string) string {
	if strings.TrimSpace(dataPath) == "" {
		return ""
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{
		shellQuoteArg(self),
		"--quiet",
		"--internal-recent-preview",
		"--internal-recent-data", shellQuoteArg(dataPath),
		"--internal-recent-selection", "{2}",
	}
	return strings.Join(parts, " ")
}

func writeRecentPreviewData(entries []fileEntry) (string, error) {
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

func runInternalRecentPreview(cfg runConfig, stdout io.Writer) error {
	entries, err := readRecentPreviewData(cfg.RecentData)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, renderRecentPreview(entries, cfg.RecentSelect, time.Now()))
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

func formatRecentAge(now, modTime time.Time) string {
	if modTime.IsZero() {
		return "unknown"
	}

	if now.Before(modTime) {
		return "just now"
	}

	d := now.Sub(modTime)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d/(7*24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d/(30*24*time.Hour)))
	default:
		return fmt.Sprintf("%dy ago", int(d/(365*24*time.Hour)))
	}
}
