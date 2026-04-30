package catclip

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/picker"
)

var errSelectionCancelled = errors.New("selection cancelled")

type errNoScopedIgnoredTargets struct {
	ScopeTargets []string
}

func (e errNoScopedIgnoredTargets) Error() string {
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

type scopeResolver struct {
	cfg                  runConfig
	gitCtx               gitContext
	matcher              scopeMatcher
	projectIgnore        scopeMatcher
	useProjectIgnore     bool
	allowFileSymlinks    bool
	textFileCache        map[string]bool
	useGitIgnore         bool
	withBinaries         bool
	includedTargets      includedTargetSet
	wantedBasenames      map[string]struct{}
	interactiveTargets   []targetMatch
	interactiveTargetsOk bool
	ignoredTargets       []targetMatch
	ignoredTargetsOk     bool
	visibleDirs          visibleDirIndex
	visibleDirsReady     bool
	visibleFiles         visibleFileIndex
	visibleFilesReady    bool
	visibleFileList      []fileEntry
	visibleFileListReady bool
}

func evaluateScope(cfg runConfig, gitCtx gitContext, scopeIndex int, s executionScope, baseRules []ignoreRule, stderr io.Writer, colors colorPalette) ([]fileEntry, []diagnostic, []string, bool, error) {
	mode := executionScopeOutputMode(s)

	includeAll := includeTargetsContainWildcard(s.IncludedTargets)

	var matcher scopeMatcher
	if includeAll {
		matcher = scopeMatcher{}
	} else {
		var err error
		matcher, err = buildScopeMatcher(baseRules, s)
		if err != nil {
			return nil, nil, nil, false, err
		}
	}
	projectIgnore, useProjectIgnore, err := buildProjectIgnoreMatcher(cfg.WorkingDir, gitCtx.Enabled)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if includeAll {
		useProjectIgnore = false
	}
	resolver := scopeResolver{
		cfg:               cfg,
		gitCtx:            gitCtx,
		matcher:           matcher,
		projectIgnore:     projectIgnore,
		useProjectIgnore:  useProjectIgnore,
		allowFileSymlinks: false,
		useGitIgnore:      gitCtx.Enabled && !includeAll,
		withBinaries:      cfg.WithBinaries,
		includedTargets:   buildIncludedTargetSet(cfg.WorkingDir, s.IncludedTargets),
		wantedBasenames:   collectWantedBasenames(s.Targets),
	}

	var diagnostics []diagnostic
	var notices []string
	var entries []fileEntry
	selectedPaths := make([]string, 0, len(s.Targets))
	hadSelectionCancel := false
	for _, target := range s.Targets {
		covered, err := resolver.interactiveQueryCoveredBySelection(target, selectedPaths)
		if err != nil {
			return nil, diagnostics, notices, hadSelectionCancel, err
		}
		if covered {
			continue
		}

		discovered, targetDiagnostics, targetNotices, selectionCancelled, err := resolver.resolveAndDiscoverTarget(scopeIndex, target, stderr, colors)
		if err != nil {
			return nil, diagnostics, notices, hadSelectionCancel, err
		}
		diagnostics = append(diagnostics, targetDiagnostics...)
		notices = append(notices, targetNotices...)
		entries = append(entries, discovered...)
		if len(discovered) > 0 {
			normalized := normalizeRelPath(target)
			if normalized == "" {
				normalized = "."
			}
			exists, err := resolver.targetPathExists(normalized)
			if err != nil {
				return nil, diagnostics, notices, hadSelectionCancel, err
			}
			if exists {
				selectedPaths = append(selectedPaths, normalized)
			}
		}
		hadSelectionCancel = hadSelectionCancel || selectionCancelled
	}

	entries = dedupeEntriesByPath(entries)

	if gitCtx.Enabled {
		entries, err = filterGitIgnoredEntries(gitCtx, entries)
		if err != nil {
			return nil, diagnostics, notices, hadSelectionCancel, err
		}
	}

	if executionScopeHasGitSelection(s) && !gitCtx.Enabled {
		diagnostics = append(diagnostics, diagnostic{message: "Warning: --changed/--staged/--unstaged/--untracked require a git repo."})
	}

	entries, err = applyScopeStages(&resolver, gitCtx, s, entries)
	if err != nil {
		return nil, diagnostics, notices, hadSelectionCancel, err
	}

	for i := range entries {
		entries[i].Mode = mode
		entries[i].SnippetPattern = s.SnippetPattern
		entries[i].Lines = s.Lines
		entries[i].LinesStart = s.LinesStart
		entries[i].LinesEnd = s.LinesEnd
		entries[i].DiffWantStaged = s.Staged
		entries[i].DiffWantUnstaged = s.Unstaged
	}
	entries = ensureEntryAbsPaths(entries, cfg.WorkingDir)
	return entries, diagnostics, dedupePreserveOrder(notices), hadSelectionCancel, nil
}

func includeTargetsContainWildcard(targets []string) bool {
	for _, t := range targets {
		if t == "*" {
			return true
		}
	}
	return false
}

func buildIncludedTargetSet(workingDir string, targets []string) includedTargetSet {
	if len(targets) == 0 {
		return includedTargetSet{}
	}
	set := includedTargetSet{
		exact:    make(map[string]struct{}, len(targets)),
		dirs:     make([]string, 0, len(targets)),
		wildcard: includeTargetsContainWildcard(targets),
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

func buildProjectIgnoreMatcher(workingDir string, gitEnabled bool) (scopeMatcher, bool, error) {
	if gitEnabled {
		return scopeMatcher{}, false, nil
	}
	rules, err := loadProjectGitignoreRules(workingDir)
	if err != nil {
		return scopeMatcher{}, false, err
	}
	if len(rules) == 0 {
		return scopeMatcher{}, false, nil
	}
	matcher, err := buildScopeMatcher(rules, executionScope{})
	if err != nil {
		return scopeMatcher{}, false, err
	}
	return matcher, true, nil
}

func (r *scopeResolver) ensureProjectIgnoreMatcher() error {
	if r.useGitIgnore || r.useProjectIgnore || r.includedTargets.wildcard {
		return nil
	}
	matcher, ok, err := buildProjectIgnoreMatcher(r.cfg.WorkingDir, false)
	if err != nil {
		return err
	}
	if ok {
		r.projectIgnore = matcher
		r.useProjectIgnore = true
	}
	return nil
}

func (r *scopeResolver) projectDirIgnored(relPath string) (bool, string, error) {
	if err := r.ensureProjectIgnoreMatcher(); err != nil {
		return false, "", err
	}
	if !r.useProjectIgnore {
		return false, "", nil
	}
	if ignored, rule := r.projectIgnore.dirIgnored(relPath); ignored {
		return true, rule, nil
	}
	return false, "", nil
}

func (r *scopeResolver) projectFileIgnored(relPath string) (bool, string, error) {
	if err := r.ensureProjectIgnoreMatcher(); err != nil {
		return false, "", err
	}
	if !r.useProjectIgnore {
		return false, "", nil
	}
	if ignored, rule := r.projectIgnore.fileIgnoredByFileRule(relPath); ignored {
		return true, rule, nil
	}
	if ignored, rule := r.projectIgnore.dirRuleBlockingFile(relPath); ignored {
		return true, rule, nil
	}
	return false, "", nil
}

func (r *scopeResolver) targetIncluded(target string) bool {
	if len(r.includedTargets.exact) == 0 {
		return false
	}
	if r.includedTargets.wildcard {
		return true
	}
	target = normalizeRelPath(target)
	if _, ok := r.includedTargets.exact[target]; ok {
		return true
	}
	for _, dir := range r.includedTargets.dirs {
		if target == dir || strings.HasPrefix(target, dir+"/") {
			return true
		}
	}
	return false
}

func (r *scopeResolver) targetPathExists(relTarget string) (bool, error) {
	_, err := os.Stat(filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(relTarget)))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// canResolveTargetWithoutPrompt mirrors the non-interactive resolver's
// deterministic branches. It returns true only when a target can be handled
// without opening fzf or prompting for ambiguity resolution.
func (r *scopeResolver) canResolveTargetWithoutPrompt(target string) (bool, error) {
	if hasGlobChars(target) {
		return true, nil
	}

	normalizedTarget := normalizeRelPath(target)
	if normalizedTarget == "" {
		normalizedTarget = "."
	}

	exists, err := r.targetPathExists(normalizedTarget)
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

func (r *scopeResolver) canResolveScopedTargetWithoutPrompt(normalizedTarget string) (bool, error) {
	dirPart := path.Dir(normalizedTarget)
	baseName := path.Base(normalizedTarget)

	resolvedDir, ok, err := r.resolveChainedDirWithoutPrompt(dirPart)
	if err != nil || !ok {
		return false, err
	}

	fullRel := normalizeRelPath(path.Join(resolvedDir, baseName))
	exists, err := r.targetPathExists(fullRel)
	if err != nil {
		return false, err
	}
	if exists {
		return true, nil
	}

	blockedDir, err := r.blockInfoForDir(resolvedDir)
	if err != nil {
		return false, err
	}
	if blockedDir != nil {
		discovered, err := discoverFilesByBasenameUnder(r.cfg.WorkingDir, filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(resolvedDir)), resolvedDir, baseName, r.matcher, r.classifyTextFile, blockedDir, r.withBinaries)
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

func (r *scopeResolver) resolveAndDiscoverTarget(scopeIndex int, target string, stderr io.Writer, colors colorPalette) ([]fileEntry, []diagnostic, []string, bool, error) {
	var diagnostics []diagnostic
	var notices []string

	if filepath.IsAbs(target) {
		return nil, nil, nil, false, newUsageError("Error: Absolute paths not allowed: %s\n  Use a relative path from your project root instead.", singleQuoted(target))
	}
	if containsParentTraversal(target) {
		return nil, nil, nil, false, newUsageError("Error: Cannot traverse above working directory: %s\n  catclip only operates within the current directory tree.\n  Use a relative path from your project root instead.\n  Example: catclip config/", singleQuoted(target))
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
			diagnostics = append(diagnostics, *diag)
		}
		return discovered, diagnostics, notices, false, err
	}

	if strings.Contains(normalizedTarget, "/") {
		dirPart := path.Dir(normalizedTarget)
		baseName := path.Base(normalizedTarget)
		resolvedDir, err := r.resolveChainedDir(dirPart, stderr, colors)
		if err != nil {
			if errors.Is(err, errSelectionCancelled) {
				return nil, diagnostics, notices, true, nil
			}
			return nil, diagnostics, notices, false, err
		}
		fullRel := normalizeRelPath(path.Join(resolvedDir, baseName))
		discovered, handled, diag, err := r.resolveExactTarget(fullRel, true, colors)
		if handled {
			if diag != nil {
				diagnostics = append(diagnostics, *diag)
			}
			return discovered, diagnostics, notices, false, err
		}
		blockedDir, err := r.blockInfoForDir(resolvedDir)
		if err != nil {
			return nil, diagnostics, notices, false, err
		}
		if blockedDir != nil {
			discovered, err = discoverFilesByBasenameUnder(r.cfg.WorkingDir, filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(resolvedDir)), resolvedDir, baseName, r.matcher, r.classifyTextFile, blockedDir, r.withBinaries)
		} else {
			var skipped []skippedMatch
			discovered, skipped, err = r.resolveVisibleFilesByBasename(resolvedDir, baseName)
			notices = append(notices, formatSkippedMatchesWarning(skipped)...)
		}
		if err != nil {
			return nil, diagnostics, notices, false, err
		}
		if len(discovered) > 0 {
			return withTargetRoot(discovered, resolvedDir), diagnostics, notices, false, nil
		}
		fuzzyFiles, err := r.fuzzySearchFilesUnder(resolvedDir, baseName, blockedDir)
		if err != nil {
			return nil, diagnostics, notices, false, err
		}
		switch len(fuzzyFiles) {
		case 0:
		case 1:
			discovered, handled, diag, err := r.resolveExactTarget(fuzzyFiles[0], true, colors)
			if diag != nil {
				diagnostics = append(diagnostics, *diag)
			}
			if handled {
				return discovered, diagnostics, notices, false, err
			}
		default:
			selected, err := chooseFileMatch(r.cfg, baseName, resolvedDir, fuzzyFiles, stderr, colors)
			if err != nil {
				if errors.Is(err, errSelectionCancelled) {
					return nil, diagnostics, notices, true, nil
				}
				return nil, diagnostics, notices, false, err
			}
			selectedMatches := make([]targetMatch, 0, len(selected))
			for _, path := range selected {
				selectedMatches = append(selectedMatches, targetMatch{Path: path, Kind: "file"})
			}
			discovered, err := r.resolveTargetMatches(selectedMatches, colors)
			if err != nil {
				return nil, diagnostics, notices, false, err
			}
			return discovered, diagnostics, notices, false, nil
		}
		diagnostics = append(diagnostics, diagnostic{message: targetNotFoundWarning(target, scopeIndex, colors), isTargetNotFound: true})
		return nil, diagnostics, notices, false, nil
	}

	searchedFiles := false
	if prefersDirectFileLookup(normalizedTarget) {
		searchedFiles = true
		var skipped []skippedMatch
		discovered, skipped, err := r.resolveVisibleFilesByBasename(".", normalizedTarget)
		if err != nil {
			return nil, diagnostics, notices, false, err
		}
		notices = append(notices, formatSkippedMatchesWarning(skipped)...)
		if len(discovered) > 0 {
			return discovered, diagnostics, notices, false, nil
		}
		fuzzyFiles, err := r.fuzzySearchFiles(".", normalizedTarget)
		if err != nil {
			return nil, diagnostics, notices, false, err
		}
		switch len(fuzzyFiles) {
		case 0:
		case 1:
			discovered, handled, diag, err := r.resolveExactTarget(fuzzyFiles[0], false, colors)
			if diag != nil {
				diagnostics = append(diagnostics, *diag)
			}
			if handled {
				return discovered, diagnostics, notices, false, err
			}
		default:
			selected, err := chooseFileMatch(r.cfg, normalizedTarget, ".", fuzzyFiles, stderr, colors)
			if err != nil {
				if errors.Is(err, errSelectionCancelled) {
					return nil, diagnostics, notices, true, nil
				}
				return nil, diagnostics, notices, false, err
			}
			selectedMatches := make([]targetMatch, 0, len(selected))
			for _, path := range selected {
				selectedMatches = append(selectedMatches, targetMatch{Path: path, Kind: "file"})
			}
			discovered, err := r.resolveTargetMatches(selectedMatches, colors)
			if err != nil {
				return nil, diagnostics, notices, false, err
			}
			return discovered, diagnostics, notices, false, nil
		}
	}

	if resolvedDir, ok, err := r.resolveVisibleDirByExactBasename(".", normalizedTarget); err != nil {
		return nil, diagnostics, notices, false, err
	} else if ok && resolvedDir != "" {
		conflict, err := r.hasVisibleFileBasenameConflict(".", normalizedTarget)
		if err != nil {
			return nil, diagnostics, notices, false, err
		}
		if !conflict {
			discovered, handled, diag, err := r.resolveExactTarget(resolvedDir, false, colors)
			if diag != nil {
				diagnostics = append(diagnostics, *diag)
			}
			if handled {
				return discovered, diagnostics, notices, false, err
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
			return nil, diagnostics, notices, false, err
		}
		if len(fuzzyFiles) > 0 {
			combined, err := rankTargetMatches(normalizedTarget, matches, fuzzyFiles)
			if err != nil {
				return nil, diagnostics, notices, false, err
			}
			if len(combined) == 1 {
				discovered, handled, diag, err := r.resolveTargetMatch(combined[0], colors)
				if diag != nil {
					diagnostics = append(diagnostics, *diag)
				}
				if handled {
					return discovered, diagnostics, notices, false, err
				}
			}
			if r.cfg.canPromptForChoice() {
				selected, err := chooseTargetMatch(r.cfg, normalizedTarget, combined, stderr, colors)
				if err != nil {
					if errors.Is(err, errSelectionCancelled) {
						return nil, diagnostics, notices, true, nil
					}
					return nil, diagnostics, notices, false, err
				}
				discovered, err := r.resolveTargetMatches(selected, colors)
				if err != nil {
					return nil, diagnostics, notices, false, err
				}
				return discovered, diagnostics, notices, false, nil
			}
		}
	}
	switch len(matches) {
	case 0:
		if !searchedFiles {
			var skipped []skippedMatch
			discovered, skipped, err := r.resolveVisibleFilesByBasename(".", normalizedTarget)
			if err != nil {
				return nil, diagnostics, notices, false, err
			}
			notices = append(notices, formatSkippedMatchesWarning(skipped)...)
			if len(discovered) > 0 {
				return discovered, diagnostics, notices, false, nil
			}
			fuzzyFiles, err := r.fuzzySearchFiles(".", normalizedTarget)
			if err != nil {
				return nil, diagnostics, notices, false, err
			}
			switch len(fuzzyFiles) {
			case 0:
			case 1:
				discovered, handled, diag, err := r.resolveExactTarget(fuzzyFiles[0], false, colors)
				if diag != nil {
					diagnostics = append(diagnostics, *diag)
				}
				if handled {
					return discovered, diagnostics, notices, false, err
				}
			default:
				selected, err := chooseFileMatch(r.cfg, normalizedTarget, ".", fuzzyFiles, stderr, colors)
				if err != nil {
					if errors.Is(err, errSelectionCancelled) {
						return nil, diagnostics, notices, true, nil
					}
					return nil, diagnostics, notices, false, err
				}
				selectedMatches := make([]targetMatch, 0, len(selected))
				for _, path := range selected {
					selectedMatches = append(selectedMatches, targetMatch{Path: path, Kind: "file"})
				}
				discovered, err := r.resolveTargetMatches(selectedMatches, colors)
				if err != nil {
					return nil, diagnostics, notices, false, err
				}
				return discovered, diagnostics, notices, false, nil
			}
		}
		if len(notices) == 0 {
			diagnostics = append(diagnostics, diagnostic{message: targetNotFoundWarning(target, scopeIndex, colors), isTargetNotFound: true})
		}
		return nil, diagnostics, notices, false, nil
	case 1:
		files, err := r.discoverVisibleFilesUnder(matches[0])
		return withTargetRoot(files, matches[0]), diagnostics, notices, false, err
	default:
		selected, err := chooseDirectoryMatch(r.cfg, target, ".", matches, stderr, colors)
		if err != nil {
			if errors.Is(err, errSelectionCancelled) {
				return nil, diagnostics, notices, true, nil
			}
			return nil, nil, nil, false, err
		}
		selectedMatches := make([]targetMatch, 0, len(selected))
		for _, path := range selected {
			selectedMatches = append(selectedMatches, targetMatch{Path: path, Kind: "dir"})
		}
		files, err := r.resolveTargetMatches(selectedMatches, colors)
		if err != nil {
			return nil, diagnostics, notices, false, err
		}
		return files, diagnostics, notices, false, nil
	}
}

func (r *scopeResolver) resolveGlobTarget(scopeIndex int, pattern string, colors colorPalette) ([]fileEntry, []diagnostic, []string, bool, error) {
	allFiles, err := r.discoverVisibleFilesUnder(".")
	if err != nil {
		return nil, nil, nil, false, err
	}
	var matched []fileEntry
	for _, entry := range allFiles {
		ok, matchErr := path.Match(pattern, path.Base(entry.RelPath))
		if matchErr != nil {
			return nil, nil, nil, false, newUsageError("Error: Invalid glob pattern %s: %v", singleQuoted(pattern), matchErr)
		}
		if !ok {
			ok, _ = path.Match(pattern, entry.RelPath)
		}
		if ok {
			matched = append(matched, entry)
		}
	}
	if len(matched) == 0 {
		diag := diagnostic{
			message:          targetNotFoundWarning(pattern, scopeIndex, colors),
			isTargetNotFound: true,
		}
		return nil, []diagnostic{diag}, nil, false, nil
	}
	return withTargetRoot(matched, "."), nil, nil, false, nil
}

func (r *scopeResolver) resolveTargetMatch(match targetMatch, colors colorPalette) ([]fileEntry, bool, *diagnostic, error) {
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

func (r *scopeResolver) resolveIncludedTarget(target, normalizedTarget string, stderr io.Writer, colors colorPalette) ([]fileEntry, []diagnostic, bool, error) {
	var diagnostics []diagnostic

	if discovered, handled, diag, err := r.resolveExactTarget(normalizedTarget, false, colors); handled {
		if diag != nil {
			diagnostics = append(diagnostics, *diag)
		}
		return discovered, diagnostics, false, err
	}

	if !r.cfg.canPromptForChoice() {
		return nil, []diagnostic{{
			message: includeQueryNeedsSelectionMessage(target, colors),
			isError: true,
		}}, false, nil
	}

	matches, _, err := r.chooseIgnoredTargetMatches(target, "include> ", nil, nil, nil)
	if err != nil {
		if errors.Is(err, errSelectionCancelled) {
			return nil, nil, true, nil
		}
		return nil, nil, false, err
	}
	discovered, err := r.resolveTargetMatches(matches, colors)
	if err != nil {
		return nil, nil, false, err
	}
	return discovered, diagnostics, false, nil
}

func (r *scopeResolver) resolveExactTarget(relTarget string, fromChained bool, colors colorPalette) ([]fileEntry, bool, *diagnostic, error) {
	absTarget := filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(relTarget))
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
		hasIncludes := len(r.includedTargets.exact) > 0
		if ignored, rule := r.matcher.dirIgnored(relTarget); ignored {
			if !r.targetIncluded(relTarget) {
				return nil, true, &diagnostic{message: ignoredDirMessage(relTarget, rule, ".hiss", hasIncludes, colors), isError: true}, nil
			}
			files, err := discoverFilesUnder(r.cfg.WorkingDir, absTarget, relTarget, r.matcher, r.classifyTextFile, &blockInfo{Rule: rule, Source: ".hiss"}, r.withBinaries)
			return withTargetRoot(files, relTarget), true, nil, err
		}
		projectIgnored, projectRule, err := r.projectDirIgnored(relTarget)
		if err != nil {
			return nil, true, nil, err
		}
		if projectIgnored {
			if !r.targetIncluded(relTarget) {
				return nil, true, &diagnostic{message: ignoredDirMessage(relTarget, projectRule, ".gitignore", hasIncludes, colors), isError: true}, nil
			}
			files, err := discoverFilesUnder(r.cfg.WorkingDir, absTarget, relTarget, r.matcher, r.classifyTextFile, &blockInfo{Rule: projectRule, Source: ".gitignore"}, r.withBinaries)
			return withTargetRoot(files, relTarget), true, nil, err
		}
		gitIgnored, err := r.gitIgnored(relTarget)
		if err != nil {
			return nil, true, nil, err
		}
		if gitIgnored {
			if !r.targetIncluded(relTarget) {
				return nil, true, &diagnostic{message: ignoredDirMessage(relTarget, ".gitignore", ".gitignore", hasIncludes, colors), isError: true}, nil
			}
			files, err := discoverFilesUnder(r.cfg.WorkingDir, absTarget, relTarget, r.matcher, r.classifyTextFile, &blockInfo{Rule: ".gitignore", Source: ".gitignore"}, r.withBinaries)
			return withTargetRoot(files, relTarget), true, nil, err
		}
		files, err := r.discoverVisibleFilesUnder(relTarget)
		return withTargetRoot(files, relTarget), true, nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, true, nil, nil
	}
	if !r.withBinaries && excludedTextLikeAsset(relTarget) {
		return nil, true, nil, nil
	}
	text, err := r.classifyTextFile(relTarget, absTarget)
	if err != nil {
		return nil, true, nil, err
	}
	if !text {
		return nil, true, nil, nil
	}
	entry := fileEntry{
		AbsPath:    absTarget,
		RelPath:    relTarget,
		ModTime:    info.ModTime(),
		GitVisible: true,
	}
	if dir := normalizeRelPath(path.Dir(relTarget)); dir != "." {
		entry.TargetRoot = dir
	}
	hasIncludes := len(r.includedTargets.exact) > 0
	if ignored, rule := r.matcher.fileIgnored(relTarget); ignored {
		if !r.targetIncluded(relTarget) {
			return nil, true, &diagnostic{message: ignoredFileMessage(relTarget, rule, ".hiss", fromChained, hasIncludes, colors), isError: true}, nil
		}
		entry = withAllowedByInclude(entry, blockInfo{Rule: rule, Source: ".hiss"})
	} else {
		projectIgnored, projectRule, err := r.projectFileIgnored(relTarget)
		if err != nil {
			return nil, true, nil, err
		}
		if projectIgnored {
			if !r.targetIncluded(relTarget) {
				return nil, true, &diagnostic{message: ignoredFileMessage(relTarget, projectRule, ".gitignore", fromChained, hasIncludes, colors), isError: true}, nil
			}
			entry = withAllowedByInclude(entry, blockInfo{Rule: projectRule, Source: ".gitignore"})
		} else {
			gitIgnored, err := r.gitIgnored(relTarget)
			if err != nil {
				return nil, true, nil, err
			}
			if gitIgnored {
				if !r.targetIncluded(relTarget) {
					return nil, true, &diagnostic{message: ignoredFileMessage(relTarget, ".gitignore", ".gitignore", fromChained, hasIncludes, colors), isError: true}, nil
				}
				entry = withAllowedByInclude(entry, blockInfo{Rule: ".gitignore", Source: ".gitignore"})
			}
		}
	}
	return []fileEntry{entry}, true, nil, nil
}

func (r *scopeResolver) gitIgnored(relPath string) (bool, error) {
	if relPath == "." || relPath == "" {
		return false, nil
	}
	if !r.useGitIgnore {
		return false, nil
	}
	lines, err := runGitLines(r.gitCtx.Root, []string{r.gitCtx.toRepoPath(relPath)}, "check-ignore", "--stdin")
	if err != nil {
		return false, err
	}
	return len(lines) > 0, nil
}

func (r *scopeResolver) classifyTextFile(relPath, absPath string) (bool, error) {
	if r.withBinaries {
		return true, nil
	}
	if knownTextLikeFile(relPath) {
		return true, nil
	}
	if absPath == "" {
		absPath = filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(relPath))
	}
	absPath = filepath.Clean(absPath)
	if r.textFileCache == nil {
		r.textFileCache = make(map[string]bool)
	}
	if text, ok := r.textFileCache[absPath]; ok {
		return text, nil
	}
	text, err := isProbablyTextFile(absPath)
	if err != nil {
		return false, err
	}
	r.textFileCache[absPath] = text
	return text, nil
}

func (r *scopeResolver) blockInfoForDir(relPath string) (*blockInfo, error) {
	if relPath == "." || relPath == "" {
		return nil, nil
	}
	if ignored, rule := r.matcher.dirIgnored(relPath); ignored {
		return &blockInfo{Rule: rule, Source: ".hiss"}, nil
	}
	if ignored, rule, err := r.projectDirIgnored(relPath); err != nil {
		return nil, err
	} else if ignored {
		return &blockInfo{Rule: rule, Source: ".gitignore"}, nil
	}
	gitIgnored, err := r.gitIgnored(relPath)
	if err != nil {
		return nil, err
	}
	if gitIgnored {
		return &blockInfo{Rule: ".gitignore", Source: ".gitignore"}, nil
	}
	return nil, nil
}

func (r *scopeResolver) resolveChainedDir(relPath string, stderr io.Writer, colors colorPalette) (string, error) {
	currentAbs := r.cfg.WorkingDir
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
			currentAbs = filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(currentRel))
			continue
		}

		matches, err := r.fuzzySearchDirs(currentRel, seg)
		if err != nil {
			return "", err
		}
		switch len(matches) {
		case 0:
			return "", fmt.Errorf("Error: No directory matching %s found in %s.\n  Check the spelling, or use --hiss to see if it's excluded.", singleQuoted(seg), currentRel)
		case 1:
			currentRel = matches[0]
		default:
			selected, err := chooseDirectoryMatch(r.cfg, seg, currentRel, matches, stderr, colors)
			if err != nil {
				return "", err
			}
			if len(selected) == 0 {
				return "", errSelectionCancelled
			}
			currentRel = selected[0]
		}
		currentAbs = filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(currentRel))
	}

	return currentRel, nil
}

