package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
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

func TestTargetPreviewEntryInventoryRoundTripAndOrderValidation(t *testing.T) {
	root := t.TempDir()
	inventoryPath := filepath.Join(root, "entries.bin")
	entries := []Entry{
		{RelPath: "a.go", GitVisible: true, SizeBytes: 12, SizeKnown: true},
		{RelPath: "vendor/b.go", IgnoreBypassed: true, BlockSource: ".hiss", SizeBytes: 34, SizeKnown: true},
	}
	if err := WriteTargetPreviewEntryInventory(inventoryPath, git.Context{}, entries); err != nil {
		t.Fatal(err)
	}
	inventory, err := ReadTargetPreviewInventory(inventoryPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryPaths(inventory.Entries), []string{"a.go", "vendor/b.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entry inventory paths = %v, want %v", got, want)
	}
	if !inventory.Entries[0].SizeKnown || inventory.Entries[0].SizeBytes != 12 {
		t.Fatalf("visible metadata = %+v", inventory.Entries[0])
	}
	if !inventory.Entries[1].IgnoreBypassed || inventory.Entries[1].BlockSource != ".hiss" || inventory.Entries[1].SizeBytes != 34 {
		t.Fatalf("ignored metadata = %+v", inventory.Entries[1])
	}
	if err := WriteTargetPreviewEntryInventory(filepath.Join(root, "unsorted.bin"), git.Context{}, []Entry{
		{RelPath: "z.go"},
		{RelPath: "a.go"},
	}); err == nil {
		t.Fatal("unsorted entry inventory was accepted")
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

func TestTargetPreviewInventorySelectionMatchesCanonicalTargetGlobs(t *testing.T) {
	entries := []Entry{
		{RelPath: "docs/readme.md"},
		{RelPath: "src/a.go"},
		{RelPath: "src/nested/b.go"},
	}
	selected := SelectTargetPreviewEntries(entries, []string{"*.go"})
	if got, want := entryPaths(selected), []string{"src/a.go", "src/nested/b.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("glob selection = %v, want %v", got, want)
	}
	for _, entry := range selected {
		if entry.TargetRoot != "" {
			t.Fatalf("glob target root = %q, want canonical all-root projection", entry.TargetRoot)
		}
	}
}

func TestTargetPreviewInventoryCanonicalizesMultiTargetOrder(t *testing.T) {
	entries := []Entry{
		{RelPath: "a.txt"},
		{RelPath: "src/a.go"},
		{RelPath: "src/b.go"},
		{RelPath: "z.txt"},
	}
	selected := SelectTargetPreviewEntries(entries, []string{"src", "a.txt"})
	if got, want := entryPaths(selected), []string{"a.txt", "src/a.go", "src/b.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-target projection order = %v, want %v", got, want)
	}
	if selected[1].TargetRoot != "src" || selected[2].TargetRoot != "src" {
		t.Fatalf("sorting changed target attribution: %+v", selected)
	}
}

func BenchmarkSelectTargetPreviewEntriesCanonicalOrder(b *testing.B) {
	const perDir = 100_000
	entries := make([]Entry, 0, perDir*2)
	for i := 0; i < perDir; i++ {
		entries = append(entries, Entry{RelPath: fmt.Sprintf("a/%06d.go", i)})
	}
	for i := 0; i < perDir; i++ {
		entries = append(entries, Entry{RelPath: fmt.Sprintf("z/%06d.go", i)})
	}
	for _, tc := range []struct {
		name    string
		targets []string
	}{
		{name: "already-ordered", targets: []string{"a", "z"}},
		{name: "requires-sort", targets: []string{"z", "a"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				selected := SelectTargetPreviewEntries(entries, tc.targets)
				if len(selected) != len(entries) {
					b.Fatalf("selected %d entries, want %d", len(selected), len(entries))
				}
			}
		})
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

func TestTargetPreviewEntriesCarriesMembershipAndSuccessfulMetadata(t *testing.T) {
	root := t.TempDir()
	wantTime := time.Unix(1_700_000_000, 0)
	matches := []TargetMatch{
		{Path: "src", Kind: treeTargetKindDir},
		{Path: "src/a.go", Kind: treeTargetKindFile},
		{Path: "vendor/b.go", Kind: treeTargetKindFile, Ignored: true, IgnoreSource: ".hiss"},
	}
	metadata := map[string]search.FileMetadata{
		"src/a.go":    {SizeBytes: 12, ModTime: wantTime, Mode: 0o644},
		"vendor/b.go": {State: search.FileMetadataVanished, Error: "gone"},
	}
	entries := TargetPreviewEntries(root, matches, metadata)
	if got, want := entryPaths(entries), []string{"src/a.go", "vendor/b.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entry paths = %v, want %v", got, want)
	}
	if !entries[0].SizeKnown || entries[0].SizeBytes != 12 || !entries[0].ModTime.Equal(wantTime) {
		t.Fatalf("ready entry metadata = %+v", entries[0])
	}
	if entries[1].SizeKnown || !entries[1].IgnoreBypassed || entries[1].BlockSource != ".hiss" {
		t.Fatalf("vanished ignored entry = %+v", entries[1])
	}
}

func TestResolverFinalizesOnlyCommittedTargetMembership(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.go"), []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Cfg:                      command.Invocation{WorkingDir: root},
		targetPreviewInventory:   []TargetMatch{{Path: "src", Kind: treeTargetKindDir}, {Path: "src/a.go", Kind: treeTargetKindFile}, {Path: "outside.go", Kind: treeTargetKindFile}},
		targetPreviewInventoryOK: true,
	}
	resolver.FinalizeTargetSelection([]string{"src"})
	entries, metadata, ok := resolver.CommittedTargetSelection()
	if !ok {
		t.Fatal("selection was not committed")
	}
	if got, want := entryPaths(entries), []string{"src/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed paths = %v, want %v", got, want)
	}
	if len(metadata) != 1 || metadata["src/a.go"].State != search.FileMetadataReady {
		t.Fatalf("committed metadata = %+v", metadata)
	}
}

