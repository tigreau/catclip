package discovery

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
)

// GitStatusPathspecsForEntries returns the set of repo-relative
// pathspecs to query `git status` against for the given Entry slice.
// Used by the resolver's content-match picker, by output-plan setup
// at root, and by the startup picker. Was root output_plan.go.
func GitStatusPathspecsForEntries(gitCtx git.Context, entries []Entry) []string {
	set := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		AddGitStatusPathspec(set, gitCtx, entry)
	}
	pathspecs := make([]string, 0, len(set))
	for repoPath := range set {
		pathspecs = append(pathspecs, repoPath)
	}
	sort.Strings(pathspecs)
	return pathspecs
}

// AddGitStatusPathspec writes one entry's repo-relative pathspec into
// the in-progress set. Exposed for output_plan.go which appends to a
// shared set across plan items, so the helper can't stay private to
// GitStatusPathspecsForEntries.
func AddGitStatusPathspec(set map[string]struct{}, gitCtx git.Context, entry Entry) {
	repoPath := ""
	if entry.TargetRoot != "" && entry.TargetRoot != "." {
		repoPath = gitCtx.ToRepoPath(entry.TargetRoot)
	} else {
		repoPath = gitCtx.ToRepoPath(entry.RelPath)
	}
	repoPath = normalizeRelPath(repoPath)
	if repoPath == "" || repoPath == "." {
		return
	}
	set[repoPath] = struct{}{}
}

type textClassifier func(relPath, absPath string) (bool, error)

// discoverFilesUnder enumerates text files under rootRel using ripgrep.
// When rootBypass is set the call switches to --no-ignore so callers can
// recover paths blocked by .hiss/.gitignore while retaining attribution.
// When baseName is non-empty, only files whose basename equals baseName are
// returned; pass "" to skip the filter.
func discoverFilesUnder(workingDir, rootRel, baseName string, classifyText textClassifier, rootBypass *BlockInfo) ([]Entry, error) {
	rootRel = normalizeRelPath(rootRel)
	rels, err := ripgrepListUnder(workingDir, rootRel, rootBypass != nil)
	if err != nil {
		return nil, err
	}
	files := make([]Entry, 0, len(rels))
	for _, rel := range rels {
		if baseName != "" && path.Base(rel) != baseName {
			continue
		}
		absPath := filepath.Join(workingDir, filepath.FromSlash(rel))
		text, err := classifyText(rel, absPath)
		if err != nil {
			return nil, err
		}
		if !text {
			continue
		}
		entry := Entry{
			AbsPath: absPath,
			RelPath: rel,
		}
		if rootBypass != nil {
			entry = withIgnoreBypassed(entry, *rootBypass)
		}
		files = append(files, entry)
	}
	return files, nil
}

// ripgrepListUnder returns the rg-discovered file list under rootRel.
// When noIgnore is true rg ignores .gitignore/.hiss; otherwise the global
// .hiss is layered onto the default gitignore-aware enumeration.
func ripgrepListUnder(workingDir, rootRel string, noIgnore bool, enumeration ...search.MembershipEnumerationContext) ([]string, error) {
	return ripgrepListUnderTargets(workingDir, []string{rootRel}, noIgnore, enumeration...)
}

// ripgrepListUnderTargets enumerates a union of literal roots in one rg
// process. A dot/empty root means the working-directory-wide universe.
func ripgrepListUnderTargets(workingDir string, roots []string, noIgnore bool, enumeration ...search.MembershipEnumerationContext) ([]string, error) {
	context := search.MembershipEnumerationContext{Reason: search.MembershipReasonCanonicalFallback}
	if len(enumeration) > 0 {
		context = enumeration[0]
	}
	opts := search.RipgrepFileOptions{NoIgnore: noIgnore, Enumeration: context}
	if !noIgnore {
		hissPath, err := ReadableHissPath()
		if err != nil {
			return nil, err
		}
		opts.HissPath = hissPath
	}
	seenRoots := make(map[string]struct{}, len(roots))
	for _, rootRel := range roots {
		rootRel = normalizeRelPath(rootRel)
		if rootRel == "." || rootRel == "" {
			opts.Paths = nil
			break
		}
		if _, seen := seenRoots[rootRel]; seen {
			continue
		}
		seenRoots[rootRel] = struct{}{}
		opts.Paths = append(opts.Paths, rootRel)
	}
	rels, err := search.RunRipgrepFiles(workingDir, opts)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rels))
	hissRel := ""
	if noIgnore {
		hissRel = catclipControlHissRel(workingDir)
	}
	for _, rel := range rels {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." || rel == hissRel {
			continue
		}
		out = append(out, rel)
	}
	return out, nil
}

// catclipControlHissRel identifies Catclip's own ignore configuration when
// a portable config/HOME layout places it below the current target root. That
// file is control state, not target content. All no-ignore enumerators use the
// same exclusion so its membership cannot depend on which picker happened to
// materialize it first.
func catclipControlHissRel(workingDir string) string {
	canonicalWorkingDir := filepath.Clean(workingDir)
	if resolved, err := filepath.EvalSymlinks(canonicalWorkingDir); err == nil {
		canonicalWorkingDir = resolved
	}
	canonicalHiss := filepath.Clean(GlobalHissPath())
	if resolved, err := filepath.EvalSymlinks(canonicalHiss); err == nil {
		canonicalHiss = resolved
	}
	rel, err := filepath.Rel(canonicalWorkingDir, canonicalHiss)
	if err != nil {
		return ""
	}
	rel = normalizeRelPath(rel)
	if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return rel
}

