package discovery

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

// ExpandEntriesUnderNoIgnore performs the one authorized secondary
// enumeration in an interactive scope. The caller supplies the retained
// visible membership; this function walks only the scope's committed targets
// with ignore rules disabled and appends newly admitted text files. It does
// not apply any later narrowing or output stage.
func ExpandEntriesUnderNoIgnore(cfg command.Invocation, gitCtx git.Context, scope command.ExecutionScope, entries []Entry) ([]Entry, error) {
	finishBench := platform.InternalBenchSpan("discovery.no_ignore_generation",
		"targets", platform.InternalBenchInt(len(scope.Targets)),
		"retained", platform.InternalBenchInt(len(entries)),
	)
	resolver := Resolver{
		Cfg:               cfg,
		GitCtx:            gitCtx,
		AllowFileSymlinks: false,
		WithBinaries:      cfg.WithBinaries,
		NoIgnore:          true,
		WantedBasenames:   CollectWantedBasenames(scope.Targets),
		ScopeTargets:      append([]string(nil), scope.Targets...),
	}
	expanded, optimized, err := resolver.expandRetainedEntriesUnderNoIgnore(scope, entries)
	if err == nil && !optimized {
		// A retained interactive target is normally an exact committed path.
		// Keep canonical fuzzy/missing-target behavior as a correctness fallback
		// for callers that did not originate at that boundary.
		expanded, err = applyNoIgnoreStage(&resolver, scope, entries)
	}
	finishBench(
		"entries", platform.InternalBenchInt(len(expanded)),
		"added", platform.InternalBenchInt(len(expanded)-len(entries)),
		"err", platform.InternalBenchError(err),
	)
	return expanded, err
}

type noIgnoreCandidate struct {
	relPath    string
	targetRoot string
}

type noIgnoreTargetKind uint8

const (
	noIgnoreTargetAll noIgnoreTargetKind = iota
	noIgnoreTargetGlob
	noIgnoreTargetDir
	noIgnoreTargetFile
)

type noIgnoreTargetSpec struct {
	relPath string
	kind    noIgnoreTargetKind
}

// expandRetainedEntriesUnderNoIgnore reuses the visible generation's text
// classifications. The secondary rg walk identifies the broader path
// universe, but only paths absent from retained membership need classification
// and ignore attribution.
func (r *Resolver) expandRetainedEntriesUnderNoIgnore(scope command.ExecutionScope, retained []Entry) ([]Entry, bool, error) {
	known := make(map[string]struct{}, len(retained))
	for _, entry := range retained {
		known[normalizeRelPath(entry.RelPath)] = struct{}{}
	}
	targets := scope.Targets
	if len(targets) == 0 {
		targets = []string{"."}
	}
	specs := make([]noIgnoreTargetSpec, 0, len(targets))
	walkRoots := make([]string, 0, len(targets))
	broadWalk := false
	for _, rawTarget := range targets {
		target := normalizeRelPath(rawTarget)
		if target == "" {
			target = "."
		}
		if hasGlobChars(target) {
			if strings.Contains(target, "**") {
				return nil, true, newUsageError("%s", unsupportedTargetDoublestarMessage(target))
			}
			if _, matchErr := path.Match(target, ""); matchErr != nil {
				return nil, true, newUsageError("Error: Invalid glob pattern %s: %v", SingleQuoted(target), matchErr)
			}
			specs = append(specs, noIgnoreTargetSpec{relPath: target, kind: noIgnoreTargetGlob})
			broadWalk = true
			continue
		}

		info, err := os.Lstat(filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(target)))
		if err != nil {
			if os.IsNotExist(err) {
				return nil, false, nil
			}
			return nil, true, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			kind := noIgnoreTargetDir
			if target == "." {
				kind = noIgnoreTargetAll
				broadWalk = true
			} else {
				walkRoots = append(walkRoots, target)
			}
			specs = append(specs, noIgnoreTargetSpec{relPath: target, kind: kind})
		} else if info.Mode().IsRegular() {
			specs = append(specs, noIgnoreTargetSpec{relPath: target, kind: noIgnoreTargetFile})
			walkRoots = append(walkRoots, target)
		}
	}

	if len(specs) == 0 {
		return append([]Entry(nil), retained...), true, nil
	}
	if broadWalk {
		walkRoots = []string{"."}
	}
	rels, err := ripgrepListUnderTargets(r.Cfg.WorkingDir, walkRoots, true)
	if err != nil {
		return nil, true, err
	}
	candidates := make([]noIgnoreCandidate, 0)
	for _, rel := range rels {
		rel = normalizeRelPath(rel)
		if rel == "" {
			continue
		}
		root, matched := noIgnoreTargetRootForPath(specs, rel)
		if !matched {
			continue
		}
		if _, ok := known[rel]; ok {
			continue
		}
		known[rel] = struct{}{}
		candidates = append(candidates, noIgnoreCandidate{relPath: rel, targetRoot: root})
	}

	paths := make([]string, len(candidates))
	for i := range candidates {
		paths[i] = candidates[i].relPath
	}
	var textSet map[string]struct{}
	if !r.WithBinaries && len(paths) > 0 {
		textSet, err = search.ClassifyTextPaths(r.Cfg.WorkingDir, paths)
		if err != nil {
			return nil, true, err
		}
	}

	additions := make([]Entry, 0, len(candidates))
	for _, candidate := range candidates {
		if !r.WithBinaries {
			if _, ok := textSet[candidate.relPath]; !ok {
				continue
			}
		}
		entry := Entry{
			AbsPath:    filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(candidate.relPath)),
			RelPath:    candidate.relPath,
			TargetRoot: candidate.targetRoot,
		}
		block, err := r.fileBlockedBy(candidate.relPath)
		if err != nil {
			return nil, true, err
		}
		if block != nil {
			entry = withIgnoreBypassed(entry, *block)
		} else {
			entry.GitVisible = true
		}
		additions = append(additions, entry)
	}
	return mergeNoIgnoreEntries(retained, additions), true, nil
}

