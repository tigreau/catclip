package discovery

import (
	"errors"
	"fmt"
	"io"
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

var ErrSelectionCancelled = errors.New("selection cancelled")

// Resolver carries the per-scope discovery state — the invocation
// config, git context, working caches (text-file set, visible dirs/
// files), and a couple of picker-side hooks
// (startupEscHint, interactiveTargets cache).
//
// Fields named in CamelCase are accessed by external constructors
// (startup_picker.go's newStartupPickerResolver and internal_prediscovered.go's
// ApplyPrediscoveredScopeTail). All other fields are runtime-derived
// state managed by Resolver methods, and stay lowercase.
type Resolver struct {
	Cfg               command.Invocation
	GitCtx            git.Context
	AllowFileSymlinks bool
	WithBinaries      bool
	NoIgnore          bool
	// CaptureTargetPreviewSizes is enabled only while constructing an
	// interactive target picker. It lets the visible-file classifier start an
	// opportunistic metadata snapshot without adding size work to headless or
	// direct discovery.
	CaptureTargetPreviewSizes  bool
	WantedBasenames            map[string]struct{}
	ScopeTargets               []string
	StartupEscHint             string
	textFileSet                map[string]struct{}
	textFileSetReady           bool
	interactiveTargets         []TargetMatch
	interactiveTargetsOk       bool
	targetPreviewSizes         *search.TextSizeCapture
	targetPreviewInventory     []TargetMatch
	targetPreviewInventoryOK   bool
	retainedTargetPreviewDirs  []string
	retainedTargetPreviewPath  string
	retainedTargetPreviewReady bool
	committedTargetPreviewPath string
	committedTargetEntries     []Entry
	committedTargetMetadata    map[string]search.FileMetadata
	committedTargetReady       bool
	// targetInventoriesByScope caches no-ignore target inventories per narrowed
	// scope-target key ("" = the working-dir-wide universe). Separate cache
	// entries retain either the complete universe or only ignored rows.
	targetInventoriesByScope map[string][]TargetMatch
	// noIgnoreTargetWalks records targets already enumerated under the broad
	// no-ignore policy. Checkpoint replay uses it to avoid duplicate walks.
	noIgnoreTargetWalks  map[string]struct{}
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
	// Case-fold reverse indices populated alongside the visible-set maps.
	// On case-insensitive filesystems (APFS, NTFS) a user-typed target like
	// `Cli.go` resolves at the FS level to the canonical `cli.go`, but the
	// case-sensitive map lookups in fileBlockedBy / dirBlockedBy miss, causing
	// the resolver to falsely report ".gitignore" as the blocker. These maps
	// give those helpers a case-fold fallback so they match rg's behavior:
	// path arguments work regardless of casing on case-insensitive FS.
	visibleAllFold          map[string][]string
	visibleWithHissFold     map[string][]string
	visibleAllDirsFold      map[string][]string
	visibleWithHissDirsFold map[string][]string
	caseFoldIndexesReady    bool
	ignoreSetsReady         bool
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

func EvaluateScope(cfg command.Invocation, gitCtx git.Context, scopeIndex int, s command.ExecutionScope, stderr io.Writer, colors platform.Palette) (result Scope, err error) {
	mode := s.OutputMode()
	result = Scope{Scope: s, GitContext: gitCtx}
	defer func() {
		for i := range result.Diagnostics {
			result.Diagnostics[i].ScopeIndex = scopeIndex
		}
	}()

	resolver := Resolver{
		Cfg:               cfg,
		GitCtx:            gitCtx,
		AllowFileSymlinks: false,
		WithBinaries:      cfg.WithBinaries,
		NoIgnore:          s.NoIgnore || s.HasStage(command.StageNoIgnore),
		WantedBasenames:   CollectWantedBasenames(s.Targets),
		ScopeTargets:      append([]string(nil), s.Targets...),
	}

	var entries []Entry
	selectedPaths := make([]string, 0, len(s.Targets))
	explainedTargetCount := 0
	for _, target := range s.Targets {
		normalized := normalizeRelPath(target)
		if normalized == "" {
			normalized = "."
		}
		exactPath := false
		if !hasGlobChars(target) {
			exactPath, err = resolver.TargetPathExists(normalized)
			if err != nil {
				return result, err
			}
		}
		// Fuzzy shorthand already contained by an earlier exact directory adds
		// no files and should not open another picker. Exact targets always run:
		// an ignored child can be absent from its visible parent's walk.
		if !exactPath {
			covered, err := resolver.InteractiveQueryCoveredBySelection(target, selectedPaths)
			if err != nil {
				return result, err
			}
			if covered {
				continue
			}
		}
		diagnosticStart := len(result.Diagnostics)
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
		if len(discovered) == 0 && diagnosticsExplainTargetRequest(result.Diagnostics[diagnosticStart:]) {
			explainedTargetCount++
		}
		if len(discovered) > 0 && exactPath {
			selectedPaths = append(selectedPaths, normalized)
		}
		result.SelectionCancel = result.SelectionCancel || selectionCancelled
	}

	entries = DedupeEntriesByPath(entries)
	// Per-target diagnostics explain an empty scope only when every requested
	// target ended conclusively. If any sibling target produced entries, a later
	// stage can still empty the scope for an unrelated reason; if any sibling
	// target stayed unexplained, the generic footer still has useful work to do.
	if len(entries) > 0 || explainedTargetCount != len(s.Targets) {
		for i := range result.Diagnostics {
			result.Diagnostics[i].ExplainsEmptyResult = false
		}
	} else {
		for i := range result.Diagnostics {
			if diagnosticsExplainTargetRequest(result.Diagnostics[i : i+1]) {
				result.Diagnostics[i].ExplainsEmptyResult = true
				break
			}
		}
	}

	// If any per-target resolution produced an IsError diagnostic
	// (ignored-dir / ignored-file guidance, include-not-covering
	// guidance, etc.), the scope is unsatisfiable. Emitting the
	// partial entries from sibling targets would silently "succeed"
	// on a subset of what the user asked for — surfacing 2 cmd files
	// while `docs` in the same scope errored teaches the wrong
	// mental model. Drop the entries and mark scope-unsatisfiable so
	// the run exits 2 with the message already printed.
	if diagnosticsContainError(result.Diagnostics) {
		entries = nil
		for i := range result.Diagnostics {
			if result.Diagnostics[i].IsError {
				result.Diagnostics[i].IsScopeUnsatisfiable = true
				result.Diagnostics[i].ExplainsEmptyResult = true
			}
		}
	}

	if s.HasGitSelection() && !gitCtx.Enabled {
		// Hard-fail this scope: the user requested a git-only selection
		// without a git context. Emit an error-class Diagnostic and drop
		// entries so siblings of a --then chain can still proceed; the
		// per-scope loop in cli.go converts this into exit 2 (single
		// scope or all scopes unsatisfiable) or exit 1 (mixed success).
		// See docs/versions/v0.5.0/reports/ACTIVE_BUG_git_selection_silently_dropped_no_git.md.
		result.Diagnostics = append(result.Diagnostics, GitSelectionUnavailableDiagnostic(scopeIndex))
		result.Notices = DedupePreserveOrder(result.Notices)
		return result, nil
	}

	var stageDiagnostics []Diagnostic
	entries, stageDiagnostics, err = applyScopeStagesWithDiagnostics(&resolver, gitCtx, s, entries, scopeIndex, colors)
	if err != nil {
		return result, err
	}
	result.Diagnostics = append(result.Diagnostics, stageDiagnostics...)

	StampEntriesWithScopeOutputMode(entries, mode, s)
	result.Entries = EnsureEntryAbsPaths(entries, cfg.WorkingDir)
	result.Notices = DedupePreserveOrder(result.Notices)
	return result, nil
}

// GitSelectionUnavailableDiagnostic is the canonical diagnostic for a scope
// that requests a Git-backed filter outside a Git working tree. Retained
// interactive projections use the same value as a cold scope evaluation so a
// memo hit cannot change exit semantics or user-visible guidance.
func GitSelectionUnavailableDiagnostic(scopeIndex int) Diagnostic {
	return Diagnostic{
		Message:              command.GitSelectionRequiresGitRepoMessage(),
		IsScopeUnsatisfiable: true,
		ExplainsEmptyResult:  true,
		ScopeIndex:           scopeIndex,
	}
}

func diagnosticsExplainTargetRequest(diagnostics []Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.IsTargetNotFound || diagnostic.IsError || diagnostic.ExplainsEmptyResult {
			return true
		}
	}
	return false
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

// ensureIgnoreSets populates the cached rg-derived visible-file sets used
// for ignore attribution. visibleAll respects .gitignore only; visibleWithHiss
// layers the global .hiss on top. The dir maps are derived by walking the
// path.Dir chain of each file. No-ignore discovery still needs these sets to
// distinguish ordinary visible files from the ignored files they authorize.
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

// diagnosticsContainError reports whether any diagnostic in the slice
// is a hard error (IsError). Used to convert per-target error
// diagnostics into scope-unsatisfiable so downstream doesn't emit
// partial entries from sibling targets while one errored.
func diagnosticsContainError(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.IsError {
			return true
		}
	}
	return false
}

// lowercaseStringIndex builds a reverse index keyed by strings.ToLower so
// case-sensitive lookups can fall back to case-fold candidates. Retaining the
// original paths matters on Linux: `Case.txt` and `case.txt` can be two
// different files and must not be merged merely because their folded keys
// collide.
func lowercaseStringIndex(in map[string]struct{}) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k := range in {
		folded := strings.ToLower(k)
		out[folded] = append(out[folded], k)
	}
	return out
}

