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
func ExpandEntriesUnderNoIgnore(cfg command.Invocation, gitCtx git.Context, scope command.ExecutionScope, entries []Entry, enumeration search.MembershipEnumerationContext) ([]Entry, error) {
	finishBench := platform.InternalBenchSpan("discovery.no_ignore_generation",
		"targets", platform.InternalBenchInt(len(scope.Targets)),
		"retained", platform.InternalBenchInt(len(entries)),
	)
	resolver := Resolver{
		Cfg:                   cfg,
		GitCtx:                gitCtx,
		AllowFileSymlinks:     false,
		WithBinaries:          cfg.WithBinaries,
		NoIgnore:              true,
		WantedBasenames:       CollectWantedBasenames(scope.Targets),
		ScopeTargets:          append([]string(nil), scope.Targets...),
		MembershipEnumeration: enumeration,
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
	relPath        string
	kind           noIgnoreTargetKind
	identity       os.FileInfo
	componentCount int
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
			specs = append(specs, noIgnoreTargetSpec{
				relPath:        target,
				kind:           kind,
				identity:       info,
				componentCount: strings.Count(strings.Trim(target, "/"), "/") + 1,
			})
		} else if info.Mode().IsRegular() {
			specs = append(specs, noIgnoreTargetSpec{
				relPath:        target,
				kind:           noIgnoreTargetFile,
				identity:       info,
				componentCount: strings.Count(strings.Trim(target, "/"), "/") + 1,
			})
			walkRoots = append(walkRoots, target)
		}
	}

	if len(specs) == 0 {
		return append([]Entry(nil), retained...), true, nil
	}
	if broadWalk {
		walkRoots = []string{"."}
	}
	rels, err := ripgrepListUnderTargets(r.Cfg.WorkingDir, walkRoots, true, r.membershipEnumeration(search.MembershipReasonNoIgnoreExpansion))
	if err != nil {
		return nil, true, err
	}
	candidates := make([]noIgnoreCandidate, 0)
	for _, rel := range rels {
		rel = normalizeRelPath(rel)
		if rel == "" {
			continue
		}
		root, matched, matchErr := r.noIgnoreTargetRootForPath(specs, rel)
		if matchErr != nil {
			return nil, true, matchErr
		}
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

func (r *Resolver) noIgnoreTargetRootForPath(specs []noIgnoreTargetSpec, rel string) (string, bool, error) {
	for i := range specs {
		if err := r.resolveNoIgnoreLiteralAliasForPath(&specs[i], rel); err != nil {
			return "", false, err
		}
	}
	root, matched := noIgnoreTargetRootForPath(specs, rel)
	return root, matched, nil
}

// resolveNoIgnoreLiteralAliasForPath updates a differently-cased literal to
// the spelling emitted by the broad rg walk, but only after the filesystem
// proves both spellings name the same object. The current rel is already in
// memory; this adds no directory enumeration and normally performs no stat.
func (r *Resolver) resolveNoIgnoreLiteralAliasForPath(spec *noIgnoreTargetSpec, rel string) error {
	if spec == nil || spec.identity == nil || (spec.kind != noIgnoreTargetDir && spec.kind != noIgnoreTargetFile) {
		return nil
	}
	candidate := rel
	if spec.kind == noIgnoreTargetDir {
		var ok bool
		candidate, ok = noIgnoreDirectoryPrefix(rel, spec.componentCount)
		if !ok {
			return nil
		}
	}
	if candidate == spec.relPath {
		// The broad walk has now proved the typed spelling is already canonical;
		// stop doing alias work for the rest of this generation.
		spec.identity = nil
		return nil
	}
	if !strings.EqualFold(candidate, spec.relPath) {
		return nil
	}
	candidateInfo, err := os.Lstat(filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(candidate)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if os.SameFile(spec.identity, candidateInfo) {
		spec.relPath = candidate
		spec.identity = nil
	}
	return nil
}

// noIgnoreDirectoryPrefix returns the first componentCount components of a
// descendant path without allocating. The next slash is required because a
// directory target selects strict descendants, not a same-named file row.
func noIgnoreDirectoryPrefix(rel string, componentCount int) (string, bool) {
	if componentCount <= 0 {
		return "", false
	}
	start := 0
	for component := 0; component < componentCount; component++ {
		separator := strings.IndexByte(rel[start:], '/')
		if separator < 0 {
			return "", false
		}
		separator += start
		if component == componentCount-1 {
			return rel[:separator], true
		}
		start = separator + 1
	}
	return "", false
}

func noIgnoreTargetRootForPath(specs []noIgnoreTargetSpec, rel string) (string, bool) {
	matchedAny := false
	for _, spec := range specs {
		candidateRoot := ""
		matched := false
		switch spec.kind {
		case noIgnoreTargetAll:
			matched = true
		case noIgnoreTargetGlob:
			matched, _ = path.Match(spec.relPath, path.Base(rel))
			if !matched {
				matched, _ = path.Match(spec.relPath, rel)
			}
		case noIgnoreTargetDir:
			prefix := strings.TrimSuffix(spec.relPath, "/") + "/"
			if strings.HasPrefix(rel, prefix) {
				candidateRoot = spec.relPath
				matched = true
			}
		case noIgnoreTargetFile:
			if rel == spec.relPath {
				candidateRoot = normalizeRelPath(path.Dir(spec.relPath))
				if candidateRoot == "." {
					candidateRoot = ""
				}
				matched = true
			}
		}
		if !matched {
			continue
		}
		matchedAny = true
		// Canonical MergeFileEntry semantics: a broad/root match may be
		// enriched by the first later non-empty target root, but an existing
		// non-empty root is not replaced by another overlapping target.
		if candidateRoot != "" {
			return candidateRoot, true
		}
	}
	return "", matchedAny
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
	rels, err := ripgrepListUnder(r.Cfg.WorkingDir, rootRel, true, r.membershipEnumeration(search.MembershipReasonNoIgnoreExpansion))
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
