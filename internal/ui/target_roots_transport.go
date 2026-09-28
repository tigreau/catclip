package ui

import (
	"os"
	"path/filepath"

	"github.com/tigreau/catclip/internal/discovery"
)

// Keep large target projections out of argv without rewriting the inventory.
// Same-state revisits reuse the descriptor; reset owns its artifact directory.
func scopeViewMemoTargetRoots(args []string) (string, bool) {
	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	entry, ok := scopeViewMemoValues[key]
	if !ok || !entry.targetSelectionBase {
		scopeViewMemoMu.Unlock()
		return "", false
	}
	if entry.targetRootsPath != "" {
		scopeViewMemoMu.Unlock()
		return entry.targetRootsPath, true
	}
	targets := append([]string(nil), entry.view.Scope.Targets...)
	scopeViewMemoMu.Unlock()
	dir, err := os.MkdirTemp("", "catclip-target-roots-*")
	if err != nil {
		return "", false
	}
	path := filepath.Join(dir, "roots.json")
	if err := discovery.WriteTargetRoots(path, targets); err != nil {
		_ = os.RemoveAll(dir)
		return "", false
	}
	scopeViewMemoMu.Lock()
	current, ok := scopeViewMemoValues[key]
	if !ok || current.stateID != entry.stateID || current.targetRootsPath != "" {
		scopeViewMemoMu.Unlock()
		_ = os.RemoveAll(dir)
		return current.targetRootsPath, ok && current.stateID == entry.stateID && current.targetRootsPath != ""
	}
	current.targetRootsPath = path
	scopeViewMemoValues[key] = current
	scopeViewMemoInventoryDirs = append(scopeViewMemoInventoryDirs, dir)
	scopeViewMemoMu.Unlock()
	return path, true
}
