package discovery

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

var ErrSelectionCancelled = errors.New("selection cancelled")

type ErrNoScopedIgnoredTargets struct {
	ScopeTargets []string
}

func (e ErrNoScopedIgnoredTargets) Error() string {
	if len(e.ScopeTargets) == 1 {
		return fmt.Sprintf("--include: no ignored files or directories under '%s'\n\n  --include is scoped to the target paths. To include from elsewhere,\n  use --then to start a new scope:\n    catclip %s --then . --include <path>", e.ScopeTargets[0], e.ScopeTargets[0])
	}
	return "--include: no ignored files or directories under the current scope targets\n\n  --include is scoped to the target paths. To include from elsewhere,\n  use --then to start a new scope."
}

type includedTargetSet struct {
	exact    map[string]struct{}
	dirs     []string
	wildcard bool
}

// Resolver carries the per-scope discovery state — the invocation
// config, git context, working caches (text-file set, visible dirs/
// files), the include-target set, and a couple of picker-side hooks
// (startupEscHint, interactiveTargets cache).
//
// Fields named in CamelCase are accessed by external constructors
// (startup_picker.go's newStartupPickerResolver and internal_prediscovered.go's
// ApplyPrediscoveredScopeTail). All other fields are runtime-derived
// state managed by Resolver methods, and stay lowercase.
type Resolver struct {
	Cfg                  command.Invocation
	GitCtx               git.Context
	AllowFileSymlinks    bool
	WithBinaries         bool
	IncludedTargets      includedTargetSet
	WantedBasenames      map[string]struct{}
	ScopeTargets         []string
	StartupEscHint       string
	textFileSet          map[string]struct{}
	textFileSetReady     bool
	interactiveTargets   []TargetMatch
	interactiveTargetsOk bool
	ignoredTargets       []TargetMatch
	ignoredTargetsOk     bool
	VisibleDirs          VisibleDirIndex
	visibleDirsReady     bool
	visibleFiles         visibleFileIndex
	visibleFilesReady    bool
	VisibleFileList      []Entry
	visibleFileListReady bool
	visibleAll           map[string]struct{}
	visibleWithHiss      map[string]struct{}
	visibleAllDirs       map[string]struct{}
	visibleWithHissDirs  map[string]struct{}
	ignoreSetsReady      bool
}

// Scope is the typed output of the discover stage for a
// single execution scope. Carries the entries the scope resolved to,
// the Diagnostics and notices produced during resolution, and the
// post-picker `command.ExecutionScope` plus `git.Context` that produced them.
// Self-contained so downstream stages don't have to re-thread inputs.
//
// Phase 0 of the SCC + pipeline-linearity refactor — see
// docs/versions/v0.5.1/reports/ACTIVE_PLAN_stage_chain_checkpoints.md.
type Scope struct {
	Scope           command.ExecutionScope
	GitContext      git.Context
	Entries         []Entry
	Diagnostics     []Diagnostic
	Notices         []string
	SelectionCancel bool
}

func EvaluateScope(cfg command.Invocation, gitCtx git.Context, scopeIndex int, s command.ExecutionScope, stderr io.Writer, colors platform.Palette) (Scope, error) {
	mode := s.OutputMode()
	result := Scope{Scope: s, GitContext: gitCtx}

	resolver := Resolver{
		Cfg:               cfg,
		GitCtx:            gitCtx,
		AllowFileSymlinks: false,
		WithBinaries:      cfg.WithBinaries,
		IncludedTargets:   BuildIncludedTargetSet(cfg.WorkingDir, s.IncludedTargets),
		WantedBasenames:   CollectWantedBasenames(s.Targets),
		ScopeTargets:      append([]string(nil), s.Targets...),
	}
	var err error

	var entries []Entry
	selectedPaths := make([]string, 0, len(s.Targets))
	for _, target := range s.Targets {
		covered, err := resolver.InteractiveQueryCoveredBySelection(target, selectedPaths)
		if err != nil {
			return result, err
		}
		if covered {
			continue
		}

		discovered, targetDiagnostics, targetNotices, selectionCancelled, err := resolver.resolveAndDiscoverTarget(scopeIndex, target, stderr, colors)
		if err != nil {
			return result, err
		}
		result.Diagnostics = append(result.Diagnostics, targetDiagnostics...)
		result.Notices = append(result.Notices, targetNotices...)
		entries = append(entries, discovered...)
		// Caller-level ancestor probe: covers the case where the inline probe
		// at resolveAndDiscoverTarget's warning sites is gated out — e.g. a
		// basename target like `agent.md` whose only on-disk hit was in
		// resolveVisibleFilesByBasename's skipped-set, populating a notice but
		// preventing the warning (and therefore the inline probe). See
		// docs/versions/v0.5.7/reports/ACTIVE_PLAN_surface_ignored_ancestor.md.
		if len(discovered) == 0 && len(targetDiagnostics) == 0 && !hasGlobChars(target) {
			if cands := resolver.findIgnoredAncestors(target); len(cands) > 0 {
				result.Diagnostics = append(result.Diagnostics, Diagnostic{
					Message:          ignoredAncestorMessage(target, scopeIndex, cands, colors),
					IsTargetNotFound: true,
				})
			}
		}
		if len(discovered) > 0 && !hasGlobChars(target) {
			// selectedPaths tracks resolved single-path targets so that later
			// targets covered by the same selection can dedupe. Glob targets
			// aren't paths — they expand to multiple entries, and on Windows
			// os.Stat with `*` returns ERROR_INVALID_NAME, not ENOENT.
			normalized := normalizeRelPath(target)
			if normalized == "" {
				normalized = "."
			}
			exists, err := resolver.TargetPathExists(normalized)
			if err != nil {
				return result, err
			}
			if exists {
				selectedPaths = append(selectedPaths, normalized)
			}
		}
		result.SelectionCancel = result.SelectionCancel || selectionCancelled
	}

	entries = DedupeEntriesByPath(entries)

	if s.HasGitSelection() && !gitCtx.Enabled {
		// Hard-fail this scope: the user requested a git-only selection
		// without a git context. Emit an error-class Diagnostic and drop
		// entries so siblings of a --then chain can still proceed; the
		// per-scope loop in cli.go converts this into exit 2 (single
		// scope or all scopes unsatisfiable) or exit 1 (mixed success).
		// See docs/versions/v0.5.0/reports/ACTIVE_BUG_git_selection_silently_dropped_no_git.md.
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			Message:              command.GitSelectionRequiresGitRepoMessage(),
			IsScopeUnsatisfiable: true,
		})
		result.Notices = DedupePreserveOrder(result.Notices)
		return result, nil
	}

	entries, err = applyScopeStages(&resolver, gitCtx, s, entries)
	if err != nil {
		return result, err
	}

	StampEntriesWithScopeOutputMode(entries, mode, s)
	result.Entries = EnsureEntryAbsPaths(entries, cfg.WorkingDir)
	result.Notices = DedupePreserveOrder(result.Notices)
	return result, nil
}

func executionScopeHasEntryOutputMode(s command.ExecutionScope) bool {
	return s.Diff || s.Snippet || s.Lines
}

func StampEntriesWithScopeOutputMode(entries []Entry, mode command.EntryMode, s command.ExecutionScope) {
	for i := range entries {
		entries[i].Mode = mode
		entries[i].SnippetPattern = s.SnippetPattern
		entries[i].SnippetContextSet = s.SnippetContextSet
		entries[i].SnippetContextLines = s.SnippetContextLines
		entries[i].Lines = s.Lines
		entries[i].LinesStart = s.LinesStart
		entries[i].LinesEnd = s.LinesEnd
		entries[i].DiffWantStaged = s.Staged
		entries[i].DiffWantUnstaged = s.Unstaged
	}
}

func IncludeTargetsContainWildcard(targets []string) bool {
	for _, t := range targets {
		if t == "*" {
			return true
		}
	}
	return false
}

func BuildIncludedTargetSet(workingDir string, targets []string) includedTargetSet {
	if len(targets) == 0 {
		return includedTargetSet{}
	}
	set := includedTargetSet{
		exact:    make(map[string]struct{}, len(targets)),
		dirs:     make([]string, 0, len(targets)),
		wildcard: IncludeTargetsContainWildcard(targets),
	}
	for _, target := range targets {
		target = normalizeRelPath(target)
		if target == "" {
			continue
		}
		set.exact[target] = struct{}{}
		info, err := os.Stat(filepath.Join(workingDir, filepath.FromSlash(target)))
		if err == nil && info.IsDir() {
			set.dirs = append(set.dirs, target)
		}
	}
	return set
}

// ensureIgnoreSets populates the cached rg-derived visible-file sets used
// for ignore attribution. visibleAll respects .gitignore only; visibleWithHiss
// layers the global .hiss on top. The dir maps are derived by walking the
// path.Dir chain of each file. When --include '*' is active the caller
// short-circuits without consulting either set.
func (r *Resolver) ensureIgnoreSets() error {
	if r.ignoreSetsReady {
		return nil
	}
	visibleAll, visibleWithHiss, err := r.resolveIgnoreSets()
	if err != nil {
		return err
	}
	r.visibleAll = visibleAll
	r.visibleWithHiss = visibleWithHiss
	r.visibleAllDirs = search.DirsContainingFiles(visibleAll)
	r.visibleWithHissDirs = search.DirsContainingFiles(visibleWithHiss)
	r.ignoreSetsReady = true
	return nil
}

