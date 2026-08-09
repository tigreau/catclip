package discovery

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/search"
)

func (r *Resolver) allVisibleTargets() ([]TargetMatch, error) {
	if r.interactiveTargetsOk {
		return append([]TargetMatch(nil), r.interactiveTargets...), nil
	}
	if err := r.BuildVisibleDirIndex(); err != nil {
		return nil, err
	}
	if err := r.BuildVisibleFileList(); err != nil {
		return nil, err
	}

	targets := make([]TargetMatch, 0, len(r.VisibleDirs.Dirs)+len(r.VisibleFileList))
	for _, rel := range r.VisibleDirs.Dirs {
		targets = append(targets, TargetMatch{Path: rel, Kind: "dir", State: treeTargetStateOK})
	}
	for _, entry := range r.VisibleFileList {
		targets = append(targets, TargetMatch{Path: entry.RelPath, Kind: "file", State: treeTargetStateText})
	}

	r.interactiveTargets = targets
	r.interactiveTargetsOk = true
	return append([]TargetMatch(nil), targets...), nil
}

// AdoptVisibleTargetInventoryFrom publishes a complete target inventory built
// by a compatible resolver copy. Startup probing uses shallow per-scope copies;
// adopting only this immutable base inventory lets the eventual interactive
// picker avoid rebuilding it without sharing mutable scope state.
func (r *Resolver) AdoptVisibleTargetInventoryFrom(source *Resolver) bool {
	if source == nil || !source.interactiveTargetsOk ||
		r.Cfg.WorkingDir != source.Cfg.WorkingDir ||
		r.WithBinaries != source.WithBinaries ||
		r.AllowFileSymlinks != source.AllowFileSymlinks ||
		!sameStrings(r.ScopeTargets, source.ScopeTargets) {
		return false
	}
	r.interactiveTargets = append([]TargetMatch(nil), source.interactiveTargets...)
	r.interactiveTargetsOk = true
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ignoredTargetsCacheKey mirrors search's scoped cache keying: the
// narrowed target list joined with NUL, "" for the wide universe.
func ignoredTargetsCacheKey(narrowed []string) string {
	return strings.Join(narrowed, "\x00")
}

// narrowableScopeTargets mirrors search.scopedCacheTargets: the
// normalized literal target list that is safe to hand rg as positional
// walk roots, or nil when the universe must stay working-dir-wide (no
// targets, a "." target, a glob, or a fuzzy/missing path — resolution
// may then land anywhere, so narrowing would be incorrect).
func narrowableScopeTargets(workingDir string, targets []string) []string {
	if len(targets) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(targets))
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		if hasGlobChars(t) {
			return nil
		}
		rel := normalizeRelPath(t)
		if rel == "" || rel == "." {
			return nil
		}
		if _, err := os.Stat(filepath.Join(workingDir, filepath.FromSlash(rel))); err != nil {
			return nil
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func (r *Resolver) AllIgnoredTargets(scopeTargets []string) ([]TargetMatch, error) {
	return r.targetInventoryUnderNoIgnore(scopeTargets, true)
}

// AllNoIgnoreTargets returns the complete file-and-directory target universe
// under --no-ignore. Visible rows and rows normally blocked by .gitignore or
// .hiss share one inventory so the flag has the same meaning in traversal,
// interactive selection, and headless fuzzy resolution.
func (r *Resolver) AllNoIgnoreTargets(scopeTargets []string) ([]TargetMatch, error) {
	return r.targetInventoryUnderNoIgnore(scopeTargets, false)
}

func (r *Resolver) targetInventoryUnderNoIgnore(scopeTargets []string, ignoredOnly bool) ([]TargetMatch, error) {
	// Narrow the enumeration universe to the scope targets when they are
	// literal on-disk paths: the downstream
	// filterIgnoredTargetsByScopeTargets keeps ONLY entries under a
	// target or on a target's ancestor chain, so anything a wider walk
	// finds is discarded anyway (live failure 2026-07-04: cwd=Desktop,
	// target=vscode-main walked and content-scanned the entire Desktop
	// to then throw it all away). Ancestor entries survive narrowing
	// because dir rows are derived from the walked files' path PREFIXES —
	// a walk rooted at blocked/sub still contributes "blocked" to the
	// dir set. Wide fallback (narrowed == nil) covers "."/fuzzy/glob
	// targets, where the filter keeps everything and wide is right-sized
	// by definition.
	narrowed := narrowableScopeTargets(r.Cfg.WorkingDir, scopeTargets)
	cacheKind := "all\x00"
	if ignoredOnly {
		cacheKind = "ignored\x00"
	}
	cacheKey := cacheKind + ignoredTargetsCacheKey(narrowed)
	if cached, ok := r.targetInventoriesByScope[cacheKey]; ok {
		return append([]TargetMatch(nil), cached...), nil
	}

	rgPaths, err := search.RunRipgrepFiles(r.Cfg.WorkingDir, search.RipgrepFileOptions{NoIgnore: true, Paths: narrowed})
	if err != nil {
		return nil, err
	}
	// The text set narrows with the SAME list so the walked universe and
	// the classification universe agree (ResolveTextFileSet re-applies the
	// identical literal-targets rule internally; nil = wide for both).
	var projectTextSet map[string]struct{}
	if !r.WithBinaries {
		projectTextSet, err = search.ResolveTextFileSet(r.Cfg.WorkingDir, narrowed)
		if err != nil {
			return nil, err
		}
	}

	filePaths := make([]string, 0, len(rgPaths))
	dirSet := make(map[string]struct{}, len(rgPaths)/2)
	dirHasText := make(map[string]bool, len(rgPaths)/2)
	for _, rel := range rgPaths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." {
			continue
		}
		for d := normalizeRelPath(path.Dir(rel)); d != "" && d != "."; d = normalizeRelPath(path.Dir(d)) {
			dirSet[d] = struct{}{}
		}

		isText := r.WithBinaries
		if !isText {
			_, isText = projectTextSet[rel]
		}
		if !isText {
			continue
		}
		filePaths = append(filePaths, rel)
		for d := normalizeRelPath(path.Dir(rel)); d != "" && d != "."; d = normalizeRelPath(path.Dir(d)) {
			dirHasText[d] = true
		}
	}

	dirPaths := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirPaths = append(dirPaths, d)
	}
	sort.Strings(dirPaths)

	// Source attribution via two rg-derived visible-file sets:
	//   visibleAll      = excluded by .gitignore only
	//   visibleWithHiss = excluded by .gitignore + .hiss overlay
	// A path missing from visibleWithHiss but present in visibleAll is
	// blocked by .hiss only. Missing from visibleAll → blocked by
	// .gitignore (precedence). This inventory describes the configured ignore
	// rules even when the current scope uses --no-ignore; broad traversal policy
	// changes eligibility, not which paths those rules would normally hide.
	ignoredFiles := map[string]string{}
	ignoredDirs := map[string]string{}
	visibleAll, visibleWithHiss, err := r.resolveIgnoreSets()
	if err != nil {
		return nil, err
	}
	visibleAllDirs := search.DirsContainingFiles(visibleAll)
	visibleWithHissDirs := search.DirsContainingFiles(visibleWithHiss)

	for _, rel := range filePaths {
		if _, ok := visibleWithHiss[rel]; ok {
			continue
		}
		if _, ok := visibleAll[rel]; ok {
			ignoredFiles[rel] = ".hiss"
		} else {
			ignoredFiles[rel] = ".gitignore"
		}
	}
	for _, rel := range dirPaths {
		if _, ok := visibleWithHissDirs[rel]; ok {
			continue
		}
		if _, ok := visibleAllDirs[rel]; ok {
			ignoredDirs[rel] = ".hiss"
		} else {
			ignoredDirs[rel] = ".gitignore"
		}
	}

	targets := make([]TargetMatch, 0, len(ignoredDirs)+len(ignoredFiles))
	for _, rel := range dirPaths {
		match := TargetMatch{Path: rel, Kind: "dir", State: treeTargetStateOK}
		if source, ok := ignoredDirs[rel]; ok {
			match.Ignored = true
			match.IgnoreSource = source
		}
		if !dirHasText[rel] {
			match.State = treeTargetStateNoTextChildren
		}
		if !ignoredOnly || match.Ignored {
			targets = append(targets, match)
		}
	}
	for _, rel := range filePaths {
		match := TargetMatch{Path: rel, Kind: "file", State: treeTargetStateText}
		if source, ok := ignoredFiles[rel]; ok {
			match.Ignored = true
			match.IgnoreSource = source
		}
		if !ignoredOnly || match.Ignored {
			targets = append(targets, match)
		}
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			return targets[i].Kind < targets[j].Kind
		}
		if targets[i].IgnoreSource != targets[j].IgnoreSource {
			return targets[i].IgnoreSource < targets[j].IgnoreSource
		}
		return targets[i].Path < targets[j].Path
	})

	if r.targetInventoriesByScope == nil {
		r.targetInventoriesByScope = make(map[string][]TargetMatch, 4)
	}
	r.targetInventoriesByScope[cacheKey] = targets
	return append([]TargetMatch(nil), targets...), nil
}

// ReadableHissPath returns the global .hiss path, materializing the
// default contents if the file doesn't exist yet. A broken or unreadable
// .hiss is fatal: rg's --ignore-file silently warns and continues if the
// file can't be opened, which would let users sit with a degraded ignore
// view they didn't realize was happening. Bubble the error up instead.
func ReadableHissPath() (string, error) {
	hissPath, err := EnsureGlobalHiss()
	if err != nil {
		return "", fmt.Errorf("hiss: %w", err)
	}
	info, err := os.Stat(hissPath)
	if err != nil {
		return "", fmt.Errorf("hiss: stat %s: %w", hissPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("hiss: %s is a directory, expected a file", hissPath)
	}
	f, err := os.Open(hissPath)
	if err != nil {
		return "", fmt.Errorf("hiss: open %s: %w", hissPath, err)
	}
	f.Close()
	return hissPath, nil
}
