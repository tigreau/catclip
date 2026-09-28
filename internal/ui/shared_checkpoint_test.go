package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
)

func TestSharedRetainedCheckpointReuseAndCleanup(t *testing.T) {
	t.Chdir(t.TempDir())
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	entries := make([]discovery.Entry, 8192)
	metadata := make(map[string]search.FileMetadata, len(entries))
	for i := range entries {
		ext := "go"
		if i%2 != 0 {
			ext = "c"
		}
		name := fmt.Sprintf("file%06d.%s", i, ext)
		entries[i] = discovery.Entry{RelPath: name, SizeKnown: true, SizeBytes: 10, Mode: command.EntryModeFull}
		metadata[name] = search.FileMetadata{SizeBytes: 10}
	}
	baseArgs := []string{"--quiet", "--print", "."}
	if !scopeViewMemoAdoptTargetSelection(baseArgs, git.Context{}, entries, metadata) {
		t.Fatal("adopt failed")
	}
	// Same-generation concurrent states must single-flight their shared base.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, pattern := range []string{"*.go", "*.c"} {
		args := append(append([]string(nil), baseArgs...), "--only", pattern)
		view, err := resolvedCurrentScopeViewForArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() { defer wg.Done(); _, _, err := scopeViewMemoCheckpoint(args, view, nil); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var inventoryPath string
	var statePaths []string
	for _, pattern := range []string{"*.go", "*.c"} {
		args := append(append([]string(nil), baseArgs...), "--only", pattern)
		view, err := resolvedCurrentScopeViewForArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		path, owned, err := scopeViewMemoCheckpoint(args, view, nil)
		if err != nil || !owned {
			t.Fatalf("checkpoint %v %v", owned, err)
		}
		statePaths = append(statePaths, path)
		got, err := discovery.ReadCheckpoint(path)
		if err != nil {
			t.Fatal(err)
		}
		wantRaw, err := discovery.MarshalCheckpoint(discovery.CheckpointData{Entries: view.Entries})
		if err != nil {
			t.Fatal(err)
		}
		want, err := discovery.UnmarshalCheckpoint(wantRaw)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Entries, want.Entries) {
			t.Fatal("checkpoint entry parity")
		}
		ref := view.inventory.checkpointInventory
		if ref.Path == "" {
			t.Fatal("large state did not share inventory")
		}
		if inventoryPath != "" && inventoryPath != ref.Path {
			t.Fatal("sibling wrote another inventory")
		}
		inventoryPath = ref.Path
		again, _, err := scopeViewMemoCheckpoint(args, view, nil)
		if err != nil || again != path {
			t.Fatal("state checkpoint was not reused")
		}
	}
	if len(scopeViewMemoInventoryDirs) != 1 {
		t.Fatalf("created %d base inventories", len(scopeViewMemoInventoryDirs))
	}
	scopeViewMemoReset()
	for _, p := range append(statePaths, inventoryPath) {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("artifact survived reset: %s %v", p, err)
		}
	}
}

