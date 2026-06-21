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

// EnsureEntrySizes fills SizeBytes for entries missing it. It uses os.Stat
// because --size reasons about the payload that would be emitted; for a
// symlinked entry, that is the target file. Discovery does not currently
// emit symlink entries, so this preserves existing behavior in practice.
func EnsureEntrySizes(entries []Entry, workingDir string) ([]Entry, error) {
	entries = EnsureEntryAbsPaths(entries, workingDir)
	for i := range entries {
		if entries[i].SizeKnown {
			continue
		}
		info, err := os.Stat(entries[i].AbsPath)
		if err != nil {
			return nil, err
		}
		entries[i].SizeBytes = info.Size()
		entries[i].SizeKnown = true
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

type SizeBucket struct {
	KiB   int
	Count int
}

func ComputeSizeBuckets(entries []Entry) []SizeBucket {
	counts := map[int]int{}
	for _, entry := range entries {
		counts[SizeBucketKiB(entry.SizeBytes)]++
	}
	if len(counts) == 0 {
		return nil
	}

	keys := make([]int, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Ints(keys)

	buckets := make([]SizeBucket, 0, len(keys))
	for _, key := range keys {
		buckets = append(buckets, SizeBucket{KiB: key, Count: counts[key]})
	}
	return buckets
}
