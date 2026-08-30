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
		targets = append(targets, TargetMatch{
			Path:      entry.RelPath,
			Kind:      "file",
			State:     treeTargetStateText,
			SizeBytes: entry.SizeBytes,
			SizeKnown: entry.SizeKnown,
		})
	}

	r.interactiveTargets = targets
	r.interactiveTargetsOk = true
	return append([]TargetMatch(nil), targets...), nil
}

func (r *Resolver) ensureTargetPreviewSizeCapture(matches []TargetMatch) *search.TextSizeCapture {
	if r.targetPreviewSizes != nil && !r.targetPreviewSizes.Cancelled() {
		return r.targetPreviewSizes
	}
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		if match.Kind == treeTargetKindFile {
			paths = append(paths, match.Path)
		}
	}
	r.targetPreviewSizes = search.StartTextSizeCapture(r.Cfg.WorkingDir, paths)
	return r.targetPreviewSizes
}

// FinalizeTargetSelection seals the target picker's classified membership and
// primary metadata for the selected paths. It never enumerates: the entries
// are projected from the inventory already shown by the picker.
func (r *Resolver) FinalizeTargetSelection(targets []string) {
	if r != nil {
		// A later target frame must never inherit an earlier committed projection
		// when its own target universe cannot be represented by the picker
		// inventory.
		r.committedTargetEntries = nil
		r.committedTargetMetadata = nil
		r.committedTargetReady = false
		r.committedTargetPreviewPath = ""
	}
	if r == nil || !r.targetPreviewInventoryOK || len(targets) == 0 {
		return
	}
	if !r.targetSelectionRepresentableByPreviewInventory(targets) {
		return
	}
	base := TargetPreviewEntries(r.Cfg.WorkingDir, r.targetPreviewInventory, nil)
	selected := SelectTargetPreviewEntries(base, targets)
	paths := make([]string, 0, len(selected))
	for _, entry := range selected {
		paths = append(paths, entry.RelPath)
	}
	if r.targetPreviewSizes == nil {
		r.targetPreviewSizes = search.StartTextSizeCapture(r.Cfg.WorkingDir, paths)
	}
	metadata := r.targetPreviewSizes.FinalizeSelection(paths)
	r.committedTargetEntries = SelectTargetPreviewEntries(
		TargetPreviewEntries(r.Cfg.WorkingDir, r.targetPreviewInventory, metadata),
		targets,
	)
	r.committedTargetMetadata = metadata
	r.committedTargetReady = true
	r.reuseCompletedTargetPreviewInventory()
}

// targetSelectionRepresentableByPreviewInventory rejects exact ignored roots
// that are intentionally absent from the visible target inventory. The caller
// may then seal that authorized target generation through canonical bounded
// discovery instead of silently projecting an incomplete set. Visible roots,
// globs, and a --no-ignore inventory are complete by construction.
func (r *Resolver) targetSelectionRepresentableByPreviewInventory(targets []string) bool {
	if r.NoIgnore {
		return true
	}
	for _, rawTarget := range targets {
		target := normalizeRelPath(rawTarget)
		if target == "" || target == "." || hasGlobChars(target) {
			continue
		}
		info, err := os.Lstat(filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(target)))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		var block *BlockInfo
		if info.IsDir() {
			block, err = r.dirBlockedBy(target)
		} else if info.Mode().IsRegular() {
			block, err = r.fileBlockedBy(target)
		}
		if err != nil || block != nil {
			return false
		}
	}
	return true
}

// CommittedTargetSelection returns detached rows and terminal metadata for the
// most recently confirmed target generation.
func (r *Resolver) CommittedTargetSelection() ([]Entry, map[string]search.FileMetadata, bool) {
	if r == nil || !r.committedTargetReady {
		return nil, nil, false
	}
	entries := append([]Entry(nil), r.committedTargetEntries...)
	metadata := make(map[string]search.FileMetadata, len(r.committedTargetMetadata))
	for path, record := range r.committedTargetMetadata {
		metadata[path] = record
	}
	return entries, metadata, true
}

