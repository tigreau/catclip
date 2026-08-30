package discovery

import (
	"os"
	"sort"
)

const kibibyte = int64(1024)

// ApplySizeStage sorts entries largest-first and optionally filters by KiB
// bounds. nums is --size's typed payload: [] for sort-only, [MIN] for lower
// bound, [MIN, MAX] for inclusive range.
func ApplySizeStage(entries []Entry, workingDir string, nums []int) ([]Entry, error) {
	if len(entries) == 0 {
		return entries, nil
	}

	out := append([]Entry(nil), entries...)
	var err error
	out, err = EnsureEntrySizes(out, workingDir)
	if err != nil {
		return nil, err
	}

	if len(nums) > 0 {
		out = filterEntriesBySizeBounds(out, nums)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SizeBytes != out[j].SizeBytes {
			return out[i].SizeBytes > out[j].SizeBytes
		}
		return out[i].RelPath < out[j].RelPath
	})

	return out, nil
}

// EnsureEntrySizes fills SizeBytes and retains ModTime from the same lookup.
// Discovery excludes symlink entries, so Lstat matches the target-picker and
// checkpoint metadata contract without changing the valid file universe.
//
// Fail-fast contract: a single os.Stat error returns (nil, err). --size
// was invoked as a filter, so a missing target file is a real user
// error that should surface, not be silently skipped.
func EnsureEntrySizes(entries []Entry, workingDir string) ([]Entry, error) {
	entries = EnsureEntryAbsPaths(entries, workingDir)
	paths := make([]string, len(entries))
	for i := range entries {
		if entries[i].SizeKnown {
			continue
		}
		paths[i] = entries[i].AbsPath
	}
	infos, errs := parallelStat(paths, os.Lstat)
	for i := range entries {
		if paths[i] == "" {
			continue
		}
		if errs[i] != nil {
			return nil, errs[i]
		}
		entries[i].SizeBytes = infos[i].Size()
		entries[i].SizeKnown = true
		entries[i].ModTime = infos[i].ModTime()
	}
	return entries, nil
}

func filterEntriesBySizeBounds(entries []Entry, nums []int) []Entry {
	minBytes := int64(nums[0]) * kibibyte
	var maxBytes *int64
	if len(nums) > 1 {
		max := int64(nums[1]) * kibibyte
		maxBytes = &max
	}

	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.SizeBytes < minBytes {
			continue
		}
		if maxBytes != nil && entry.SizeBytes > *maxBytes {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func SizeBucketKiB(sizeBytes int64) int {
	if sizeBytes <= 0 {
		return 0
	}
	return int((sizeBytes + kibibyte - 1) / kibibyte)
}
