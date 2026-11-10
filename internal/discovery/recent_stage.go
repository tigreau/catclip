package discovery

import (
	"github.com/tigreau/catclip/internal/search"
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
// them, statting on disk via os.Lstat. Used by applyRecentStage and the
// metadata report when a script-mode entry did not already carry primary stat data.
//
// Like EnsureEntrySizes, omit missing/inaccessible entries on actual stat
// failure and retain diagnostics; propagate other failures.
func EnsureEntryModTimes(entries []Entry, workingDir string) ([]Entry, error) {
	entries = EnsureEntryAbsPaths(entries, workingDir)
	missing := false
	for i := range entries {
		if entries[i].ModTime.IsZero() {
			missing = true
			break
		}
	}
	if !missing {
		return entries, nil
	}
	paths := make([]string, len(entries))
	for i := range entries {
		if !entries[i].ModTime.IsZero() {
			continue
		}
		paths[i] = entries[i].AbsPath
	}
	infos, errs := parallelStat(paths, os.Lstat)
	available := entries[:0]
	for i := range entries {
		if paths[i] == "" {
			available = append(available, entries[i])
			continue
		}
		if errs[i] != nil {
			if search.RecordAccessFailure(errs[i], true) {
				continue
			}
			return nil, errs[i]
		}
		entries[i].ModTime = infos[i].ModTime()
		entries[i].SizeBytes = infos[i].Size()
		entries[i].SizeKnown = true
		available = append(available, entries[i])
	}
	entries = available
	return entries, nil
}

// Token validation for --recent lives in internal/cli
// (cli.ParseRecentLimitToken); stage appliers take already-parsed ints.