func TestResolverFastConfirmationLeavesCommittedPreviewTransportLazy(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.go"), []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir, err := os.MkdirTemp("", "catclip-target-commit-test-")
	if err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(sessionDir, "targets.bin")
	if err := os.WriteFile(basePath, []byte("picker inventory placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Cfg:                      command.Invocation{WorkingDir: root},
		targetPreviewInventory:   []TargetMatch{{Path: "src", Kind: treeTargetKindDir}, {Path: "src/a.go", Kind: treeTargetKindFile}, {Path: "outside.go", Kind: treeTargetKindFile}},
		targetPreviewInventoryOK: true,
	}
	resolver.retainTargetPreviewInventory(sessionDir, basePath)
	defer resolver.ReleaseRetainedTargetPreviewInventory()

	resolver.FinalizeTargetSelection([]string{"src"})
	if _, ok := resolver.CommittedTargetPreviewInventoryPath(); ok {
		t.Fatal("fast confirmation eagerly wrote an inventory before a filter requested it")
	}
	if matches, globErr := filepath.Glob(filepath.Join(sessionDir, "targets.committed-*.bin")); globErr != nil || len(matches) != 0 {
		t.Fatalf("fast confirmation left an eager committed artifact: matches=%v err=%v", matches, globErr)
	}
	entries, metadata, ok := resolver.CommittedTargetSelection()
	if !ok {
		t.Fatal("target selection was not committed")
	}
	if got, want := entryPaths(entries), []string{"src/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed paths = %v, want %v", got, want)
	}
	if !entries[0].SizeKnown || entries[0].SizeBytes != int64(len("package a\n")) || metadata["src/a.go"].State != search.FileMetadataReady {
		t.Fatalf("committed metadata = entry %+v, records %+v", entries[0], metadata)
	}
}

func TestResolverReusesPickerInventoryWhenMetadataFinishedBeforeConfirmation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir, err := os.MkdirTemp("", "catclip-target-ready-test-")
	if err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(sessionDir, "targets.bin")
	if err := WriteTargetPreviewInventory(basePath, git.Context{}, []TargetMatch{
		{Path: "a.go", Kind: treeTargetKindFile, SizeBytes: int64(len("package a\n")), SizeKnown: true},
	}); err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{
		Cfg:                      command.Invocation{WorkingDir: root},
		targetPreviewInventory:   []TargetMatch{{Path: "a.go", Kind: treeTargetKindFile}},
		targetPreviewInventoryOK: true,
	}
	resolver.retainTargetPreviewInventory(sessionDir, basePath, true)
	defer resolver.ReleaseRetainedTargetPreviewInventory()

	resolver.FinalizeTargetSelection([]string{"a.go"})
	committedPath, ok := resolver.CommittedTargetPreviewInventoryPath()
	if !ok || committedPath != basePath {
		t.Fatalf("committed path = %q, %t; want existing base %q", committedPath, ok, basePath)
	}
	matches, err := filepath.Glob(filepath.Join(sessionDir, "targets.committed-*.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("metadata-complete picker rewrote committed inventory: %v", matches)
	}
}

