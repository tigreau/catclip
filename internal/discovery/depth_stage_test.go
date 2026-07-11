package discovery

import (
	"reflect"
	"testing"
)

// entryDepth is the target-anchored depth primitive introduced in v0.6.6.
// These tests pin both the fixed point (empty TargetRoot behaves exactly like
// the old project-relative depth) and the anchored behavior.
func TestEntryDepth(t *testing.T) {
	cases := []struct {
		name       string
		relPath    string
		targetRoot string
		want       int
	}{
		{"fixed point root file", "README.md", "", 1},
		{"fixed point nested", "src/main.ts", "", 2},
		{"fixed point dot root", "src/main.ts", ".", 2},
		{"dir target direct child", "cmd/main.go", "cmd", 1},
		{"dir target grandchild", "cmd/pkg/x.go", "cmd", 2},
		{"nested target root", "a/b/c.go", "a/b", 1},
		{"file target is depth one", "cmd/main.go", "cmd", 1},
		{"rel equals root", "cmd", "cmd", 1},
		{"target root not a prefix falls back", "other/x.go", "cmd", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := entryDepth(Entry{RelPath: tc.relPath, TargetRoot: tc.targetRoot})
			if got != tc.want {
				t.Fatalf("entryDepth(%q, root=%q) = %d, want %d", tc.relPath, tc.targetRoot, got, tc.want)
			}
		})
	}
}

// Multi-target: `catclip cmd docs --depth 1` keeps the direct children of each
// target (the union), not the project-root depth-1 files.
func TestApplyDepthStageAnchorsPerTarget(t *testing.T) {
	entries := []Entry{
		{RelPath: "cmd/main.go", TargetRoot: "cmd"},
		{RelPath: "cmd/sub/x.go", TargetRoot: "cmd"},
		{RelPath: "docs/intro.md", TargetRoot: "docs"},
		{RelPath: "docs/deep/y.md", TargetRoot: "docs"},
	}

	filtered, err := ApplyDepthStage(entries, 1)
	if err != nil {
		t.Fatalf("ApplyDepthStage returned error: %v", err)
	}
	if got, want := testStageRelPaths(filtered), []string{
		"cmd/main.go",
		"docs/intro.md",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected per-target direct children %v, got %v", want, got)
	}
}

func TestComputeDepthBucketsAggregatesAcrossTargets(t *testing.T) {
	entries := []Entry{
		{RelPath: "cmd/main.go", TargetRoot: "cmd"},
		{RelPath: "cmd/sub/x.go", TargetRoot: "cmd"},
		{RelPath: "docs/intro.md", TargetRoot: "docs"},
		{RelPath: "docs/deep/y.md", TargetRoot: "docs"},
		// Duplicate RelPath must not double-count (dedup absorbed into buckets).
		{RelPath: "cmd/main.go", TargetRoot: "cmd"},
	}

	buckets := ComputeDepthBuckets(entries)
	want := []DepthBucket{
		{Depth: 1, CumulativeCount: 2},
		{Depth: 2, CumulativeCount: 4},
	}
	if !reflect.DeepEqual(buckets, want) {
		t.Fatalf("expected anchored buckets %v, got %v", want, buckets)
	}
}

// Fixed point: with empty TargetRoot the buckets match project-relative depth,
// so bare/`.` flows are unchanged.
func TestComputeDepthBucketsFixedPoint(t *testing.T) {
	entries := testStageEntries(
		"README.md",
		"src/main.ts",
		"src/components/Button.tsx",
	)
	buckets := ComputeDepthBuckets(entries)
	want := []DepthBucket{
		{Depth: 1, CumulativeCount: 1},
		{Depth: 2, CumulativeCount: 2},
		{Depth: 3, CumulativeCount: 3},
	}
	if !reflect.DeepEqual(buckets, want) {
		t.Fatalf("expected fixed-point buckets %v, got %v", want, buckets)
	}
}