// dirBlockedBy reports the ignore source blocking a directory's contents,
// or nil if visible. When neither cached set covers the dir we probe rg
// with --no-ignore: empty dirs return nothing (treated as not blocked),
// while dirs whose descendants are all gitignored return paths. With
// --include '*', the caller routes through the bypass path; here we
// synthesize a block so callers that branch on block != nil pick that path.
func (r *Resolver) dirBlockedBy(relPath string) (*BlockInfo, error) {
	if r.IncludedTargets.wildcard {
		return &BlockInfo{Source: ""}, nil
	}
	if relPath == "." || relPath == "" {
		return nil, nil
	}
	if err := r.ensureIgnoreSets(); err != nil {
		return nil, err
	}
	if _, ok := r.visibleWithHissDirs[relPath]; ok {
		return nil, nil
	}
	if _, ok := r.visibleAllDirs[relPath]; ok {
		return &BlockInfo{Source: ".hiss"}, nil
	}
	paths, err := search.RunRipgrepFiles(r.Cfg.WorkingDir, search.RipgrepFileOptions{
		NoIgnore: true,
		Paths:    []string{relPath},
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	return &BlockInfo{Source: ".gitignore"}, nil
}

// fileBlockedBy reports the ignore source blocking a specific file, or nil
// if the file is visible. Wildcard --include synthesizes a block so callers
// route the file through the bypass path uniformly with directories.
func (r *Resolver) fileBlockedBy(relPath string) (*BlockInfo, error) {
	if relPath == "." || relPath == "" {
		return nil, nil
	}
	if r.IncludedTargets.wildcard {
		return &BlockInfo{Source: ""}, nil
	}
	if err := r.ensureIgnoreSets(); err != nil {
		return nil, err
	}
	if _, ok := r.visibleWithHiss[relPath]; ok {
		return nil, nil
	}
	if _, ok := r.visibleAll[relPath]; ok {
		return &BlockInfo{Source: ".hiss"}, nil
	}
	return &BlockInfo{Source: ".gitignore"}, nil
}

func (r *Resolver) targetIncluded(target string) bool {
	if len(r.IncludedTargets.exact) == 0 {
		return false
	}
	if r.IncludedTargets.wildcard {
		return true
	}
	target = normalizeRelPath(target)
	if _, ok := r.IncludedTargets.exact[target]; ok {
		return true
	}
	for _, dir := range r.IncludedTargets.dirs {
		if target == dir || strings.HasPrefix(target, dir+"/") {
			return true
		}
	}
	return false
}

// includedDescendantsOf returns the user-supplied include paths that are
// strict descendants of target. Used by the ignored-target error path to
// detect the "user pointed --include inside the target" mistake and
// produce a tailored teaching message instead of the generic
// "Your --include does not cover this target" boilerplate.
func (r *Resolver) includedDescendantsOf(target string) []string {
	target = normalizeRelPath(target)
	if target == "" || target == "." {
		return nil
	}
	prefix := target + "/"
	var descendants []string
	for path := range r.IncludedTargets.exact {
		if strings.HasPrefix(path, prefix) {
			descendants = append(descendants, path)
		}
	}
	sort.Strings(descendants)
	return descendants
}

// rewriteDeepIncludeScope, longestAncestorTarget, and rewriteStagesForDeepInclude
// moved to internal/command in the v0.6.0 command extraction. The CLI form
// goes through command.RewriteDeepIncludeScope, called from
// command.ExecutionScopeFromScopeSpec at the spec→scope boundary.

func (r *Resolver) TargetPathExists(relTarget string) (bool, error) {
	_, err := os.Stat(filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(relTarget)))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// CanResolveTargetWithoutPrompt mirrors the non-interactive resolver's
// deterministic branches. It returns true only when a target can be handled
// without opening fzf or prompting for ambiguity resolution.
// TargetIsReachable reports whether a typed target can be discovered by
// catclip — visibly, by basename/fuzzy match, or via an authorized
// --include subtree. Used by the startup-picker entrypoint to suppress
// filter-value pickers (--only / --exclude / --include) when the target is
// gitignored without --include coverage; surfacing the ignored-target error
// is the right response, not a picker that can't help.
// See docs/versions/v0.5.7/reports/ACTIVE_BUG_filter_picker_fires_before_target_check.md.
func (r *Resolver) TargetIsReachable(target string) (bool, error) {
	normalized := normalizeRelPath(target)
	if normalized == "" || normalized == "." || hasGlobChars(normalized) {
		return true, nil
	}

	abs := filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(normalized))
	info, statErr := os.Stat(abs)
	if statErr == nil {
		var block *BlockInfo
		var err error
		if info.IsDir() {
			block, err = r.dirBlockedBy(normalized)
		} else {
			block, err = r.fileBlockedBy(normalized)
		}
		if err != nil {
			return false, err
		}
		if block == nil || block.Source == "" {
			return true, nil
		}
		// Path exists but is blocked. Reachable only if --include covers it.
		if r.IncludedTargets.wildcard || r.targetIncluded(normalized) {
			return true, nil
		}
		return false, nil
	}

	// Path doesn't exist directly. Try basename + fuzzy hits in the visible
	// set, then --include'd subtrees.
	discovered, _, err := r.resolveVisibleFilesByBasename(".", normalized)
	if err != nil {
		return false, err
	}
	if len(discovered) > 0 {
		return true, nil
	}
	files, err := r.fuzzySearchFiles(".", normalized)
	if err != nil {
		return false, err
	}
	if len(files) > 0 {
		return true, nil
	}
	dirs, err := r.fuzzySearchDirs(".", normalized)
	if err != nil {
		return false, err
	}
	if len(dirs) > 0 {
		return true, nil
	}
	if r.hasAnyIncludeActive() {
		hits, err := r.FindBasenameInIncludedSubtrees(normalized)
		if err != nil {
			return false, err
		}
		if len(hits) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// HasAnyVisibleMatch reports whether the target resolves to *any* visible
// candidate — an existing path, an exact-basename file, or a fuzzy file/dir
// hit. Used by the startup-picker gate to distinguish "multi-hit visible
// (genuine ambiguity, picker is the right response)" from "zero matches
// anywhere (let the not-found warning fire instead)". A glob target is
// trivially treated as "has matches" so the gate doesn't fire there.
// See docs/versions/v0.5.7/reports/ACTIVE_PLAN_startup_picker_gated_on_ambiguity.md.
func (r *Resolver) HasAnyVisibleMatch(target string) (bool, error) {
	if target == "" || hasGlobChars(target) {
		return true, nil
	}
	normalized := normalizeRelPath(target)
	if normalized == "" || normalized == "." {
		return true, nil
	}
	exists, err := r.TargetPathExists(normalized)
	if err != nil {
		return false, err
	}
	if exists {
		return true, nil
	}
	discovered, _, err := r.resolveVisibleFilesByBasename(".", normalized)
	if err != nil {
		return false, err
	}
	if len(discovered) > 0 {
		return true, nil
	}
	files, err := r.fuzzySearchFiles(".", normalized)
	if err != nil {
		return false, err
	}
	if len(files) > 0 {
		return true, nil
	}
	dirs, err := r.fuzzySearchDirs(".", normalized)
	if err != nil {
		return false, err
	}
	return len(dirs) > 0, nil
}

func (r *Resolver) CanResolveTargetWithoutPrompt(target string) (bool, error) {
	if hasGlobChars(target) {
		return true, nil
	}

	normalizedTarget := normalizeRelPath(target)
	if normalizedTarget == "" {
		normalizedTarget = "."
	}

	exists, err := r.TargetPathExists(normalizedTarget)
	if err != nil {
		return false, err
	}
	if exists {
		return true, nil
	}

	if strings.Contains(normalizedTarget, "/") {
		return r.canResolveScopedTargetWithoutPrompt(normalizedTarget)
	}

	if resolvedDir, ok, err := r.resolveVisibleDirByExactBasename(".", normalizedTarget); err != nil {
		return false, err
	} else if ok && resolvedDir != "" {
		conflict, err := r.hasVisibleFileBasenameConflict(".", normalizedTarget)
		if err != nil {
			return false, err
		}
		if !conflict {
			return true, nil
		}
	}

	searchedFiles := false
	if prefersDirectFileLookup(normalizedTarget) {
		searchedFiles = true
		discovered, skipped, err := r.resolveVisibleFilesByBasename(".", normalizedTarget)
		if err != nil {
			return false, err
		}
		if len(discovered) > 0 || len(skipped) > 0 {
			return true, nil
		}
		fuzzyFiles, err := r.fuzzySearchFiles(".", normalizedTarget)
		if err != nil {
			return false, err
		}
		switch len(fuzzyFiles) {
		case 0:
		case 1:
			return true, nil
		default:
			return false, nil
		}
	}

	matches, err := r.fuzzySearchDirs(".", normalizedTarget)
	if err != nil {
		return false, err
	}
	if !searchedFiles && len(matches) > 0 {
		fuzzyFiles, err := r.fuzzySearchFiles(".", normalizedTarget)
		if err != nil {
			return false, err
		}
		if len(fuzzyFiles) > 0 {
			combined, err := rankTargetMatches(normalizedTarget, matches, fuzzyFiles)
			if err != nil {
				return false, err
			}
			return len(combined) == 1, nil
		}
	}

	switch len(matches) {
	case 0:
		if searchedFiles {
			return false, nil
		}
		discovered, skipped, err := r.resolveVisibleFilesByBasename(".", normalizedTarget)
		if err != nil {
			return false, err
		}
		if len(discovered) > 0 || len(skipped) > 0 {
			return true, nil
		}
		fuzzyFiles, err := r.fuzzySearchFiles(".", normalizedTarget)
		if err != nil {
			return false, err
		}
		return len(fuzzyFiles) == 1, nil
	case 1:
		return true, nil
	default:
		return false, nil
	}
}

func (r *Resolver) canResolveScopedTargetWithoutPrompt(normalizedTarget string) (bool, error) {
	dirPart := path.Dir(normalizedTarget)
	baseName := path.Base(normalizedTarget)

	resolvedDir, ok, err := r.resolveChainedDirWithoutPrompt(dirPart)
	if err != nil || !ok {
		return false, err
	}

	fullRel := normalizeRelPath(path.Join(resolvedDir, baseName))
	exists, err := r.TargetPathExists(fullRel)
	if err != nil {
		return false, err
	}
	if exists {
		return true, nil
	}

	blockedDir, err := r.BlockInfoForDir(resolvedDir)
	if err != nil {
		return false, err
	}
	if blockedDir != nil {
		discovered, err := discoverFilesUnder(r.Cfg.WorkingDir, resolvedDir, baseName, r.classifyTextFile, blockedDir)
		if err != nil {
			return false, err
		}
		if len(discovered) > 0 {
			return true, nil
		}
	} else {
		discovered, skipped, err := r.resolveVisibleFilesByBasename(resolvedDir, baseName)
		if err != nil {
			return false, err
		}
		if len(discovered) > 0 || len(skipped) > 0 {
			return true, nil
		}
	}

	fuzzyFiles, err := r.fuzzySearchFilesUnder(resolvedDir, baseName, blockedDir)
	if err != nil {
		return false, err
	}
	return len(fuzzyFiles) == 1, nil
}

func (r *Resolver) resolveAndDiscoverTarget(scopeIndex int, target string, stderr io.Writer, colors platform.Palette) ([]Entry, []Diagnostic, []string, bool, error) {
	var Diagnostics []Diagnostic
	var notices []string

	if filepath.IsAbs(target) {
		return nil, nil, nil, false, newUsageError("Error: Absolute paths not allowed: %s\n  Use a relative path from your project root instead.", SingleQuoted(target))
	}
	if ContainsParentTraversal(target) {
		return nil, nil, nil, false, newUsageError("Error: Cannot traverse above working directory: %s\n  catclip only operates within the current directory tree.\n  Use a relative path from your project root instead.\n  Example: catclip config/", SingleQuoted(target))
	}

	if hasGlobChars(target) {
		return r.resolveGlobTarget(scopeIndex, target, colors)
	}

	normalizedTarget := normalizeRelPath(target)
	if normalizedTarget == "" {
		normalizedTarget = "."
	}
	if r.targetIncluded(normalizedTarget) {
		discovered, targetDiagnostics, selectionCancelled, err := r.resolveIncludedTarget(target, normalizedTarget, stderr, colors)
		return discovered, targetDiagnostics, notices, selectionCancelled, err
	}

	if discovered, handled, diag, err := r.resolveExactTarget(normalizedTarget, false, colors); handled {
		if diag != nil {
			Diagnostics = append(Diagnostics, *diag)
		}
		return discovered, Diagnostics, notices, false, err
	}

	if strings.Contains(normalizedTarget, "/") {
		dirPart := path.Dir(normalizedTarget)
		baseName := path.Base(normalizedTarget)
		resolvedDir, err := r.resolveChainedDir(dirPart, stderr, colors)
		if err != nil {
			if errors.Is(err, ErrSelectionCancelled) {
				return nil, Diagnostics, notices, true, nil
			}
			return nil, Diagnostics, notices, false, err
		}
		fullRel := normalizeRelPath(path.Join(resolvedDir, baseName))
		discovered, handled, diag, err := r.resolveExactTarget(fullRel, true, colors)
		if handled {
			if diag != nil {
				Diagnostics = append(Diagnostics, *diag)
			}
			return discovered, Diagnostics, notices, false, err
		}
		blockedDir, err := r.BlockInfoForDir(resolvedDir)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		if blockedDir != nil {
			discovered, err = discoverFilesUnder(r.Cfg.WorkingDir, resolvedDir, baseName, r.classifyTextFile, blockedDir)
		} else {
			var skipped []SkippedMatch
			discovered, skipped, err = r.resolveVisibleFilesByBasename(resolvedDir, baseName)
			notices = append(notices, formatSkippedMatchesWarning(skipped)...)
		}
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		if len(discovered) > 0 {
			return withTargetRoot(discovered, resolvedDir), Diagnostics, notices, false, nil
		}
		fuzzyFiles, err := r.fuzzySearchFilesUnder(resolvedDir, baseName, blockedDir)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		switch len(fuzzyFiles) {
		case 0:
		case 1:
			discovered, handled, diag, err := r.resolveExactTarget(fuzzyFiles[0], true, colors)
			if diag != nil {
				Diagnostics = append(Diagnostics, *diag)
			}
			if handled {
				return discovered, Diagnostics, notices, false, err
			}
		default:
			selected, err := chooseFileMatch(r.Cfg, baseName, resolvedDir, fuzzyFiles, stderr, colors)
			if err != nil {
				if errors.Is(err, ErrSelectionCancelled) {
					return nil, Diagnostics, notices, true, nil
				}
				return nil, Diagnostics, notices, false, err
			}
			selectedMatches := make([]TargetMatch, 0, len(selected))
			for _, path := range selected {
				selectedMatches = append(selectedMatches, TargetMatch{Path: path, Kind: "file"})
			}
			discovered, err := r.resolveTargetMatches(selectedMatches, colors)
			if err != nil {
				return nil, Diagnostics, notices, false, err
			}
			return discovered, Diagnostics, notices, false, nil
		}
		Diagnostics = append(Diagnostics, Diagnostic{Message: targetNotFoundOrIgnoredAncestorMessage(r, target, scopeIndex, colors), IsTargetNotFound: true})
		return nil, Diagnostics, notices, false, nil
	}

	searchedFiles := false
	if prefersDirectFileLookup(normalizedTarget) {
		searchedFiles = true
		var skipped []SkippedMatch
		discovered, skipped, err := r.resolveVisibleFilesByBasename(".", normalizedTarget)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		notices = append(notices, formatSkippedMatchesWarning(skipped)...)
		if len(discovered) > 0 {
			return discovered, Diagnostics, notices, false, nil
		}
		// After visible miss, also search the authorized (--include'd)
		// subtrees so basename + --include behaves like path + --include does.
		// See docs/versions/v0.5.7/reports/ACTIVE_BUG_basename_target_ignores_include_subtree.md.
		// Invariant: only runs after visible lookup returned zero.
		includedHits, err := r.FindBasenameInIncludedSubtrees(normalizedTarget)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		switch len(includedHits) {
		case 0:
			// fall through to fuzzy + existing not-found / probe paths
		case 1:
			incMatches := []TargetMatch{hitToTargetMatch(includedHits[0])}
			incDiscovered, err := r.resolveTargetMatches(incMatches, colors)
			if err != nil {
				return nil, Diagnostics, notices, false, err
			}
			return incDiscovered, Diagnostics, notices, false, nil
		default:
			paths := make([]string, len(includedHits))
			for i, h := range includedHits {
				paths[i] = h.Path
			}
			selected, err := chooseFileMatch(r.Cfg, normalizedTarget, ".", paths, stderr, colors)
			if err != nil {
				if errors.Is(err, ErrSelectionCancelled) {
					return nil, Diagnostics, notices, true, nil
				}
				return nil, Diagnostics, notices, false, err
			}
			hitByPath := make(map[string]includedBasenameHit, len(includedHits))
			for _, h := range includedHits {
				hitByPath[h.Path] = h
			}
			selectedMatches := make([]TargetMatch, 0, len(selected))
			for _, p := range selected {
				h, ok := hitByPath[normalizeRelPath(p)]
				if !ok {
					h = includedBasenameHit{Path: p}
				}
				selectedMatches = append(selectedMatches, hitToTargetMatch(h))
			}
			incDiscovered, err := r.resolveTargetMatches(selectedMatches, colors)
			if err != nil {
				return nil, Diagnostics, notices, false, err
			}
			return incDiscovered, Diagnostics, notices, false, nil
		}
		fuzzyFiles, err := r.fuzzySearchFiles(".", normalizedTarget)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		switch len(fuzzyFiles) {
		case 0:
		case 1:
			discovered, handled, diag, err := r.resolveExactTarget(fuzzyFiles[0], false, colors)
			if diag != nil {
				Diagnostics = append(Diagnostics, *diag)
			}
			if handled {
				return discovered, Diagnostics, notices, false, err
			}
		default:
			selected, err := chooseFileMatch(r.Cfg, normalizedTarget, ".", fuzzyFiles, stderr, colors)
			if err != nil {
				if errors.Is(err, ErrSelectionCancelled) {
					return nil, Diagnostics, notices, true, nil
				}
				return nil, Diagnostics, notices, false, err
			}
			selectedMatches := make([]TargetMatch, 0, len(selected))
			for _, path := range selected {
				selectedMatches = append(selectedMatches, TargetMatch{Path: path, Kind: "file"})
			}
			discovered, err := r.resolveTargetMatches(selectedMatches, colors)
			if err != nil {
				return nil, Diagnostics, notices, false, err
			}
			return discovered, Diagnostics, notices, false, nil
		}
	}

	if resolvedDir, ok, err := r.resolveVisibleDirByExactBasename(".", normalizedTarget); err != nil {
		return nil, Diagnostics, notices, false, err
	} else if ok && resolvedDir != "" {
		conflict, err := r.hasVisibleFileBasenameConflict(".", normalizedTarget)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		if !conflict {
			discovered, handled, diag, err := r.resolveExactTarget(resolvedDir, false, colors)
			if diag != nil {
				Diagnostics = append(Diagnostics, *diag)
			}
			if handled {
				return discovered, Diagnostics, notices, false, err
			}
		}
	}

	matches, err := r.fuzzySearchDirs(".", normalizedTarget)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if !searchedFiles && len(matches) > 0 {
		fuzzyFiles, err := r.fuzzySearchFiles(".", normalizedTarget)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		if len(fuzzyFiles) > 0 {
			combined, err := rankTargetMatches(normalizedTarget, matches, fuzzyFiles)
			if err != nil {
				return nil, Diagnostics, notices, false, err
			}
			if len(combined) == 1 {
				discovered, handled, diag, err := r.resolveTargetMatch(combined[0], colors)
				if diag != nil {
					Diagnostics = append(Diagnostics, *diag)
				}
				if handled {
					return discovered, Diagnostics, notices, false, err
				}
			}
			if canPromptForChoice(r.Cfg) {
				selected, err := chooseTargetMatch(r.Cfg, normalizedTarget, combined, stderr, colors)
				if err != nil {
					if errors.Is(err, ErrSelectionCancelled) {
						return nil, Diagnostics, notices, true, nil
					}
					return nil, Diagnostics, notices, false, err
				}
				discovered, err := r.resolveTargetMatches(selected, colors)
				if err != nil {
					return nil, Diagnostics, notices, false, err
				}
				return discovered, Diagnostics, notices, false, nil
			}
		}
	}
	switch len(matches) {
	case 0:
		if !searchedFiles {
			var skipped []SkippedMatch
			discovered, skipped, err := r.resolveVisibleFilesByBasename(".", normalizedTarget)
			if err != nil {
				return nil, Diagnostics, notices, false, err
			}
			notices = append(notices, formatSkippedMatchesWarning(skipped)...)
			if len(discovered) > 0 {
				return discovered, Diagnostics, notices, false, nil
			}
			// Same include-subtree probe as in the prefersDirectFileLookup
			// branch: handles dir-shorthand targets (no extension, e.g. `docker`)
			// when the user --include's the ancestor.
			// ACTIVE_BUG_basename_target_ignores_include_subtree.md.
			includedHits, err := r.FindBasenameInIncludedSubtrees(normalizedTarget)
			if err != nil {
				return nil, Diagnostics, notices, false, err
			}
			switch len(includedHits) {
			case 0:
				// fall through to fuzzy + not-found / probe
			case 1:
				incMatches := []TargetMatch{hitToTargetMatch(includedHits[0])}
				incDiscovered, err := r.resolveTargetMatches(incMatches, colors)
				if err != nil {
					return nil, Diagnostics, notices, false, err
				}
				return incDiscovered, Diagnostics, notices, false, nil
			default:
				paths := make([]string, len(includedHits))
				for i, h := range includedHits {
					paths[i] = h.Path
				}
				selected, err := chooseFileMatch(r.Cfg, normalizedTarget, ".", paths, stderr, colors)
				if err != nil {
					if errors.Is(err, ErrSelectionCancelled) {
						return nil, Diagnostics, notices, true, nil
					}
					return nil, Diagnostics, notices, false, err
				}
				hitByPath := make(map[string]includedBasenameHit, len(includedHits))
				for _, h := range includedHits {
					hitByPath[h.Path] = h
				}
				selectedMatches := make([]TargetMatch, 0, len(selected))
				for _, p := range selected {
					h, ok := hitByPath[normalizeRelPath(p)]
					if !ok {
						h = includedBasenameHit{Path: p}
					}
					selectedMatches = append(selectedMatches, hitToTargetMatch(h))
				}
				incDiscovered, err := r.resolveTargetMatches(selectedMatches, colors)
				if err != nil {
					return nil, Diagnostics, notices, false, err
				}
				return incDiscovered, Diagnostics, notices, false, nil
			}
			fuzzyFiles, err := r.fuzzySearchFiles(".", normalizedTarget)
			if err != nil {
				return nil, Diagnostics, notices, false, err
			}
			switch len(fuzzyFiles) {
			case 0:
			case 1:
				discovered, handled, diag, err := r.resolveExactTarget(fuzzyFiles[0], false, colors)
				if diag != nil {
					Diagnostics = append(Diagnostics, *diag)
				}
				if handled {
					return discovered, Diagnostics, notices, false, err
				}
			default:
				selected, err := chooseFileMatch(r.Cfg, normalizedTarget, ".", fuzzyFiles, stderr, colors)
				if err != nil {
					if errors.Is(err, ErrSelectionCancelled) {
						return nil, Diagnostics, notices, true, nil
					}
					return nil, Diagnostics, notices, false, err
				}
				selectedMatches := make([]TargetMatch, 0, len(selected))
				for _, path := range selected {
					selectedMatches = append(selectedMatches, TargetMatch{Path: path, Kind: "file"})
				}
				discovered, err := r.resolveTargetMatches(selectedMatches, colors)
				if err != nil {
					return nil, Diagnostics, notices, false, err
				}
				return discovered, Diagnostics, notices, false, nil
			}
		}
		if len(notices) == 0 {
			Diagnostics = append(Diagnostics, Diagnostic{Message: targetNotFoundOrIgnoredAncestorMessage(r, target, scopeIndex, colors), IsTargetNotFound: true})
		}
		return nil, Diagnostics, notices, false, nil
	case 1:
		files, err := r.discoverVisibleFilesUnder(matches[0])
		return withTargetRoot(files, matches[0]), Diagnostics, notices, false, err
	default:
		selected, err := chooseDirectoryMatch(r.Cfg, target, ".", matches, stderr, colors)
		if err != nil {
			if errors.Is(err, ErrSelectionCancelled) {
				return nil, Diagnostics, notices, true, nil
			}
			return nil, nil, nil, false, err
		}
		selectedMatches := make([]TargetMatch, 0, len(selected))
		for _, path := range selected {
			selectedMatches = append(selectedMatches, TargetMatch{Path: path, Kind: "dir"})
		}
		files, err := r.resolveTargetMatches(selectedMatches, colors)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		return files, Diagnostics, notices, false, nil
	}
}

func (r *Resolver) resolveGlobTarget(scopeIndex int, pattern string, colors platform.Palette) ([]Entry, []Diagnostic, []string, bool, error) {
	allFiles, err := r.discoverVisibleFilesUnder(".")
	if err != nil {
		return nil, nil, nil, false, err
	}
	var matched []Entry
	for _, entry := range allFiles {
		ok, matchErr := path.Match(pattern, path.Base(entry.RelPath))
		if matchErr != nil {
			return nil, nil, nil, false, newUsageError("Error: Invalid glob pattern %s: %v", SingleQuoted(pattern), matchErr)
		}
		if !ok {
			ok, _ = path.Match(pattern, entry.RelPath)
		}
		if ok {
			matched = append(matched, entry)
		}
	}
	if len(matched) == 0 {
		diag := Diagnostic{
			Message:          targetNotFoundWarning(pattern, scopeIndex, colors),
			IsTargetNotFound: true,
		}
		return nil, []Diagnostic{diag}, nil, false, nil
	}
	return withTargetRoot(matched, "."), nil, nil, false, nil
}

func (r *Resolver) resolveTargetMatch(match TargetMatch, colors platform.Palette) ([]Entry, bool, *Diagnostic, error) {
	if match.Ignored {
		return r.resolveExactTarget(match.Path, false, colors)
	}
	switch match.Kind {
	case "file":
		return r.resolveExactTarget(match.Path, false, colors)
	case "dir":
		files, err := r.discoverVisibleFilesUnder(match.Path)
		if err != nil {
			return nil, true, nil, err
		}
		return withTargetRoot(files, match.Path), true, nil, nil
	default:
		return nil, false, nil, nil
	}
}

func (r *Resolver) resolveIncludedTarget(target, normalizedTarget string, stderr io.Writer, colors platform.Palette) ([]Entry, []Diagnostic, bool, error) {
	var Diagnostics []Diagnostic

	if discovered, handled, diag, err := r.resolveExactTarget(normalizedTarget, false, colors); handled {
		if diag != nil {
			Diagnostics = append(Diagnostics, *diag)
		}
		return discovered, Diagnostics, false, err
	}

	if !canPromptForChoice(r.Cfg) {
		return nil, []Diagnostic{{
			Message: includeQueryNeedsSelectionMessage(target, colors),
			IsError: true,
		}}, false, nil
	}

	matches, _, err := r.chooseIgnoredTargetMatches(target, "include> ", nil, nil, nil)
	if err != nil {
		if errors.Is(err, ErrSelectionCancelled) {
			return nil, nil, true, nil
		}
		return nil, nil, false, err
	}
	discovered, err := r.resolveTargetMatches(matches, colors)
	if err != nil {
		return nil, nil, false, err
	}
	return discovered, Diagnostics, false, nil
}

func (r *Resolver) resolveExactTarget(relTarget string, fromChained bool, colors platform.Palette) ([]Entry, bool, *Diagnostic, error) {
	absTarget := filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(relTarget))
	info, err := os.Lstat(absTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil, nil
		}
		return nil, true, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, true, nil, nil
	}

	if info.IsDir() {
		hasIncludes := len(r.IncludedTargets.exact) > 0
		block, err := r.dirBlockedBy(relTarget)
		if err != nil {
			return nil, true, nil, err
		}
		if block != nil {
			if !r.targetIncluded(relTarget) {
				return nil, true, &Diagnostic{Message: ignoredDirMessage(relTarget, block.Source, hasIncludes, r.includedDescendantsOf(relTarget), colors), IsError: true}, nil
			}
			files, err := discoverFilesUnder(r.Cfg.WorkingDir, relTarget, "", r.classifyTextFile, block)
			return withTargetRoot(files, relTarget), true, nil, err
		}
		files, err := r.discoverVisibleFilesUnder(relTarget)
		return withTargetRoot(files, relTarget), true, nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, true, nil, nil
	}
	text, err := r.classifyTextFile(relTarget, absTarget)
	if err != nil {
		return nil, true, nil, err
	}
	if !text {
		return nil, true, nil, nil
	}
	entry := Entry{
		AbsPath:    absTarget,
		RelPath:    relTarget,
		ModTime:    info.ModTime(),
		SizeBytes:  info.Size(),
		SizeKnown:  true,
		GitVisible: true,
	}
	if dir := normalizeRelPath(path.Dir(relTarget)); dir != "." {
		entry.TargetRoot = dir
	}
	hasIncludes := len(r.IncludedTargets.exact) > 0
	block, err := r.fileBlockedBy(relTarget)
	if err != nil {
		return nil, true, nil, err
	}
	if block != nil {
		if !r.targetIncluded(relTarget) {
			return nil, true, &Diagnostic{Message: ignoredFileMessage(relTarget, block.Source, fromChained, hasIncludes, colors), IsError: true}, nil
		}
		entry = withAllowedByInclude(entry, *block)
	}
	return []Entry{entry}, true, nil, nil
}

// ensureTextFileSet pulls the rg-derived NUL-free file set for the resolver's
// working directory from the process-level cache. The cache amortizes one rg
// scan across every resolver in a catclip run; without it, each resolver
// (current scope, ignored-target probe, etc.) would re-scan independently.
func (r *Resolver) ensureTextFileSet() error {
	if r.textFileSetReady {
		return nil
	}
	set, err := search.ResolveTextFileSet(r.Cfg.WorkingDir, r.ScopeTargets)
	if err != nil {
		return err
	}
	r.textFileSet = set
	r.textFileSetReady = true
	return nil
}

// isTextFromSet reports whether rel is in the rg-derived text-file set.
// Short-circuits on --with-binaries; otherwise defers to rg's
// NUL-detection scan via the cached set.
func (r *Resolver) isTextFromSet(rel string) bool {
	if r.WithBinaries {
		return true
	}
	_, ok := r.textFileSet[rel]
	return ok
}

func (r *Resolver) classifyTextFile(relPath, absPath string) (bool, error) {
	if r.WithBinaries {
		return true, nil
	}
	// rg's NUL-detection text-set is the sole content classifier.
	// No Go fallback — rg's answer is final. A path absent from the set
	// is classified as binary; --with-binaries to override.
	rel := normalizeRelPath(relPath)
	if rel == "" || rel == "." {
		return false, nil
	}
	if err := r.ensureTextFileSet(); err != nil {
		return false, err
	}
	_, ok := r.textFileSet[rel]
	return ok, nil
}

func (r *Resolver) BlockInfoForDir(relPath string) (*BlockInfo, error) {
	return r.dirBlockedBy(relPath)
}

func (r *Resolver) resolveChainedDir(relPath string, stderr io.Writer, colors platform.Palette) (string, error) {
	currentAbs := r.Cfg.WorkingDir
	currentRel := "."

	for _, seg := range strings.Split(relPath, "/") {
		if seg == "" || seg == "." {
			continue
		}

		exactAbs := filepath.Join(currentAbs, seg)
		info, err := os.Stat(exactAbs)
		if err == nil && info.IsDir() {
			candidateRel := normalizeRelPath(path.Join(currentRel, seg))
			currentAbs = exactAbs
			currentRel = candidateRel
			continue
		}

		resolvedDir, ok, err := r.resolveVisibleDirByExactBasename(currentRel, seg)
		if err != nil {
			return "", err
		}
		if ok && resolvedDir != "" {
			currentRel = resolvedDir
			currentAbs = filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(currentRel))
			continue
		}

		matches, err := r.fuzzySearchDirs(currentRel, seg)
		if err != nil {
			return "", err
		}
		switch len(matches) {
		case 0:
			return "", fmt.Errorf("Error: No directory matching %s found in %s.\n  Check the spelling, or use --hiss to see if it's excluded.", SingleQuoted(seg), currentRel)
		case 1:
			currentRel = matches[0]
		default:
			selected, err := chooseDirectoryMatch(r.Cfg, seg, currentRel, matches, stderr, colors)
			if err != nil {
				return "", err
			}
			if len(selected) == 0 {
				return "", ErrSelectionCancelled
			}
			currentRel = selected[0]
		}
		currentAbs = filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(currentRel))
	}

	return currentRel, nil
}

