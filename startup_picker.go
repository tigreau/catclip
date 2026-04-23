package catclip

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/picker"
)

type startupModifierMode string

const (
	startupModifierModeFlags    startupModifierMode = "flags"
	startupModifierModeThen     startupModifierMode = "then"
	startupModifierModeInclude  startupModifierMode = "include"
	startupModifierModeOnly     startupModifierMode = "only"
	startupModifierModeExclude  startupModifierMode = "exclude"
	startupModifierModeRecent   startupModifierMode = "recent"
	startupModifierModeDepth    startupModifierMode = "depth"
	startupModifierModeContains startupModifierMode = "contains"
	startupModifierModeSnippet  startupModifierMode = "snippet"
	startupModifierModeGit      startupModifierMode = "git"
)

type startupTrailingAction string

const (
	startupTrailingActionNone         startupTrailingAction = ""
	startupTrailingActionModifierMenu startupTrailingAction = "modifier-menu"
	startupTrailingActionOnly         startupTrailingAction = "only"
	startupTrailingActionExclude      startupTrailingAction = "exclude"
	startupTrailingActionRecent       startupTrailingAction = "recent"
	startupTrailingActionContains     startupTrailingAction = "contains"
	startupTrailingActionSnippet      startupTrailingAction = "snippet"
)

type startupFileSetRowKind string

const (
	startupFileSetRowAll              startupFileSetRowKind = "all"
	startupFileSetRowFile             startupFileSetRowKind = "file"
	startupFileSetRowExtensionPattern startupFileSetRowKind = "extension-pattern"
)

type startupFileSetRow struct {
	Kind          startupFileSetRowKind
	Display       string
	Value         string
	PreviewTarget string
	PreviewKind   string
	PreviewState  string
}

type startupModifierChoice struct {
	Key         string
	Label       string
	Description string
	Args        []string
	Mode        startupModifierMode
}

type startupPickerResult struct {
	Args    []string
	UsedFzf bool
}

type startupCurrentScopeState struct {
	Known                   bool
	Empty                   bool
	NeedsInclude            bool
	HasScopedIgnoredTargets bool
	GitKnown                bool
	AnyChanged   bool
	AllChanged   bool
	AnyStaged    bool
	AllStaged    bool
	AnyUnstaged  bool
	AllUnstaged  bool
	AnyUntracked bool
	AllUntracked bool
	MaxDepth     int
	Config       runConfig
}

var startupModifierChoices = []startupModifierChoice{
	{
		Key:         "only",
		Label:       "--only",
		Description: "Keep only matching files",
		Args:        []string{"--only"},
		Mode:        startupModifierModeOnly,
	},
	{
		Key:         "exclude",
		Label:       "--exclude",
		Description: "Remove matching files",
		Args:        []string{"--exclude"},
		Mode:        startupModifierModeExclude,
	},
	{
		Key:         "recent",
		Label:       "--recent",
		Description: "Keep the most recently modified files",
		Args:        []string{"--recent"},
		Mode:        startupModifierModeRecent,
	},
	{
		Key:         "depth",
		Label:       "--depth",
		Description: "Keep only files up to a path depth",
		Args:        []string{"--depth"},
		Mode:        startupModifierModeDepth,
	},
	{
		Key:         "paths",
		Label:       "--paths",
		Description: "Emit bare relative paths for this scope",
		Args:        []string{"--paths"},
		Mode:        startupModifierModeFlags,
	},
	{
		Key:         "contains",
		Label:       "--contains",
		Description: "Keep files whose contents match a regex",
		Args:        []string{"--contains"},
		Mode:        startupModifierModeContains,
	},
	{
		Key:         "snippet",
		Label:       "--snippet",
		Description: "Keep matching regex snippets from file contents",
		Args:        []string{"--snippet"},
		Mode:        startupModifierModeSnippet,
	},
	{
		Key:         "include",
		Label:       "--include",
		Description: "Allow ignored files or folders",
		Args:        []string{"--include"},
		Mode:        startupModifierModeInclude,
	},
	{
		Key:         "changed",
		Label:       "--changed",
		Description: "Keep only git-changed files",
		Args:        []string{"--changed"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "staged",
		Label:       "--staged",
		Description: "Keep only staged git files",
		Args:        []string{"--staged"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "unstaged",
		Label:       "--unstaged",
		Description: "Keep only unstaged git files",
		Args:        []string{"--unstaged"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "untracked",
		Label:       "--untracked",
		Description: "Keep only untracked git files",
		Args:        []string{"--untracked"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "changed-diff",
		Label:       "--changed-diff",
		Description: "Show patches for git-changed files",
		Args:        []string{"--changed-diff"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "staged-diff",
		Label:       "--staged-diff",
		Description: "Show patches for staged git files",
		Args:        []string{"--staged-diff"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "unstaged-diff",
		Label:       "--unstaged-diff",
		Description: "Show patches for unstaged git files",
		Args:        []string{"--unstaged-diff"},
		Mode:        startupModifierModeGit,
	},
	// Keep --then last in this slice so it appears at the top of the default
	// bottom-stacked filter menu. It teaches scope chaining and should stay
	// above newly added rows in the on-screen order.
	{
		Key:         "then",
		Label:       "--then",
		Description: "Add another catclip command after this one, with its own targets and filters",
		Args:        []string{"--then"},
		Mode:        startupModifierModeThen,
	},
}

func maybeResolveStartupPickerArgs(args []string) (startupPickerResult, bool, error) {
	if rawArgsHeadlessStdoutMode(args) {
		return startupPickerResult{}, false, nil
	}
	if rawArgsUseStdinPathValues(args) {
		return startupPickerResult{}, false, nil
	}
	if !canPromptInteractively() {
		return startupPickerResult{}, false, nil
	}

	enabled, err := shouldUseStartupPicker(args)
	if err != nil {
		return startupPickerResult{}, true, err
	}
	if !enabled {
		return startupPickerResult{}, false, nil
	}
	if err := validateStartupPreflightArgs(args); err != nil {
		return startupPickerResult{}, true, err
	}

	resolver, err := newStartupPickerResolver()
	if err != nil {
		return startupPickerResult{}, true, err
	}
	direct, err := startupCommandCanRunDirectly(resolver, args)
	if err != nil {
		return startupPickerResult{}, true, err
	}
	if direct {
		return startupPickerResult{}, false, nil
	}

	resolvedArgs, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, args)
	if err != nil {
		if errors.Is(err, errSelectionCancelled) {
			return startupPickerResult{}, true, nil
		}
		return startupPickerResult{}, true, err
	}
	return startupPickerResult{Args: resolvedArgs, UsedFzf: usedFzf}, true, nil
}

func newStartupPickerResolver() (*scopeResolver, error) {
	cfg, err := parseArgs([]string{"."})
	if err != nil {
		return nil, err
	}
	gitCtx := detectGitContext(cfg.WorkingDir)
	baseRules, err := loadIgnoreRules()
	if err != nil {
		return nil, err
	}
	matcher, err := buildScopeMatcher(baseRules, executionScope{})
	if err != nil {
		return nil, err
	}
	projectIgnore, useProjectIgnore, err := buildProjectIgnoreMatcher(cfg.WorkingDir, gitCtx.Enabled)
	if err != nil {
		return nil, err
	}
	return &scopeResolver{
		cfg:               cfg,
		gitCtx:            gitCtx,
		matcher:           matcher,
		projectIgnore:     projectIgnore,
		useProjectIgnore:  useProjectIgnore,
		allowFileSymlinks: false,
		useGitIgnore:      gitCtx.Enabled,
	}, nil
}

func startupCommandCanRunDirectly(resolver *scopeResolver, args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if _, action, ok := detectStartupTrailingAction(args); ok && action != startupTrailingActionNone {
		return false, nil
	}
	if startupHasUnresolvedScope(args) {
		return false, nil
	}
	needsFileSetResolution, err := startupArgsNeedFileSetResolution(args)
	if err != nil {
		return false, err
	}
	if needsFileSetResolution {
		return false, nil
	}
	cfg, err := parseArgs(args)
	if err != nil {
		return false, nil
	}
	for _, scopeSpec := range configCommandScopes(cfg) {
		scopeResolver := *resolver
		scopeTargets := scopeSpec.Targets()
		if len(scopeSpec.IncludedTargets()) > 0 {
			includeTargets := scopeSpec.IncludedTargets()
			if includeTargetsContainWildcard(includeTargets) {
				scopeResolver.includedTargets = buildIncludedTargetSet(scopeResolver.cfg.WorkingDir, includeTargets)
			} else {
				exactIncludedTargets, unresolvedIncludeQueries, err := scopeResolver.resolveExactIgnoredIncludeTargets(includeTargets, scopeTargets)
				if err != nil {
					return false, err
				}
				if len(unresolvedIncludeQueries) > 0 {
					return false, nil
				}
				scopeResolver.includedTargets = buildIncludedTargetSet(scopeResolver.cfg.WorkingDir, exactIncludedTargets)
			}
		}
		for _, target := range scopeTargets {
			canResolve, err := scopeResolver.canResolveTargetWithoutPrompt(target)
			if err != nil {
				return false, err
			}
			if !canResolve {
				return false, nil
			}
		}
	}
	return true, nil
}

func startupArgsNeedFileSetResolution(args []string) (bool, error) {
	currentArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--only", "--exclude":
			values, next := consumeModifierValues(args, i+1)
			if len(values) == 0 {
				currentArgs = append(currentArgs, arg)
				continue
			}
			for _, value := range values {
				keepLiteral, err := startupFileSetValueShouldStayLiteral(currentArgs, arg, value)
				if err != nil {
					return false, err
				}
				if !keepLiteral {
					return true, nil
				}
			}
			currentArgs = append(currentArgs, arg)
			currentArgs = append(currentArgs, values...)
			i = next - 1
		default:
			currentArgs = append(currentArgs, arg)
		}
	}
	return false, nil
}

func startupHasUnresolvedScope(args []string) bool {
	scopeHasExplicitTarget := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--preview":
			continue
		case "--then":
			if !scopeHasExplicitTarget {
				return true
			}
			scopeHasExplicitTarget = false
			continue
		case "--include", "--only", "--exclude", "--contains", "--snippet", "--depth":
			if !scopeHasExplicitTarget {
				return true
			}
			if args[i] == "--include" || args[i] == "--only" || args[i] == "--exclude" {
				_, next := consumeModifierValues(args, i+1)
				i = next - 1
				continue
			}
			if args[i] == "--depth" {
				if i+1 < len(args) {
					if _, err := parseDepthToken(args[i+1]); err == nil {
						i++
					}
				}
				continue
			}
			if i+1 < len(args) {
				i++
			}
			continue
		case "--paths", "--changed", "--staged", "--unstaged", "--untracked", "--changed-diff", "--staged-diff", "--unstaged-diff", "--":
			if !scopeHasExplicitTarget {
				return true
			}
			continue
		case "--recent":
			if !scopeHasExplicitTarget {
				return true
			}
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				if _, err := parseRecentLimitToken(args[i+1]); err == nil {
					i++
				}
			}
			continue
		default:
			if strings.HasPrefix(arg, "--contains=") || strings.HasPrefix(arg, "--recent=") || strings.HasPrefix(arg, "--depth=") {
				return true
			}
			if strings.HasPrefix(arg, "--") || (strings.HasPrefix(arg, "-") && len(arg) > 1) {
				continue
			}
			scopeHasExplicitTarget = true
		}
	}
	return !scopeHasExplicitTarget
}

