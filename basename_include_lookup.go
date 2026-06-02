package catclip

import (
	"os"
	"path"
	"path/filepath"
	"sort"
)

// includedBasenameHit pairs a relative path with whether it's a directory,
// so callers can build the right targetMatch.Kind without a second stat.
type includedBasenameHit struct {
	Path string
	Dir  bool
}

func hitToTargetMatch(h includedBasenameHit) targetMatch {
	kind := "file"
	if h.Dir {
		kind = "dir"
	}
	return targetMatch{Path: h.Path, Kind: kind}
}

// Basename targets default to a ignore-respecting visible index that never
// sees --include'd content. After the visible lookup misses, this helper
// searches the *authorized* subtrees (only those — not the whole ignored set)
// for matching files or dirs, so a typed basename paired with `--include
// <ignored-ancestor-dir>` resolves the same way the slash-path branch
// already does. See:
//   docs/versions/v0.5.7/reports/RESOLVED_BUG_basename_target_ignores_include_subtree.md.
//
// Invariant: this runs only after normal (visible) resolution returned zero.
// Zero cost on the happy path.

func (r *scopeResolver) hasAnyIncludeActive() bool {
	return r.includedTargets.wildcard ||
		len(r.includedTargets.exact) > 0 ||
		len(r.includedTargets.dirs) > 0
}

// findBasenameInIncludedSubtrees returns paths whose basename equals `target`,
// or whose path contains `target` as a directory segment, restricted to the
// content authorized via --include. Each hit carries an `Dir` flag so the
// caller can build the right `targetMatch.Kind` without a second stat.
// Returns hits sorted and deduped by Path.
func (r *scopeResolver) findBasenameInIncludedSubtrees(target string) ([]includedBasenameHit, error) {
	if !r.hasAnyIncludeActive() {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []includedBasenameHit
	add := func(rel string, isDir bool) {
		rel = normalizeRelPath(rel)
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		out = append(out, includedBasenameHit{Path: rel, Dir: isDir})
	}

	statIsDir := func(rel string) bool {
		info, err := os.Stat(filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(rel)))
		return err == nil && info.IsDir()
	}

	// 1. Exact --include paths whose basename matches the target.
	for ex := range r.includedTargets.exact {
		if path.Base(ex) == target {
			add(ex, statIsDir(ex))
		}
	}

	probeUnder := func(dir string) error {
		opts := ripgrepFileOptions{
			NoIgnore:  true,
			Basenames: []string{target, "**/" + target + "/**"},
		}
		if dir != "" && dir != "." {
			opts.Paths = []string{dir}
		}
		hits, err := runRipgrepFiles(r.cfg.WorkingDir, opts)
		if err != nil {
			return err
		}
		for _, h := range hits {
			rel := normalizeRelPath(h)
			if rel == "" {
				continue
			}
			cand, isDir := classifyAncestorCandidate(rel, target)
			if cand == "" {
				continue
			}
			add(cand, isDir)
		}
		return nil
	}

	// 2. Wildcard --include '*' — search anywhere under workingDir.
	if r.includedTargets.wildcard {
		if err := probeUnder("."); err != nil {
			return nil, err
		}
	}

	// 3. Each --include'd dir — search inside.
	for _, dir := range r.includedTargets.dirs {
		if err := probeUnder(dir); err != nil {
			return nil, err
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