func TestResolverDoesNotReusePreviewArtifactAcrossTargetGenerations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "visible.go"), []byte("package visible\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir, err := os.MkdirTemp("", "catclip-target-generation-test-")
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(sessionDir, "targets.bin")
	if err := os.WriteFile(oldPath, []byte("old broad inventory"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := Resolver{Cfg: command.Invocation{WorkingDir: root}}
	resolver.retainTargetPreviewInventory(sessionDir, oldPath, true)
	defer resolver.ReleaseRetainedTargetPreviewInventory()

	// A later exact target can auto-accept before opening fzf. Establishing its
	// target universe must detach the older generation before finalization.
	resolver.beginTargetPreviewGeneration()
	resolver.targetPreviewInventory = []TargetMatch{{Path: "visible.go", Kind: treeTargetKindFile}}
	resolver.targetPreviewInventoryOK = true
	resolver.FinalizeTargetSelection([]string{"visible.go"})
	if _, ok := resolver.CommittedTargetPreviewInventoryPath(); ok {
		t.Fatal("later exact target reused an earlier generation's compact artifact")
	}
	if _, ok := resolver.RetainedTargetPreviewInventoryPath(); ok {
		t.Fatal("earlier artifact remained current after a new target generation")
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("detached artifact was removed before session cleanup: %v", err)
	}
}

func TestImmediateTargetConfirmationTransfersPreviewInventoryLease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake fzf requires /bin/sh")
	}
	root := t.TempDir()
	fzf := filepath.Join(root, "fake-fzf")
	if err := os.WriteFile(fzf, []byte("#!/bin/sh\nIFS= read -r line || exit 1\nprintf '%s\\n' \"$line\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Keep enough speculative work queued that the fake picker can confirm its
	// first row immediately. Correctness must not depend on a preview child
	// having started or on the background metadata pass having completed.
	pendingPaths := make([]string, 4096)
	for i := range pendingPaths {
		pendingPaths[i] = fmt.Sprintf("pending/%05d.go", i)
	}
	sizeCapture := search.StartTextSizeCapture(root, pendingPaths)
	defer sizeCapture.Stop()

	matches := []TargetMatch{
		{Path: "src", Kind: treeTargetKindDir},
		{Path: "src/a.go", Kind: treeTargetKindFile},
	}
	labels, _ := TargetMatchLabels(matches[:1])
	selected, lease, err := chooseManyTargetMatchesWithFzfChrome(
		fzf,
		"",
		"select> ",
		"",
		"",
		labels,
		matches,
		git.Context{},
		sizeCapture,
		false,
		false,
	)
	if err != nil {
		t.Fatalf("chooseManyTargetMatchesWithFzfChrome() error = %v", err)
	}
	defer lease.Release()
	if got, want := selected, []string{"src"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected = %v, want %v", got, want)
	}
	if lease.sessionDir == "" || lease.inventoryPath == "" {
		t.Fatal("successful immediate confirmation did not return an inventory lease")
	}
	if _, err := ReadTargetPreviewInventory(lease.inventoryPath, root); err != nil {
		t.Fatalf("leased inventory was removed before transfer: %v", err)
	}

	resolver := &Resolver{}
	leasedDir := lease.sessionDir
	lease.TransferTo(resolver)
	retainedPath, ok := resolver.RetainedTargetPreviewInventoryPath()
	if !ok || retainedPath == "" {
		t.Fatal("resolver did not adopt the retained target inventory")
	}
	lease.Release()
	if _, err := os.Stat(retainedPath); err != nil {
		t.Fatalf("released local lease deleted resolver-owned inventory: %v", err)
	}

	resolver.ReleaseRetainedTargetPreviewInventory()
	if _, ok := resolver.RetainedTargetPreviewInventoryPath(); ok {
		t.Fatal("released resolver still reports a retained inventory")
	}
	if _, err := os.Stat(leasedDir); !os.IsNotExist(err) {
		t.Fatalf("session directory survived resolver release: %v", err)
	}
}

func TestCancelledTargetPickerDoesNotRetainPreviewSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake fzf requires /bin/sh")
	}
	root := t.TempDir()
	fzf := filepath.Join(root, "fake-fzf")
	if err := os.WriteFile(fzf, []byte("#!/bin/sh\nexit 130\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	previewSession := ""
	_, err := chooseManyWithFzfOptionsResult(
		fzf,
		"",
		"select> ",
		"2",
		"1,2",
		"",
		"",
		func(sessionDir string) previewCommandSetup {
			previewSession = sessionDir
			return previewCommandSetup{RetainSessionOnSuccess: true}
		},
		[]string{"[dir]\tsrc"},
	)
	if err != ErrSelectionCancelled {
		t.Fatalf("error = %v, want ErrSelectionCancelled", err)
	}
	if previewSession == "" {
		t.Fatal("preview builder did not receive a session")
	}
	if _, statErr := os.Stat(previewSession); !os.IsNotExist(statErr) {
		t.Fatalf("cancelled picker retained session directory: %v", statErr)
	}
}

func TestResolverReleasesEveryRetainedTargetGenerationAtSessionEnd(t *testing.T) {
	root := t.TempDir()
	resolver := &Resolver{}
	dirs := []string{
		filepath.Join(root, "generation-1"),
		filepath.Join(root, "generation-2"),
	}
	for index, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		inventoryPath := filepath.Join(dir, "targets.bin")
		if err := os.WriteFile(inventoryPath, []byte("inventory"), 0o600); err != nil {
			t.Fatal(err)
		}
		resolver.retainTargetPreviewInventory(dir, inventoryPath)
		if index == 1 {
			if got, ok := resolver.RetainedTargetPreviewInventoryPath(); !ok || got != inventoryPath {
				t.Fatalf("latest retained path = %q, %t; want %q, true", got, ok, inventoryPath)
			}
		}
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("retained generation was removed before session end: %v", err)
		}
	}
	resolver.ReleaseRetainedTargetPreviewInventory()
	for _, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("retained generation survived session release: %v", err)
		}
	}
}

func entryPaths(entries []Entry) []string {
	out := make([]string, len(entries))
	for i := range entries {
		out[i] = entries[i].RelPath
	}
	return out
}
