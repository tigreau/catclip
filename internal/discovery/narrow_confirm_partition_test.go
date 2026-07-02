package discovery

import (
	"reflect"
	"testing"
)

func TestPartitionIgnoredByIncludes_EmptyInputs(t *testing.T) {
	all, ignored := PartitionIgnoredByIncludes(nil, []string{"docs"})
	if all != nil || ignored != nil {
		t.Fatalf("empty entries: want nil/nil, got %v / %v", all, ignored)
	}
	entries := []Entry{{RelPath: "a.txt"}}
	all, ignored = PartitionIgnoredByIncludes(entries, nil)
	if !reflect.DeepEqual(all, entries) || ignored != nil {
		t.Fatalf("empty includes: want entries/nil, got %v / %v", all, ignored)
	}
}

func TestPartitionIgnoredByIncludes_DotsAndEmptyIncludesDropped(t *testing.T) {
	entries := []Entry{
		{RelPath: "docs/x.md", AllowedByInclude: true},
	}
	all, ignored := PartitionIgnoredByIncludes(entries, []string{".", "", "  "})
	if !reflect.DeepEqual(all, entries) {
		t.Fatalf("all should pass through, got %v", all)
	}
	// All include paths normalized away → no narrow set.
	if ignored != nil {
		t.Fatalf("dots/empty includes should produce no ignored set, got %v", ignored)
	}
}

func TestPartitionIgnoredByIncludes_OnlyAuthorizedByIncludeQualifies(t *testing.T) {
	entries := []Entry{
		{RelPath: "src/main.go", AllowedByInclude: false},
		{RelPath: "docs/readme.md", AllowedByInclude: true, BlockSource: ".gitignore"},
		{RelPath: "docs/sub/x.md", AllowedByInclude: true, BlockSource: ".gitignore"},
		{RelPath: "docs/tracked.md", AllowedByInclude: false}, // tracked, not via include
	}
	all, ignored := PartitionIgnoredByIncludes(entries, []string{"docs"})
	if len(all) != 4 {
		t.Fatalf("all should preserve all entries, got %d", len(all))
	}
	if len(ignored) != 2 {
		t.Fatalf("ignored should contain only the 2 --include-admitted entries, got %d: %v", len(ignored), ignored)
	}
	wantPaths := map[string]bool{"docs/readme.md": true, "docs/sub/x.md": true}
	for _, e := range ignored {
		if !wantPaths[e.RelPath] {
			t.Fatalf("unexpected ignored entry: %s", e.RelPath)
		}
	}
}

func TestPartitionIgnoredByIncludes_OnlyDescendantsOfIncludePathsQualify(t *testing.T) {
	entries := []Entry{
		{RelPath: "docs/a.md", AllowedByInclude: true},
		{RelPath: "vendor/b.go", AllowedByInclude: true}, // include-admitted but outside `docs`
		{RelPath: "docs/sub/c.md", AllowedByInclude: true},
	}
	_, ignored := PartitionIgnoredByIncludes(entries, []string{"docs"})
	if len(ignored) != 2 {
		t.Fatalf("expected 2 entries under docs/, got %d: %v", len(ignored), ignored)
	}
	for _, e := range ignored {
		if e.RelPath == "vendor/b.go" {
			t.Fatalf("vendor entry must NOT be in 'narrow under docs' set, got %v", e)
		}
	}
}

func TestPartitionIgnoredByIncludes_PathEqualsIncludeQualifies(t *testing.T) {
	entries := []Entry{
		{RelPath: "docs", AllowedByInclude: true}, // include path itself (a dir entry)
	}
	_, ignored := PartitionIgnoredByIncludes(entries, []string{"docs"})
	if len(ignored) != 1 || ignored[0].RelPath != "docs" {
		t.Fatalf("entry equal to include path should be included, got %v", ignored)
	}
}

func TestPartitionIgnoredByIncludes_StablePreservesInputOrder(t *testing.T) {
	entries := []Entry{
		{RelPath: "docs/z.md", AllowedByInclude: true},
		{RelPath: "docs/a.md", AllowedByInclude: true},
		{RelPath: "docs/m.md", AllowedByInclude: true},
	}
	_, ignored := PartitionIgnoredByIncludes(entries, []string{"docs"})
	gotOrder := make([]string, 0, len(ignored))
	for _, e := range ignored {
		gotOrder = append(gotOrder, e.RelPath)
	}
	want := []string{"docs/z.md", "docs/a.md", "docs/m.md"}
	if !reflect.DeepEqual(gotOrder, want) {
		t.Fatalf("partition must preserve input order, got %v want %v", gotOrder, want)
	}
}

func TestPartitionIgnoredByIncludes_MultipleIncludesUnion(t *testing.T) {
	entries := []Entry{
		{RelPath: "docs/a.md", AllowedByInclude: true},
		{RelPath: "vendor/b.go", AllowedByInclude: true},
		{RelPath: "src/c.go", AllowedByInclude: false},
	}
	_, ignored := PartitionIgnoredByIncludes(entries, []string{"docs", "vendor"})
	if len(ignored) != 2 {
		t.Fatalf("expected union of both include subtrees, got %d: %v", len(ignored), ignored)
	}
}