func (r *scopeResolver) resolveChainedDirWithoutPrompt(relPath string) (string, bool, error) {
	currentAbs := r.cfg.WorkingDir
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
			currentAbs = filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(currentRel))
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
		currentAbs = filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(currentRel))
	}

	return currentRel, true, nil
}

func (r *scopeResolver) targetNeedsInclude(target string) (bool, error) {
	normalizedTarget := normalizeRelPath(target)
	if normalizedTarget == "" || normalizedTarget == "." {
		return false, nil
	}
	_, handled, diag, err := r.resolveExactTarget(normalizedTarget, false, colorPalette{})
	if err != nil {
		return false, err
	}
	return handled && diag != nil && diag.isError, nil
}

func (r *scopeResolver) resolveVisibleDirByExactBasename(baseRel, basename string) (string, bool, error) {
	if basename == "" || basename == "." {
		return "", false, nil
	}
	if err := r.buildVisibleDirIndex(); err != nil {
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
	for _, rel := range r.visibleDirs.dirs {
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

func (r *scopeResolver) hasVisibleFileBasenameConflict(baseRel, needle string) (bool, error) {
	if needle == "" || needle == "." {
		return false, nil
	}
	if err := r.buildVisibleFileList(); err != nil {
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

	for _, entry := range r.visibleFileList {
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

func (r *scopeResolver) chooseRootTargetMatches(query, prompt string, includeCopyAll bool, selectedPaths []string) ([]targetMatch, error) {
	query = normalizeInteractivePickerQuery(query)
	if selectionContainsAll(selectedPaths) {
		return nil, errSelectionCancelled
	}
	stopSpinner := func() {}
	if !r.interactiveTargetsOk {
		stopSpinner = startLoadingSpinner(os.Stderr, "Loading targets...")
	}
	allTargets, err := r.allVisibleTargets()
	stopSpinner()
	if err != nil {
		return nil, err
	}
	options := make([]targetMatch, 0, len(allTargets))
	for _, target := range allTargets {
		if coveredBySelection(target.Path, selectedPaths) {
			continue
		}
		options = append(options, target)
	}
	if includeCopyAll {
		options = append([]targetMatch{{Path: ".", Kind: "all"}}, options...)
	}
	if len(options) == 0 {
		return nil, errSelectionCancelled
	}
	if match, ok := exactInteractiveTargetMatch(options, query); ok {
		return []targetMatch{match}, nil
	}

	path, err := fuzzyResolverBinary()
	if err != nil {
		return nil, err
	}

	labels, index := targetMatchLabels(options)
	selectedLabels, err := chooseManyTargetMatchesWithFzfHeader(path, query, prompt, targetPickerHeader(prompt), labels, false)
	if err != nil {
		return nil, err
	}

	selected := make([]targetMatch, 0, len(selectedLabels))
	for _, key := range selectedLabels {
		match, ok := index[key]
		if ok {
			if match.Kind == "all" {
				return []targetMatch{match}, nil
			}
			selected = append(selected, match)
		}
	}
	if len(selected) == 0 {
		return nil, errSelectionCancelled
	}
	return selected, nil
}

func (r *scopeResolver) chooseIgnoredTargetMatches(query, prompt string, selectedPaths, explicitTargets, scopeTargets []string) ([]targetMatch, int, error) {
	query = normalizeInteractivePickerQuery(query)
	stopSpinner := func() {}
	if !r.ignoredTargetsOk {
		stopSpinner = startLoadingSpinner(os.Stderr, "Loading ignored targets...")
	}
	allTargets, err := r.allIgnoredTargets()
	stopSpinner()
	if err != nil {
		return nil, 0, err
	}
	allTargets = filterIgnoredTargetsByScopeTargets(allTargets, scopeTargets)
	if len(allTargets) == 0 && len(scopeTargets) > 0 {
		return nil, 0, errNoScopedIgnoredTargets{ScopeTargets: scopeTargets}
	}
	options := filterRedundantTargetMatches(allTargets, selectionPathsForIgnoredTargets(selectedPaths))
	options = filterAuthorizationOnlyIncludeMatches(options, explicitTargets)
	totalOptions := len(options)
	if totalOptions == 0 {
		return nil, 0, errSelectionCancelled
	}
	if match, ok := exactTargetPathMatch(options, query); ok {
		return []targetMatch{match}, totalOptions, nil
	}

	path, err := fuzzyResolverBinary()
	if err != nil {
		return nil, 0, err
	}
	labels, index := targetMatchLabels(options)
	selectedLabels, err := chooseManyTargetMatchesWithFzfHeader(path, query, prompt, ignoredTargetPickerHeader(), labels, true)
	if err != nil {
		return nil, 0, err
	}

	selected := make([]targetMatch, 0, len(selectedLabels))
	for _, key := range selectedLabels {
		match, ok := index[key]
		if ok {
			selected = append(selected, match)
		}
	}
	if len(selected) == 0 {
		return nil, 0, errSelectionCancelled
	}
	return selected, totalOptions, nil
}

func selectionPathsForIgnoredTargets(selectedPaths []string) []string {
	filtered := make([]string, 0, len(selectedPaths))
	for _, selected := range selectedPaths {
		if normalizeRelPath(selected) == "." {
			continue
		}
		filtered = append(filtered, selected)
	}
	return filtered
}

func (r *scopeResolver) resolveInteractiveIncludeTargets(query string, selectedPaths, explicitTargets, scopeTargets []string) ([]string, error) {
	matches, totalOptions, err := r.chooseIgnoredTargetMatches(query, "include> ", selectedPaths, explicitTargets, scopeTargets)
	if err != nil {
		return nil, err
	}
	if totalOptions > 0 && len(matches) == totalOptions {
		return []string{"*"}, nil
	}
	return targetMatchPaths(matches), nil
}

func (r *scopeResolver) resolveExactIgnoredIncludeTarget(query string, scopeTargets []string) (string, bool, error) {
	options, err := r.allIgnoredTargets()
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

func (r *scopeResolver) resolveExactIgnoredIncludeTargets(queries []string, scopeTargets []string) ([]string, []string, error) {
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
	return dedupePreserveOrder(exact), remaining, nil
}

// filterIgnoredTargetsByScopeTargets filters ignored targets to only those
// that fall under any scope target OR are ancestors of any scope target.
// Ancestors are included because --include authorizes discovery of an ignored
// directory, which may contain the scope target itself. If any scope target is
// "." (root), all targets are returned.
func filterIgnoredTargetsByScopeTargets(targets []targetMatch, scopeTargets []string) []targetMatch {
	if len(scopeTargets) == 0 {
		return targets
	}
	for _, st := range scopeTargets {
		if normalizeRelPath(st) == "." || normalizeRelPath(st) == "" {
			return targets
		}
	}

	out := make([]targetMatch, 0, len(targets))
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

func targetMatchPaths(matches []targetMatch) []string {
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		if match.Kind == "done" {
			continue
		}
		paths = append(paths, match.Path)
	}
	return paths
}

func exactInteractiveTargetMatch(options []targetMatch, query string) (targetMatch, bool) {
	if !shouldAutoAcceptInteractiveQuery(query) {
		return targetMatch{}, false
	}
	return exactTargetPathMatch(options, query)
}

func exactTargetPathMatch(options []targetMatch, query string) (targetMatch, bool) {
	trimmed := strings.TrimSuffix(query, "/")
	want := normalizeRelPath(trimmed)
	if want == "" || want == "." {
		return targetMatch{}, false
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
	return targetMatch{}, false
}

func shouldAutoAcceptInteractiveQuery(query string) bool {
	trimmed := strings.TrimSuffix(query, "/")
	if trimmed == "" || trimmed == "." {
		return false
	}
	return strings.Contains(trimmed, "/")
}

func normalizeInteractivePickerQuery(query string) string {
	if strings.TrimSpace(query) == "*" {
		return ""
	}
	return query
}

func (r *scopeResolver) interactiveQueryCoveredBySelection(query string, selectedPaths []string) (bool, error) {
	query = normalizeInteractivePickerQuery(query)
	if query == "" || len(selectedPaths) == 0 {
		return false, nil
	}
	if selectionContainsAll(selectedPaths) {
		return true, nil
	}

	normalized := normalizeRelPath(query)
	if normalized != "" && normalized != "." {
		exists, err := r.targetPathExists(normalized)
		if err != nil {
			return false, err
		}
		if exists && coveredBySelection(normalized, selectedPaths) {
			return true, nil
		}
	}
	if strings.Contains(normalized, "/") {
		return false, nil
	}

	sawMatch := false

	if err := r.buildVisibleDirIndex(); err != nil {
		return false, err
	}
	for _, rel := range r.visibleDirs.dirs {
		if path.Base(rel) != normalized {
			continue
		}
		sawMatch = true
		if !coveredBySelection(rel, selectedPaths) {
			return false, nil
		}
	}

	if err := r.buildVisibleFileList(); err != nil {
		return false, err
	}
	for _, entry := range r.visibleFileList {
		base := path.Base(entry.RelPath)
		if base != normalized && strings.TrimSuffix(base, path.Ext(base)) != normalized {
			continue
		}
		sawMatch = true
		if !coveredBySelection(entry.RelPath, selectedPaths) {
			return false, nil
		}
	}
	return sawMatch, nil
}

func filterRedundantTargetMatches(candidates []targetMatch, selectedPaths []string) []targetMatch {
	if len(selectedPaths) == 0 {
		return candidates
	}
	filtered := make([]targetMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if coveredBySelection(candidate.Path, selectedPaths) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func filterAuthorizationOnlyIncludeMatches(candidates []targetMatch, explicitTargets []string) []targetMatch {
	if len(explicitTargets) == 0 {
		return candidates
	}
	filtered := make([]targetMatch, 0, len(candidates))
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

func coveredBySelection(path string, selectedPaths []string) bool {
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

func selectionContainsAll(selectedPaths []string) bool {
	for _, selected := range selectedPaths {
		if normalizeRelPath(selected) == "." {
			return true
		}
	}
	return false
}

func (r *scopeResolver) allVisibleTargets() ([]targetMatch, error) {
	if r.interactiveTargetsOk {
		return append([]targetMatch(nil), r.interactiveTargets...), nil
	}
	if err := r.buildVisibleDirIndex(); err != nil {
		return nil, err
	}
	if err := r.buildVisibleFileList(); err != nil {
		return nil, err
	}

	targets := make([]targetMatch, 0, len(r.visibleDirs.dirs)+len(r.visibleFileList))
	for _, rel := range r.visibleDirs.dirs {
		targets = append(targets, targetMatch{Path: rel, Kind: "dir", State: treeTargetStateOK})
	}
	for _, entry := range r.visibleFileList {
		targets = append(targets, targetMatch{Path: entry.RelPath, Kind: "file", State: treeTargetStateText})
	}

	r.interactiveTargets = targets
	r.interactiveTargetsOk = true
	return append([]targetMatch(nil), targets...), nil
}

func (r *scopeResolver) allIgnoredTargets() ([]targetMatch, error) {
	if r.ignoredTargetsOk {
		return append([]targetMatch(nil), r.ignoredTargets...), nil
	}
	if err := r.ensureProjectIgnoreMatcher(); err != nil {
		return nil, err
	}

	rgPaths, err := runRipgrepFiles(r.cfg.WorkingDir, ripgrepFileOptions{NoIgnore: true})
	if err != nil {
		return nil, err
	}

	dirSet, err := collectAllDirPaths(r.cfg.WorkingDir)
	if err != nil {
		return nil, err
	}
	filePaths := make([]string, 0, len(rgPaths))
	dirHasChild := make(map[string]bool, len(dirSet))
	dirHasText := make(map[string]bool, len(dirSet))
	for _, rel := range rgPaths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." {
			continue
		}
		parent := normalizeRelPath(path.Dir(rel))
		if parent != "" && parent != "." {
			dirHasChild[parent] = true
		}

		if !r.withBinaries && excludedTextLikeAsset(rel) {
			continue
		}
		text, err := r.classifyTextFile(rel, "")
		if err != nil {
			return nil, err
		}
		if text {
			filePaths = append(filePaths, rel)
			for d := parent; d != "" && d != "."; d = normalizeRelPath(path.Dir(d)) {
				dirHasText[d] = true
			}
		}
	}

	dirPaths := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirPaths = append(dirPaths, d)
		if parent := normalizeRelPath(path.Dir(d)); parent != "" && parent != "." {
			dirHasChild[parent] = true
		}
	}
	sort.Strings(dirPaths)

	relPaths := make([]string, 0, len(dirPaths)+len(filePaths))
	relPaths = append(relPaths, dirPaths...)
	relPaths = append(relPaths, filePaths...)

	gitIgnored := map[string]gitIgnoreMatch{}
	if r.useGitIgnore {
		gitIgnored, err = collectGitIgnoreMatchesForRelPaths(r.gitCtx, relPaths)
		if err != nil {
			return nil, err
		}
	}

	targets := make([]targetMatch, 0, len(dirPaths)+len(filePaths))
	for _, rel := range dirPaths {
		match := targetMatch{Path: rel, Kind: "dir", State: treeTargetStateOK}
		if ignored, _ := r.matcher.dirIgnored(rel); ignored {
			match.Ignored = true
			match.IgnoreSource = ".hiss"
		} else if _, ok := gitIgnored[rel]; ok {
			match.Ignored = true
			match.IgnoreSource = ".gitignore"
		} else if ignored, _, err := r.projectDirIgnored(rel); err != nil {
			return nil, err
		} else if ignored {
			match.Ignored = true
			match.IgnoreSource = ".gitignore"
		}
		if !dirHasChild[rel] {
			match.State = treeTargetStateEmpty
		} else if !dirHasText[rel] {
			match.State = treeTargetStateNoTextChildren
		}
		if match.Ignored {
			targets = append(targets, match)
		}
	}
	for _, rel := range filePaths {
		match := targetMatch{Path: rel, Kind: "file", State: treeTargetStateText}
		if ignored, _ := r.matcher.fileIgnored(rel); ignored {
			match.Ignored = true
			match.IgnoreSource = ".hiss"
		} else if _, ok := gitIgnored[rel]; ok {
			match.Ignored = true
			match.IgnoreSource = ".gitignore"
		} else if ignored, _, err := r.projectFileIgnored(rel); err != nil {
			return nil, err
		} else if ignored {
			match.Ignored = true
			match.IgnoreSource = ".gitignore"
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
	return append([]targetMatch(nil), targets...), nil
}

func collectAllDirPaths(root string) (map[string]struct{}, error) {
	dirs := make(map[string]struct{}, 256)
	var walk func(absDir, relDir string) error
	walk = func(absDir, relDir string) error {
		entries, err := os.ReadDir(absDir)
		if err != nil {
			return nil
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := entry.Name()
			childRel := name
			if relDir != "" {
				childRel = relDir + "/" + name
			}
			dirs[childRel] = struct{}{}
			if err := walk(filepath.Join(absDir, name), childRel); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	return dirs, nil
}

func (r *scopeResolver) resolveTargetMatches(matches []targetMatch, colors colorPalette) ([]fileEntry, error) {
	entries := make([]fileEntry, 0, len(matches))
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
	return dedupeEntriesByPath(entries), nil
}

func (r *scopeResolver) dirVisible(relPath string) (bool, error) {
	if relPath == "." || relPath == "" {
		return true, nil
	}
	if ignored, _ := r.matcher.dirIgnored(relPath); ignored {
		return false, nil
	}
	if ignored, _, err := r.projectDirIgnored(relPath); err != nil {
		return false, err
	} else if ignored {
		return false, nil
	}
	if !r.useGitIgnore {
		return true, nil
	}
	if err := r.buildVisibleDirIndex(); err != nil {
		return false, err
	}
	_, ok := r.visibleDirs.set[relPath]
	return ok, nil
}

func (r *scopeResolver) buildVisibleDirIndex() error {
	if r.visibleDirsReady {
		return nil
	}
	if err := r.buildVisibleFileList(); err != nil {
		return err
	}

	dirSet := make(map[string]struct{}, len(r.visibleFileList))
	for _, entry := range r.visibleFileList {
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

	r.visibleDirs = visibleDirIndex{
		dirs:        dirs,
		set:         make(map[string]struct{}, len(dirs)),
		symlinkDirs: nil,
	}
	for _, rel := range dirs {
		r.visibleDirs.set[rel] = struct{}{}
	}
	r.visibleDirsReady = true
	return nil
}

func (r *scopeResolver) buildVisibleFileIndex() error {
	if r.visibleFilesReady {
		return nil
	}
	if err := r.ensureProjectIgnoreMatcher(); err != nil {
		return err
	}
	if len(r.wantedBasenames) == 0 {
		r.visibleFiles = visibleFileIndex{
			byBase:        map[string][]fileEntry{},
			skippedByBase: map[string][]skippedMatch{},
		}
		r.visibleFilesReady = true
		return nil
	}

	paths, err := runRipgrepFiles(r.cfg.WorkingDir, ripgrepFileOptions{
		NoIgnore:  true,
		Basenames: sortedStringSet(r.wantedBasenames),
	})
	if err != nil {
		return err
	}
	candidates, err := r.textEntriesFromRipgrepPaths(paths, false)
	if err != nil {
		return err
	}

	gitIgnored := map[string]gitIgnoreMatch{}
	if r.useGitIgnore {
		gitIgnored, err = collectGitIgnoreMatches(r.gitCtx, candidates)
		if err != nil {
			return err
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].RelPath < candidates[j].RelPath
	})

	byBase := make(map[string][]fileEntry, len(candidates))
	skippedByBase := make(map[string][]skippedMatch, len(candidates))
	for _, entry := range candidates {
		base := path.Base(entry.RelPath)
		if ignored, rule := r.matcher.dirRuleBlockingFile(entry.RelPath); ignored {
			skippedByBase[base] = append(skippedByBase[base], skippedMatch{
				RelPath:     entry.RelPath,
				BlockRule:   rule,
				BlockSource: ".hiss",
				BlockKind:   "directory",
			})
			continue
		}
		if r.useProjectIgnore {
			if ignored, rule := r.projectIgnore.dirRuleBlockingFile(entry.RelPath); ignored {
				skippedByBase[base] = append(skippedByBase[base], skippedMatch{
					RelPath:     entry.RelPath,
					BlockRule:   rule,
					BlockSource: ".gitignore",
					BlockKind:   "directory",
				})
				continue
			}
		}
		if gitMatch, ok := gitIgnored[entry.RelPath]; ok && gitMatch.DirRule {
			skippedByBase[base] = append(skippedByBase[base], skippedMatch{
				RelPath:     entry.RelPath,
				BlockRule:   gitMatch.Rule,
				BlockSource: ".gitignore",
				BlockKind:   "directory",
			})
			continue
		}
		if ignored, rule := r.matcher.fileIgnoredByFileRule(entry.RelPath); ignored {
			skippedByBase[base] = append(skippedByBase[base], skippedMatch{
				RelPath:     entry.RelPath,
				BlockRule:   rule,
				BlockSource: ".hiss",
				BlockKind:   "file",
			})
			continue
		}
		if r.useProjectIgnore {
			if ignored, rule := r.projectIgnore.fileIgnoredByFileRule(entry.RelPath); ignored {
				skippedByBase[base] = append(skippedByBase[base], skippedMatch{
					RelPath:     entry.RelPath,
					BlockRule:   rule,
					BlockSource: ".gitignore",
					BlockKind:   "file",
				})
				continue
			}
		}
		if gitMatch, ok := gitIgnored[entry.RelPath]; ok {
			skippedByBase[base] = append(skippedByBase[base], skippedMatch{
				RelPath:     entry.RelPath,
				BlockRule:   gitMatch.Rule,
				BlockSource: ".gitignore",
				BlockKind:   "file",
			})
			continue
		}
		entry.GitVisible = true
		byBase[base] = append(byBase[base], entry)
	}

	r.visibleFiles = visibleFileIndex{
		byBase:        byBase,
		skippedByBase: skippedByBase,
	}
	r.visibleFilesReady = true
	return nil
}

func (r *scopeResolver) buildVisibleFileList() error {
	if r.visibleFileListReady {
		return nil
	}
	paths, err := runRipgrepFiles(r.cfg.WorkingDir, ripgrepFileOptions{NoIgnore: !r.useGitIgnore})
	if err != nil {
		return err
	}
	entries, err := r.textEntriesFromRipgrepPaths(paths, true)
	if err != nil {
		return err
	}
	entries = markEntriesGitVisible(entries)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RelPath < entries[j].RelPath
	})
	r.visibleFileList = entries
	r.visibleFileListReady = true
	return nil
}

func (r *scopeResolver) textEntriesFromRipgrepPaths(relPaths []string, applyIgnore bool) ([]fileEntry, error) {
	if err := r.ensureProjectIgnoreMatcher(); err != nil {
		return nil, err
	}
	entries := make([]fileEntry, 0, len(relPaths))
	for _, rel := range relPaths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." || coveredBySelection(rel, r.visibleDirs.symlinkDirs) {
			continue
		}
		if applyIgnore {
			if ignored, _ := r.matcher.fileIgnored(rel); ignored {
				continue
			}
			if r.useProjectIgnore {
				if ignored, _ := r.projectIgnore.fileIgnored(rel); ignored {
					continue
				}
			}
		}
		if !r.withBinaries && excludedTextLikeAsset(rel) {
			continue
		}

		text, err := r.classifyTextFile(rel, "")
		if err != nil {
			return nil, err
		}
		if !text {
			continue
		}

		entries = append(entries, fileEntry{RelPath: rel})
	}
	return entries, nil
}

func (r *scopeResolver) discoverVisibleFilesUnder(rootRel string) ([]fileEntry, error) {
	rootRel = normalizeRelPath(rootRel)
	opts := ripgrepFileOptions{
		NoIgnore: !r.useGitIgnore,
	}
	if rootRel != "." && rootRel != "" {
		opts.Paths = []string{rootRel}
	}
	paths, err := runRipgrepFiles(r.cfg.WorkingDir, opts)
	if err != nil {
		return nil, err
	}
	entries, err := r.textEntriesFromRipgrepPaths(paths, true)
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

func (r *scopeResolver) resolveVisibleFilesByBasename(baseRel, baseName string) ([]fileEntry, []skippedMatch, error) {
	if err := r.buildVisibleFileIndex(); err != nil {
		return nil, nil, err
	}

	candidates := ensureEntryAbsPaths(append([]fileEntry(nil), r.visibleFiles.byBase[baseName]...), r.cfg.WorkingDir)
	skipped := r.visibleFiles.skippedByBase[baseName]

	baseRel = normalizeRelPath(baseRel)
	if baseRel == "." || baseRel == "" {
		return candidates, append([]skippedMatch(nil), skipped...), nil
	}

	prefix := baseRel + "/"
	matches := make([]fileEntry, 0, len(candidates))
	for _, entry := range candidates {
		if strings.HasPrefix(entry.RelPath, prefix) {
			matches = append(matches, entry)
		}
	}
	blocked := make([]skippedMatch, 0, len(skipped))
	for _, match := range skipped {
		if strings.HasPrefix(match.RelPath, prefix) {
			blocked = append(blocked, match)
		}
	}
	return matches, blocked, nil
}

func (r *scopeResolver) lookupVisibleFilesByExactBasename(baseName string) ([]fileEntry, []skippedMatch, error) {
	clone := *r
	clone.wantedBasenames = map[string]struct{}{baseName: {}}
	clone.visibleFiles = visibleFileIndex{}
	clone.visibleFilesReady = false
	return clone.resolveVisibleFilesByBasename(".", baseName)
}

func collectGitIgnoredPaths(gitCtx gitContext, entries []fileEntry) (map[string]struct{}, error) {
	if !gitCtx.Enabled || len(entries) == 0 {
		return nil, nil
	}

	repoPaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		repoPaths = append(repoPaths, gitCtx.toRepoPath(entry.RelPath))
	}

	ignoredRepoPaths, err := runGitLines(gitCtx.Root, repoPaths, "check-ignore", "--stdin")
	if err != nil {
		return nil, err
	}
	ignored := make(map[string]struct{}, len(ignoredRepoPaths))
	for _, repoPath := range ignoredRepoPaths {
		workPath := gitCtx.toWorkPath(repoPath)
		if workPath == "" {
			workPath = normalizeRelPath(repoPath)
		}
		ignored[normalizeRelPath(workPath)] = struct{}{}
	}
	return ignored, nil
}

func collectGitIgnoreMatches(gitCtx gitContext, entries []fileEntry) (map[string]gitIgnoreMatch, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	relPaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		relPaths = append(relPaths, entry.RelPath)
	}
	return collectGitIgnoreMatchesForRelPaths(gitCtx, relPaths)
}

func collectGitIgnoreMatchesForRelPaths(gitCtx gitContext, relPaths []string) (map[string]gitIgnoreMatch, error) {
	if !gitCtx.Enabled || len(relPaths) == 0 {
		return nil, nil
	}

	repoPaths := make([]string, 0, len(relPaths))
	seen := make(map[string]struct{}, len(relPaths))
	for _, relPath := range relPaths {
		relPath = normalizeRelPath(relPath)
		if relPath == "" || relPath == "." {
			continue
		}
		repoPath := gitCtx.toRepoPath(relPath)
		if _, ok := seen[repoPath]; ok {
			continue
		}
		seen[repoPath] = struct{}{}
		repoPaths = append(repoPaths, repoPath)
	}
	if len(repoPaths) == 0 {
		return nil, nil
	}

	cmd := exec.Command("git", "check-ignore", "-v", "--stdin")
	cmd.Dir = gitCtx.Root
	cmd.Stdin = strings.NewReader(strings.Join(repoPaths, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, nil
	}

	matches := make(map[string]gitIgnoreMatch)
	for _, line := range strings.Split(text, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		meta := parts[0]
		repoPath := normalizeRelPath(parts[1])
		workPath := gitCtx.toWorkPath(repoPath)
		if workPath == "" {
			workPath = repoPath
		}

		metaParts := strings.SplitN(meta, ":", 3)
		if len(metaParts) != 3 {
			continue
		}
		rule := metaParts[2]
		matches[normalizeRelPath(workPath)] = gitIgnoreMatch{
			Rule:    rule,
			DirRule: strings.HasSuffix(rule, "/"),
		}
	}
	return matches, nil
}

func (r *scopeResolver) fuzzySearchDirs(baseRel, needle string) ([]string, error) {
	if err := r.buildVisibleDirIndex(); err != nil {
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
	for _, rel := range r.visibleDirs.dirs {
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			continue
		}
		matches = append(matches, rel)
	}
	return fuzzyFilterCandidates(needle, matches)
}

func (r *scopeResolver) fuzzySearchFiles(baseRel, needle string) ([]string, error) {
	if err := r.buildVisibleFileList(); err != nil {
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

	candidates := make([]string, 0, len(r.visibleFileList))
	for _, entry := range r.visibleFileList {
		if prefix != "" && !strings.HasPrefix(entry.RelPath, prefix) {
			continue
		}
		candidates = append(candidates, entry.RelPath)
	}
	return fuzzyFilterCandidates(needle, candidates)
}

func (r *scopeResolver) fuzzySearchFilesUnder(baseRel, needle string, rootBypass *blockInfo) ([]string, error) {
	if rootBypass == nil {
		return r.fuzzySearchFiles(baseRel, needle)
	}

	rootAbs := filepath.Join(r.cfg.WorkingDir, filepath.FromSlash(baseRel))
	entries, err := discoverFilesUnder(r.cfg.WorkingDir, rootAbs, baseRel, r.matcher, r.classifyTextFile, rootBypass, r.withBinaries)
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0, len(entries))
	for _, entry := range entries {
		candidates = append(candidates, entry.RelPath)
	}
	return fuzzyFilterCandidates(needle, candidates)
}

func chooseDirectoryMatch(cfg runConfig, needle, currentRel string, matches []string, stderr io.Writer, colors colorPalette) ([]string, error) {
	if !cfg.canPromptForChoice() {
		return nil, headlessDirectoryAmbiguityError(needle, currentRel, matches)
	}

	path, err := fuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	return chooseManyWithTypedFzf(path, needle, "dir> ", matches, treeTargetKindDir, treeTargetStateOK)
}

func chooseFileMatch(cfg runConfig, needle, currentRel string, matches []string, stderr io.Writer, colors colorPalette) ([]string, error) {
	if !cfg.canPromptForChoice() {
		return nil, headlessFileAmbiguityError(needle, currentRel, matches)
	}

	path, err := fuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	return chooseManyWithTypedFzf(path, needle, "file> ", matches, treeTargetKindFile, treeTargetStateText)
}

func chooseTargetMatch(cfg runConfig, needle string, matches []targetMatch, stderr io.Writer, colors colorPalette) ([]targetMatch, error) {
	if !cfg.canPromptForChoice() {
		return nil, headlessTargetAmbiguityError(needle, matches)
	}

	path, err := fuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	labels, index := targetMatchLabels(matches)
	selectedKeys, err := chooseManyTargetMatchesWithFzf(path, needle, "select> ", labels, false)
	if err != nil {
		return nil, err
	}
	selected := make([]targetMatch, 0, len(selectedKeys))
	for _, key := range selectedKeys {
		match, ok := index[key]
		if ok {
			selected = append(selected, match)
		}
	}
	if len(selected) == 0 {
		return nil, errSelectionCancelled
	}
	return selected, nil
}

const headlessCandidateListLimit = 10

func formatHeadlessCandidateList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	limit := len(items)
	if limit > headlessCandidateListLimit {
		limit = headlessCandidateListLimit
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
		fmt.Fprintf(&b, "Error: Multiple directories match %s in headless mode (--headless).", singleQuoted(needle))
	} else {
		fmt.Fprintf(&b, "Error: Multiple directories match %s in %s in headless mode (--headless).", singleQuoted(needle), currentRel)
	}
	b.WriteString(formatHeadlessCandidateList(matches))
	b.WriteString("\n  Use a more specific path segment to disambiguate.")
	return errors.New(b.String())
}

func headlessFileAmbiguityError(needle, currentRel string, matches []string) error {
	var b strings.Builder
	if currentRel == "." {
		fmt.Fprintf(&b, "Error: Multiple files match %s in headless mode (--headless).", singleQuoted(needle))
	} else {
		fmt.Fprintf(&b, "Error: Multiple files match %s in %s in headless mode (--headless).", singleQuoted(needle), currentRel)
	}
	b.WriteString(formatHeadlessCandidateList(matches))
	b.WriteString("\n  Use a more specific name or path to disambiguate.")
	return errors.New(b.String())
}

func headlessTargetAmbiguityError(needle string, matches []targetMatch) error {
	items := make([]string, 0, len(matches))
	for _, match := range matches {
		items = append(items, fmt.Sprintf("[%s] %s", match.Kind, match.Path))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Error: Multiple files and directories match %s in headless mode (--headless).", singleQuoted(needle))
	b.WriteString(formatHeadlessCandidateList(items))
	b.WriteString("\n  Use a more specific name or path to disambiguate.")
	return errors.New(b.String())
}

func fzfBinary() (string, bool) {
	return bundledToolBinary("CATCLIP_FZF", "fzf")
}

func treePreviewBinary() (string, bool) {
	return companionBinary("CATCLIP_TREE", "catclip-tree")
}

func fuzzyResolverBinary() (string, error) {
	path, ok := fzfBinary()
	if ok {
		return path, nil
	}
	return "", fmt.Errorf("Error: this catclip install is missing bundled fzf.\n  Reinstall catclip with its packaged tools; runtime does not fall back to arbitrary PATH copies.")
}

func fuzzyFilterCandidates(query string, candidates []string) ([]string, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	path, err := fuzzyResolverBinary()
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

func chooseWithFzf(bin, query, prompt string, candidates []string, kind, state string) (string, error) {
	return chooseWithFzfLines(bin, query, prompt, "1,2", fzfPreviewCommand(false), formatFzfCandidates(candidates, kind, state))
}

func chooseSingleFzfLine(query, prompt, withNth string, lines []string) (string, error) {
	bin, err := fuzzyResolverBinary()
	if err != nil {
		return "", err
	}
	return chooseWithFzfLines(bin, query, prompt, withNth, "", lines)
}

func chooseTargetWithFzf(bin, query, prompt string, candidates []string, includeTarget bool) (string, error) {
	return chooseWithFzfLines(bin, query, prompt, "1", fzfPreviewCommand(includeTarget), candidates)
}

func chooseWithFzfLines(bin, query, prompt, withNth, previewCommand string, lines []string) (string, error) {
	stopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          query,
		Prompt:         prompt,
		WithNth:        withNth,
		PreviewCommand: previewCommand,
		Lines:          lines,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return "", errSelectionCancelled
	}
	if err != nil {
		return "", err
	}
	if len(result.Matches) == 0 {
		return "", errSelectionCancelled
	}
	return result.Matches[0], nil
}

func chooseManyWithFzf(bin, query, prompt string, candidates []string) ([]string, error) {
	return chooseManyWithFzfNth(bin, query, prompt, "1,2", candidates)
}

func chooseManyFilePathsWithFzf(query, prompt, header string, candidates []string) ([]string, error) {
	bin, err := fuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1,2", header, fzfPreviewCommand(false), formatFzfCandidates(candidates, treeTargetKindFile, treeTargetStateText))
}

func fzfFileSetPreviewCommand(currentArgs []string, previewFlag string) string {
	treeBin, ok := treePreviewBinary()
	if !ok {
		return ""
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{shellQuoteArg(self), "--quiet", "--internal-tree-payload"}
	for _, arg := range currentArgs {
		parts = append(parts, shellQuoteArg(arg))
	}
	if previewFlag != "" {
		parts = append(parts, previewFlag, "{+2}")
	}
	parts = append(parts,
		"--internal-tree-target", "{3}",
		"--internal-tree-kind", "{4}",
		"--internal-tree-state", "{5}",
	)
	parts = append(parts, "|", shellQuoteArg(treeBin))
	parts = append(parts, fzfTreeRenderArgs()...)
	return strings.Join(parts, " ")
}

func fzfDiffFilePreviewCommand(currentArgs []string) string {
	treeBin, ok := treePreviewBinary()
	if !ok {
		return ""
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{shellQuoteArg(self), "--quiet", "--internal-file-preview", "--internal-file-path", "{3}"}
	for _, arg := range currentArgs {
		parts = append(parts, shellQuoteArg(arg))
	}
	parts = append(parts, "--only", "{+2}")
	parts = append(parts, "|", shellQuoteArg(treeBin))
	parts = append(parts, fzfTreeRenderArgs()...)
	return strings.Join(parts, " ")
}

func chooseContentMatchesWithFzf(query string, currentArgs []string, flag string) (fzfChooseResult, error) {
	bin, err := fuzzyResolverBinary()
	if err != nil {
		return fzfChooseResult{}, err
	}

	command := fzfContentMatchListCommand(currentArgs, flag)
	if command == "" {
		return fzfChooseResult{}, errSelectionCancelled
	}

	stopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          query,
		Prompt:         "match> ",
		WithNth:        "1",
		Nth:            "1",
		Header:         contentMatchPickerHeader(flag),
		PreviewCommand: fzfContentPreviewCommand(flag),
		Disabled:       true,
		Multi:          true,
		PrintQuery:     true,
		Bindings:       append([]string{"start:reload:" + command, "change:reload:" + command}, multiSelectPickerBindings()...),
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return fzfChooseResult{}, errSelectionCancelled
	}
	if err != nil {
		return fzfChooseResult{}, err
	}
	if strings.TrimSpace(result.Query) == "" && result.Key == "" && len(result.Matches) == 0 {
		return fzfChooseResult{}, errSelectionCancelled
	}
	return fzfChooseResult{Query: result.Query, Key: result.Key, Matches: result.Matches}, nil
}

func chooseManyWithTypedFzf(bin, query, prompt string, candidates []string, kind, state string) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1,2", "", fzfPreviewCommand(false), formatFzfCandidates(candidates, kind, state))
}

func chooseManyWithFzfNth(bin, query, prompt, nth string, candidates []string) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, nth, "1,2", "", fzfPreviewCommand(false), formatFzfCandidates(candidates, "", ""))
}

func chooseManyTargetMatchesWithFzfHeader(bin, query, prompt, header string, candidates []string, includeTarget bool) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1", header, fzfPreviewCommand(includeTarget), candidates)
}

func chooseManyTargetMatchesWithFzf(bin, query, prompt string, candidates []string, includeTarget bool) ([]string, error) {
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1", "", fzfPreviewCommand(includeTarget), candidates)
}

type fzfChooseResult struct {
	Query   string
	Key     string
	Matches []string
}

func chooseManyWithFzfOptions(bin, query, prompt, nth, withNth, header, previewCommand string, candidates []string) ([]string, error) {
	stopActiveSpinner()
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          query,
		Prompt:         prompt,
		WithNth:        withNth,
		Nth:            nth,
		Header:         header,
		PreviewCommand: previewCommand,
		Multi:          true,
		Bindings:       multiSelectPickerBindings(),
		Lines:          candidates,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return nil, errSelectionCancelled
	}
	if err != nil {
		return nil, err
	}
	if len(result.Matches) == 0 {
		return nil, errSelectionCancelled
	}
	return result.Matches, nil
}

func fzfPreviewCommand(includeTarget bool) string {
	treeBin, ok := treePreviewBinary()
	if !ok {
		return ""
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	selfQ := shellQuoteArg(self)
	treeQ := shellQuoteArg(treeBin)
	treeArgs := strings.Join(fzfTreeRenderArgs(), " ")

	// {+2} passes all selected targets (falls back to focused when none selected).
	// {2}/{3}/{4} are the focused entry's metadata for tree highlight.
	if includeTarget {
		return selfQ + ` --quiet {+2} --internal-tree-payload` +
			` --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4}` +
			` --include {+2} | ` + treeQ + ` ` + treeArgs
	}
	return selfQ + ` --quiet --internal-tree-payload` +
		` --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4}` +
		` {+2} | ` + treeQ + ` ` + treeArgs
}

func fzfContentPreviewCommand(flag string) string {
	treeBin, ok := treePreviewBinary()
	if !ok {
		return ""
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{
		shellQuoteArg(self),
		"--quiet",
		"--internal-file-preview",
		"--internal-file-path", "{3}",
		// fzf already shell-quotes placeholders like {q}; adding our own quotes
		// breaks regex input that includes spaces or quote characters.
		flag, "{q}",
	}
	parts = append(parts, "|", shellQuoteArg(treeBin))
	parts = append(parts, fzfTreeRenderArgs()...)
	return strings.Join(parts, " ")
}

func fzfContentMatchListCommand(currentArgs []string, flag string) string {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	parts := []string{shellQuoteArg(self), "--quiet", "--internal-content-match-list"}
	for _, arg := range currentArgs {
		parts = append(parts, shellQuoteArg(arg))
	}
	// fzf already shell-quotes placeholders like {q}; adding our own quotes
	// breaks regex input that includes spaces or quote characters.
	parts = append(parts, flag, "{q}")
	return strings.Join(parts, " ")
}

func contentMatchPickerHeader(flag string) string {
	firstLine := "Keep files whose contents match a regex."
	if flag == "--snippet" {
		firstLine = "Extract snippets whose contents match a regex."
	}
	return pickerHeader(
		firstLine,
		"Type a regex.",
		fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  [Esc] cancel", multiSelectToggleAllKey()),
	)
}

func multiSelectPickerBindings() []string {
	return []string{
		"tab:toggle+down",
		"btab:toggle+up",
		multiSelectToggleAllBinding(),
		"multi:refresh-preview",
	}
}

func multiSelectToggleAllBinding() string {
	return multiSelectToggleAllBindingForGOOS(runtime.GOOS)
}

func multiSelectToggleAllBindingForGOOS(goos string) string {
	if goos == "darwin" {
		return "ctrl-a:toggle-all"
	}
	return "alt-a:toggle-all"
}

func multiSelectToggleAllKey() string {
	return multiSelectToggleAllKeyForGOOS(runtime.GOOS)
}

func multiSelectToggleAllKeyForGOOS(goos string) string {
	if goos == "darwin" {
		return "Ctrl-A"
	}
	return "Alt-A"
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\"'\\*?[]{}()$&;|<>") {
		return arg
	}
	return strconv.Quote(arg)
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

func rankTargetMatches(query string, dirs, files []string) ([]targetMatch, error) {
	matches := make([]targetMatch, 0, len(dirs)+len(files))
	for _, dir := range dirs {
		matches = append(matches, targetMatch{Path: dir, Kind: "dir"})
	}
	for _, file := range files {
		matches = append(matches, targetMatch{Path: file, Kind: "file"})
	}
	if len(matches) == 0 {
		return nil, nil
	}
	path, err := fuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	labels, index := targetMatchLabels(matches)
	filtered, err := runFzfFilterLines(path, query, labels)
	if err != nil {
		return nil, err
	}
	ranked := make([]targetMatch, 0, len(filtered))
	for _, key := range filtered {
		match, ok := index[key]
		if ok {
			ranked = append(ranked, match)
		}
	}
	return ranked, nil
}

func targetMatchLabels(matches []targetMatch) ([]string, map[string]targetMatch) {
	labels := make([]string, 0, len(matches))
	index := make(map[string]targetMatch, len(matches))
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
			targetMatchPreviewKind(match),
			targetMatchPreviewState(match),
		}, "\t"))
		index[match.Path] = match
	}
	return labels, index
}

func targetMatchPreviewKind(match targetMatch) string {
	switch match.Kind {
	case "all", treeTargetKindDir:
		return treeTargetKindDir
	case treeTargetKindFile:
		return treeTargetKindFile
	default:
		return normalizeTreeTargetKind(match.Kind)
	}
}

func targetMatchPreviewState(match targetMatch) string {
	if state := normalizeTreeTargetState(match.State); state != "" {
		return state
	}
	switch targetMatchPreviewKind(match) {
	case treeTargetKindDir:
		return treeTargetStateOK
	case treeTargetKindFile:
		return treeTargetStateText
	default:
		return ""
	}
}

func targetMatchKey(match targetMatch) string {
	return match.Kind + "\x00" + match.Path
}

func targetPickerHeader(prompt string) string {
	firstLine := "Pick files and folders to include."
	if prompt == "then> " {
		firstLine = "Add more files and folders."
	}
	return pickerHeader(
		firstLine,
		"Type to search by name.",
		"[Up/Down] move  [Enter] confirm  [Tab] mark  [Esc] cancel",
	)
}

func safeTargetPickerHeader() string {
	return targetPickerHeader("select> ")
}

func ignoredTargetPickerHeader() string {
	return pickerHeader(
		"Add files and folders ignored by .gitignore or .hiss.",
		"Type to search by name.",
		fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  [Esc] cancel", multiSelectToggleAllKey()),
	)
}

func pickerHeader(lines ...string) string {
	if len(lines) > 4 {
		lines = lines[:4]
	}
	for len(lines) < 4 {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func targetNotFoundWarning(target string, scopeIndex int, colors colorPalette) string {
	if strings.Contains(target, "/") {
		return fmt.Sprintf("%sWarning:%s Target %s not found (scope %d).\n\n  %sIf the parent directory is ignored, use --include to allow it first.%s\n  %sExample:%s %scatclip --include %s --only %s%s",
			colors.Warn, colors.Reset, singleQuoted(target), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, singleQuoted(path.Dir(target)), singleQuoted(path.Base(target)), colors.Reset)
	}
	if prefersDirectFileLookup(target) {
		return fmt.Sprintf("%sWarning:%s No file named %s found (scope %d).\n\n  %sDirect file targets use exact basenames first. Non-exact file shorthand is resolved by fzf across safe directories.%s\n\n  %sIf an ignored rule is hiding it, use --include to allow that blocked file or directory first.%s",
			colors.Warn, colors.Reset, singleQuoted(target), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset)
	}
	return fmt.Sprintf("%sWarning:%s No file or directory %s found (scope %d).\n\n  %sDirectory shorthand is resolved by fzf. File targets use exact basenames first, then fzf across safe directories.%s\n\n  %sIf the thing you want is ignored, use --include to browse blocked targets for this scope.%s",
		colors.Warn, colors.Reset, singleQuoted(target), scopeIndex+1,
		colors.Dim, colors.Reset,
		colors.Dim, colors.Reset)
}

func ignoredDirMessage(relTarget, rule, source string, includesActive bool, colors colorPalette) string {
	if includesActive {
		return fmt.Sprintf("\n%sError: %s is ignored by rule %s in %s%s\n\n  %sYour --include does not cover this target. Add it directly:%s\n  %sExample:%s %scatclip --include %s%s\n  %sTo remove permanently:%s   %scatclip --hiss%s %s(delete the rule)%s",
			colors.Err, singleQuoted(relTarget), singleQuoted(rule), source, colors.Reset,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset, colors.OK, singleQuoted(relTarget), colors.Reset,
			colors.Dim, colors.Reset, colors.OK, colors.Reset, colors.Dim, colors.Reset)
	}
	return fmt.Sprintf("\n%sError: %s is ignored by rule %s in %s%s\n\n  %sUse --include to allow it for this run.%s\n  %sExample:%s %scatclip --include %s%s\n  %sTo narrow inside it:%s   %scatclip --include %s --only \"*.ext\"%s\n  %sTo remove permanently:%s   %scatclip --hiss%s %s(delete the rule)%s",
		colors.Err, singleQuoted(relTarget), singleQuoted(rule), source, colors.Reset,
		colors.Dim, colors.Reset,
		colors.Dim, colors.Reset, colors.OK, singleQuoted(relTarget), colors.Reset,
		colors.Dim, colors.Reset, colors.OK, singleQuoted(relTarget), colors.Reset,
		colors.Dim, colors.Reset, colors.OK, colors.Reset, colors.Dim, colors.Reset)
}

func ignoredFileMessage(relTarget, rule, source string, fromChained, includesActive bool, colors colorPalette) string {
	if includesActive {
		message := fmt.Sprintf("\n%sError: %s is ignored by rule %s in %s%s\n\n  %sYour --include does not cover this target. Add it directly:%s\n  %sExample:%s %scatclip --include %s%s",
			colors.Err, singleQuoted(relTarget), singleQuoted(rule), source, colors.Reset,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset, colors.OK, singleQuoted(relTarget), colors.Reset)
		if fromChained {
			return message
		}
		return message + fmt.Sprintf("\n  %sTo remove permanently:%s   %scatclip --hiss%s %s(delete the rule)%s",
			colors.Dim, colors.Reset, colors.OK, colors.Reset, colors.Dim, colors.Reset)
	}
	message := fmt.Sprintf("\n%sError: %s is ignored by rule %s in %s%s\n\n  %sUse --include to allow it for this run.%s\n  %sExample:%s %scatclip --include %s%s",
		colors.Err, singleQuoted(relTarget), singleQuoted(rule), source, colors.Reset,
		colors.Dim, colors.Reset,
		colors.Dim, colors.Reset, colors.OK, singleQuoted(relTarget), colors.Reset)
	if fromChained {
		return message
	}
	return message + fmt.Sprintf("\n  %sTo remove permanently:%s   %scatclip --hiss%s %s(delete the rule)%s",
		colors.Dim, colors.Reset, colors.OK, colors.Reset, colors.Dim, colors.Reset)
}

func ignoredTargetNeedsIncludeMessage(resolvedPath, query string, colors colorPalette) string {
	if normalizeRelPath(query) == normalizeRelPath(resolvedPath) {
		return fmt.Sprintf("\n%sError: %s is ignored.%s\n\n  %sUse --include to allow it for this run.%s\n  %sExample:%s %scatclip --include %s%s",
			colors.Err, singleQuoted(resolvedPath), colors.Reset,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset, colors.OK, singleQuoted(resolvedPath), colors.Reset)
	}
	return fmt.Sprintf("\n%sError: %s only matches ignored targets.%s\n\n  %sUse --include to browse blocked files and directories for this scope.%s\n  %sExample:%s %scatclip --include %s%s",
		colors.Err, singleQuoted(query), colors.Reset,
		colors.Dim, colors.Reset,
		colors.Dim, colors.Reset, colors.OK, singleQuoted(query), colors.Reset)
}

func includeQueryNeedsSelectionMessage(query string, colors colorPalette) string {
	return fmt.Sprintf("\n%sError: %s needs an ignored-target selection.%s\n\n  %sUse --include with an exact ignored path, or run it in a TTY so catclip can open the ignored picker.%s",
		colors.Err, singleQuoted(query), colors.Reset,
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

func withAllowedByInclude(entry fileEntry, block blockInfo) fileEntry {
	entry.AllowedByInclude = true
	entry.BlockRule = block.Rule
	entry.BlockSource = block.Source
	return entry
}

func withTargetRoot(entries []fileEntry, targetRoot string) []fileEntry {
	targetRoot = normalizeRelPath(targetRoot)
	if targetRoot == "." || targetRoot == "" {
		return entries
	}
	for i := range entries {
		entries[i].TargetRoot = targetRoot
	}
	return entries
}

func markEntriesGitVisible(entries []fileEntry) []fileEntry {
	for i := range entries {
		entries[i].GitVisible = true
	}
	return entries
}

func collectWantedBasenames(targets []string) map[string]struct{} {
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

func formatSkippedMatchesWarning(matches []skippedMatch) []string {
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
		rule := match.BlockRule
		if rule == "" {
			rule = match.BlockSource
		}
		lines = append(lines, fmt.Sprintf("  %s  [%s]", match.RelPath, rule))
	}
	return []string{strings.Join(lines, "\n")}
}

func singleQuoted(value string) string {
	return "'" + value + "'"
}

func writeNoFilesMatchedMessage(cfg runConfig, stderr io.Writer, colors colorPalette, hadSelectionCancel bool) error {
	if hadSelectionCancel {
		return nil
	}

	anyChanged := false
	hasStaged := false
	hasUnstaged := false
	hasUntracked := false
	for _, scopeSpec := range configCommandScopes(cfg) {
		s := executionScopeFromCommandScopeSpec(scopeSpec)
		anyChanged = anyChanged || executionScopeHasGitSelection(s)
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

	if _, err := fmt.Fprintf(stderr, "%sNo text files found matching your criteria.%s\n", colors.Warn, colors.Reset); err != nil {
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
	if _, err := fmt.Fprintf(stderr, "\n  %sTry: catclip --hiss                        # view/edit ignore rules%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stderr, "  %s     catclip --include blocked-dir        # browse blocked dirs/files for this run%s\n", colors.Dim, colors.Reset)
	return err
}
