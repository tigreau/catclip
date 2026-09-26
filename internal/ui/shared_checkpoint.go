package ui

import (
	"os"
	"path/filepath"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
)

// Large, broad selections amortize reading the generation inventory. Small
// selections keep their standalone document instead of decoding the whole base.
const sharedCheckpointMinEntries = 4096

func writeRetainedStateCheckpoint(key string, entry scopeViewMemoEntry, path string, view resolvedScopeView, data discovery.CheckpointData) error {
	if len(data.Entries) < sharedCheckpointMinEntries {
		return discovery.WriteCheckpoint(path, view.Invocation.WorkingDir, data)
	}
	// Serialize base publication with reset. The session registry owns base
	// directories independently of any one state; reset cannot remove a base
	// between its creation and publication by a surviving child state.
	scopeViewMemoMu.Lock()
	defer scopeViewMemoMu.Unlock()
	current, ok := scopeViewMemoValues[key]
	if !ok || current.stateID != entry.stateID || current.inventory != entry.inventory {
		// The caller's final publication check will discard this stale artifact.
		return nil
	}
	inventory := entry.inventory
	inventory.mu.Lock()
	defer inventory.mu.Unlock()
	if !inventory.metadataSealed || len(data.Entries)*4 < len(inventory.entries) ||
		(inventory.checkpointInventory.Path == "" && len(data.Entries)*2 < len(inventory.entries)) {
		return discovery.WriteCheckpoint(path, view.Invocation.WorkingDir, data)
	}
	if inventory.checkpointInventory.Path == "" {
		finish := platform.InternalBenchSpan("ui.checkpoint_inventory.write", "entries", platform.InternalBenchInt(len(inventory.entries)))
		dir, err := os.MkdirTemp("", "catclip-session-inventory-*")
		if err != nil {
			finish("err", "true")
			return err
		}
		ref, err := discovery.WriteCheckpointInventory(filepath.Join(dir, "inventory.bin"), inventory.entries)
		finish("err", platform.InternalBenchError(err))
		if err != nil {
			_ = os.RemoveAll(dir)
			return err
		}
		inventory.checkpointInventory = ref
		scopeViewMemoInventoryDirs = append(scopeViewMemoInventoryDirs, dir)
	}
	return discovery.WriteSharedCheckpoint(path, inventory.checkpointInventory, inventory.entries, entry.fileIDs, data)
}
