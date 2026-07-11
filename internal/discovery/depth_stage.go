package discovery

import (
	"path"
	"strings"
)

// Token validation for --depth lives in internal/cli
// (cli.ParseDepthToken); stage appliers take already-parsed ints.

// entryDepth is the depth of an entry relative to its target root, giving
// --depth rg --max-depth semantics (depth 1 = a direct child of the target).
// When TargetRoot is empty or "." — bare catclip, catclip ., and glob targets
// — this is the project-relative depth, so those flows are the fixed point and
// stay byte-identical to the old project-root anchoring.
func entryDepth(e Entry) int {
	root := normalizeRelPath(e.TargetRoot)
	rel := normalizeRelPath(e.RelPath)
	if root == "" || root == "." {
		return relPathDepth(rel)
	}
	if rel == root {
		// The target root itself; never produced for a directory target
		// today (entries are strict descendants). Treat as a direct member.
		return 1
	}
	if strings.HasPrefix(rel, root+"/") {
		return relPathDepth(strings.TrimPrefix(rel, root+"/"))
	}
	return relPathDepth(rel) // fallback: TargetRoot is not a prefix of RelPath
}

func ApplyDepthStage(entries []Entry, depth int) ([]Entry, error) {
	if len(entries) == 0 {
		return entries, nil
	}

	maxDepth := MaxEntryPathDepth(entries)
	if maxDepth > 0 && depth > maxDepth {
		return nil, DepthExceedsCurrentScopeError(depth, maxDepth)
	}

	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entryDepth(entry) <= depth {
			out = append(out, entry)
		}
	}
	if len(out) == 0 {
		minDepth := minEntryPathDepth(entries)
		return nil, depthNoFilesAtLevelError(depth, minDepth, maxDepth)
	}
	return out, nil
}

func DepthExceedsCurrentScopeError(depth, maxDepth int) error {
	return newUsageError("Error: --depth %d exceeds the current scope max depth %d.\n  Choose a value between 1 and %d.", depth, maxDepth, maxDepth)
}

func depthNoFilesAtLevelError(depth, minDepth, maxDepth int) error {
	if minDepth == maxDepth {
		return newUsageError("Error: no files at depth %d. All files are at depth %d.\n  Try --depth %d.", depth, minDepth, minDepth)
	}
	return newUsageError("Error: no files at depth %d. Files in this scope range from depth %d to %d.\n  Try --depth %d or higher.", depth, minDepth, maxDepth, minDepth)
}

func minEntryPathDepth(entries []Entry) int {
	minDepth := 0
	for _, entry := range entries {
		d := entryDepth(entry)
		if d > 0 && (minDepth == 0 || d < minDepth) {
			minDepth = d
		}
	}
	return minDepth
}

func MaxEntryPathDepth(entries []Entry) int {
	maxDepth := 0
	for _, entry := range entries {
		depth := entryDepth(entry)
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	return maxDepth
}

type DepthBucket struct {
	Depth           int
	CumulativeCount int
}

// ComputeDepthBuckets groups the scope's entries by their target-anchored
// depth (see entryDepth). It dedupes by RelPath — keeping the first entry's
// TargetRoot — so it absorbs the caller-side dedup the picker used to do.
func ComputeDepthBuckets(entries []Entry) []DepthBucket {
	counts := map[int]int{}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.RelPath == "" {
			continue
		}
		if _, ok := seen[e.RelPath]; ok {
			continue
		}
		seen[e.RelPath] = struct{}{}
		d := entryDepth(e)
		if d > 0 {
			counts[d]++
		}
	}
	if len(counts) == 0 {
		return nil
	}

	maxDepth := 0
	for d := range counts {
		if d > maxDepth {
			maxDepth = d
		}
	}

	var buckets []DepthBucket
	cumulative := 0
	for d := 1; d <= maxDepth; d++ {
		cumulative += counts[d]
		if counts[d] > 0 {
			buckets = append(buckets, DepthBucket{Depth: d, CumulativeCount: cumulative})
		}
	}
	return buckets
}

func relPathDepth(relPath string) int {
	relPath = normalizeRelPath(relPath)
	if relPath == "" || relPath == "." {
		return 0
	}
	cleaned := path.Clean(relPath)
	if cleaned == "." || cleaned == "" {
		return 0
	}
	depth := 1
	for _, ch := range cleaned {
		if ch == '/' {
			depth++
		}
	}
	return depth
}