// ensureCaseFoldIndexes builds the expensive reverse maps only after a
// canonical path lookup misses. Ordinary exact-cased targets use the direct
// visible sets and should not pay one allocation per project path merely to
// support the case-insensitive filesystem fallback.
func (r *Resolver) ensureCaseFoldIndexes() error {
	if r.caseFoldIndexesReady {
		return nil
	}
	if err := r.ensureIgnoreSets(); err != nil {
		return err
	}
	r.visibleAllFold = lowercaseStringIndex(r.visibleAll)
	r.visibleWithHissFold = lowercaseStringIndex(r.visibleWithHiss)
	r.visibleAllDirsFold = lowercaseStringIndex(r.visibleAllDirs)
	r.visibleWithHissDirsFold = lowercaseStringIndex(r.visibleWithHissDirs)
	r.caseFoldIndexesReady = true
	return nil
}

// caseFoldCandidateIsSameFile asks the filesystem whether a folded candidate
// is the same physical path as relPath. APFS/NTFS wrong-case lookups return the
// same file and preserve Catclip's existing recovery; case-sensitive filesystems
// can hold distinct case-colliding paths, which must remain distinct.
func (r *Resolver) caseFoldCandidateIsSameFile(relPath string, candidates []string) bool {
	if len(candidates) == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(relPath)))
	if err != nil {
		return false
	}
	for _, candidate := range candidates {
		candidateInfo, err := os.Stat(filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(candidate)))
		if err == nil && os.SameFile(info, candidateInfo) {
			return true
		}
	}
	return false
}