func (r *Resolver) resolveChainedDirWithoutPrompt(relPath string) (string, bool, error) {
	currentAbs := r.Cfg.WorkingDir
	currentRel := "."

	for _, seg := range strings.Split(relPath, "/") {
		if seg == "" || seg == "." {
			continue
		}

		exactAbs := filepath.Join(currentAbs, seg)
		info, err := os.Stat(exactAbs)
		if err == nil && info.IsDir() {
			candidateRel := normalizeRelPath(path.Join(currentRel, seg))
			currentAbs = exactAbs
			currentRel = candidateRel
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return "", false, err
		}

		resolvedDir, ok, err := r.resolveVisibleDirByExactBasename(currentRel, seg)
		if err != nil {
			return "", false, err
		}
		if ok && resolvedDir != "" {
			currentRel = resolvedDir
			currentAbs = filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(currentRel))
			continue
		}

		matches, err := r.fuzzySearchDirs(currentRel, seg)
		if err != nil {
			return "", false, err
		}
		if len(matches) != 1 {
			return "", false, nil
		}
		currentRel = matches[0]
		currentAbs = filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(currentRel))
	}

	return currentRel, true, nil
}

func (r *Resolver) TargetNeedsInclude(target string) (bool, error) {
	normalizedTarget := normalizeRelPath(target)
	if normalizedTarget == "" || normalizedTarget == "." {
		return false, nil
	}
	_, handled, diag, err := r.resolveExactTarget(normalizedTarget, false, platform.Palette{})
	if err != nil {
		return false, err
	}
	return handled && diag != nil && diag.IsError, nil
}

