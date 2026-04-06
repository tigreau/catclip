package catclip

import (
	"os"
	"sort"
	"strconv"
)

func consumeOptionalRecentLimit(args []string, start int) (*int, int, error) {
	if start >= len(args) || isModifierBoundaryToken(args[start]) {
		return nil, start, nil
	}

	limit, err := parseRecentLimitToken(args[start])
	if err != nil {
		return nil, start, err
	}
	return intPtr(limit), start + 1, nil
}

func parseRecentLimitToken(token string) (int, error) {
	limit, err := strconv.Atoi(token)
	if err != nil || limit <= 0 {
		return 0, newUsageError("Error: --recent takes an optional positive integer.\n  Example: catclip src --recent\n  Example: catclip src --recent 5")
	}
	return limit, nil
}

func applyRecentStage(entries []fileEntry, workingDir string, limit *int) ([]fileEntry, error) {
	if len(entries) == 0 {
		return entries, nil
	}

	out := append([]fileEntry(nil), entries...)
	var err error
	out, err = ensureEntryModTimes(out, workingDir)
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

func ensureEntryModTimes(entries []fileEntry, workingDir string) ([]fileEntry, error) {
	entries = ensureEntryAbsPaths(entries, workingDir)
	for i := range entries {
		if !entries[i].ModTime.IsZero() {
			continue
		}
		info, err := os.Stat(entries[i].AbsPath)
		if err != nil {
			return nil, err
		}
		entries[i].ModTime = info.ModTime()
	}
	return entries, nil
}

func intPtr(v int) *int {
	return &v
}