func detectStartupTrailingAction(args []string) ([]string, startupTrailingAction, bool) {
	if len(args) == 0 {
		return nil, startupTrailingActionNone, false
	}
	switch args[len(args)-1] {
	case "--":
		return append([]string(nil), args[:len(args)-1]...), startupTrailingActionModifierMenu, true
	default:
		return nil, startupTrailingActionNone, false
	}
}

func shouldUseStartupPicker(args []string) (bool, error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-h", "--help", "--help-all", "--version", "-V", "--hiss", "--hiss-reset",
			"--internal-tree-payload", "--internal-tree-target", "--internal-tree-kind", "--internal-tree-state",
			"--internal-file-preview", "--internal-file-path",
			"--internal-content-match-list",
			"--internal-recent-preview", "--internal-recent-data", "--internal-recent-selection":
			return false, nil
		case "--include", "--only", "--exclude", "--contains", "--snippet", "--depth":
			if arg == "--depth" {
				if i+1 < len(args) {
					if _, err := parseDepthToken(args[i+1]); err == nil {
						i++
					}
				}
				continue
			}
			if arg == "--contains" || arg == "--snippet" {
				if i+1 < len(args) {
					i++
				}
				continue
			}
			_, next := consumeModifierValues(args, i+1)
			i = next - 1
			continue
		case "--paths":
			continue
		case "--recent":
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				if _, err := parseRecentLimitToken(args[i+1]); err != nil {
					return false, nil
				}
				i++
			}
			continue
		case "--then":
			continue
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree",
			"--preview", "--changed", "--staged", "--unstaged", "--untracked",
			"--changed-diff", "--staged-diff", "--unstaged-diff", "--", "--diff":
			continue
		}
		if strings.HasPrefix(arg, "--contains=") || strings.HasPrefix(arg, "--recent=") || strings.HasPrefix(arg, "--depth=") {
			return true, nil
		}
		if strings.HasPrefix(arg, "--") || (strings.HasPrefix(arg, "-") && len(arg) > 1) {
			return false, nil
		}
		if filepath.IsAbs(arg) {
			return false, newUsageError("Error: Absolute paths not allowed: %s\n  Use a relative path from your project root instead.", singleQuoted(arg))
		}
		if containsParentTraversal(arg) {
			return false, newUsageError("Error: Cannot traverse above working directory: %s\n  catclip only operates within the current directory tree.\n  Use a relative path from your project root instead.\n  Example: catclip config/", singleQuoted(arg))
		}
	}
	return true, nil
}

type startupInputParse struct {
	targets          []string
	includeQueries   []string
	modifiers        []string
	nextScopeTargets []string
	hasThen          bool
}

func parseStartupInputTokens(tokens []string) (startupInputParse, error) {
	parsed := startupInputParse{
		targets:          make([]string, 0, len(tokens)),
		includeQueries:   make([]string, 0, len(tokens)),
		modifiers:        make([]string, 0, len(tokens)),
		nextScopeTargets: make([]string, 0, len(tokens)),
	}
	seenModifier := false

	for i := 0; i < len(tokens); i++ {
		if !seenModifier && tokens[i] == "--then" {
			parsed.hasThen = true
			parsed.nextScopeTargets = append(parsed.nextScopeTargets, tokens[i+1:]...)
			for _, token := range parsed.nextScopeTargets {
				if strings.HasPrefix(token, "-") {
					return startupInputParse{}, newUsageError("Error: --then expects next-scope targets here.\n  Example: --then tests")
				}
			}
			return parsed, nil
		}
		if !seenModifier && tokens[i] == "--include" {
			if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
				i++
				parsed.includeQueries = append(parsed.includeQueries, tokens[i])
				continue
			}
			return startupInputParse{}, requiredStageValueError("--include")
		}
		if !seenModifier && !strings.HasPrefix(tokens[i], "-") {
			parsed.targets = append(parsed.targets, tokens[i])
			continue
		}

		seenModifier = true
		switch tokens[i] {
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree",
			"--preview", "--changed", "--staged", "--unstaged", "--untracked",
			"--changed-diff", "--staged-diff", "--unstaged-diff", "--paths":
			parsed.modifiers = append(parsed.modifiers, tokens[i])
		case "--only", "--exclude", "--contains", "--snippet", "--depth":
			if i+1 >= len(tokens) {
				switch tokens[i] {
				case "--only":
					return startupInputParse{}, requiredStageValueError("--only")
				case "--exclude":
					return startupInputParse{}, requiredStageValueError("--exclude")
				case "--snippet":
					return startupInputParse{}, requiredStageValueError("--snippet")
				case "--depth":
					return startupInputParse{}, requiredStageValueError("--depth")
				default:
					return startupInputParse{}, containsMissingPatternError(tokens, i)
				}
			}
			if tokens[i] == "--depth" {
				if _, err := parseDepthToken(tokens[i+1]); err != nil {
					return startupInputParse{}, err
				}
			}
			parsed.modifiers = append(parsed.modifiers, tokens[i], tokens[i+1])
			i++
		case "--recent":
			parsed.modifiers = append(parsed.modifiers, tokens[i])
			if i+1 >= len(tokens) || isModifierBoundaryToken(tokens[i+1]) {
				continue
			}
			if _, err := parseRecentLimitToken(tokens[i+1]); err != nil {
				return startupInputParse{}, err
			}
			parsed.modifiers = append(parsed.modifiers, tokens[i+1])
			i++
		case "--include":
			return startupInputParse{}, newUsageError("Error: --include must come before modifiers.\n  Use it while selecting targets for the current scope.")
		case "--then":
			parsed.hasThen = true
			parsed.nextScopeTargets = append(parsed.nextScopeTargets, tokens[i+1:]...)
			for _, token := range parsed.nextScopeTargets {
				if strings.HasPrefix(token, "-") {
					return startupInputParse{}, newUsageError("Error: --then expects next-scope targets here.\n  Example: --then tests")
				}
			}
			return parsed, nil
		case "--":
			return startupInputParse{}, newUsageError("Error: modifier input cannot include positional targets.\n  Use --then to start a new scope.")
		default:
			switch {
			case strings.HasPrefix(tokens[i], "--contains="):
				return startupInputParse{}, newUsageError("Error: --contains requires a space before the pattern.\n  Use: --contains 'pattern'")
			case strings.HasPrefix(tokens[i], "--recent="):
				return startupInputParse{}, newUsageError("Error: --recent requires a space before the value.\n  Use: --recent 5\n  Or:  --recent")
			case strings.HasPrefix(tokens[i], "--depth="):
				return startupInputParse{}, newUsageError("Error: --depth requires a space before the value.\n  Use: --depth 2")
			case strings.HasPrefix(tokens[i], "--"):
				return startupInputParse{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(tokens[i]))
			case strings.HasPrefix(tokens[i], "-") && len(tokens[i]) > 1:
				return startupInputParse{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(tokens[i]))
			default:
				return startupInputParse{}, positionalAfterModifierError()
			}
		}
	}
	return parsed, nil
}

func resolveStartupScopeInputs(resolver *scopeResolver, targetTokens, includeQueries, alreadySelected, explicitTargets []string) ([]string, []string, []string, bool, error) {
	return resolveStartupScopeInputsWithPrompt(resolver, targetTokens, includeQueries, alreadySelected, explicitTargets, "select> ")
}

