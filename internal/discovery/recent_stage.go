package discovery

import (
	"os"
	"sort"
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
//
// Fail-fast contract: mirrors EnsureEntrySizes. --recent was invoked
// as a filter, so a missing entry is a real error that surfaces here.
func EnsureEntryModTimes(entries []Entry, workingDir string) ([]Entry, error) {
	entries = EnsureEntryAbsPaths(entries, workingDir)
	paths := make([]string, len(entries))
	for i := range entries {
		if !entries[i].ModTime.IsZero() {
			continue
		}
		paths[i] = entries[i].AbsPath
	}
	infos, errs := parallelStat(paths, os.Stat)
	for i := range entries {
		if paths[i] == "" {
			continue
		}
		if errs[i] != nil {
			return nil, errs[i]
		}
		entries[i].ModTime = infos[i].ModTime()
		entries[i].SizeBytes = infos[i].Size()
		entries[i].SizeKnown = true
	}
	return entries, nil
}

// Token validation for --recent lives in internal/cli
// (cli.ParseRecentLimitToken); stage appliers take already-parsed ints.