func (r *Resolver) resolveVisibleDirByExactBasename(baseRel, basename string) (string, bool, error) {
	if basename == "" || basename == "." {
		return "", false, nil
	}
	if err := r.BuildVisibleDirIndex(); err != nil {
		return "", false, err
	}

	baseRel = normalizeRelPath(baseRel)
	if baseRel == "" {
		baseRel = "."
	}
	prefix := ""
	if baseRel != "." {
		prefix = baseRel + "/"
	}

	var match string
	for _, rel := range r.VisibleDirs.Dirs {
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			continue
		}
		if path.Base(rel) != basename {
			continue
		}
		if match != "" {
			return "", false, nil
		}
		match = rel
	}
	if match == "" {
		return "", false, nil
	}
	return match, true, nil
}

func (r *Resolver) hasVisibleFileBasenameConflict(baseRel, needle string) (bool, error) {
	if needle == "" || needle == "." {
		return false, nil
	}
	if err := r.BuildVisibleFileList(); err != nil {
		return false, err
	}

	baseRel = normalizeRelPath(baseRel)
	if baseRel == "" {
		baseRel = "."
	}
	prefix := ""
	if baseRel != "." {
		prefix = baseRel + "/"
	}

	for _, entry := range r.VisibleFileList {
		if prefix != "" && !strings.HasPrefix(entry.RelPath, prefix) {
			continue
		}
		base := path.Base(entry.RelPath)
		if base == needle {
			return true, nil
		}
		if strings.TrimSuffix(base, path.Ext(base)) == needle {
			return true, nil
		}
	}
	return false, nil
}

func (r *Resolver) ChooseRootTargetMatches(query, prompt string, includeCopyAll bool, selectedPaths []string) ([]TargetMatch, error) {
	query = NormalizeInteractivePickerQuery(query)
	if SelectionContainsAll(selectedPaths) {
		return nil, ErrSelectionCancelled
	}
	stopSpinner := func() {}
	if !r.interactiveTargetsOk {
		stopSpinner = platform.StartLoadingSpinner(os.Stderr, "Loading targets...")
	}
	allTargets, err := r.allVisibleTargets()
	stopSpinner()
	if err != nil {
		return nil, err
	}
	options := make([]TargetMatch, 0, len(allTargets))
	for _, target := range allTargets {
		if CoveredBySelection(target.Path, selectedPaths) {
			continue
		}
		options = append(options, target)
	}
	if includeCopyAll {
		options = append([]TargetMatch{{Path: ".", Kind: "all"}}, options...)
	}
	if len(options) == 0 {
		return nil, ErrSelectionCancelled
	}
	if match, ok := exactInteractiveTargetMatch(options, query); ok {
		return []TargetMatch{match}, nil
	}

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}

	labels, index := TargetMatchLabels(options)
	selectedLabels, err := chooseManyTargetMatchesWithFzfHeader(path, query, prompt, TargetPickerHeaderWithEscHint(prompt, r.StartupEscHint), labels, false)
	if err != nil {
		return nil, err
	}

	selected := make([]TargetMatch, 0, len(selectedLabels))
	for _, key := range selectedLabels {
		match, ok := index[key]
		if ok {
			if match.Kind == "all" {
				return []TargetMatch{match}, nil
			}
			selected = append(selected, match)
		}
	}
	if len(selected) == 0 {
		return nil, ErrSelectionCancelled
	}
	return selected, nil
}

func (r *Resolver) chooseIgnoredTargetMatches(query, prompt string, selectedPaths, explicitTargets, scopeTargets []string) ([]TargetMatch, int, error) {
	query = NormalizeInteractivePickerQuery(query)
	stopSpinner := func() {}
	if !r.ignoredTargetsOk {
		stopSpinner = platform.StartLoadingSpinner(os.Stderr, "Loading ignored targets...")
	}
	allTargets, err := r.AllIgnoredTargets()
	stopSpinner()
	if err != nil {
		return nil, 0, err
	}
	allTargets = filterIgnoredTargetsByScopeTargets(allTargets, scopeTargets)
	if len(allTargets) == 0 && len(scopeTargets) > 0 {
		return nil, 0, ErrNoScopedIgnoredTargets{ScopeTargets: scopeTargets}
	}
	options := FilterRedundantTargetMatches(allTargets, SelectionPathsForIgnoredTargets(selectedPaths))
	options = filterAuthorizationOnlyIncludeMatches(options, explicitTargets)
	totalOptions := len(options)
	if totalOptions == 0 {
		return nil, 0, ErrSelectionCancelled
	}
	if match, ok := exactTargetPathMatch(options, query); ok {
		return []TargetMatch{match}, totalOptions, nil
	}

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, 0, err
	}
	labels, index := TargetMatchLabels(options)
	selectedLabels, err := chooseManyTargetMatchesWithFzfHeader(path, query, prompt, IgnoredTargetPickerHeaderWithEscHint(r.StartupEscHint), labels, true)
	if err != nil {
		return nil, 0, err
	}

	selected := make([]TargetMatch, 0, len(selectedLabels))
	for _, key := range selectedLabels {
		match, ok := index[key]
		if ok {
			selected = append(selected, match)
		}
	}
	if len(selected) == 0 {
		return nil, 0, ErrSelectionCancelled
	}
	return selected, totalOptions, nil
}

func SelectionPathsForIgnoredTargets(selectedPaths []string) []string {
	filtered := make([]string, 0, len(selectedPaths))
	for _, selected := range selectedPaths {
		if normalizeRelPath(selected) == "." {
			continue
		}
		filtered = append(filtered, selected)
	}
	return filtered
}