func resolveStartupScopeInputsWithPrompt(resolver *scopeResolver, targetTokens, includeQueries, alreadySelected, explicitTargets []string, prompt string) ([]string, []string, []string, bool, error) {
	selectedPaths := append([]string(nil), alreadySelected...)
	resolvedArgs := make([]string, 0, len(targetTokens)+len(includeQueries)*2)
	resolvedTargets := make([]string, 0, len(targetTokens)+len(includeQueries))
	resolvedExplicitTargets := make([]string, 0, len(targetTokens))
	selectedExplicitTargets := append([]string(nil), explicitTargets...)
	usedPicker := false
	// Resolve targets first so we know the scope for include filtering.
	if len(targetTokens) == 0 && len(includeQueries) == 0 {
		resolved, used, err := resolveStartupInitialTargets(resolver, nil, selectedPaths, prompt)
		if err != nil {
			return nil, nil, nil, true, err
		}
		resolvedArgs = append(resolvedArgs, resolved...)
		resolvedPaths := startupResolvedTargetPaths(resolved)
		resolvedTargets = append(resolvedTargets, resolvedPaths...)
		resolvedExplicitTargets = append(resolvedExplicitTargets, resolvedPaths...)
		return resolvedArgs, resolvedTargets, resolvedExplicitTargets, used, nil
	}

	for _, token := range targetTokens {
		resolved, used, err := resolveStartupInitialTargets(resolver, []string{token}, selectedPaths, prompt)
		if err != nil {
			return nil, nil, nil, true, err
		}
		resolvedArgs = append(resolvedArgs, resolved...)
		resolvedPaths := startupResolvedTargetPaths(resolved)
		resolvedTargets = append(resolvedTargets, resolvedPaths...)
		resolvedExplicitTargets = append(resolvedExplicitTargets, resolvedPaths...)
		selectedPaths = append(selectedPaths, resolvedPaths...)
		selectedExplicitTargets = append(selectedExplicitTargets, resolvedPaths...)
		usedPicker = usedPicker || used
	}

	// Now resolve includes scoped to the resolved targets.
	scopeTargetsForInclude := selectedExplicitTargets
	exactIncludedTargets, unresolvedIncludeQueries, err := resolver.resolveExactIgnoredIncludeTargets(includeQueries, scopeTargetsForInclude)
	if err != nil {
		return nil, nil, nil, false, err
	}
	includedTargets := append([]string(nil), exactIncludedTargets...)

	if len(includedTargets) > 0 {
		resolvedTargets = append(resolvedTargets, includedTargets...)
		selectedPaths = append(selectedPaths, includedTargets...)
	}
	for _, query := range unresolvedIncludeQueries {
		resolved, err := resolver.resolveInteractiveIncludeTargets(query, selectedPaths, selectedExplicitTargets, scopeTargetsForInclude)
		if err != nil {
			return nil, nil, nil, true, err
		}
		includedTargets = append(includedTargets, resolved...)
		resolvedTargets = append(resolvedTargets, resolved...)
		selectedPaths = append(selectedPaths, resolved...)
		usedPicker = true
	}
	if len(includedTargets) > 0 {
		resolvedArgs = append(resolvedArgs, "--include")
		resolvedArgs = append(resolvedArgs, includedTargets...)
	}

	return resolvedArgs, resolvedTargets, resolvedExplicitTargets, usedPicker, nil
}

func resolveStartupInitialTargets(resolver *scopeResolver, args []string, alreadySelected []string, prompt string) ([]string, bool, error) {
	if selectionContainsAll(alreadySelected) {
		return nil, true, nil
	}
	if len(args) == 0 {
		selected, err := resolver.chooseRootTargetMatches("", prompt, true, alreadySelected)
		if err != nil {
			return nil, true, err
		}
		if len(selected) == 0 {
			return nil, true, errSelectionCancelled
		}
		return targetMatchArgs(selected), true, nil
	}
	if len(args) == 1 && normalizeRelPath(args[0]) == "." {
		return []string{"."}, false, nil
	}

	resolved := make([]string, 0, len(args))
	usedPicker := false
	for _, arg := range args {
		query := startupTargetQuery(arg)
		normalized := normalizeRelPath(arg)
		if normalized == "" {
			normalized = "."
		}
		selectedPaths := append(append([]string(nil), alreadySelected...), startupResolvedTargetPaths(resolved)...)

		if normalized == "." {
			resolved = append(resolved, normalized)
			continue
		}

		if hasGlobChars(arg) {
			resolved = append(resolved, arg)
			continue
		}

		exists, err := resolver.targetPathExists(normalized)
		if err != nil {
			return nil, usedPicker, err
		}
		if exists {
			if coveredBySelection(normalized, selectedPaths) {
				continue
			}
			resolved = append(resolved, normalized)
			continue
		}

		covered, err := resolver.interactiveQueryCoveredBySelection(query, selectedPaths)
		if err != nil {
			return nil, usedPicker, err
		}
		if covered {
			continue
		}

		selected, err := resolver.chooseRootTargetMatches(query, prompt, false, selectedPaths)
		if err != nil {
			return nil, true, err
		}
		if len(selected) == 0 {
			return nil, true, errSelectionCancelled
		}
		resolved = append(resolved, targetMatchArgs(selected)...)
		usedPicker = true
	}
	return resolved, usedPicker, nil
}

func startupTargetQuery(raw string) string {
	value := strings.ReplaceAll(raw, "\\", "/")
	return normalizeRelPath(value)
}

func targetMatchArgs(matches []targetMatch) []string {
	args := make([]string, 0, len(matches)*2)
	ignoredPaths := make([]string, 0, len(matches))
	ignoredOnly := true
	for _, match := range matches {
		if match.Kind == "done" {
			continue
		}
		if match.Ignored {
			ignoredPaths = append(ignoredPaths, match.Path)
			continue
		}
		ignoredOnly = false
	}
	if ignoredOnly && len(ignoredPaths) > 0 {
		args = append(args, "--include")
		args = append(args, ignoredPaths...)
		return args
	}
	for _, match := range matches {
		if match.Kind == "done" {
			continue
		}
		if match.Ignored {
			args = append(args, "--include", match.Path)
			continue
		}
		args = append(args, match.Path)
	}
	return args
}

func startupResolvedTargetPaths(args []string) []string {
	paths := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--include" {
			values, next := consumeModifierValues(args, i+1)
			paths = append(paths, values...)
			i = next - 1
			continue
		}
		paths = append(paths, args[i])
	}
	return paths
}