// This compares actual retained checkpoint preparation against standalone JSON
// on the complete corpus, excluding discovery and completed primary stat.
// Preview decoding is measured separately from the menu-opening boundary.
func TestSharedCheckpointCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full corpus")
	}
	corpus := filepath.Join(os.Getenv("HOME"), "Desktop", "catclip-test-data")
	t.Chdir(corpus)
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	baseArgs := []string{"--quiet", "--print", "."}
	base, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := retainedScopeViewEntriesWithMetadata(base); !ok {
		t.Fatal("metadata incomplete")
	}
	t.Logf("corpus entries=%d", len(base.Entries))
	for _, pattern := range []string{"*.c", "*", "*.h", "*.md"} {
		args := append(append([]string(nil), baseArgs...), "--only", pattern)
		start := time.Now()
		view, err := resolvedCurrentScopeViewForArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		stateTime := time.Since(start)
		ready, ok := retainedScopeViewEntriesWithMetadata(view)
		if !ok {
			t.Fatal("state metadata incomplete")
		}
		data := discovery.CheckpointData{Entries: ready, GitContext: view.GitContext, NoIgnore: view.Scope.NoIgnore}
		legacy := filepath.Join(t.TempDir(), "scope.json")
		start = time.Now()
		// Include the same validation and metadata projection paid by the old
		// retained checkpoint writer, not just its JSON encoding call.
		_, key := scopeViewMemoKey(args)
		scopeViewMemoMu.Lock()
		memo := scopeViewMemoValues[key]
		scopeViewMemoMu.Unlock()
		expected := materializeScopeView(memo)
		if !sameCheckpointScopeProjection(view, expected) {
			t.Fatal("projection mismatch")
		}
		if _, ok := retainedScopeViewEntriesWithMetadata(view); !ok {
			t.Fatal("metadata missing")
		}
		if err := discovery.WriteCheckpoint(legacy, corpus, data); err != nil {
			t.Fatal(err)
		}
		legacyWrite := time.Since(start)
		start = time.Now()
		want, err := discovery.ReadCheckpoint(legacy)
		if err != nil {
			t.Fatal(err)
		}
		legacyRead := time.Since(start)
		start = time.Now()
		path, owned, err := scopeViewMemoCheckpoint(args, view, nil)
		if err != nil || !owned {
			t.Fatalf("checkpoint %v %v", owned, err)
		}
		sharedPrepare := time.Since(start)
		start = time.Now()
		got, err := discovery.ReadCheckpoint(path)
		if err != nil {
			t.Fatal(err)
		}
		sharedRead := time.Since(start)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("full corpus checkpoint parity failed")
		}
		oldInfo, _ := os.Stat(legacy)
		newInfo, _ := os.Stat(path)
		var inventoryBytes int64
		if ref := view.inventory.checkpointInventory; ref.Path != "" {
			info, err := os.Stat(ref.Path)
			if err != nil {
				t.Fatal(err)
			}
			inventoryBytes = info.Size()
		}
		t.Logf("pattern=%s entries=%d derive=%s legacy_prepare=%s retained_prepare=%s legacy_read=%s retained_read=%s legacy_bytes=%d state_bytes=%d inventory_bytes=%d", pattern, len(ready), stateTime, legacyWrite, sharedPrepare, legacyRead, sharedRead, oldInfo.Size(), newInfo.Size(), inventoryBytes)
	}
	// Expanding the retained universe must create a new immutable base while
	// old descriptors remain readable and undo does not leak ignored paths.
	oldArgs := append(append([]string(nil), baseArgs...), "--only", "*")
	oldPath, ok := scopeViewMemoCheckpointPath(oldArgs)
	if !ok {
		t.Fatal("old checkpoint missing")
	}
	oldData, err := discovery.ReadCheckpoint(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	noIgnoreArgs := append(append([]string(nil), baseArgs...), "--no-ignore")
	expanded, err := resolvedCurrentScopeViewForArgs(noIgnoreArgs)
	if err != nil {
		t.Fatal(err)
	}
	newPath, owned, err := scopeViewMemoCheckpoint(noIgnoreArgs, expanded, nil)
	if err != nil || !owned {
		t.Fatalf("no-ignore checkpoint: %v", err)
	}
	newData, err := discovery.ReadCheckpoint(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(newData.Entries) != len(expanded.Entries) || !newData.NoIgnore {
		t.Fatal("no-ignore projection lost")
	}
	undo, err := discovery.ReadCheckpoint(oldPath)
	if err != nil || !reflect.DeepEqual(undo, oldData) {
		t.Fatalf("expansion changed old checkpoint: %v", err)
	}
	t.Logf("no-ignore=%d old=%d shared inventories=%d", len(newData.Entries), len(oldData.Entries), len(scopeViewMemoInventoryDirs))
}