func (r *Resolver) ResolveInteractiveIncludeTargets(query string, selectedPaths, explicitTargets, scopeTargets []string) ([]string, error) {
	matches, totalOptions, err := r.chooseIgnoredTargetMatches(query, "include> ", selectedPaths, explicitTargets, scopeTargets)
	if err != nil {
		return nil, err
	}
	if totalOptions > 0 && len(matches) == totalOptions {
		return []string{"*"}, nil
	}
	return TargetMatchPaths(matches), nil
}

func (r *Resolver) resolveExactIgnoredIncludeTarget(query string, scopeTargets []string) (string, bool, error) {
	options, err := r.AllIgnoredTargets()
	if err != nil {
		return "", false, err
	}
	options = filterIgnoredTargetsByScopeTargets(options, scopeTargets)
	match, ok := exactTargetPathMatch(options, query)
	if !ok {
		return "", false, nil
	}
	return match.Path, true, nil
}

func (r *Resolver) ResolveExactIgnoredIncludeTargets(queries []string, scopeTargets []string) ([]string, []string, error) {
	exact := make([]string, 0, len(queries))
	remaining := make([]string, 0, len(queries))
	for _, query := range queries {
		path, ok, err := r.resolveExactIgnoredIncludeTarget(query, scopeTargets)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			exact = append(exact, path)
			continue
		}
		remaining = append(remaining, query)
	}
	return DedupePreserveOrder(exact), remaining, nil
}

// filterIgnoredTargetsByScopeTargets filters ignored targets to only those
// that fall under any scope target OR are ancestors of any scope target.
// Ancestors are included because --include authorizes discovery of an ignored
// directory, which may contain the scope target itself. If any scope target is
// "." (root), all targets are returned.
func filterIgnoredTargetsByScopeTargets(targets []TargetMatch, scopeTargets []string) []TargetMatch {
	if len(scopeTargets) == 0 {
		return targets
	}
	for _, st := range scopeTargets {
		if normalizeRelPath(st) == "." || normalizeRelPath(st) == "" {
			return targets
		}
	}

	out := make([]TargetMatch, 0, len(targets))
	for _, target := range targets {
		rel := normalizeRelPath(target.Path)
		for _, st := range scopeTargets {
			st = normalizeRelPath(st)
			// Descendant or exact match: ignored target is under scope target.
			if rel == st || strings.HasPrefix(rel, st+"/") {
				out = append(out, target)
				break
			}
			// Ancestor: ignored target is a parent of scope target.
			// This authorizes discovery of the scope target itself.
			if strings.HasPrefix(st, rel+"/") {
				out = append(out, target)
				break
			}
		}
	}
	return out
}

func TargetMatchPaths(matches []TargetMatch) []string {
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		if match.Kind == "done" {
			continue
		}
		paths = append(paths, match.Path)
	}
	return paths
}

func exactInteractiveTargetMatch(options []TargetMatch, query string) (TargetMatch, bool) {
	if !shouldAutoAcceptInteractiveQuery(query) {
		return TargetMatch{}, false
	}
	return exactTargetPathMatch(options, query)
}

func exactTargetPathMatch(options []TargetMatch, query string) (TargetMatch, bool) {
	trimmed := strings.TrimSuffix(query, "/")
	want := normalizeRelPath(trimmed)
	if want == "" || want == "." {
		return TargetMatch{}, false
	}
	for _, option := range options {
		if option.Kind == "all" {
			continue
		}
		if option.Path == want {
			if strings.HasSuffix(query, "/") && option.Kind != "dir" {
				continue
			}
			return option, true
		}
	}
	return TargetMatch{}, false
}

func shouldAutoAcceptInteractiveQuery(query string) bool {
	trimmed := strings.TrimSuffix(query, "/")
	if trimmed == "" || trimmed == "." {
		return false
	}
	return strings.Contains(trimmed, "/")
}

func NormalizeInteractivePickerQuery(query string) string {
	if strings.TrimSpace(query) == "*" {
		return ""
	}
	return query
}

func (r *Resolver) InteractiveQueryCoveredBySelection(query string, selectedPaths []string) (bool, error) {
	query = NormalizeInteractivePickerQuery(query)
	if query == "" || len(selectedPaths) == 0 {
		return false, nil
	}
	if hasGlobChars(query) {
		return false, nil
	}
	if SelectionContainsAll(selectedPaths) {
		return true, nil
	}

	normalized := normalizeRelPath(query)
	if normalized != "" && normalized != "." {
		exists, err := r.TargetPathExists(normalized)
		if err != nil {
			return false, err
		}
		if exists && CoveredBySelection(normalized, selectedPaths) {
			return true, nil
		}
	}
	if strings.Contains(normalized, "/") {
		return false, nil
	}

	sawMatch := false

	if err := r.BuildVisibleDirIndex(); err != nil {
		return false, err
	}
	for _, rel := range r.VisibleDirs.Dirs {
		if path.Base(rel) != normalized {
			continue
		}
		sawMatch = true
		if !CoveredBySelection(rel, selectedPaths) {
			return false, nil
		}
	}

	if err := r.BuildVisibleFileList(); err != nil {
		return false, err
	}
	for _, entry := range r.VisibleFileList {
		base := path.Base(entry.RelPath)
		if base != normalized && strings.TrimSuffix(base, path.Ext(base)) != normalized {
			continue
		}
		sawMatch = true
		if !CoveredBySelection(entry.RelPath, selectedPaths) {
			return false, nil
		}
	}
	return sawMatch, nil
}

func FilterRedundantTargetMatches(candidates []TargetMatch, selectedPaths []string) []TargetMatch {
	if len(selectedPaths) == 0 {
		return candidates
	}
	filtered := make([]TargetMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if CoveredBySelection(candidate.Path, selectedPaths) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func filterAuthorizationOnlyIncludeMatches(candidates []TargetMatch, explicitTargets []string) []TargetMatch {
	if len(explicitTargets) == 0 {
		return candidates
	}
	filtered := make([]TargetMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Kind == "dir" && includeTargetIsAncestorOnlyForTargets(explicitTargets, candidate.Path) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func includeTargetIsAncestorOnlyForTargets(targets []string, includeTarget string) bool {
	includeTarget = normalizeRelPath(includeTarget)
	if includeTarget == "" || includeTarget == "." {
		return false
	}
	for _, target := range targets {
		target = normalizeRelPath(target)
		if target == "" || target == "." {
			continue
		}
		if strings.HasPrefix(target, includeTarget+"/") {
			return true
		}
	}
	return false
}

func CoveredBySelection(path string, selectedPaths []string) bool {
	for _, selected := range selectedPaths {
		selected = normalizeRelPath(selected)
		switch {
		case selected == ".":
			return true
		case path == selected:
			return true
		case selected != "" && strings.HasPrefix(path, selected+"/"):
			return true
		}
	}
	return false
}

func SelectionContainsAll(selectedPaths []string) bool {
	for _, selected := range selectedPaths {
		if normalizeRelPath(selected) == "." {
			return true
		}
	}
	return false
}

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

func (r *Resolver) AllIgnoredTargets() ([]TargetMatch, error) {
	if r.ignoredTargetsOk {
		return append([]TargetMatch(nil), r.ignoredTargets...), nil
	}

	rgPaths, err := search.RunRipgrepFiles(r.Cfg.WorkingDir, search.RipgrepFileOptions{NoIgnore: true})
	if err != nil {
		return nil, err
	}
	// AllIgnoredTargets walks project-wide (no Paths above) — the
	// downstream filterIgnoredTargetsByScopeTargets narrows to scope. It
	// uses a project-wide text-file set rather than r.textFileSet, which
	// is scope-narrowed and would hide ignored items outside the scope's
	// target subtree (.gitignore-blocked dirs an ancestor of the scope
	// might want surfaced for --include).
	projectTextSet, err := search.ResolveTextFileSet(r.Cfg.WorkingDir, nil)
	if err != nil {
		return nil, err
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
	// .gitignore (precedence). When --include '*', skip the diff: nothing
	// counts as ignored.
	ignoredFiles := map[string]string{}
	ignoredDirs := map[string]string{}
	if !r.IncludedTargets.wildcard {
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
		if match.Ignored {
			targets = append(targets, match)
		}
	}
	for _, rel := range filePaths {
		match := TargetMatch{Path: rel, Kind: "file", State: treeTargetStateText}
		if source, ok := ignoredFiles[rel]; ok {
			match.Ignored = true
			match.IgnoreSource = source
		}
		if match.Ignored {
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

	r.ignoredTargets = targets
	r.ignoredTargetsOk = true
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

func (r *Resolver) resolveTargetMatches(matches []TargetMatch, colors platform.Palette) ([]Entry, error) {
	entries := make([]Entry, 0, len(matches))
	for _, match := range matches {
		if match.Kind == "done" {
			continue
		}
		discovered, handled, _, err := r.resolveTargetMatch(match, colors)
		if err != nil {
			return nil, err
		}
		if handled {
			entries = append(entries, discovered...)
		}
	}
	return DedupeEntriesByPath(entries), nil
}

func (r *Resolver) BuildVisibleDirIndex() error {
	if r.visibleDirsReady {
		return nil
	}
	if err := r.BuildVisibleFileList(); err != nil {
		return err
	}

	dirSet := make(map[string]struct{}, len(r.VisibleFileList))
	for _, entry := range r.VisibleFileList {
		dir := path.Dir(entry.RelPath)
		for dir != "." && dir != "" {
			dirSet[dir] = struct{}{}
			dir = path.Dir(dir)
		}
	}

	dirs := make([]string, 0, len(dirSet))
	for rel := range dirSet {
		dirs = append(dirs, rel)
	}
	sort.Strings(dirs)

	r.VisibleDirs = VisibleDirIndex{
		Dirs:        dirs,
		Set:         make(map[string]struct{}, len(dirs)),
		SymlinkDirs: nil,
	}
	for _, rel := range dirs {
		r.VisibleDirs.Set[rel] = struct{}{}
	}
	r.visibleDirsReady = true
	return nil
}

func (r *Resolver) buildVisibleFileIndex() error {
	if r.visibleFilesReady {
		return nil
	}
	if len(r.WantedBasenames) == 0 {
		r.visibleFiles = visibleFileIndex{
			byBase:        map[string][]Entry{},
			skippedByBase: map[string][]SkippedMatch{},
		}
		r.visibleFilesReady = true
		return nil
	}

	paths, err := search.RunRipgrepFiles(r.Cfg.WorkingDir, search.RipgrepFileOptions{
		NoIgnore:  true,
		Basenames: sortedStringSet(r.WantedBasenames),
	})
	if err != nil {
		return err
	}
	candidates, err := r.textEntriesFromRipgrepPaths(paths)
	if err != nil {
		return err
	}

	visibleAll, visibleWithHiss, err := r.resolveIgnoreSets()
	if err != nil {
		return err
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].RelPath < candidates[j].RelPath
	})

	byBase := make(map[string][]Entry, len(candidates))
	skippedByBase := make(map[string][]SkippedMatch, len(candidates))
	for _, entry := range candidates {
		base := path.Base(entry.RelPath)
		if r.IncludedTargets.wildcard {
			entry.GitVisible = true
			byBase[base] = append(byBase[base], entry)
			continue
		}
		if _, ok := visibleWithHiss[entry.RelPath]; ok {
			entry.GitVisible = true
			byBase[base] = append(byBase[base], entry)
			continue
		}
		source := ".gitignore"
		if _, ok := visibleAll[entry.RelPath]; ok {
			source = ".hiss"
		}
		skippedByBase[base] = append(skippedByBase[base], SkippedMatch{
			RelPath:     entry.RelPath,
			BlockSource: source,
		})
	}

	r.visibleFiles = visibleFileIndex{
		byBase:        byBase,
		skippedByBase: skippedByBase,
	}
	r.visibleFilesReady = true
	return nil
}

// resolveIgnoreSets fetches the cached visible-file sets used for
// rg-driven ignore attribution: visibleAll respects .gitignore only;
// visibleWithHiss layers the global .hiss on top. When --include '*' is
// active the caller short-circuits without consulting either set.
func (r *Resolver) resolveIgnoreSets() (map[string]struct{}, map[string]struct{}, error) {
	visibleAll, err := search.ResolveVisibleFileSet(r.Cfg.WorkingDir, "")
	if err != nil {
		return nil, nil, err
	}
	visibleWithHiss := visibleAll
	hissPath, err := ReadableHissPath()
	if err != nil {
		return nil, nil, err
	}
	if hissPath != "" {
		visibleWithHiss, err = search.ResolveVisibleFileSet(r.Cfg.WorkingDir, hissPath)
		if err != nil {
			return nil, nil, err
		}
	}
	return visibleAll, visibleWithHiss, nil
}

func (r *Resolver) BuildVisibleFileList() error {
	if r.visibleFileListReady {
		return nil
	}
	hissPath, err := ReadableHissPath()
	if err != nil {
		return err
	}
	paths, err := search.RunRipgrepFiles(r.Cfg.WorkingDir, search.RipgrepFileOptions{HissPath: hissPath})
	if err != nil {
		return err
	}
	entries, err := r.textEntriesFromRipgrepPaths(paths)
	if err != nil {
		return err
	}
	entries = markEntriesGitVisible(entries)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RelPath < entries[j].RelPath
	})
	r.VisibleFileList = entries
	r.visibleFileListReady = true
	return nil
}

func (r *Resolver) textEntriesFromRipgrepPaths(relPaths []string) ([]Entry, error) {
	if err := r.ensureTextFileSet(); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(relPaths))
	for _, rel := range relPaths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." || CoveredBySelection(rel, r.VisibleDirs.SymlinkDirs) {
			continue
		}
		if !r.isTextFromSet(rel) {
			continue
		}

		entries = append(entries, Entry{RelPath: rel})
	}
	return entries, nil
}

func (r *Resolver) discoverVisibleFilesUnder(rootRel string) ([]Entry, error) {
	rootRel = normalizeRelPath(rootRel)
	hissPath, err := ReadableHissPath()
	if err != nil {
		return nil, err
	}
	opts := search.RipgrepFileOptions{HissPath: hissPath}
	if rootRel != "." && rootRel != "" {
		opts.Paths = []string{rootRel}
	}
	paths, err := search.RunRipgrepFiles(r.Cfg.WorkingDir, opts)
	if err != nil {
		return nil, err
	}
	entries, err := r.textEntriesFromRipgrepPaths(paths)
	if err != nil {
		return nil, err
	}
	return markEntriesGitVisible(entries), nil
}

func sortedStringSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	for value := range values {
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (r *Resolver) resolveVisibleFilesByBasename(baseRel, baseName string) ([]Entry, []SkippedMatch, error) {
	if err := r.buildVisibleFileIndex(); err != nil {
		return nil, nil, err
	}

	candidates := EnsureEntryAbsPaths(append([]Entry(nil), r.visibleFiles.byBase[baseName]...), r.Cfg.WorkingDir)
	skipped := r.visibleFiles.skippedByBase[baseName]

	baseRel = normalizeRelPath(baseRel)
	if baseRel == "." || baseRel == "" {
		return candidates, append([]SkippedMatch(nil), skipped...), nil
	}

	prefix := baseRel + "/"
	matches := make([]Entry, 0, len(candidates))
	for _, entry := range candidates {
		if strings.HasPrefix(entry.RelPath, prefix) {
			matches = append(matches, entry)
		}
	}
	blocked := make([]SkippedMatch, 0, len(skipped))
	for _, match := range skipped {
		if strings.HasPrefix(match.RelPath, prefix) {
			blocked = append(blocked, match)
		}
	}
	return matches, blocked, nil
}

func (r *Resolver) fuzzySearchDirs(baseRel, needle string) ([]string, error) {
	if err := r.BuildVisibleDirIndex(); err != nil {
		return nil, err
	}

	baseRel = normalizeRelPath(baseRel)
	if baseRel == "" {
		baseRel = "."
	}
	prefix := ""
	if baseRel != "." {
		prefix = baseRel + "/"
	}

	matches := make([]string, 0, 16)
	for _, rel := range r.VisibleDirs.Dirs {
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			continue
		}
		matches = append(matches, rel)
	}
	return fuzzyFilterCandidates(needle, matches)
}

func (r *Resolver) fuzzySearchFiles(baseRel, needle string) ([]string, error) {
	if err := r.BuildVisibleFileList(); err != nil {
		return nil, err
	}

	baseRel = normalizeRelPath(baseRel)
	if baseRel == "" {
		baseRel = "."
	}
	prefix := ""
	if baseRel != "." {
		prefix = baseRel + "/"
	}

	candidates := make([]string, 0, len(r.VisibleFileList))
	for _, entry := range r.VisibleFileList {
		if prefix != "" && !strings.HasPrefix(entry.RelPath, prefix) {
			continue
		}
		candidates = append(candidates, entry.RelPath)
	}
	return fuzzyFilterCandidates(needle, candidates)
}

func (r *Resolver) fuzzySearchFilesUnder(baseRel, needle string, rootBypass *BlockInfo) ([]string, error) {
	if rootBypass == nil {
		return r.fuzzySearchFiles(baseRel, needle)
	}

	entries, err := discoverFilesUnder(r.Cfg.WorkingDir, baseRel, "", r.classifyTextFile, rootBypass)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, len(entries))
	for _, entry := range entries {
		candidates = append(candidates, entry.RelPath)
	}
	return fuzzyFilterCandidates(needle, candidates)
}