// dirBlockedBy reports the ignore source blocking a directory's contents,
// or nil if visible. When neither cached set covers the dir we probe rg
// with --no-ignore: empty dirs return nothing (treated as not blocked),
// while dirs whose descendants are all gitignored return paths. With
// --no-ignore, the caller routes through the bypass path; here we
// synthesize a block so callers that branch on block != nil pick that path.
func (r *Resolver) dirBlockedBy(relPath string) (*BlockInfo, error) {
	if r.NoIgnore {
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
	// Case-fold fallback: on case-insensitive filesystems (APFS, NTFS) the
	// user-typed `Docs` resolves at the FS level to canonical `docs`. rg
	// already accepts this transparently; mirror that behavior here so
	// dirBlockedBy doesn't falsely attribute the miss to .gitignore.
	if err := r.ensureCaseFoldIndexes(); err != nil {
		return nil, err
	}
	lowered := strings.ToLower(relPath)
	if r.caseFoldCandidateIsSameFile(relPath, r.visibleWithHissDirsFold[lowered]) {
		return nil, nil
	}
	if r.caseFoldCandidateIsSameFile(relPath, r.visibleAllDirsFold[lowered]) {
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
// if the file is visible. Even --no-ignore must retain this attribution:
// its no-ignore walk is broad, but ordinary visible files must not be marked or
// rendered as newly admitted ignored files.
func (r *Resolver) fileBlockedBy(relPath string) (*BlockInfo, error) {
	if relPath == "." || relPath == "" {
		return nil, nil
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
	// Case-fold fallback: matches dirBlockedBy. On macOS APFS / Windows NTFS
	// the user-typed `Cli.go` is the same physical file as the visible-set's
	// canonical `cli.go`; without this fallback the missed lookup falsely
	// resolves to ".gitignore" and the user sees a wrong-attribution error.
	if err := r.ensureCaseFoldIndexes(); err != nil {
		return nil, err
	}
	lowered := strings.ToLower(relPath)
	if r.caseFoldCandidateIsSameFile(relPath, r.visibleWithHissFold[lowered]) {
		return nil, nil
	}
	if r.caseFoldCandidateIsSameFile(relPath, r.visibleAllFold[lowered]) {
		return &BlockInfo{Source: ".hiss"}, nil
	}
	return &BlockInfo{Source: ".gitignore"}, nil
}

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

func (r *Resolver) targetPathIsDirectory(relTarget string) (bool, error) {
	info, err := os.Stat(filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(relTarget)))
	if err == nil {
		return info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
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

	if err := ValidateTargetBoundary(target); err != nil {
		return nil, nil, nil, false, err
	}

	if hasGlobChars(target) {
		return r.resolveGlobTarget(scopeIndex, target, colors)
	}

	normalizedTarget := normalizeRelPath(target)
	if normalizedTarget == "" {
		normalizedTarget = "."
	}
	// Exact paths remain direct instructions under either ignore policy. A
	// non-exact --no-ignore query is resolved later against one combined visible
	// and ignored inventory; it is not an ignored-only fallback.
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
		if blockedDir != nil && !r.NoIgnore {
			Diagnostics = append(Diagnostics, Diagnostic{Message: targetNotFoundOrIgnoredAncestorMessage(r, target, scopeIndex, colors), IsTargetNotFound: true})
			return nil, Diagnostics, notices, false, nil
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
			selected, err := chooseFileMatch(r.Cfg, baseName, resolvedDir, fuzzyFiles, blockedDir != nil, stderr, colors)
			if err != nil {
				if errors.Is(err, ErrSelectionCancelled) {
					return nil, Diagnostics, notices, true, nil
				}
				return nil, Diagnostics, notices, false, err
			}
			selectedMatches := make([]TargetMatch, 0, len(selected))
			for _, p := range selected {
				m := TargetMatch{Path: p, Kind: "file"}
				if blockedDir != nil {
					m.Ignored = true
					m.IgnoreSource = blockedDir.Source
				}
				selectedMatches = append(selectedMatches, m)
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

	if r.NoIgnore {
		targetMatches, err := r.noIgnoreQueryTargetMatches(normalizedTarget)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		if len(targetMatches) == 0 {
			Diagnostics = append(Diagnostics, Diagnostic{Message: targetNotFoundOrIgnoredAncestorMessage(r, target, scopeIndex, colors), IsTargetNotFound: true})
			return nil, Diagnostics, notices, false, nil
		}
		discovered, cancelled, err := r.resolveRankedTargetMatches(normalizedTarget, targetMatches, stderr, colors)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		return discovered, Diagnostics, notices, cancelled, nil
	}

	searchedFiles := false
	if prefersDirectFileLookup(normalizedTarget) {
		searchedFiles = true
		var skipped []SkippedMatch
		discovered, skipped, err := r.resolveVisibleFilesByBasename(".", normalizedTarget)
		if err != nil {
			return nil, Diagnostics, notices, false, err
		}
		if len(discovered) > 0 {
			return discovered, Diagnostics, notices, false, nil
		}
		if hidden := r.ignoredExactFileCandidates(skipped); len(hidden) > 0 {
			Diagnostics = append(Diagnostics, Diagnostic{
				Message:          ignoredAncestorMessage(target, scopeIndex, hidden, colors),
				IsTargetNotFound: true,
			})
			return nil, Diagnostics, notices, false, nil
		}
		notices = append(notices, formatSkippedMatchesWarning(skipped)...)
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

	targetMatches, err := r.fuzzySearchTargetMatches(".", normalizedTarget)
	if err != nil {
		return nil, nil, nil, false, err
	}
	switch len(targetMatches) {
	case 0:
	case 1:
		// A visible exact basename retains priority over hidden duplicates.
		// For an extensionless non-exact result, check hidden exact identity
		// before accepting the lower-quality fuzzy match. Zero-match execution
		// already probes through targetNotFoundOrIgnoredAncestorMessage, while
		// multi-match execution is safely ambiguity-gated.
		if !searchedFiles && path.Base(targetMatches[0].Path) != normalizedTarget {
			if hidden := r.findIgnoredAncestors(normalizedTarget); len(hidden) > 0 {
				Diagnostics = append(Diagnostics, Diagnostic{
					Message:          ignoredAncestorMessage(target, scopeIndex, hidden, colors),
					IsTargetNotFound: true,
				})
				return nil, Diagnostics, notices, false, nil
			}
		}
		discovered, handled, diag, err := r.resolveTargetMatch(targetMatches[0], colors)
		if diag != nil {
			Diagnostics = append(Diagnostics, *diag)
		}
		if handled {
			return discovered, Diagnostics, notices, false, err
		}
	default:
		selected, err := chooseFuzzyTargetMatches(r.Cfg, normalizedTarget, targetMatches, stderr, colors)
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

	// The complete mixed fzf pass found nothing. For extensionless shorthand,
	// retain exact-basename diagnostics that are not
	// part of the visible picker inventory.
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
	}

	if len(notices) == 0 {
		Diagnostics = append(Diagnostics, Diagnostic{Message: targetNotFoundOrIgnoredAncestorMessage(r, target, scopeIndex, colors), IsTargetNotFound: true})
	}
	return nil, Diagnostics, notices, false, nil
}

func (r *Resolver) resolveGlobTarget(scopeIndex int, pattern string, colors platform.Palette) ([]Entry, []Diagnostic, []string, bool, error) {
	if strings.Contains(pattern, "**") {
		return nil, nil, nil, false, newUsageError("%s", unsupportedTargetDoublestarMessage(pattern))
	}
	allFiles, err := r.discoverGlobCandidateFiles()
	if err != nil {
		return nil, nil, nil, false, err
	}
	if r.NoIgnore {
		r.markNoIgnoreTargetWalk(pattern)
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
			Message:             globZeroMatchWarning(r, pattern, scopeIndex, colors),
			IsTargetNotFound:    true,
			ExplainsEmptyResult: true,
			ScopeIndex:          scopeIndex,
		}
		return nil, []Diagnostic{diag}, nil, false, nil
	}
	return withTargetRoot(matched, "."), nil, nil, false, nil
}

func (r *Resolver) discoverGlobCandidateFiles() ([]Entry, error) {
	if r.NoIgnore {
		return r.discoverFilesUnderNoIgnore(".")
	}
	entries, err := r.discoverVisibleFilesUnder(".")
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *Resolver) resolveRankedTargetMatches(query string, matches []TargetMatch, stderr io.Writer, colors platform.Palette) ([]Entry, bool, error) {
	switch len(matches) {
	case 0:
		return nil, false, nil
	case 1:
		discovered, handled, _, err := r.resolveTargetMatch(matches[0], colors)
		if err != nil || !handled {
			return nil, false, err
		}
		return discovered, false, nil
	default:
		selected, err := chooseFuzzyTargetMatches(r.Cfg, query, matches, stderr, colors)
		if err != nil {
			if errors.Is(err, ErrSelectionCancelled) {
				return nil, true, nil
			}
			return nil, false, err
		}
		discovered, err := r.resolveTargetMatches(selected, colors)
		return discovered, false, err
	}
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
		if r.NoIgnore {
			files, err := r.discoverFilesUnderNoIgnore(relTarget)
			r.markNoIgnoreTargetWalk(relTarget)
			return withTargetRoot(files, relTarget), true, nil, err
		}
		// An exact directory target is an explicit traversal root. Ripgrep
		// admits that root even when an ancestor ignore rule hides it, while
		// continuing to enforce ignore files below the root. This avoids a
		// project-wide permission scan and mirrors ordinary path-oriented CLI
		// tools.
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
	if r.NoIgnore {
		block, err := r.fileBlockedBy(relTarget)
		if err != nil {
			return nil, true, nil, err
		}
		if block != nil {
			entry = withIgnoreBypassed(entry, *block)
		}
	}
	if dir := normalizeRelPath(path.Dir(relTarget)); dir != "." {
		entry.TargetRoot = dir
	}
	// A concrete file target is also a direct instruction. Do not build the
	// cwd-wide visible inventories merely to decide whether this one path may
	// be read.
	if r.NoIgnore {
		r.markNoIgnoreTargetWalk(relTarget)
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
// visibleWithHiss layers the global .hiss on top. No-ignore discovery still uses
// these sets to distinguish ordinary visible files from admitted ignored ones.
func (r *Resolver) resolveIgnoreSets() (map[string]struct{}, map[string]struct{}, error) {
	visibleAll, err := search.ResolveVisibleFileSet(r.Cfg.WorkingDir, "", nil)
	if err != nil {
		return nil, nil, err
	}
	visibleWithHiss := visibleAll
	hissPath, err := ReadableHissPath()
	if err != nil {
		return nil, nil, err
	}
	if hissPath != "" {
		visibleWithHiss, err = search.ResolveVisibleFileSet(r.Cfg.WorkingDir, hissPath, nil)
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
	var entries []Entry
	if r.CaptureTargetPreviewSizes && !r.WithBinaries {
		var textSet map[string]struct{}
		textSet, r.targetPreviewSizes, err = search.ClassifyTextPathsWithSizeCapture(r.Cfg.WorkingDir, paths)
		if err == nil {
			entries = r.entriesFromClassifiedPaths(paths, textSet)
		}
	} else {
		entries, err = r.textEntriesFromRipgrepPaths(paths)
	}
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

func (r *Resolver) entriesFromClassifiedPaths(relPaths []string, textSet map[string]struct{}) []Entry {
	entries := make([]Entry, 0, len(relPaths))
	for _, rel := range relPaths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." || CoveredBySelection(rel, r.VisibleDirs.SymlinkDirs) {
			continue
		}
		if _, ok := textSet[rel]; !ok {
			continue
		}
		entries = append(entries, Entry{RelPath: rel})
	}
	return entries
}

func (r *Resolver) textEntriesFromRipgrepPaths(relPaths []string) ([]Entry, error) {
	var textSet map[string]struct{}
	if !r.WithBinaries {
		var err error
		textSet, err = search.ClassifyTextPaths(r.Cfg.WorkingDir, relPaths)
		if err != nil {
			return nil, err
		}
	}
	entries := make([]Entry, 0, len(relPaths))
	for _, rel := range relPaths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." || CoveredBySelection(rel, r.VisibleDirs.SymlinkDirs) {
			continue
		}
		if !r.WithBinaries {
			if _, ok := textSet[rel]; !ok {
				continue
			}
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

func withIgnoreBypassed(entry Entry, block BlockInfo) Entry {
	entry.GitVisible = false
	entry.IgnoreBypassed = true
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