// ShellStyleExtension is kept for non-classification consumers (e.g.,
// extension counting in startup_picker). Catclip's text/binary
// classification flows entirely through rg per
// docs/architecture/ACTIVE_NOTE_ripgrep_is_required.md; no Go-side
// allowlist exists. A previous `knownTextLikeFile` attempt was
// reverted — see
// docs/versions/v0.5.0/reports/RESOLVED_BUG_windows_contains_slow.md
// for the analysis (the allowlist almost never actually avoided the
// rg text-set call in practice, since real projects always contain at
// least one non-allowlisted file like a `.git` blob or build artifact).
func ShellStyleExtension(relPath string) string {
	base := strings.ToLower(path.Base(relPath))
	lastDot := strings.LastIndexByte(base, '.')
	if lastDot <= 0 || lastDot == len(base)-1 {
		return ""
	}
	return base[lastDot+1:]
}

// hasGlobChars duplicates cli.HasGlobChars so the discovery cluster
// can stay independent of internal/cli. Will travel with discovery.go
// into internal/discovery in the next commit.
func hasGlobChars(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func ContainsParentTraversal(value string) bool {
	normalized := strings.ReplaceAll(value, "\\", "/")
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func normalizeRelPath(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "./")
	if value == "." || value == "/" {
		return "."
	}
	return value
}

func DedupeEntriesByPath(entries []Entry) []Entry {
	if len(entries) == 0 {
		return entries
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].RelPath != entries[j].RelPath {
			return entries[i].RelPath < entries[j].RelPath
		}
		return EntryModePriority(entries[i].Mode) > EntryModePriority(entries[j].Mode)
	})

	out := entries[:1]
	for _, entry := range entries[1:] {
		last := &out[len(out)-1]
		if entry.RelPath != last.RelPath {
			out = append(out, entry)
			continue
		}
		if LinesEntriesShouldCoexist(*last, entry) {
			out = append(out, entry)
			continue
		}
		MergeFileEntry(last, entry)
	}
	return out
}

func DedupeEntriesByPathPreserveOrder(entries []Entry) []Entry {
	if len(entries) == 0 {
		return entries
	}

	out := make([]Entry, 0, len(entries))
	indicesByPath := make(map[string][]int, len(entries))
	for _, entry := range entries {
		indices, ok := indicesByPath[entry.RelPath]
		if !ok {
			indicesByPath[entry.RelPath] = []int{len(out)}
			out = append(out, entry)
			continue
		}
		merged := false
		for _, idx := range indices {
			if !LinesEntriesShouldCoexist(out[idx], entry) {
				MergeFileEntry(&out[idx], entry)
				merged = true
				break
			}
		}
		if !merged {
			indicesByPath[entry.RelPath] = append(indices, len(out))
			out = append(out, entry)
		}
	}
	return out
}

// LinesEntriesShouldCoexist returns true when two command.EntryModeLines entries for the
// same path carry different ranges and should both survive dedup.
func LinesEntriesShouldCoexist(a, b Entry) bool {
	if a.Mode != command.EntryModeLines || b.Mode != command.EntryModeLines {
		return false
	}
	// Bare lines (LinesStart == 0) absorbs any ranged entry.
	if a.LinesStart == 0 || b.LinesStart == 0 {
		return false
	}
	// Same range → dedupe.
	if a.LinesStart == b.LinesStart && a.LinesEnd == b.LinesEnd {
		return false
	}
	return true
}

func MergeFileEntry(dst *Entry, incoming Entry) {
	if EntryModePriority(incoming.Mode) > EntryModePriority(dst.Mode) {
		*dst = incoming
		return
	}
	if incoming.Mode == command.EntryModeDiff && dst.Mode == command.EntryModeDiff {
		dst.DiffWantStaged = dst.DiffWantStaged || incoming.DiffWantStaged
		dst.DiffWantUnstaged = dst.DiffWantUnstaged || incoming.DiffWantUnstaged
	}
	if incoming.GitVisible && !dst.GitVisible {
		dst.GitVisible = true
	}
	if incoming.IgnoreBypassed && !dst.IgnoreBypassed {
		dst.IgnoreBypassed = true
		dst.BlockSource = incoming.BlockSource
	}
	if dst.TargetRoot == "" && incoming.TargetRoot != "" {
		dst.TargetRoot = incoming.TargetRoot
	}
	if dst.AbsPath == "" && incoming.AbsPath != "" {
		dst.AbsPath = incoming.AbsPath
	}
	if dst.ModTime.IsZero() && !incoming.ModTime.IsZero() {
		dst.ModTime = incoming.ModTime
	}
	if !dst.SizeKnown && incoming.SizeKnown {
		dst.SizeBytes = incoming.SizeBytes
		dst.SizeKnown = true
	}
}

func DedupePreserveOrder(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func EntryModePriority(mode command.EntryMode) int {
	switch mode {
	case command.EntryModeDiff:
		return 3
	case command.EntryModeSnippet:
		return 2
	case command.EntryModeLines:
		return 1
	default:
		return 0
	}
}