func chooseDirectoryMatch(cfg command.Invocation, needle, currentRel string, matches []string, stderr io.Writer, colors platform.Palette) ([]string, error) {
	if !canPromptForChoice(cfg) {
		return nil, headlessDirectoryAmbiguityError(needle, currentRel, matches)
	}

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	return chooseManyWithTypedFzf(path, needle, "dir> ", matches, treeTargetKindDir, treeTargetStateOK)
}

func chooseFileMatch(cfg command.Invocation, needle, currentRel string, matches []string, stderr io.Writer, colors platform.Palette) ([]string, error) {
	if !canPromptForChoice(cfg) {
		return nil, headlessFileAmbiguityError(needle, currentRel, matches)
	}

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	return chooseManyWithTypedFzf(path, needle, "file> ", matches, treeTargetKindFile, treeTargetStateText)
}

func chooseTargetMatch(cfg command.Invocation, needle string, matches []TargetMatch, stderr io.Writer, colors platform.Palette) ([]TargetMatch, error) {
	if !canPromptForChoice(cfg) {
		return nil, headlessTargetAmbiguityError(needle, matches)
	}

	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	labels, index := TargetMatchLabels(matches)
	selectedKeys, err := chooseManyTargetMatchesWithFzf(path, needle, "select> ", labels, false)
	if err != nil {
		return nil, err
	}
	selected := make([]TargetMatch, 0, len(selectedKeys))
	for _, key := range selectedKeys {
		match, ok := index[key]
		if ok {
			selected = append(selected, match)
		}
	}
	if len(selected) == 0 {
		return nil, ErrSelectionCancelled
	}
	return selected, nil
}

const HeadlessCandidateListLimit = 10

func FormatHeadlessCandidateList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	limit := len(items)
	if limit > HeadlessCandidateListLimit {
		limit = HeadlessCandidateListLimit
	}
	var b strings.Builder
	b.WriteString("\n  Matches:")
	for _, item := range items[:limit] {
		fmt.Fprintf(&b, "\n    - %s", item)
	}
	if len(items) > limit {
		fmt.Fprintf(&b, "\n    - ... and %d more", len(items)-limit)
	}
	return b.String()
}

func headlessDirectoryAmbiguityError(needle, currentRel string, matches []string) error {
	var b strings.Builder
	if currentRel == "." {
		fmt.Fprintf(&b, "Error: Multiple directories match %s in headless mode (--headless).", SingleQuoted(needle))
	} else {
		fmt.Fprintf(&b, "Error: Multiple directories match %s in %s in headless mode (--headless).", SingleQuoted(needle), currentRel)
	}
	b.WriteString(FormatHeadlessCandidateList(matches))
	b.WriteString("\n  Use a more specific path segment to disambiguate.")
	return errors.New(b.String())
}

func headlessFileAmbiguityError(needle, currentRel string, matches []string) error {
	var b strings.Builder
	if currentRel == "." {
		fmt.Fprintf(&b, "Error: Multiple files match %s in headless mode (--headless).", SingleQuoted(needle))
	} else {
		fmt.Fprintf(&b, "Error: Multiple files match %s in %s in headless mode (--headless).", SingleQuoted(needle), currentRel)
	}
	b.WriteString(FormatHeadlessCandidateList(matches))
	b.WriteString("\n  Use a more specific name or path to disambiguate.")
	return errors.New(b.String())
}

func headlessTargetAmbiguityError(needle string, matches []TargetMatch) error {
	items := make([]string, 0, len(matches))
	for _, match := range matches {
		items = append(items, fmt.Sprintf("[%s] %s", match.Kind, match.Path))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Error: Multiple files and directories match %s in headless mode (--headless).", SingleQuoted(needle))
	b.WriteString(FormatHeadlessCandidateList(items))
	b.WriteString("\n  Use a more specific name or path to disambiguate.")
	return errors.New(b.String())
}

func FzfBinary() (string, bool) {
	return platform.BundledToolBinary("CATCLIP_FZF", "fzf")
}

func FuzzyResolverBinary() (string, error) {
	path, ok := FzfBinary()
	if ok {
		return path, nil
	}
	return "", fmt.Errorf("Error: this catclip install is missing bundled fzf.\n  Reinstall catclip with its packaged tools; runtime does not fall back to arbitrary PATH copies.")
}

func fuzzyFilterCandidates(query string, candidates []string) ([]string, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	return runFzfFilter(path, query, candidates)
}

func runFzfFilter(bin, query string, candidates []string) ([]string, error) {
	return runFzfFilterLines(bin, query, formatFzfCandidates(candidates, "", ""))
}

func runFzfFilterLines(bin, query string, lines []string) ([]string, error) {
	return picker.Filter(bin, query, lines)
}

func chooseWithFzfLines(bin, query, prompt, withNth, previewCommand string, lines []string) (string, error) {
	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          query,
		Prompt:         prompt,
		WithNth:        withNth,
		PreviewCommand: previewCommand,
		Lines:          lines,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return "", ErrSelectionCancelled
	}
	if err != nil {
		return "", err
	}
	if len(result.Matches) == 0 {
		return "", ErrSelectionCancelled
	}
	return result.Matches[0], nil
}

// FzfFileSetPreviewCommand is the legacy fallback command for free-form
// file-set previews. Normal modifier pickers use
// startupCheckpointFileSetPreviewCommand so preview keystrokes load entries[N]
// instead of rediscovering the project.
func FzfFileSetPreviewCommand(currentArgs []string, previewFlag string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{ShellQuoteArg(self), "--quiet", "--internal-tree-preview"}
	for _, arg := range currentArgs {
		parts = append(parts, ShellQuoteArg(arg))
	}
	if previewFlag != "" {
		parts = append(parts, previewFlag, "{+2}")
	}
	parts = append(parts,
		"--internal-tree-target", "{3}",
		"--internal-tree-kind", "{4}",
		"--internal-tree-state", "{5}",
	)
	return strings.Join(parts, " ")
}

// FzfDiffFilePreviewCommand is intentionally not checkpoint-backed: diff
// pickers preview one focused file via --internal-file-preview, so they do not
// rerun project discovery for a tree payload.
func FzfDiffFilePreviewCommand(currentArgs []string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{ShellQuoteArg(self), "--quiet", "--internal-file-preview", "--internal-file-path", "{3}"}
	for _, arg := range currentArgs {
		parts = append(parts, ShellQuoteArg(arg))
	}
	parts = append(parts, "--only", "{+2}")
	return strings.Join(parts, " ")
}

func ChooseContentMatchesWithFzf(query string, currentArgs []string, flag string) (fzfChooseResult, error) {
	return ChooseContentMatchesWithFzfAndEscHint(query, currentArgs, flag, "")
}

