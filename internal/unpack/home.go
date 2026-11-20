package unpack

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Bundle paths are relative to the folder catclip ran in. Copied inside
// frontend/, src/App.tsx means frontend/src/App.tsx; unpacked from the
// repository root or from backend/, the same path would create a new
// src/App.tsx in the wrong place. FindBundleHome looks for a nearby folder
// where the bundle's files do exist, so --unpack can point the user there
// instead of scattering files.

// homeCheckEntries bounds the work: a few paths decide where a bundle belongs.
const homeCheckEntries = 20

// FindBundleHome returns a folder near root (a subfolder, a parent, or a
// parent's subfolder) where at least one of the bundle's paths exists, when
// none exists under root itself. found is false when the files exist here, or
// nowhere nearby (a genuinely new set of files).
func FindBundleHome(root string, b Bundle) (home string, found bool) {
	paths := bundlePaths(b)
	if len(paths) == 0 || existingCount(root, paths) > 0 {
		return "", false
	}
	// A retry can have only the destination left. An old source in a
	// nearby checkout is weaker evidence than that local destination.
	for _, entry := range b.Entries[:min(len(b.Entries), homeCheckEntries)] {
		if entry.From != "" {
			if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Path))); err == nil && info.Mode().IsRegular() {
				return "", false
			}
		}
	}
	best, bestCount := "", 0
	for _, dir := range nearbyFolders(root) {
		if n := existingCount(dir, paths); n > bestCount {
			best, bestCount = dir, n
		}
	}
	return best, bestCount > 0
}

func bundlePaths(b Bundle) []string {
	paths := make([]string, 0, min(len(b.Entries), homeCheckEntries))
	for _, e := range b.Entries {
		if len(paths) == homeCheckEntries {
			break
		}
		// A move's new path does not exist yet; its old path does.
		if e.From != "" {
			paths = append(paths, e.From)
			continue
		}
		paths = append(paths, e.Path)
	}
	return paths
}

func existingCount(dir string, paths []string) int {
	n := 0
	for _, p := range paths {
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil && info.Mode().IsRegular() {
			n++
		}
	}
	return n
}

// nearbyFolders lists root's subfolders, up to three parents, and the
// subfolders of each parent, without hidden folders or dependency trees.
func nearbyFolders(root string) []string {
	var dirs []string
	dirs = append(dirs, subfolders(root)...)
	current := root
	for range 3 {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		dirs = append(dirs, parent)
		for _, sib := range subfolders(parent) {
			if sib != current {
				dirs = append(dirs, sib)
			}
		}
		current = parent
	}
	return dirs
}

func subfolders(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out
}
