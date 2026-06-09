package discovery

import (
	"os"
	"sort"
	"strconv"
)

// applyRecentStage filters entries to the most-recently-modified slice
// (capped by --recent's optional integer limit). Stats happen via
// ensureEntryModTimes so entries that came in from a checkpoint with
// ModTime already filled don't restat.
func ApplyRecentStage(entries []Entry, workingDir string, limit *int) ([]Entry, error) {
	if len(entries) == 0 {
		return entries, nil
	}

	out := append([]Entry(nil), entries...)
	var err error
	out, err = EnsureEntryModTimes(out, workingDir)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].ModTime.After(out[j].ModTime)
		}
		return out[i].RelPath < out[j].RelPath
	})

	if limit != nil && *limit < len(out) {
		out = out[:*limit]
	}
	return out, nil
}

// EnsureEntryModTimes fills ModTime + SizeBytes for any entry missing
// them, statting on disk via os.Stat. Used by applyRecentStage and by
// root's preview_table.go (for the file-list preview pane).
func EnsureEntryModTimes(entries []Entry, workingDir string) ([]Entry, error) {
	entries = EnsureEntryAbsPaths(entries, workingDir)
	for i := range entries {
		if !entries[i].ModTime.IsZero() {
			continue
		}
		info, err := os.Stat(entries[i].AbsPath)
		if err != nil {
			return nil, err
		}
		entries[i].ModTime = info.ModTime()
		entries[i].SizeBytes = info.Size()
		entries[i].SizeKnown = true
	}
	return entries, nil
}

// ParseRecentLimitToken parses --recent's optional integer argument.
// Exported so the root parser-side helpers can validate args before
// applyRecentStage runs.
func ParseRecentLimitToken(token string) (int, error) {
	limit, err := strconv.Atoi(token)
	if err != nil || limit <= 0 {
		return 0, newUsageError("Error: --recent takes an optional positive integer.\n  Example: catclip src --recent\n  Example: catclip src --recent 5")
	}
	return limit, nil
}