func noIgnoreTargetRootForPath(specs []noIgnoreTargetSpec, rel string) (string, bool) {
	for _, spec := range specs {
		switch spec.kind {
		case noIgnoreTargetAll:
			return "", true
		case noIgnoreTargetGlob:
			matched, _ := path.Match(spec.relPath, path.Base(rel))
			if !matched {
				matched, _ = path.Match(spec.relPath, rel)
			}
			if matched {
				return "", true
			}
		case noIgnoreTargetDir:
			prefix := strings.TrimSuffix(spec.relPath, "/") + "/"
			if strings.HasPrefix(rel, prefix) {
				return spec.relPath, true
			}
		case noIgnoreTargetFile:
			if rel == spec.relPath {
				root := normalizeRelPath(path.Dir(spec.relPath))
				if root == "." {
					root = ""
				}
				return root, true
			}
		}
	}
	return "", false
}

// mergeNoIgnoreEntries restores canonical path order without sorting the full
// expanded Entry slice. rg already sorts the admitted delta; retained picker
// membership is normally sorted too, so the common path is one linear merge.
func mergeNoIgnoreEntries(retained, additions []Entry) []Entry {
	base := append([]Entry(nil), retained...)
	if !sort.SliceIsSorted(base, func(i, j int) bool { return base[i].RelPath < base[j].RelPath }) {
		sort.SliceStable(base, func(i, j int) bool { return base[i].RelPath < base[j].RelPath })
	}
	if len(additions) == 0 {
		return base
	}
	if !sort.SliceIsSorted(additions, func(i, j int) bool { return additions[i].RelPath < additions[j].RelPath }) {
		sort.SliceStable(additions, func(i, j int) bool { return additions[i].RelPath < additions[j].RelPath })
	}
	out := make([]Entry, 0, len(base)+len(additions))
	i, j := 0, 0
	for i < len(base) && j < len(additions) {
		if base[i].RelPath <= additions[j].RelPath {
			out = append(out, base[i])
			i++
		} else {
			out = append(out, additions[j])
			j++
		}
	}
	out = append(out, base[i:]...)
	out = append(out, additions[j:]...)
	return out
}

func (r *Resolver) noIgnoreTargetWalked(target string) bool {
	if r.noIgnoreTargetWalks == nil {
		return false
	}
	_, ok := r.noIgnoreTargetWalks[normalizeRelPath(target)]
	return ok
}

func (r *Resolver) markNoIgnoreTargetWalk(target string) {
	if r.noIgnoreTargetWalks == nil {
		r.noIgnoreTargetWalks = make(map[string]struct{})
	}
	r.noIgnoreTargetWalks[normalizeRelPath(target)] = struct{}{}
}

// discoverFilesUnderNoIgnore performs one no-ignore walk below a target while
// retaining ignore attribution for rendering and picker previews.
func (r *Resolver) discoverFilesUnderNoIgnore(rootRel string) ([]Entry, error) {
	rels, err := ripgrepListUnder(r.Cfg.WorkingDir, rootRel, true)
	if err != nil {
		return nil, err
	}
	// The no-ignore walk already owns the complete candidate path list. Classify
	// that list directly and publish it to this resolver; asking
	// classifyTextFile to lazily call ResolveTextFileSet would launch a second
	// --no-ignore enumeration of the same roots before doing identical work.
	if !r.WithBinaries {
		r.textFileSet, err = search.ClassifyTextPaths(r.Cfg.WorkingDir, rels)
		if err != nil {
			return nil, err
		}
		r.textFileSetReady = true
	}
	files := make([]Entry, 0, len(rels))
	for _, rel := range rels {
		rel = normalizeRelPath(rel)
		if rel == "" {
			continue
		}
		absPath := filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(rel))
		text, err := r.classifyTextFile(rel, absPath)
		if err != nil {
			return nil, err
		}
		if !text {
			continue
		}
		entry := Entry{AbsPath: absPath, RelPath: rel}
		block, err := r.fileBlockedBy(rel)
		if err != nil {
			return nil, err
		}
		if block != nil {
			entry = withIgnoreBypassed(entry, *block)
		} else {
			entry.GitVisible = true
		}
		files = append(files, entry)
	}
	return files, nil
}