// RetainedTargetPreviewInventoryPath returns the compact inventory backing the
// most recently committed interactive target picker. The path remains valid
// until the resolver adopts another target inventory or the interactive
// session releases it.
func (r *Resolver) RetainedTargetPreviewInventoryPath() (string, bool) {
	if r == nil || r.retainedTargetPreviewPath == "" {
		return "", false
	}
	return r.retainedTargetPreviewPath, true
}

// CommittedTargetPreviewInventoryPath returns a compact, metadata-complete
// artifact usable by the most recently confirmed target selection. It may be
// the picker-wide completed inventory; the preview child still projects the
// exact sealed membership from the scope's canonical targets.
func (r *Resolver) CommittedTargetPreviewInventoryPath() (string, bool) {
	if r == nil || r.committedTargetPreviewPath == "" {
		return "", false
	}
	return r.committedTargetPreviewPath, true
}

func (r *Resolver) reuseCompletedTargetPreviewInventory() bool {
	if r == nil || r.retainedTargetPreviewPath == "" {
		return false
	}
	// If primary metadata completed while the picker was open, the existing
	// broad inventory is already truthful for every possible selection. Reuse
	// it directly instead of serializing the committed subset again. A pending
	// base publishes its completed sidecar only after the background writer has
	// finished, and picker cleanup joins that writer before ownership transfer.
	if r.retainedTargetPreviewReady {
		r.committedTargetPreviewPath = r.retainedTargetPreviewPath
		return true
	}
	if sizedPath := TargetPreviewSizedInventoryPath(r.retainedTargetPreviewPath); sizedPath != "" {
		if _, err := os.Stat(sizedPath); err == nil {
			r.committedTargetPreviewPath = sizedPath
			return true
		}
	}
	return false
}

// beginTargetPreviewGeneration detaches the current target universe from any
// compact artifact owned by an earlier picker generation. Retained directories
// stay registered for session cleanup, but an exact auto-accepted target must
// not accidentally publish an older visible/no-ignore universe as its preview.
func (r *Resolver) beginTargetPreviewGeneration() {
	if r == nil {
		return
	}
	r.retainedTargetPreviewPath = ""
	r.retainedTargetPreviewReady = false
	r.committedTargetPreviewPath = ""
}

func (r *Resolver) retainTargetPreviewInventory(sessionDir, inventoryPath string, baseComplete ...bool) {
	if r == nil || sessionDir == "" || inventoryPath == "" {
		return
	}
	for _, retainedDir := range r.retainedTargetPreviewDirs {
		if retainedDir == sessionDir {
			r.retainedTargetPreviewPath = inventoryPath
			r.retainedTargetPreviewReady = len(baseComplete) > 0 && baseComplete[0]
			return
		}
	}
	r.retainedTargetPreviewDirs = append(r.retainedTargetPreviewDirs, sessionDir)
	r.retainedTargetPreviewPath = inventoryPath
	r.retainedTargetPreviewReady = len(baseComplete) > 0 && baseComplete[0]
}

// ReleaseRetainedTargetPreviewInventory closes the target inventory's
// interactive-session lifetime. It is safe to call on cancellation, errors,
// and after a prior release.
func (r *Resolver) ReleaseRetainedTargetPreviewInventory() {
	if r == nil {
		return
	}
	for _, sessionDir := range r.retainedTargetPreviewDirs {
		_ = os.RemoveAll(sessionDir)
	}
	r.retainedTargetPreviewDirs = nil
	r.retainedTargetPreviewPath = ""
	r.retainedTargetPreviewReady = false
	r.committedTargetPreviewPath = ""
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
	hissRel := catclipControlHissRel(r.Cfg.WorkingDir)
	for _, rel := range rgPaths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." || rel == hissRel {
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
