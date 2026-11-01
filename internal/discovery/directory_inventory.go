package discovery

import (
	"os"
	"path"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/search"
)

type directoryInventoryKey struct {
	root, hiss string
	noIgnore   bool
}

// directoryPaths reuses a completed parent enumeration within this resolver.
// For normal-ignore walks a visible descendant is required: an explicitly
// named ignored directory can admit files absent from the parent's inventory.
// No-ignore parents already include that membership. Cached paths are raw rg
// membership, before classification or stages; target-root stamping stays with
// the caller and exact-target eligibility/diagnostics still run normally.
func (r *Resolver) directoryPaths(root string, opts search.RipgrepFileOptions) ([]string, error) {
	// User rg configuration may impose root-relative limits such as max-depth.
	// Such a parent listing is not proof of a complete child inventory.
	if os.Getenv("RIPGREP_CONFIG_PATH") != "" {
		return search.RunRipgrepFiles(r.Cfg.WorkingDir, opts)
	}
	root = normalizeRelPath(root)
	if root == "" {
		root = "."
	}
	// Parent-relative and absolute roots do not share cwd-relative ancestry
	// with these inventories (path.Dir("..") is ".", but cwd cannot cover ..).
	if root == ".." || strings.HasPrefix(root, "../") || path.IsAbs(root) || strings.Contains(root, ":") {
		return search.RunRipgrepFiles(r.Cfg.WorkingDir, opts)
	}
	key := directoryInventoryKey{root: root, hiss: opts.HissPath, noIgnore: opts.NoIgnore}
	if paths, ok := r.directoryInventories[key]; ok {
		return paths, nil
	}
	for parent := path.Dir(root); root != "."; parent = path.Dir(parent) {
		parentKey := directoryInventoryKey{root: parent, hiss: key.hiss, noIgnore: key.noIgnore}
		if paths, ok := r.directoryInventories[parentKey]; ok {
			prefix := root + "/"
			start := sort.SearchStrings(paths, prefix)
			end := start
			for end < len(paths) && strings.HasPrefix(paths[end], prefix) {
				end++
			}
			if key.noIgnore || end > start {
				return paths[start:end], nil
			}
		}
		if parent == "." || parent == "/" || parent == path.Dir(parent) {
			break
		}
	}
	paths, err := search.RunRipgrepFiles(r.Cfg.WorkingDir, opts)
	if err != nil {
		return nil, err
	}
	// Single-target commands need no extra inventory retention.
	if len(r.ScopeTargets) > 1 {
		if r.directoryInventories == nil {
			r.directoryInventories = make(map[directoryInventoryKey][]string)
		}
		r.directoryInventories[key] = paths
	}
	return paths, nil
}