func ChooseContentMatchesWithFzfAndEscHint(query string, currentArgs []string, flag string, escHint string) (fzfChooseResult, error) {
	bin, err := FuzzyResolverBinary()
	if err != nil {
		return fzfChooseResult{}, err
	}

	command, checkpointPath, cleanup := fzfCheckpointContentMatchListCommand(currentArgs, flag)
	defer cleanup()
	if command == "" {
		return fzfChooseResult{}, ErrSelectionCancelled
	}

	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          query,
		Prompt:         "match> ",
		WithNth:        "1",
		Nth:            "1",
		Header:         ContentMatchPickerHeaderWithEscHint(flag, escHint),
		PreviewCommand: FzfContentPreviewCommand(flag, checkpointPath),
		PreviewWindow:  ContentMatchPreviewWindow(flag),
		Disabled:       true,
		Multi:          true,
		PrintQuery:     true,
		Bindings:       append([]string{"start:reload:" + command, "change:reload:" + command}, MultiSelectPickerBindings()...),
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return fzfChooseResult{}, ErrSelectionCancelled
	}
	if err != nil {
		return fzfChooseResult{}, err
	}
	if strings.TrimSpace(result.Query) == "" && result.Key == "" && len(result.Matches) == 0 {
		return fzfChooseResult{}, ErrSelectionCancelled
	}
	return fzfChooseResult{Query: result.Query, Key: result.Key, Matches: result.Matches}, nil
}

func chooseManyWithTypedFzf(bin, query, prompt string, candidates []string, kind, state string) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1,2", "", FzfPreviewCommand(false), formatFzfCandidates(candidates, kind, state))
}

func chooseManyWithFzfNth(bin, query, prompt, nth string, candidates []string) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, nth, "1,2", "", FzfPreviewCommand(false), formatFzfCandidates(candidates, "", ""))
}

func chooseManyTargetMatchesWithFzfHeader(bin, query, prompt, header string, candidates []string, includeTarget bool) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1", header, FzfPreviewCommand(includeTarget), candidates)
}

func chooseManyTargetMatchesWithFzf(bin, query, prompt string, candidates []string, includeTarget bool) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1", "", FzfPreviewCommand(includeTarget), candidates)
}

type fzfChooseResult struct {
	Query   string
	Key     string
	Matches []string
}

func chooseManyWithFzfOptions(bin, query, prompt, nth, withNth, header, previewCommand string, candidates []string) ([]string, error) {
	platform.StopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          query,
		Prompt:         prompt,
		WithNth:        withNth,
		Nth:            nth,
		Header:         header,
		PreviewCommand: previewCommand,
		Multi:          true,
		Bindings:       MultiSelectPickerBindings(),
		Lines:          candidates,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return nil, ErrSelectionCancelled
	}
	if err != nil {
		return nil, err
	}
	if len(result.Matches) == 0 {
		return nil, ErrSelectionCancelled
	}
	return result.Matches, nil
}

// FzfPreviewCommand is used by target-selection pickers before a parent scope
// has settled entries. SCC does not apply there; modifier previews use the
// checkpoint wrappers in startup_picker.go / fzfCheckpointContentMatchListCommand.
func FzfPreviewCommand(includeTarget bool) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	selfQ := ShellQuoteArg(self)

	// {+2} passes all selected targets (falls back to focused when none selected).
	// {2}/{3}/{4} are the focused entry's metadata for tree highlight.
	if includeTarget {
		return selfQ + ` --quiet {+2} --internal-tree-preview` +
			` --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4}` +
			` --include {+2}`
	}
	return selfQ + ` --quiet --internal-tree-preview` +
		` --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4}` +
		` {+2}`
}

// FzfContentPreviewCommand builds the preview-pane command for the
// content-match picker. The same command serves three states inside
// runInternalFilePreview:
//
//   - Empty {q}: emits the contextual hint document (smart-case tips +
//     pattern examples). No checkpoint needed.
//   - Non-empty {q}, empty {3} (the `[all current matches]` row): if a
//     checkpoint path is wired in, emits the full scope tree from the
//     checkpoint. Otherwise emits nothing.
//   - Non-empty {q}, non-empty {3}: per-file preview with match
//     highlighting (or snippet extraction / diff).
//
// checkpointPath is empty when the caller couldn't write a checkpoint
// (legacy fallback path); in that case the `[all current matches]`
// preview is empty, matching pre-v0.5.2 behavior. Pass the path
// returned by fzfCheckpointContentMatchListCommand to enable the tree.
func FzfContentPreviewCommand(flag, checkpointPath string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{
		ShellQuoteArg(self),
		"--quiet",
		"--internal-file-preview",
		"--internal-file-path", "{3}",
	}
	if checkpointPath != "" {
		parts = append(parts, "--internal-prediscovered", ShellQuoteArg(checkpointPath))
	}
	// fzf already shell-quotes placeholders like {q}; adding our own quotes
	// breaks regex input that includes spaces or quote characters.
	parts = append(parts, flag, "{q}")
	return strings.Join(parts, " ")
}

// FzfContentMatchListCommand is the legacy fallback when the checkpoint match
// list command cannot be built. Normal content pickers call
// fzfCheckpointContentMatchListCommand, which loads entries[N] from disk.
func FzfContentMatchListCommand(currentArgs []string, flag string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{ShellQuoteArg(self), "--quiet", "--internal-content-match-list"}
	for _, arg := range currentArgs {
		parts = append(parts, ShellQuoteArg(arg))
	}
	// fzf already shell-quotes placeholders like {q}; adding our own quotes
	// breaks regex input that includes spaces or quote characters.
	parts = append(parts, flag, "{q}")
	return strings.Join(parts, " ")
}

// fzfCheckpointContentMatchListCommand returns:
//   - the fzf `reload` command string for the content-match list,
//   - the checkpoint path on disk (empty when the fast SCC path was not
//     taken — caller should treat that as "no tree preview available"),
//   - a cleanup function that removes the tmpdir housing the checkpoint.
//
// The checkpoint path is exposed so the preview command builder can wire
// the same JSON file into --internal-file-preview's empty-path branch
// (the `[all current matches]` row's scope tree). Match-list reload and
// preview share the same checkpoint file — one JSON write per picker
// open.
func fzfCheckpointContentMatchListCommand(currentArgs []string, flag string) (string, string, func()) {
	fallback := func() string {
		return FzfContentMatchListCommand(currentArgs, flag)
	}
	noop := func() {}
	switch flag {
	case "--contains", "--snippet":
	default:
		return fallback(), "", noop
	}
	if scopeViewResolverFn == nil {
		return fallback(), "", noop
	}
	view, ok := scopeViewResolverFn(currentArgs)
	if !ok || len(view.Entries) == 0 {
		return fallback(), "", noop
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return "", "", noop
	}
	tmpdir, err := os.MkdirTemp("", "catclip-scc-*")
	if err != nil {
		return fallback(), "", noop
	}
	checkpointPath := filepath.Join(tmpdir, "scope.json")
	statuses := map[string]string{}
	if view.GitContext.Enabled {
		statuses, err = git.StatusMapForPathspecs(view.GitContext, GitStatusPathspecsForEntries(view.GitContext, view.Entries))
		if err != nil {
			_ = os.RemoveAll(tmpdir)
			return fallback(), "", noop
		}
	}
	if err := WriteCheckpoint(checkpointPath, view.WorkingDir, CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    view.Entries,
	}); err != nil {
		_ = os.RemoveAll(tmpdir)
		return fallback(), "", noop
	}

	parts := []string{ShellQuoteArg(self), "--quiet", "--internal-content-match-list", "--internal-prediscovered", ShellQuoteArg(checkpointPath)}
	// fzf already shell-quotes placeholders like {q}; adding our own quotes
	// breaks regex input that includes spaces or quote characters.
	parts = append(parts, flag, "{q}")
	return strings.Join(parts, " "), checkpointPath, func() {
		_ = os.RemoveAll(tmpdir)
	}
}

// ContentMatchPreviewWindow returns the fzf --preview-window spec for the
// content match picker. For --contains, it appends a `+{6}-/2` offset so
// the preview pane opens centered on the first match per focused file
// (column 6 carries the first-match line number, populated by
// attachFirstMatchLines). Snippet mode skips the offset because the
// preview already renders matched blocks, not the full file — centering
// on a line number would scroll PAST the snippet content.
//
// Cross-platform: --preview-window's `+{N}-/2` syntax is fzf-native, not
// shell-evaluated, so cmd.exe / PowerShell / sh handle it identically.
// The substitution value is always a positive integer (the [all current
// matches] row uses contentMatchAllMatchesPreviewLine = "1") so fzf never
// sees an empty `{6}` that could break the flag parse.
func ContentMatchPreviewWindow(flag string) string {
	if flag != "--contains" {
		return ""
	}
	return picker.DefaultPreviewWindow + ":+{6}-/2"
}

func ContentMatchPickerHeader(flag string) string {
	return ContentMatchPickerHeaderWithEscHint(flag, "")
}

func ContentMatchPickerHeaderWithEscHint(flag, escHint string) string {
	firstLine := "Keep files whose contents match a regex."
	if flag == "--snippet" {
		firstLine = "Extract snippets whose contents match a regex."
	}
	return PickerHeader(
		firstLine,
		"Type a regex.",
		fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  %s", platform.MultiSelectToggleAllKey(), startupEscLabel(escHint)),
	)
}

func MultiSelectPickerBindings() []string {
	return []string{
		"tab:toggle+down",
		"btab:toggle+up",
		platform.MultiSelectToggleAllBinding(),
		"multi:refresh-preview",
	}
}

func ShellQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\"'\\*?[]{}()$&;|<>") {
		return arg
	}
	return strconv.Quote(arg)
}

// shellEnforceSingleQuote always wraps arg in POSIX single quotes, escaping
// embedded single quotes, so a value is rendered as an unambiguous literal even
// when it has no shell-special characters. Used for regex modifiers so resolved
// commands show the pattern quoted and copy-paste safely regardless of $, *, or
// spaces in the pattern.
func shellEnforceSingleQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func formatFzfCandidates(candidates []string, kind, state string) []string {
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		lines = append(lines, strings.Join([]string{
			path.Base(candidate),
			candidate,
			kind,
			state,
		}, "\t"))
	}
	return lines
}

func rankTargetMatches(query string, dirs, files []string) ([]TargetMatch, error) {
	matches := make([]TargetMatch, 0, len(dirs)+len(files))
	for _, dir := range dirs {
		matches = append(matches, TargetMatch{Path: dir, Kind: "dir"})
	}
	for _, file := range files {
		matches = append(matches, TargetMatch{Path: file, Kind: "file"})
	}
	if len(matches) == 0 {
		return nil, nil
	}
	path, err := FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	labels, index := TargetMatchLabels(matches)
	filtered, err := runFzfFilterLines(path, query, labels)
	if err != nil {
		return nil, err
	}
	ranked := make([]TargetMatch, 0, len(filtered))
	for _, key := range filtered {
		match, ok := index[key]
		if ok {
			ranked = append(ranked, match)
		}
	}
	return ranked, nil
}

func TargetMatchLabels(matches []TargetMatch) ([]string, map[string]TargetMatch) {
	labels := make([]string, 0, len(matches))
	index := make(map[string]TargetMatch, len(matches))
	for _, match := range matches {
		label := fmt.Sprintf("[%s] %s", match.Kind, match.Path)
		if match.Kind == "all" {
			plain := "[select all files]"
			label = "\x1b[1m" + plain + "\x1b[0m"
		} else if match.Ignored {
			source := strings.TrimSpace(match.IgnoreSource)
			if source == "" {
				source = "ignored"
			}
			label = fmt.Sprintf("[ignored %s %s] %s", match.Kind, source, match.Path)
		}
		labels = append(labels, strings.Join([]string{
			label,
			match.Path,
			TargetMatchPreviewKind(match),
			TargetMatchPreviewState(match),
		}, "\t"))
		index[match.Path] = match
	}
	return labels, index
}

func TargetMatchPreviewKind(match TargetMatch) string {
	switch match.Kind {
	case "all", treeTargetKindDir:
		return treeTargetKindDir
	case treeTargetKindFile:
		return treeTargetKindFile
	default:
		return normalizeTreeTargetKind(match.Kind)
	}
}

func TargetMatchPreviewState(match TargetMatch) string {
	if state := normalizeTreeTargetState(match.State); state != "" {
		return state
	}
	switch TargetMatchPreviewKind(match) {
	case treeTargetKindDir:
		return treeTargetStateOK
	case treeTargetKindFile:
		return treeTargetStateText
	default:
		return ""
	}
}

func TargetMatchKey(match TargetMatch) string {
	return match.Kind + "\x00" + match.Path
}

func targetPickerHeader(prompt string) string {
	return TargetPickerHeaderWithEscHint(prompt, "")
}

func TargetPickerHeaderWithEscHint(prompt, escHint string) string {
	firstLine := "Pick files and folders to include."
	if prompt == "then> " {
		firstLine = "Add more files and folders."
	}
	return PickerHeader(
		firstLine,
		"Type to search by name.",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  [Tab] mark  %s", startupEscLabel(escHint)),
	)
}

