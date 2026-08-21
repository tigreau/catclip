package discovery

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/git"
)

func TestTargetPreviewInventoryRoundTripAndSelection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "targets.bin")
	wantGit := git.Context{Enabled: true, Root: root, WorkPrefix: "nested", HasHead: true}
	matches := []TargetMatch{
		{Path: "src/api-old/wrong.ts", Kind: treeTargetKindFile},
		{Path: "src/api/nested/beta.ts", Kind: treeTargetKindFile, Ignored: true, IgnoreSource: ".hiss"},
		{Path: "src/api/alpha file.ts", Kind: treeTargetKindFile, SizeBytes: 42, SizeKnown: true},
		{Path: "src/api", Kind: treeTargetKindDir},
		{Path: "dist/generated.js", Kind: treeTargetKindFile, Ignored: true, IgnoreSource: ".gitignore"},
	}
	if err := WriteTargetPreviewInventory(path, wantGit, matches); err != nil {
		t.Fatalf("WriteTargetPreviewInventory() error = %v", err)
	}
	inventory, err := ReadTargetPreviewInventory(path, root)
	if err != nil {
		t.Fatalf("ReadTargetPreviewInventory() error = %v", err)
	}
	if !reflect.DeepEqual(inventory.GitContext, wantGit) {
		t.Fatalf("Git context = %#v, want %#v", inventory.GitContext, wantGit)
	}

	selected := SelectTargetPreviewEntries(inventory.Entries, []string{"src/api", "src/api/alpha file.ts"})
	if got, want := entryPaths(selected), []string{"src/api/alpha file.ts", "src/api/nested/beta.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected paths = %v, want %v", got, want)
	}
	if selected[0].TargetRoot != "src/api" || selected[1].TargetRoot != "src/api" {
		t.Fatalf("target roots = %q, %q; want src/api", selected[0].TargetRoot, selected[1].TargetRoot)
	}
	if selected[0].IgnoreBypassed || !selected[0].GitVisible {
		t.Fatalf("visible entry metadata = %+v", selected[0])
	}
	if !selected[0].SizeKnown || selected[0].SizeBytes != 42 {
		t.Fatalf("visible entry size metadata = %+v", selected[0])
	}
	if !selected[1].IgnoreBypassed || selected[1].GitVisible || selected[1].BlockSource != ".hiss" {
		t.Fatalf("ignored entry metadata = %+v", selected[1])
	}
}

func TestTargetPreviewInventorySelectAllAndExactFile(t *testing.T) {
	entries := []Entry{
		{RelPath: "a.ts"},
		{RelPath: "src/a.ts"},
		{RelPath: "src/nested/b.ts"},
	}
	if got, want := entryPaths(SelectTargetPreviewEntries(entries, []string{"."})), entryPaths(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("select all = %v, want %v", got, want)
	}
	selected := SelectTargetPreviewEntries(entries, []string{"src/a.ts"})
	if got, want := entryPaths(selected), []string{"src/a.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exact file = %v, want %v", got, want)
	}
	if selected[0].TargetRoot != "src/a.ts" {
		t.Fatalf("exact file target root = %q", selected[0].TargetRoot)
	}
}

func TestTargetPreviewInventoryDirectorySelectionRetainsDescendantsOutsideCandidateRows(t *testing.T) {
	// A no-ignore query may narrow the visible fzf rows to the exact "src"
	// directory. Its preview still projects from the full parent inventory.
	entries := []Entry{
		{RelPath: "outside.ts"},
		{RelPath: "src/a.ts"},
		{RelPath: "src/nested/b.ts"},
	}
	selected := SelectTargetPreviewEntries(entries, []string{"src"})
	if got, want := entryPaths(selected), []string{"src/a.ts", "src/nested/b.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("directory preview paths = %v, want %v", got, want)
	}
}

func TestTargetPreviewInventoryPendingSizesAndCompletedSidecar(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "targets.bin")
	matches := []TargetMatch{
		{Path: "src/a.ts", Kind: treeTargetKindFile},
		{Path: "src", Kind: treeTargetKindDir},
	}
	if err := WriteTargetPreviewInventoryWithOptions(basePath, git.Context{}, matches, TargetPreviewInventoryWriteOptions{
		SizesPending: true,
	}); err != nil {
		t.Fatal(err)
	}
	base, err := ReadTargetPreviewInventory(basePath, root)
	if err != nil {
		t.Fatal(err)
	}
	if !base.SizesPending || len(base.Entries) != 1 || base.Entries[0].SizeKnown {
		t.Fatalf("base inventory = %+v", base)
	}

	completed := ApplyTargetPreviewSizes(matches, map[string]int64{"src/a.ts": 17})
	if err := WriteTargetPreviewInventory(TargetPreviewSizedInventoryPath(basePath), git.Context{}, completed); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sized, ok, err := WaitForTargetPreviewSizedInventory(ctx, basePath, root)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || sized.SizesPending || len(sized.Entries) != 1 || !sized.Entries[0].SizeKnown || sized.Entries[0].SizeBytes != 17 {
		t.Fatalf("completed inventory = %+v, ok = %t", sized, ok)
	}
}

func TestWaitForTargetPreviewSizedInventoryStopsAtFailureMarker(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "targets.bin")
	if err := os.WriteFile(TargetPreviewSizedInventoryDonePath(basePath), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, ok, err := WaitForTargetPreviewSizedInventory(ctx, basePath, root)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("failure marker unexpectedly reported a completed inventory")
	}
}

func TestApplyTargetPreviewSizesOnlyUpdatesFileMatches(t *testing.T) {
	matches := []TargetMatch{
		{Path: "src/a.ts", Kind: treeTargetKindFile, Ignored: true, IgnoreSource: ".hiss"},
		{Path: "src", Kind: treeTargetKindDir},
	}
	got := ApplyTargetPreviewSizes(matches, map[string]int64{"src/a.ts": 23, "src": 99})
	if !got[0].SizeKnown || got[0].SizeBytes != 23 || !got[0].Ignored || got[0].IgnoreSource != ".hiss" {
		t.Fatalf("file match = %+v", got[0])
	}
	if got[1].SizeKnown || got[1].SizeBytes != 0 {
		t.Fatalf("directory match = %+v", got[1])
	}
	if matches[0].SizeKnown {
		t.Fatal("ApplyTargetPreviewSizes mutated its input")
	}
}

func entryPaths(entries []Entry) []string {
	out := make([]string, len(entries))
	for i := range entries {
		out[i] = entries[i].RelPath
	}
	return out
}