func startupCurrentScopeTargetPaths(args []string) ([]string, error) {
	cfg, err := parseArgs(args)
	if err != nil {
		return nil, err
	}
	scopeSpecs := configCommandScopes(cfg)
	if len(scopeSpecs) == 0 {
		return nil, nil
	}
	lastTargets := scopeSpecs[len(scopeSpecs)-1].Targets()
	targets := make([]string, 0, len(lastTargets))
	for _, target := range lastTargets {
		target = normalizeRelPath(target)
		if target == "" {
			target = "."
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func startupCurrentScopeIncludedTargetPaths(args []string) ([]string, error) {
	cfg, err := parseArgs(args)
	if err != nil {
		return nil, err
	}
	scopeSpecs := configCommandScopes(cfg)
	if len(scopeSpecs) == 0 {
		return nil, nil
	}
	lastIncluded := scopeSpecs[len(scopeSpecs)-1].IncludedTargets()
	targets := make([]string, 0, len(lastIncluded))
	for _, target := range lastIncluded {
		target = normalizeRelPath(target)
		if target == "" || target == "." {
			continue
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func resolveStartupArgs(resolver *scopeResolver, args []string) ([]string, []string, bool, error) {
	return resolveStartupArgsWithMode(resolver, args, false)
}

func resolveInteractiveStartupArgs(resolver *scopeResolver, args []string) ([]string, []string, bool, error) {
	return resolveStartupArgsWithMode(resolver, args, true)
}

func resolveStartupArgsWithMode(resolver *scopeResolver, args []string, requireScopeBeforeModifiers bool) ([]string, []string, bool, error) {
	targetPrompt := "select> "
	if len(args) == 0 {
		resolvedArgs, resolvedTargets, _, usedFzf, err := resolveStartupScopeInputsWithPrompt(resolver, nil, nil, nil, nil, targetPrompt)
		return resolvedArgs, resolvedTargets, usedFzf, err
	}

	finalArgs := make([]string, 0, len(args))
	currentScopeTargets := make([]string, 0, len(args))
	currentScopeExplicitTargets := make([]string, 0, len(args))
	usedFzf := false
	modifierMode := false
	hadScopeInput := false

	for i := 0; i < len(args); {
		arg := args[i]

		if !modifierMode && !strings.HasPrefix(arg, "-") {
			resolvedArgs, resolvedTargets, resolvedExplicitTargets, targetUsedFzf, err := resolveStartupScopeInputsWithPrompt(resolver, []string{arg}, nil, currentScopeTargets, currentScopeExplicitTargets, targetPrompt)
			if err != nil {
				return nil, nil, false, err
			}
			finalArgs = append(finalArgs, resolvedArgs...)
			currentScopeTargets = append(currentScopeTargets, resolvedTargets...)
			currentScopeExplicitTargets = append(currentScopeExplicitTargets, resolvedExplicitTargets...)
			usedFzf = usedFzf || targetUsedFzf
			hadScopeInput = true
			i++
			continue
		}

		if requireScopeBeforeModifiers && !hadScopeInput && startupLeadingModifierNeedsInitialScope(arg) {
			resolvedArgs, resolvedTargets, resolvedExplicitTargets, targetUsedFzf, err := resolveStartupScopeInputsWithPrompt(resolver, nil, nil, currentScopeTargets, currentScopeExplicitTargets, targetPrompt)
			if err != nil {
				return nil, nil, false, err
			}
			finalArgs = append(finalArgs, resolvedArgs...)
			currentScopeTargets = append(currentScopeTargets, resolvedTargets...)
			currentScopeExplicitTargets = append(currentScopeExplicitTargets, resolvedExplicitTargets...)
			usedFzf = usedFzf || targetUsedFzf
			hadScopeInput = true
			continue
		}

		switch arg {
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--preview":
			finalArgs = append(finalArgs, arg)
			i++
		case "--then":
			if requireScopeBeforeModifiers && len(currentScopeTargets) == 0 {
				resolvedArgs, resolvedTargets, resolvedExplicitTargets, targetUsedFzf, err := resolveStartupScopeInputsWithPrompt(resolver, nil, nil, currentScopeTargets, currentScopeExplicitTargets, targetPrompt)
				if err != nil {
					return nil, nil, false, err
				}
				finalArgs = append(finalArgs, resolvedArgs...)
				currentScopeTargets = append(currentScopeTargets, resolvedTargets...)
				currentScopeExplicitTargets = append(currentScopeExplicitTargets, resolvedExplicitTargets...)
				usedFzf = usedFzf || targetUsedFzf
				hadScopeInput = true
			}
			finalArgs = append(finalArgs, "--then")
			currentScopeTargets = currentScopeTargets[:0]
			currentScopeExplicitTargets = currentScopeExplicitTargets[:0]
			modifierMode = false
			hadScopeInput = false
			targetPrompt = "then> "
			i++
		case "--":
			choice, err := chooseStartupModifier(finalArgs)
			if err != nil {
				return nil, nil, false, err
			}
			if err := startupValidateModifierChoice(finalArgs, choice); err != nil {
				return nil, nil, false, err
			}
			usedFzf = true
			if choice.Mode == startupModifierModeFlags {
				finalArgs = append(finalArgs, choice.Args...)
				modifierMode = true
				hadScopeInput = true
				i++
				continue
			}
			if choice.Mode == startupModifierModeThen {
				finalArgs = append(finalArgs, "--then")
				currentScopeTargets = currentScopeTargets[:0]
				currentScopeExplicitTargets = currentScopeExplicitTargets[:0]
				modifierMode = false
				hadScopeInput = false
				targetPrompt = "then> "
				i++
				continue
			}
			if choice.Mode == startupModifierModeGit {
				argsAfterStage, newScopeTargets, stageUsedFzf, consumed, err := resolveStartupModifierStage(resolver, finalArgs, currentScopeTargets, currentScopeExplicitTargets, choice.Args, args[i+1:], true)
				if err != nil {
					return nil, nil, false, err
				}
				if len(choice.Args) > 1 {
					argsAfterStage = append(argsAfterStage, choice.Args[1:]...)
				}
				finalArgs = argsAfterStage
				currentScopeTargets = newScopeTargets
				usedFzf = usedFzf || stageUsedFzf
				i += 1 + consumed
				modifierMode = true
				hadScopeInput = true
				continue
			}
			argsAfterStage, newScopeTargets, stageUsedFzf, consumed, err := resolveStartupModifierStage(resolver, finalArgs, currentScopeTargets, currentScopeExplicitTargets, choice.Args, args[i+1:], true)
			if err != nil {
				return nil, nil, false, err
			}
			finalArgs = argsAfterStage
			currentScopeTargets = newScopeTargets
			usedFzf = usedFzf || stageUsedFzf
			i += 1 + consumed
			modifierMode = true
			hadScopeInput = true
		case "--include", "--only", "--exclude", "--contains", "--snippet", "--recent", "--depth", "--paths":
			argsAfterStage, newScopeTargets, stageUsedFzf, consumed, err := resolveStartupModifierStage(resolver, finalArgs, currentScopeTargets, currentScopeExplicitTargets, []string{arg}, args[i+1:], false)
			if err != nil {
				return nil, nil, false, err
			}
			finalArgs = argsAfterStage
			currentScopeTargets = newScopeTargets
			usedFzf = usedFzf || stageUsedFzf
			i += 1 + consumed
			modifierMode = true
			hadScopeInput = true
		case "--changed", "--staged", "--unstaged", "--untracked", "--changed-diff", "--staged-diff", "--unstaged-diff":
			argsAfterStage, newScopeTargets, stageUsedFzf, consumed, err := resolveStartupModifierStage(resolver, finalArgs, currentScopeTargets, currentScopeExplicitTargets, []string{arg}, args[i+1:], false)
			if err != nil {
				return nil, nil, false, err
			}
			finalArgs = argsAfterStage
			currentScopeTargets = newScopeTargets
			usedFzf = usedFzf || stageUsedFzf
			i += 1 + consumed
			modifierMode = true
			hadScopeInput = true
		default:
			switch {
			case strings.HasPrefix(arg, "--contains="):
				return nil, nil, false, newUsageError("Error: --contains requires a space before the pattern.\n  Use: catclip src --contains 'pattern'\n  Not: catclip src --contains='pattern'")
			case strings.HasPrefix(arg, "--recent="):
				return nil, nil, false, newUsageError("Error: --recent requires a space before the value.\n  Use: catclip src --recent 5\n  Or:  catclip src --recent")
			case strings.HasPrefix(arg, "--depth="):
				return nil, nil, false, newUsageError("Error: --depth requires a space before the value.\n  Use: catclip src --depth 2")
			case strings.HasPrefix(arg, "--"):
				return nil, nil, false, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			case strings.HasPrefix(arg, "-") && len(arg) > 1:
				return nil, nil, false, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			default:
				return nil, nil, false, positionalAfterModifierError()
			}
		}
	}

	if !hadScopeInput {
		resolvedArgs, resolvedTargets, resolvedExplicitTargets, targetUsedFzf, err := resolveStartupScopeInputsWithPrompt(resolver, nil, nil, currentScopeTargets, currentScopeExplicitTargets, targetPrompt)
		if err != nil {
			return nil, nil, false, err
		}
		finalArgs = append(finalArgs, resolvedArgs...)
		currentScopeTargets = append(currentScopeTargets, resolvedTargets...)
		currentScopeExplicitTargets = append(currentScopeExplicitTargets, resolvedExplicitTargets...)
		usedFzf = usedFzf || targetUsedFzf
	}

	return finalArgs, currentScopeTargets, usedFzf, nil
}

func startupLeadingModifierNeedsInitialScope(arg string) bool {
	switch arg {
	case "--then":
		return false
	case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--preview":
		return true
	case "--", "--include", "--only", "--exclude", "--contains", "--snippet", "--recent", "--depth", "--paths",
		"--changed", "--staged", "--unstaged", "--untracked",
		"--changed-diff", "--staged-diff", "--unstaged-diff":
		return true
	default:
		return strings.HasPrefix(arg, "--")
	}
}

func resolveStartupModifierTokens(currentArgs, modifierTokens []string) ([]string, bool, error) {
	finalArgs := append([]string(nil), currentArgs...)
	usedFzf := false
	for i := 0; i < len(modifierTokens); i++ {
		switch modifierTokens[i] {
		case "--only", "--exclude":
			if i+1 >= len(modifierTokens) {
				return nil, false, errSelectionCancelled
			}
			value := modifierTokens[i+1]
			i++
			var (
				err          error
				valueUsedFzf bool
			)
			finalArgs, valueUsedFzf, err = resolveStartupModifierValue(finalArgs, modifierTokens[i-1], value)
			if err != nil {
				return nil, false, err
			}
			usedFzf = usedFzf || valueUsedFzf
		case "--contains":
			if i+1 >= len(modifierTokens) {
				return nil, false, errSelectionCancelled
			}
			finalArgs = append(finalArgs, modifierTokens[i], modifierTokens[i+1])
			i++
		case "--recent":
			finalArgs = append(finalArgs, modifierTokens[i])
			if i+1 >= len(modifierTokens) {
				continue
			}
			if isModifierBoundaryToken(modifierTokens[i+1]) {
				continue
			}
			finalArgs = append(finalArgs, modifierTokens[i+1])
			i++
		default:
			finalArgs = append(finalArgs, modifierTokens[i])
		}
	}
	return finalArgs, usedFzf, nil
}

func resolveStartupModifierValue(currentArgs []string, flag, value string) ([]string, bool, error) {
	return append(append([]string(nil), currentArgs...), flag, value), false, nil
}

func pathBase(relPath string) string {
	parts := strings.Split(relPath, "/")
	return parts[len(parts)-1]
}

func resolveStartupTrailingActionArgs(resolver *scopeResolver, prefixArgs []string, action startupTrailingAction) ([]string, bool, error) {
	for len(prefixArgs) > 0 && prefixArgs[len(prefixArgs)-1] == "--" {
		prefixArgs = prefixArgs[:len(prefixArgs)-1]
	}
	switch action {
	case startupTrailingActionModifierMenu:
		args, _, usedFzf, err := resolveStartupArgs(resolver, append(append([]string(nil), prefixArgs...), "--"))
		return args, usedFzf, err
	case startupTrailingActionOnly:
		args, usedFzf, err := resolveStartupScopeFileSetArgs(prefixArgs, "--only", "only> ")
		return args, usedFzf, err
	case startupTrailingActionExclude:
		args, usedFzf, err := resolveStartupScopeFileSetArgs(prefixArgs, "--exclude", "exclude> ")
		return args, usedFzf, err
	case startupTrailingActionRecent:
		args, err := resolveStartupRecentArgs(prefixArgs)
		return args, true, err
	case startupTrailingActionContains:
		args, usedFzf, err := resolveStartupContentArgs(prefixArgs, "--contains")
		return args, usedFzf, err
	case startupTrailingActionSnippet:
		args, usedFzf, err := resolveStartupContentArgs(prefixArgs, "--snippet")
		return args, usedFzf, err
	default:
		return append([]string(nil), prefixArgs...), false, nil
	}
}

func resolveBareStartupModifierArgs(resolver *scopeResolver) ([]string, bool, error) {
	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{"--"})
	return args, usedFzf, err
}

func trimTrailingModifierPlaceholders(args []string) []string {
	for len(args) > 0 && args[len(args)-1] == "--" {
		args = args[:len(args)-1]
	}
	return args
}

func chooseStartupModifier(currentArgs []string) (startupModifierChoice, error) {
	state, err := startupCurrentScopeStateForArgs(currentArgs)
	if err != nil {
		return startupModifierChoice{}, err
	}
	if state.Known && state.Empty && !state.NeedsInclude {
		return startupModifierChoice{}, startupNoFilesMatchedError(state.Config)
	}
	lines, index := startupModifierChoiceLines(startupAvailableModifierChoicesWithState(currentArgs, state))
	if len(lines) == 0 {
		return startupModifierChoice{}, errSelectionCancelled
	}
	bin, err := fuzzyResolverBinary()
	if err != nil {
		return startupModifierChoice{}, err
	}
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Prompt:         "filter> ",
		WithNth:        "1,3",
		Nth:            "1",
		Header:         startupModifierPickerHeader(),
		PreviewCommand: startupModifierCurrentScopePreviewCommand(state),
		NoSort:         true,
		Lines:          lines,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return startupModifierChoice{}, errSelectionCancelled
	}
	if err != nil {
		return startupModifierChoice{}, err
	}
	if len(result.Matches) == 0 {
		return startupModifierChoice{}, errSelectionCancelled
	}
	selected := result.Matches[0]
	choice, ok := index[selected]
	if !ok {
		return startupModifierChoice{}, errSelectionCancelled
	}
	return choice, nil
}

func resolveStartupModifierArgs(resolver *scopeResolver, currentArgs, currentScopeTargets, currentScopeExplicitTargets []string) ([]string, bool, error) {
	choice, err := chooseStartupModifier(currentArgs)
	if err != nil {
		return nil, false, err
	}
	if err := startupValidateModifierChoice(currentArgs, choice); err != nil {
		return nil, false, err
	}

	finalArgs := trimTrailingModifierPlaceholders(append([]string(nil), currentArgs...))
	switch choice.Mode {
	case startupModifierModeThen:
		return append(finalArgs, "--then"), true, nil
	case startupModifierModeInclude:
		currentIncludedTargets, err := startupCurrentScopeIncludedTargetPaths(currentArgs)
		if err != nil {
			return nil, false, err
		}
		args, _, _, includeUsedFzf, err := resolveStartupScopeInputs(resolver, nil, []string{""}, currentIncludedTargets, currentScopeExplicitTargets)
		if err != nil {
			return nil, true, err
		}
		return append(finalArgs, args...), true || includeUsedFzf, nil
	case startupModifierModeOnly:
		args, onlyUsedFzf, err := resolveStartupScopeFileSetArgs(finalArgs, "--only", "only> ")
		return args, true || onlyUsedFzf, err
	case startupModifierModeExclude:
		args, excludeUsedFzf, err := resolveStartupScopeFileSetArgs(finalArgs, "--exclude", "exclude> ")
		return args, true || excludeUsedFzf, err
	case startupModifierModeRecent:
		args, err := resolveStartupRecentArgs(finalArgs)
		return args, true, err
	case startupModifierModeDepth:
		args, usedFzf, err := resolveStartupDepthArgs(finalArgs)
		if err != nil {
			return nil, true || usedFzf, err
		}
		return args, true || usedFzf, nil
	case startupModifierModeContains:
		args, containsUsedFzf, err := resolveStartupContentArgs(finalArgs, "--contains")
		if err != nil {
			return nil, true || containsUsedFzf, err
		}
		return args, true || containsUsedFzf, nil
	case startupModifierModeSnippet:
		args, containsUsedFzf, err := resolveStartupContentArgs(finalArgs, "--snippet")
		if err != nil {
			return nil, true || containsUsedFzf, err
		}
		return args, true || containsUsedFzf, nil
	case startupModifierModeGit:
		diffPreview := strings.HasSuffix(choice.Args[0], "-diff")
		args, gitUsedFzf, err := resolveStartupGitScopeArgs(resolver, append(finalArgs, choice.Args[0]), startupGitStagePrompt(choice.Args[0]), nil, true, diffPreview)
		if err != nil {
			return nil, true || gitUsedFzf, err
		}
		return args, true || gitUsedFzf, nil
	case startupModifierModeFlags:
		return append(finalArgs, choice.Args...), true, nil
	default:
		return nil, false, errSelectionCancelled
	}
}

func resolveStartupRecentArgs(currentArgs []string) ([]string, error) {
	return resolveStartupRecentPickerArgs(currentArgs, "")
}

func resolveStartupContentArgs(currentArgs []string, flag string) ([]string, bool, error) {
	if err := validateCurrentScopeFlagAddition(currentArgs, flag); err != nil {
		return nil, false, err
	}
	result, err := chooseContentMatchesWithFzf("", currentArgs, flag)
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(result.Query) == "" {
		return nil, false, errSelectionCancelled
	}

	finalArgs := append([]string(nil), currentArgs...)
	finalArgs = append(finalArgs, flag, result.Query)
	if contentMatchSelectionIncludesAllRow(result.Matches) {
		return finalArgs, true, nil
	}
	matchPaths, err := contentMatchPathsForArgs(currentArgs, flag, result.Query)
	if err != nil {
		return nil, false, err
	}
	if startupStageSelectionCoversAll(result.Matches, matchPaths) {
		return finalArgs, true, nil
	}
	if len(result.Matches) > 0 {
		finalArgs = append(finalArgs, "--only")
		finalArgs = append(finalArgs, result.Matches...)
	}
	return finalArgs, true, nil
}

func resolveStartupScopeFileSetArgs(currentArgs []string, flag, prompt string) ([]string, bool, error) {
	return resolveStartupScopeFileSetArgsWithQuery(currentArgs, flag, prompt, "")
}

func resolveStartupScopeFileSetArgsWithQuery(currentArgs []string, flag, prompt, query string) ([]string, bool, error) {
	var values []string
	if query != "" {
		values = []string{query}
	}
	stageValues, usedFzf, err := resolveStartupModifierStageValues(currentArgs, flag, prompt, values, query == "", "")
	if err != nil {
		return nil, false, err
	}
	stageValues, err = normalizeInteractiveFileSetStageValues(currentArgs, stageValues)
	if err != nil {
		return nil, false, err
	}
	finalArgs := append([]string(nil), currentArgs...)
	finalArgs = append(finalArgs, flag)
	finalArgs = append(finalArgs, stageValues...)
	return finalArgs, usedFzf, nil
}

func resolveStartupModifierStage(resolver *scopeResolver, currentArgs, currentScopeTargets, currentScopeExplicitTargets []string, choiceArgs, remaining []string, allowInteractiveCompletion bool) ([]string, []string, bool, int, error) {
	if len(choiceArgs) == 0 {
		return nil, nil, false, 0, errSelectionCancelled
	}
	flag := choiceArgs[0]

	switch flag {
	case "--include":
		currentIncludedTargets, err := startupCurrentScopeIncludedTargetPaths(currentArgs)
		if err != nil {
			return nil, nil, false, 0, err
		}
		values, consumed := startupStageValues(remaining)
		if len(values) == 0 {
			if !allowInteractiveCompletion {
				return nil, nil, false, 0, requiredStageValueError(flag)
			}
			resolvedArgs, resolvedTargets, _, usedFzf, err := resolveStartupScopeInputs(resolver, nil, []string{""}, currentIncludedTargets, currentScopeExplicitTargets)
			if err != nil {
				return nil, nil, false, 0, err
			}
			finalArgs := append(append([]string(nil), currentArgs...), resolvedArgs...)
			return finalArgs, append(append([]string(nil), currentScopeTargets...), resolvedTargets...), usedFzf, 0, nil
		}
		exactStageValues, unresolvedValues, err := resolver.resolveExactIgnoredIncludeTargets(values, currentScopeExplicitTargets)
		if err != nil {
			return nil, nil, false, 0, err
		}
		stageValues := append([]string(nil), exactStageValues...)
		usedFzf := false
		selectedPaths := append(append([]string(nil), currentIncludedTargets...), exactStageValues...)
		for _, value := range unresolvedValues {
			resolved, err := resolver.resolveInteractiveIncludeTargets(value, selectedPaths, currentScopeExplicitTargets, currentScopeExplicitTargets)
			if err != nil {
				return nil, nil, false, 0, err
			}
			if len(resolved) == 0 {
				return nil, nil, false, 0, errSelectionCancelled
			}
			stageValues = append(stageValues, resolved...)
			selectedPaths = append(selectedPaths, resolved...)
			usedFzf = true
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		finalArgs = append(finalArgs, stageValues...)
		return finalArgs, append(append([]string(nil), currentScopeTargets...), stageValues...), usedFzf, consumed, nil
	case "--only", "--exclude":
		values, consumed := startupStageValues(remaining)
		if len(values) == 0 && !allowInteractiveCompletion {
			return nil, nil, false, 0, requiredStageValueError(flag)
		}
		stageValues, usedFzf, err := resolveStartupModifierStageValues(currentArgs, flag, startupStagePrompt(flag), values, len(values) == 0, "")
		if err != nil {
			return nil, nil, false, 0, err
		}
		stageValues, err = normalizeInteractiveFileSetStageValues(currentArgs, stageValues)
		if err != nil {
			return nil, nil, false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		finalArgs = append(finalArgs, stageValues...)
		return finalArgs, append([]string(nil), currentScopeTargets...), usedFzf, consumed, nil
	case "--recent":
		if len(remaining) == 0 || isModifierBoundaryToken(remaining[0]) {
			if !allowInteractiveCompletion {
				finalArgs := append(append([]string(nil), currentArgs...), flag)
				return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
			}
			args, err := resolveStartupRecentArgs(currentArgs)
			return args, append([]string(nil), currentScopeTargets...), true, 0, err
		}
		limit, err := parseRecentLimitToken(remaining[0])
		if err != nil {
			return nil, nil, false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, strconv.Itoa(limit))
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--depth":
		if err := validateCurrentScopeFlagAddition(currentArgs, "--depth"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if len(remaining) == 0 || (allowInteractiveCompletion && startupRemainingIsBarePlaceholderChain(remaining)) {
			if !allowInteractiveCompletion {
				return nil, append([]string(nil), currentScopeTargets...), false, 0, requiredStageValueError("--depth")
			}
			args, usedFzf, err := resolveStartupDepthArgs(currentArgs)
			return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
		}
		depth, err := validateStartupDepthValue(currentArgs, remaining[0])
		if err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, strconv.Itoa(depth))
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--paths":
		if err := validateCurrentScopeFlagAddition(currentArgs, "--paths"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
	case "--contains":
		if err := validateCurrentScopeFlagAddition(currentArgs, "--contains"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if len(remaining) == 0 || (allowInteractiveCompletion && startupRemainingIsBarePlaceholderChain(remaining)) {
			if !allowInteractiveCompletion {
				return nil, append([]string(nil), currentScopeTargets...), false, 0, containsMissingPatternError(currentArgs, len(currentArgs))
			}
			args, usedFzf, err := resolveStartupContentArgs(currentArgs, "--contains")
			return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, remaining[0])
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--snippet":
		if err := validateCurrentScopeFlagAddition(currentArgs, "--snippet"); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if len(remaining) == 0 || (allowInteractiveCompletion && startupRemainingIsBarePlaceholderChain(remaining)) {
			if !allowInteractiveCompletion {
				return nil, append([]string(nil), currentScopeTargets...), false, 0, requiredStageValueError("--snippet")
			}
			args, usedFzf, err := resolveStartupContentArgs(currentArgs, "--snippet")
			return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, remaining[0])
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--changed", "--staged", "--unstaged", "--untracked":
		if err := validateCurrentScopeFlagAddition(currentArgs, flag); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		if err := startupValidateGitStageArgs(currentArgs, flag, choiceArgs, remaining); err != nil {
			return nil, nil, false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		if len(remaining) > 0 && !isModifierBoundaryToken(remaining[0]) {
			return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
		}
		if !resolver.gitCtx.Enabled {
			return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
		}
		diffPreview := false
		argsAfterStage, usedFzf, err := resolveStartupGitScopeArgs(resolver, finalArgs, startupGitStagePrompt(flag), nil, true, diffPreview)
		if err != nil {
			return nil, nil, false, 0, err
		}
		return argsAfterStage, append([]string(nil), currentScopeTargets...), usedFzf, 0, nil
	case "--changed-diff", "--staged-diff", "--unstaged-diff":
		if err := validateCurrentScopeFlagAddition(currentArgs, flag); err != nil {
			return nil, append([]string(nil), currentScopeTargets...), false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		if len(remaining) > 0 && !isModifierBoundaryToken(remaining[0]) {
			return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
		}
		if !resolver.gitCtx.Enabled {
			return finalArgs, append([]string(nil), currentScopeTargets...), false, 0, nil
		}
		argsAfterStage, usedFzf, err := resolveStartupGitScopeArgs(resolver, finalArgs, startupGitStagePrompt(flag), nil, true, true)
		if err != nil {
			return nil, nil, false, 0, err
		}
		return argsAfterStage, append([]string(nil), currentScopeTargets...), usedFzf, 0, nil
	default:
		return nil, nil, false, 0, errSelectionCancelled
	}
}

func startupRemainingIsBarePlaceholderChain(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		if token != "--" {
			return false
		}
	}
	return true
}

func startupValidateGitStageArgs(currentArgs []string, flag string, choiceArgs, remaining []string) error {
	if flag != "--untracked" {
		return nil
	}

	hasDiff := currentScopeHasFlag(currentArgs, "--changed-diff") ||
		currentScopeHasFlag(currentArgs, "--staged-diff") ||
		currentScopeHasFlag(currentArgs, "--unstaged-diff") ||
		slices.Contains(choiceArgs[1:], "--changed-diff") ||
		slices.Contains(choiceArgs[1:], "--staged-diff") ||
		slices.Contains(choiceArgs[1:], "--unstaged-diff") ||
		(len(remaining) > 0 && (remaining[0] == "--changed-diff" || remaining[0] == "--staged-diff" || remaining[0] == "--unstaged-diff"))
	if !hasDiff {
		return nil
	}

	if currentScopeHasFlag(currentArgs, "--changed") ||
		currentScopeHasFlag(currentArgs, "--staged") ||
		currentScopeHasFlag(currentArgs, "--unstaged") {
		return nil
	}

	return untrackedDiffError()
}

func resolveStartupGitScopeArgs(resolver *scopeResolver, currentArgs []string, prompt string, values []string, allowInteractiveEmpty bool, diffPreview bool) ([]string, bool, error) {
	if !resolver.gitCtx.Enabled {
		return append([]string(nil), currentArgs...), false, nil
	}

	stageFlag := "--changed"
	for i := len(currentArgs) - 1; i >= 0; i-- {
		switch currentArgs[i] {
		case "--changed", "--staged", "--unstaged", "--untracked",
			"--changed-diff", "--staged-diff", "--unstaged-diff":
			stageFlag = currentArgs[i]
			i = -1
		case "--then":
			i = -1
		}
	}

	previewCommand := startupFileSetPreviewCommand(currentArgs, stageFlag, diffPreview)
	stageValues, usedFzf, err := resolveStartupModifierStageValues(currentArgs, stageFlag, prompt, values, allowInteractiveEmpty, previewCommand)
	if err != nil {
		return nil, false, err
	}
	if len(stageValues) == 0 {
		return append([]string(nil), currentArgs...), usedFzf, nil
	}
	if startupStageSelectionIncludesAllRow(stageValues, stageFlag) {
		return append([]string(nil), currentArgs...), usedFzf, nil
	}

	relPaths, err := startupScopeFileSetPaths(currentArgs)
	if err != nil {
		return nil, false, err
	}
	if startupStageSelectionCoversAll(stageValues, relPaths) {
		return append([]string(nil), currentArgs...), usedFzf, nil
	}

	finalArgs := append([]string(nil), currentArgs...)
	finalArgs = append(finalArgs, "--only")
	finalArgs = append(finalArgs, stageValues...)
	return finalArgs, usedFzf, nil
}

func startupGitStagePrompt(flag string) string {
	switch flag {
	case "--staged", "--staged-diff":
		return "staged> "
	case "--unstaged", "--unstaged-diff":
		return "unstaged> "
	case "--untracked":
		return "untracked> "
	default:
		return "changed> "
	}
}

func startupStageSelectionCoversAll(selected, candidates []string) bool {
	if len(selected) == 0 || len(candidates) == 0 {
		return false
	}

	seenSelected := make(map[string]struct{}, len(selected))
	for _, value := range selected {
		seenSelected[normalizeRelPath(value)] = struct{}{}
	}
	if len(seenSelected) != len(candidates) {
		return false
	}
	for _, candidate := range candidates {
		if _, ok := seenSelected[normalizeRelPath(candidate)]; !ok {
			return false
		}
	}
	return true
}

func resolveStartupModifierStageValues(currentArgs []string, flag, prompt string, values []string, allowInteractiveEmpty bool, previewCommand string) ([]string, bool, error) {
	if previewCommand == "" {
		previewCommand = startupFileSetPreviewCommand(currentArgs, flag, false)
	}
	if len(values) == 0 {
		if !allowInteractiveEmpty {
			return nil, false, errSelectionCancelled
		}
		relPaths, err := startupScopeFileSetPaths(currentArgs)
		if err != nil {
			return nil, false, err
		}
		if len(relPaths) == 0 {
			return nil, false, errSelectionCancelled
		}
		selected, err := chooseManyStartupFileSetRowsWithFzf("", prompt, startupFileSetPickerHeader(flag), previewCommand, startupFileSetRows(flag, relPaths))
		if err != nil {
			return nil, false, err
		}
		return selected, true, nil
	}

	resolvedValues := make([]string, 0, len(values))
	usedFzf := false
	var rows []startupFileSetRow
	for _, value := range values {
		keepLiteral, err := startupFileSetValueShouldStayLiteral(currentArgs, flag, value)
		if err != nil {
			return nil, false, err
		}
		if keepLiteral {
			resolvedValues = append(resolvedValues, value)
			continue
		}
		if rows == nil {
			relPaths, err := startupScopeFileSetPaths(currentArgs)
			if err != nil {
				return nil, false, err
			}
			rows = startupFileSetRows(flag, relPaths)
		}
		selected, err := chooseManyStartupFileSetRowsWithFzf(value, prompt, startupFileSetPickerHeader(flag), previewCommand, rows)
		if err != nil {
			return nil, false, err
		}
		resolvedValues = append(resolvedValues, selected...)
		usedFzf = true
	}

	return resolvedValues, usedFzf, nil
}

func startupFileSetValueShouldStayLiteral(currentArgs []string, flag, value string) (bool, error) {
	if startupLooksLikeLiteralFileSetPattern(value) {
		return true, nil
	}
	if startupFileSetQueryMatchesExistingPath(currentArgs, value) {
		return true, nil
	}
	relPaths, err := startupScopeFileSetPaths(currentArgs)
	if err != nil {
		return false, err
	}
	for _, row := range startupFileSetRows(flag, relPaths) {
		if row.Kind == startupFileSetRowAll {
			continue
		}
		if row.Value == value {
			return true, nil
		}
	}
	return false, nil
}

func startupLooksLikeLiteralFileSetPattern(value string) bool {
	return strings.ContainsAny(value, "*?[")
}

func startupFileSetQueryMatchesExistingPath(currentArgs []string, value string) bool {
	cfg, err := parseArgs(currentArgs)
	if err != nil {
		return false
	}
	trimmed := strings.TrimSuffix(value, "/")
	normalized := normalizeRelPath(trimmed)
	if normalized == "" || normalized == "." {
		return false
	}
	info, err := os.Stat(filepath.Join(cfg.WorkingDir, filepath.FromSlash(normalized)))
	if err != nil {
		return false
	}
	if strings.HasSuffix(value, "/") {
		return info.IsDir()
	}
	return true
}

func startupStageValues(tokens []string) ([]string, int) {
	values := make([]string, 0, len(tokens))
	i := 0
	for i < len(tokens) {
		if isModifierBoundaryToken(tokens[i]) {
			break
		}
		values = append(values, tokens[i])
		i++
	}
	return values, i
}

func startupStagePrompt(flag string) string {
	if flag == "--exclude" {
		return "exclude> "
	}
	return "only> "
}

func startupFileSetPickerHeader(flag string) string {
	switch flag {
	case "--exclude":
		return pickerHeader(
			"Remove files whose paths match.",
			"Type a path pattern.",
			fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  [Esc] cancel", multiSelectToggleAllKey()),
		)
	case "--changed", "--changed-diff":
		firstLine := "Pick git-changed files."
		if flag == "--changed-diff" {
			firstLine = "Pick diffs for git-changed files."
		}
		return pickerHeader(
			firstLine,
			"Type a path to narrow the list.",
			fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  [Esc] cancel", multiSelectToggleAllKey()),
		)
	case "--staged", "--staged-diff":
		firstLine := "Pick git-staged files."
		if flag == "--staged-diff" {
			firstLine = "Pick diffs for git-staged files."
		}
		return pickerHeader(
			firstLine,
			"Type a path to narrow the list.",
			fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  [Esc] cancel", multiSelectToggleAllKey()),
		)
	case "--unstaged", "--unstaged-diff":
		firstLine := "Pick git-unstaged files."
		if flag == "--unstaged-diff" {
			firstLine = "Pick diffs for git-unstaged files."
		}
		return pickerHeader(
			firstLine,
			"Type a path to narrow the list.",
			fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  [Esc] cancel", multiSelectToggleAllKey()),
		)
	case "--untracked":
		return pickerHeader(
			"Pick untracked files.",
			"Type a path to narrow the list.",
			fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  [Esc] cancel", multiSelectToggleAllKey()),
		)
	default:
		return pickerHeader(
			"Keep only files whose paths match.",
			"Type a path pattern.",
			fmt.Sprintf("[Enter] confirm  [Tab] mark  [%s] toggle  [Esc] cancel", multiSelectToggleAllKey()),
		)
	}
}

func startupScopeFileSetPaths(currentArgs []string) ([]string, error) {
	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(view.Entries))
	relPaths := make([]string, 0, len(view.Entries))
	for _, entry := range view.Entries {
		if entry.RelPath == "" {
			continue
		}
		if _, ok := seen[entry.RelPath]; ok {
			continue
		}
		seen[entry.RelPath] = struct{}{}
		relPaths = append(relPaths, entry.RelPath)
	}
	sort.Strings(relPaths)
	return relPaths, nil
}

func startupFileSetRows(flag string, relPaths []string) []startupFileSetRow {
	rows := make([]startupFileSetRow, 0, len(relPaths)+8)
	if allRow := startupAllFileSetRow(flag); allRow != nil {
		rows = append(rows, *allRow)
	}
	if flag == "--only" || flag == "--exclude" {
		// fzf's current bottom-prompt layout renders earlier input rows closest
		// to the prompt. Prepend synthetic pattern rows here so they stay near
		// the prompt at the bottom instead of floating to the top of the window.
		rows = append(rows, startupExtensionPatternRows(relPaths)...)
	}
	rows = append(rows, startupFilePathRows(relPaths)...)
	return rows
}

func startupFileSetPreviewCommand(currentArgs []string, flag string, diffPreview bool) string {
	if diffPreview {
		return fzfDiffFilePreviewCommand(currentArgs)
	}
	if activeDiffFlag := currentScopeDiffPreviewFlag(currentArgs); activeDiffFlag != "" {
		return fzfDiffFilePreviewCommand(currentArgs)
	}
	previewFlag := flag
	switch flag {
	case "--changed", "--staged", "--unstaged", "--untracked":
		// Git file pickers narrow the already-selected git set. Preview should
		// mirror the final lowering to `--only`, not append a bogus value to the
		// git selector itself.
		previewFlag = "--only"
	}
	return fzfFileSetPreviewCommand(currentArgs, previewFlag)
}

func startupModifierCurrentScopePreviewCommand(state startupCurrentScopeState) string {
	scopeSpecs := configCommandScopes(state.Config)
	if !state.Known || state.Empty || len(scopeSpecs) == 0 {
		return ""
	}

	treeBin, ok := treePreviewBinary()
	if !ok {
		return ""
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return ""
	}

	scopeArgs := canonicalScopeArgs(executionScopeFromCommandScopeSpec(scopeSpecs[len(scopeSpecs)-1]))
	if len(scopeArgs) == 0 {
		return ""
	}

	parts := []string{shellQuoteArg(self), "--quiet", "--internal-tree-payload"}
	parts = append(parts, scopeArgs...)
	parts = append(parts, "|", shellQuoteArg(treeBin))
	parts = append(parts, fzfTreeRenderArgs()...)
	return strings.Join(parts, " ")
}

func currentScopeHasFlag(args []string, flag string) bool {
	for i := len(args) - 1; i >= 0; i-- {
		switch args[i] {
		case flag:
			return true
		case "--then":
			return false
		}
	}
	return false
}

func currentScopeDiffPreviewFlag(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		switch args[i] {
		case "--changed-diff", "--staged-diff", "--unstaged-diff":
			return args[i]
		case "--then":
			return ""
		}
	}
	return ""
}

func startupAvailableModifierChoices(currentArgs []string) []startupModifierChoice {
	state, err := startupCurrentScopeStateForArgs(currentArgs)
	if err != nil {
		state = startupCurrentScopeState{}
	}
	return startupAvailableModifierChoicesWithState(currentArgs, state)
}

func startupAvailableModifierChoicesWithState(currentArgs []string, state startupCurrentScopeState) []startupModifierChoice {
	if state.Known && state.Empty {
		if !state.NeedsInclude {
			return nil
		}
		return []startupModifierChoice{
			{
				Key:         "include",
				Label:       "--include",
				Description: "Allow ignored files or folders",
				Args:        []string{"--include"},
				Mode:        startupModifierModeInclude,
			},
		}
	}
	choices := make([]startupModifierChoice, 0, len(startupModifierChoices))
	for _, choice := range startupModifierChoices {
		if startupModifierChoiceAllowed(currentArgs, choice, state) {
			choices = append(choices, choice)
		}
	}
	return choices
}

func startupModifierChoiceAllowed(currentArgs []string, choice startupModifierChoice, state startupCurrentScopeState) bool {
	if validateCurrentScopeFlagSequence(currentArgs, choice.Args) != nil {
		return false
	}
	if currentScopeHasNarrowGitChangeFilter(currentArgs) {
		if startupChoiceIsGitChangeFilter(choice) {
			return false
		}
		if startupChoiceIsDiffModifier(choice) && !startupDiffModifierMatchesActiveFilter(currentArgs, choice) {
			return false
		}
	}
	return startupModifierChoiceMeaningful(choice, state)
}

// currentScopeHasNarrowGitChangeFilter returns true when the current scope
// already has a specific git change filter (--staged, --unstaged, --untracked).
// After a narrow filter, broader (--changed) and sibling filters are hidden
// because they would either widen the scope or produce an empty intersection.
// --changed is NOT narrow — it's the superset, and narrowing after it is valid.
func currentScopeHasNarrowGitChangeFilter(args []string) bool {
	return currentScopeHasFlag(args, "--staged") ||
		currentScopeHasFlag(args, "--unstaged") ||
		currentScopeHasFlag(args, "--untracked")
}

func startupChoiceIsGitChangeFilter(choice startupModifierChoice) bool {
	switch choice.Args[0] {
	case "--changed", "--staged", "--unstaged", "--untracked":
		return true
	}
	return false
}

func startupChoiceIsDiffModifier(choice startupModifierChoice) bool {
	switch choice.Args[0] {
	case "--changed-diff", "--staged-diff", "--unstaged-diff":
		return true
	}
	return false
}

// startupDiffModifierMatchesActiveFilter returns true when the diff modifier
// corresponds to the active narrow git filter. E.g. --unstaged-diff matches
// --unstaged, --staged-diff matches --staged. --changed-diff matches --changed
// but --changed is not a narrow filter so this is only called for narrow ones.
// --untracked has no matching diff (untracked files have no diff).
func startupDiffModifierMatchesActiveFilter(args []string, choice startupModifierChoice) bool {
	switch choice.Args[0] {
	case "--staged-diff":
		return currentScopeHasFlag(args, "--staged")
	case "--unstaged-diff":
		return currentScopeHasFlag(args, "--unstaged")
	case "--changed-diff":
		return currentScopeHasFlag(args, "--changed")
	}
	return false
}

func startupValidateModifierChoice(currentArgs []string, choice startupModifierChoice) error {
	return validateCurrentScopeFlagSequence(currentArgs, choice.Args)
}

func startupModifierChoiceMeaningful(choice startupModifierChoice, state startupCurrentScopeState) bool {
	if !state.Known {
		return true
	}

	switch choice.Args[0] {
	case "--depth":
		return state.MaxDepth > 1
	case "--changed":
		if !state.GitKnown {
			return false
		}
		return state.AnyChanged && !state.AllChanged
	case "--staged":
		if !state.GitKnown {
			return false
		}
		return state.AnyStaged && !state.AllStaged
	case "--unstaged":
		if !state.GitKnown {
			return false
		}
		return state.AnyUnstaged && !state.AllUnstaged
	case "--untracked":
		if !state.GitKnown {
			return false
		}
		return state.AnyUntracked && !state.AllUntracked
	case "--changed-diff":
		if !state.GitKnown {
			return false
		}
		return state.AnyChanged
	case "--staged-diff":
		if !state.GitKnown {
			return false
		}
		return state.AnyStaged
	case "--unstaged-diff":
		if !state.GitKnown {
			return false
		}
		return state.AnyUnstaged
	case "--include":
		return state.HasScopedIgnoredTargets
	default:
		return true
	}
}

func startupCurrentScopeStateForArgs(currentArgs []string) (startupCurrentScopeState, error) {
	view, ok, err := startupResolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil {
		return startupCurrentScopeState{}, err
	}
	if !ok {
		return startupCurrentScopeState{}, nil
	}

	state := startupCurrentScopeState{
		Known:    true,
		Empty:    len(view.Entries) == 0,
		MaxDepth: maxEntryPathDepth(view.Entries),
		Config:   view.Config,
	}
	if state.Empty {
		needsInclude, err := startupCurrentScopeNeedsInclude(view.Config)
		if err != nil {
			return startupCurrentScopeState{}, err
		}
		state.NeedsInclude = needsInclude
	}
	scopeSpecs := configCommandScopes(view.Config)
	if len(scopeSpecs) > 0 {
		current := scopeSpecs[len(scopeSpecs)-1]
		hasScopedIgnored, err := startupHasScopedIgnoredTargets(current.Targets())
		if err == nil {
			state.HasScopedIgnoredTargets = hasScopedIgnored
		}
	}
	if state.Empty || !view.GitContext.Enabled {
		return state, nil
	}
	state.GitKnown = true

	currentRepoPaths := make(map[string]struct{}, len(view.Entries))
	for _, entry := range view.Entries {
		repoPath := normalizeRelPath(view.GitContext.toRepoPath(entry.RelPath))
		if repoPath == "" {
			continue
		}
		currentRepoPaths[repoPath] = struct{}{}
	}
	total := len(currentRepoPaths)
	if total == 0 {
		return state, nil
	}

	changed, err := startupCurrentScopeSelectionSet(view.GitContext, executionScope{Changed: true}, currentRepoPaths)
	if err != nil {
		return startupCurrentScopeState{}, err
	}
	staged, err := startupCurrentScopeSelectionSet(view.GitContext, executionScope{Changed: true, Staged: true}, currentRepoPaths)
	if err != nil {
		return startupCurrentScopeState{}, err
	}
	unstaged, err := startupCurrentScopeSelectionSet(view.GitContext, executionScope{Changed: true, Unstaged: true}, currentRepoPaths)
	if err != nil {
		return startupCurrentScopeState{}, err
	}
	untracked, err := startupCurrentScopeSelectionSet(view.GitContext, executionScope{Changed: true, Untracked: true}, currentRepoPaths)
	if err != nil {
		return startupCurrentScopeState{}, err
	}

	state.AnyChanged, state.AllChanged = startupAnyAllForCurrentScope(total, changed)
	state.AnyStaged, state.AllStaged = startupAnyAllForCurrentScope(total, staged)
	state.AnyUnstaged, state.AllUnstaged = startupAnyAllForCurrentScope(total, unstaged)
	state.AnyUntracked, state.AllUntracked = startupAnyAllForCurrentScope(total, untracked)
	return state, nil
}

func startupCurrentScopeNeedsInclude(cfg runConfig) (bool, error) {
	scopeSpecs := configCommandScopes(cfg)
	if len(scopeSpecs) == 0 {
		return false, nil
	}

	resolver, err := newStartupPickerResolver()
	if err != nil {
		return false, err
	}

	current := scopeSpecs[len(scopeSpecs)-1]
	if len(current.IncludedTargets()) > 0 {
		exactIncludedTargets, unresolvedIncludeQueries, err := resolver.resolveExactIgnoredIncludeTargets(current.IncludedTargets(), current.Targets())
		if err != nil {
			return false, err
		}
		if len(unresolvedIncludeQueries) > 0 {
			return false, nil
		}
		resolver.includedTargets = buildIncludedTargetSet(resolver.cfg.WorkingDir, exactIncludedTargets)
	}

	for _, target := range current.Targets() {
		target = normalizeRelPath(target)
		if target == "" || target == "." {
			continue
		}
		needsInclude, err := resolver.targetNeedsInclude(target)
		if err != nil {
			return false, err
		}
		if needsInclude {
			return true, nil
		}
	}
	return false, nil
}

func startupHasScopedIgnoredTargets(scopeTargets []string) (bool, error) {
	resolver, err := newStartupPickerResolver()
	if err != nil {
		return false, err
	}
	all, err := resolver.allIgnoredTargets()
	if err != nil {
		return false, err
	}
	scoped := filterIgnoredTargetsByScopeTargets(all, scopeTargets)
	return len(scoped) > 0, nil
}

func startupCurrentScopeSelectionSet(gitCtx gitContext, sel executionScope, currentRepoPaths map[string]struct{}) (map[string]struct{}, error) {
	paths, err := collectChangedRepoPaths(gitCtx, sel)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, repoPath := range paths {
		repoPath = normalizeRelPath(repoPath)
		if _, ok := currentRepoPaths[repoPath]; !ok {
			continue
		}
		set[repoPath] = struct{}{}
	}
	return set, nil
}

func startupAnyAllForCurrentScope(total int, set map[string]struct{}) (bool, bool) {
	if total == 0 || len(set) == 0 {
		return false, false
	}
	return true, len(set) == total
}

func startupNoFilesMatchedError(cfg runConfig) error {
	var b strings.Builder
	if err := writeNoFilesMatchedMessage(cfg, &b, activeColorPalette(), false); err != nil {
		return err
	}
	return newExitError(1, strings.TrimRight(b.String(), "\n"))
}

func startupFilePathRows(relPaths []string) []startupFileSetRow {
	rows := make([]startupFileSetRow, 0, len(relPaths))
	for _, relPath := range relPaths {
		rows = append(rows, startupFileSetRow{
			Kind:          startupFileSetRowFile,
			Display:       pickerFilePathDisplayLabel(relPath),
			Value:         relPath,
			PreviewTarget: relPath,
			PreviewKind:   treeTargetKindFile,
			PreviewState:  treeTargetStateText,
		})
	}
	return rows
}

func startupAllFileSetRow(flag string) *startupFileSetRow {
	label := startupAllFileSetLabel(flag)
	if label == "" {
		return nil
	}
	return &startupFileSetRow{
		Kind:    startupFileSetRowAll,
		Display: label,
	}
}

func startupAllFileSetLabel(flag string) string {
	switch flag {
	case "--changed":
		return "[all changed files]"
	case "--staged":
		return "[all staged files]"
	case "--unstaged":
		return "[all unstaged files]"
	case "--untracked":
		return "[all untracked files]"
	default:
		return ""
	}
}

func startupStageSelectionIncludesAllRow(selected []string, flag string) bool {
	label := startupAllFileSetLabel(flag)
	if label == "" {
		return false
	}
	for _, value := range selected {
		if value == label {
			return true
		}
	}
	return false
}

func contentMatchSelectionIncludesAllRow(selected []string) bool {
	for _, value := range selected {
		if value == contentMatchAllMatchesLabel {
			return true
		}
	}
	return false
}

func startupExtensionPatternRows(relPaths []string) []startupFileSetRow {
	counts := make(map[string]int)
	for _, relPath := range relPaths {
		ext := shellStyleExtension(relPath)
		if ext == "" {
			continue
		}
		counts[ext]++
	}
	if len(counts) == 0 {
		return nil
	}

	exts := make([]string, 0, len(counts))
	for ext := range counts {
		exts = append(exts, ext)
	}
	sort.Strings(exts)

	rows := make([]startupFileSetRow, 0, len(exts))
	for _, ext := range exts {
		pattern := "*." + ext
		fileWord := "files"
		if counts[ext] == 1 {
			fileWord = "file"
		}
		rows = append(rows, startupFileSetRow{
			Kind:    startupFileSetRowExtensionPattern,
			Display: fmt.Sprintf("[pattern] %s (%d %s)", pattern, counts[ext], fileWord),
			Value:   pattern,
		})
	}
	return rows
}

func formatStartupFileSetRows(rows []startupFileSetRow) []string {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, strings.Join([]string{
			row.Display,
			row.Value,
			row.PreviewTarget,
			row.PreviewKind,
			row.PreviewState,
			string(row.Kind),
		}, "\t"))
	}
	return lines
}

func chooseManyStartupFileSetRowsWithFzf(query, prompt, header, previewCommand string, rows []startupFileSetRow) ([]string, error) {
	bin, err := fuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Query:          query,
		Prompt:         prompt,
		WithNth:        "1",
		Nth:            "1",
		Header:         header,
		PreviewCommand: previewCommand,
		Multi:          true,
		Bindings:       multiSelectPickerBindings(),
		NoSort:         startupFileSetRowsNeedStableOrder(rows),
		Lines:          formatStartupFileSetRows(rows),
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

func startupFileSetRowsNeedStableOrder(rows []startupFileSetRow) bool {
	for _, row := range rows {
		if row.Kind == startupFileSetRowExtensionPattern {
			return true
		}
	}
	return false
}

func startupModifierChoiceLines(choices []startupModifierChoice) ([]string, map[string]startupModifierChoice) {
	lines := make([]string, 0, len(choices))
	index := make(map[string]startupModifierChoice, len(choices))
	labelWidth := 0
	for _, choice := range choices {
		if len(choice.Label) > labelWidth {
			labelWidth = len(choice.Label)
		}
	}
	for _, choice := range choices {
		label := fmt.Sprintf("%-*s", labelWidth, choice.Label)
		lines = append(lines, strings.Join([]string{label, choice.Key, choice.Description}, "\t"))
		index[choice.Key] = choice
	}
	return lines, index
}

func startupModifierPickerHeader() string {
	return pickerHeader(
		"Choose what to do next.",
		"Preview shows the current files.",
		"[Up/Down] move  [Enter] confirm  [Esc] cancel",
	)
}