func SafeTargetPickerHeader() string {
	return targetPickerHeader("select> ")
}

func IgnoredTargetPickerHeader() string {
	return IgnoredTargetPickerHeaderWithEscHint("")
}

func IgnoredTargetPickerHeaderWithEscHint(escHint string) string {
	return PickerHeader(
		"Add files and folders ignored by .gitignore or .hiss.",
		"Type to search by name.",
		fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  %s", platform.MultiSelectToggleAllKey(), startupEscLabel(escHint)),
	)
}

func PickerHeader(lines ...string) string {
	if len(lines) > 4 {
		lines = lines[:4]
	}
	for len(lines) < 4 {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func targetNotFoundWarning(target string, scopeIndex int, colors platform.Palette) string {
	if strings.Contains(target, "/") {
		return fmt.Sprintf("%sWarning:%s Target %s not found (scope %d).\n\n  %sIf the parent directory is ignored, use --include to allow it first.%s\n  %sExample:%s %scatclip --include %s --only %s%s",
			colors.Warn, colors.Reset, SingleQuoted(target), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, SingleQuoted(path.Dir(target)), SingleQuoted(path.Base(target)), colors.Reset)
	}
	if prefersDirectFileLookup(target) {
		return fmt.Sprintf("%sWarning:%s No file named %s found (scope %d).\n\n  %sDirect file targets use exact basenames first. Non-exact file shorthand is resolved by fzf across safe directories.%s\n\n  %sIf an ignored rule is hiding it, use --include to allow that blocked file or directory first.%s",
			colors.Warn, colors.Reset, SingleQuoted(target), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset)
	}
	return fmt.Sprintf("%sWarning:%s No file or directory %s found (scope %d).\n\n  %sDirectory shorthand is resolved by fzf. File targets use exact basenames first, then fzf across safe directories.%s\n\n  %sIf the thing you want is ignored, use --include to browse blocked targets for this scope.%s",
		colors.Warn, colors.Reset, SingleQuoted(target), scopeIndex+1,
		colors.Dim, colors.Reset,
		colors.Dim, colors.Reset)
}

// IgnoreRemovalHint formats the "To remove permanently" line for an ignored
// target. It branches on the source: --hiss only edits ~/.config/catclip/.hiss,
// so for any other source (.gitignore, .git/info/exclude, global excludes)
// pointing at --hiss is wrong advice — the user can't delete a .gitignore rule
// via --hiss. For those, send them to --all-ignore-rules, which lists every
// rule with its file:line so they can find and edit the right place.
func IgnoreRemovalHint(source string, colors platform.Palette) string {
	if source == ".hiss" {
		return fmt.Sprintf("\n  %sTo remove permanently:%s   %scatclip --hiss%s %s(delete the rule)%s",
			colors.Dim, colors.Reset, colors.OK, colors.Reset, colors.Dim, colors.Reset)
	}
	return fmt.Sprintf("\n  %sTo remove permanently:%s   find the rule with %scatclip --all-ignore-rules%s, then edit that file",
		colors.Dim, colors.Reset, colors.OK, colors.Reset)
}

func ignoredDirMessage(relTarget, source string, includesActive bool, includedDescendants []string, colors platform.Palette) string {
	// Most actionable case: the user passed `--include <path>` where the
	// include path lives inside the target (so the include is a
	// descendant, not the ancestor that --include needs). Show them the
	// two correct shapes instead of the generic "your --include does not
	// cover this target" line.
	if len(includedDescendants) > 0 {
		descendant := includedDescendants[0]
		return fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %s--include %s points inside %s — it doesn't authorize %s itself.%s\n  %s--include must name the gitignored target, or an ancestor of it.%s\n\n  %sTo open %s and narrow to %s:%s\n    %scatclip %s --include %s --only %s%s\n  %sTo open %s directly:%s\n    %scatclip %s --include %s%s",
			colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
			colors.Dim, SingleQuoted(descendant), SingleQuoted(relTarget), SingleQuoted(relTarget), colors.Reset,
			colors.Dim, colors.Reset,
			colors.Dim, SingleQuoted(relTarget), SingleQuoted(descendant), colors.Reset,
			colors.OK, relTarget, SingleQuoted(relTarget), SingleQuoted(descendant), colors.Reset,
			colors.Dim, SingleQuoted(descendant), colors.Reset,
			colors.OK, descendant, SingleQuoted(relTarget), colors.Reset,
		) + IgnoreRemovalHint(source, colors)
	}
	if includesActive {
		return fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %sYour --include does not cover this target. Add it directly:%s\n  %sExample:%s %scatclip --include %s%s",
			colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset, colors.OK, SingleQuoted(relTarget), colors.Reset,
		) + IgnoreRemovalHint(source, colors)
	}
	return fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %sUse --include to allow it for this run.%s\n  %sExample:%s %scatclip --include %s%s\n  %sTo narrow inside it:%s   %scatclip --include %s --only \"*.ext\"%s",
		colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
		colors.Dim, colors.Reset,
		colors.Dim, colors.Reset, colors.OK, SingleQuoted(relTarget), colors.Reset,
		colors.Dim, colors.Reset, colors.OK, SingleQuoted(relTarget), colors.Reset,
	) + IgnoreRemovalHint(source, colors)
}

func ignoredFileMessage(relTarget, source string, fromChained, includesActive bool, colors platform.Palette) string {
	if includesActive {
		message := fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %sYour --include does not cover this target. Add it directly:%s\n  %sExample:%s %scatclip --include %s%s",
			colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset, colors.OK, SingleQuoted(relTarget), colors.Reset)
		if fromChained {
			return message
		}
		return message + fmt.Sprintf("\n  %sTo remove permanently:%s   %scatclip --hiss%s %s(delete the rule)%s",
			colors.Dim, colors.Reset, colors.OK, colors.Reset, colors.Dim, colors.Reset)
	}
	message := fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %sUse --include to allow it for this run.%s\n  %sExample:%s %scatclip --include %s%s",
		colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
		colors.Dim, colors.Reset,
		colors.Dim, colors.Reset, colors.OK, SingleQuoted(relTarget), colors.Reset)
	if fromChained {
		return message
	}
	return message + IgnoreRemovalHint(source, colors)
}

func includeQueryNeedsSelectionMessage(query string, colors platform.Palette) string {
	return fmt.Sprintf("\n%sError: %s needs an ignored-target selection.%s\n\n  %sUse --include with an exact ignored path, or run it in a TTY so catclip can open the ignored picker.%s",
		colors.Err, SingleQuoted(query), colors.Reset,
		colors.Dim, colors.Reset)
}

func looksLikeFileTarget(base string) bool {
	if strings.Contains(base, ".") {
		return true
	}
	switch strings.ToLower(base) {
	case "makefile", "dockerfile", "containerfile", "jenkinsfile", "procfile",
		"gemfile", "rakefile", "guardfile", "vagrantfile", "cmakelists.txt",
		"configure", "configure.ac", ".gitignore", ".gitattributes", ".gitmodules",
		".gitkeep", ".keep", ".editorconfig", ".npmrc", ".yarnrc", ".nvmrc":
		return true
	default:
		return false
	}
}

func prefersDirectFileLookup(target string) bool {
	base := path.Base(target)
	return looksLikeFileTarget(base) || strings.Contains(base, ".")
}

func withAllowedByInclude(entry Entry, block BlockInfo) Entry {
	entry.AllowedByInclude = true
	entry.BlockSource = block.Source
	return entry
}

func withTargetRoot(entries []Entry, targetRoot string) []Entry {
	targetRoot = normalizeRelPath(targetRoot)
	if targetRoot == "." || targetRoot == "" {
		return entries
	}
	for i := range entries {
		entries[i].TargetRoot = targetRoot
	}
	return entries
}

func markEntriesGitVisible(entries []Entry) []Entry {
	for i := range entries {
		entries[i].GitVisible = true
	}
	return entries
}

func CollectWantedBasenames(targets []string) map[string]struct{} {
	wanted := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		normalized := normalizeRelPath(target)
		if normalized == "" || normalized == "." {
			continue
		}
		if !strings.Contains(normalized, "/") && !prefersDirectFileLookup(normalized) {
			continue
		}
		base := path.Base(normalized)
		if base == "" || base == "." {
			continue
		}
		wanted[base] = struct{}{}
	}
	return wanted
}

func formatSkippedMatchesWarning(matches []SkippedMatch) []string {
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].RelPath < matches[j].RelPath
	})

	label := "matches"
	if len(matches) == 1 {
		label = "match"
	}
	lines := []string{fmt.Sprintf("Warning: %d %s skipped by ignore rules:", len(matches), label)}
	for _, match := range matches {
		lines = append(lines, fmt.Sprintf("  %s  [%s]", match.RelPath, match.BlockSource))
	}
	return []string{strings.Join(lines, "\n")}
}

func SingleQuoted(value string) string {
	return "'" + value + "'"
}

func WriteNoFilesMatchedMessage(scopes []command.ExecutionScope, stderr io.Writer, colors platform.Palette, hadSelectionCancel bool) error {
	if hadSelectionCancel {
		return nil
	}

	anyChanged := false
	hasStaged := false
	hasUnstaged := false
	hasUntracked := false
	for _, s := range scopes {
		anyChanged = anyChanged || s.HasGitSelection()
		hasStaged = hasStaged || s.Staged
		hasUnstaged = hasUnstaged || s.Unstaged
		hasUntracked = hasUntracked || s.Untracked
	}

	if anyChanged {
		flags := "--changed"
		if hasStaged || hasUnstaged || hasUntracked {
			var parts []string
			if hasStaged {
				parts = append(parts, "--staged")
			}
			if hasUnstaged {
				parts = append(parts, "--unstaged")
			}
			if hasUntracked {
				parts = append(parts, "--untracked")
			}
			flags = strings.Join(parts, "/")
		}

		if _, err := fmt.Fprintf(stderr, "%sNo %s files found.%s\n", colors.Warn, flags, colors.Reset); err != nil {
			return err
		}
		switch {
		case hasStaged && !hasUnstaged && !hasUntracked:
			_, _ = fmt.Fprintf(stderr, "  %sNo files are staged for commit. Use 'git add' to stage changes.%s\n", colors.Dim, colors.Reset)
		case hasUnstaged && !hasStaged && !hasUntracked:
			_, _ = fmt.Fprintf(stderr, "  %sNo tracked files have uncommitted modifications.%s\n", colors.Dim, colors.Reset)
		case hasUntracked && !hasStaged && !hasUnstaged:
			_, _ = fmt.Fprintf(stderr, "  %sNo new untracked files in the target directories.%s\n", colors.Dim, colors.Reset)
		default:
			_, _ = fmt.Fprintf(stderr, "  %sYour working tree may be clean, or the target has no modifications.%s\n", colors.Dim, colors.Reset)
		}
		_, err := fmt.Fprintf(stderr, "  %sRun without %s to select all files.%s\n", colors.Dim, flags, colors.Reset)
		return err
	}

	if _, err := fmt.Fprintf(stderr, "\n%sNo text files found matching your criteria.%s\n", colors.Warn, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "\n  %sPossible causes:%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s  1. Directory is empty or contains only binary files%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s  2. All files matched by ignore rules%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s  3. Typo in target name%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	// Add a case-sensitivity bullet only when a regex filter was used —
	// --contains and --snippet are PCRE2 regex matchers, case-sensitive
	// by default. --only/--exclude are shell globs and don't have this
	// concern; targets aren't pattern-matched at all. Keeping the bullet
	// conditional avoids misleading users who hit zero-match for an
	// unrelated reason.
	usedRegexFilter := false
	for _, s := range scopes {
		if s.Contains != "" || s.Snippet {
			usedRegexFilter = true
			break
		}
	}
	if usedRegexFilter {
		if _, err := fmt.Fprintf(stderr, "  %s  4. Pattern contains uppercase letters (smart-case: uppercase = exact match)%s\n", colors.Dim, colors.Reset); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(stderr, "\n  %sTry: catclip --all-ignore-rules            # see every ignore rule in effect (.hiss + .gitignore)%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s     catclip --include blocked-dir         # browse blocked dirs/files for this run%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stderr, "  %s     catclip --hiss                        # edit catclip's own ignore rules%s\n", colors.Dim, colors.Reset)
	return err
}
