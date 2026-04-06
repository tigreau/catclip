package catclip

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/picker"
)

type startupModifierMode string

const (
	startupModifierModeFlags           startupModifierMode = "flags"
	startupModifierModeThen            startupModifierMode = "then"
	startupModifierModeInclude         startupModifierMode = "include"
	startupModifierModeOnly            startupModifierMode = "only"
	startupModifierModeExclude         startupModifierMode = "exclude"
	startupModifierModeRecent          startupModifierMode = "recent"
	startupModifierModeContains        startupModifierMode = "contains"
	startupModifierModeContainsSnippet startupModifierMode = "contains-snippet"
	startupModifierModeGit             startupModifierMode = "git"
)

type startupTrailingAction string

const (
	startupTrailingActionNone         startupTrailingAction = ""
	startupTrailingActionModifierMenu startupTrailingAction = "modifier-menu"
	startupTrailingActionOnly         startupTrailingAction = "only"
	startupTrailingActionExclude      startupTrailingAction = "exclude"
	startupTrailingActionRecent       startupTrailingAction = "recent"
	startupTrailingActionContains     startupTrailingAction = "contains"
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
		Key:         "contains",
		Label:       "--contains",
		Description: "Keep files whose contents match a regex",
		Args:        []string{"--contains"},
		Mode:        startupModifierModeContains,
	},
	{
		Key:         "contains-snippet",
		Label:       "--contains --snippet",
		Description: "Keep matching regex snippets from file contents",
		Args:        []string{"--contains", "--snippet"},
		Mode:        startupModifierModeContainsSnippet,
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
		Label:       "--changed --diff",
		Description: "Show patches for git-changed files",
		Args:        []string{"--changed", "--diff"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "staged-diff",
		Label:       "--staged --diff",
		Description: "Show patches for staged git files",
		Args:        []string{"--staged", "--diff"},
		Mode:        startupModifierModeGit,
	},
	{
		Key:         "unstaged-diff",
		Label:       "--unstaged --diff",
		Description: "Show patches for unstaged git files",
		Args:        []string{"--unstaged", "--diff"},
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
	matcher, err := buildScopeMatcher(baseRules, scope{})
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
	cfg, err := parseArgs(args)
	if err != nil {
		return false, nil
	}
	for _, scope := range cfg.Scopes {
		if len(scope.IncludedTargets) > 0 {
			return false, nil
		}
		for _, target := range scope.Targets {
			canResolve, err := resolver.canResolveTargetWithoutPrompt(target)
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

func startupHasUnresolvedScope(args []string) bool {
	scopeHasExplicitTarget := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-t", "--no-tree", "--preview":
			continue
		case "--then":
			if !scopeHasExplicitTarget {
				return true
			}
			scopeHasExplicitTarget = false
			continue
		case "--include", "--only", "--exclude":
			if !scopeHasExplicitTarget {
				return true
			}
			_, next := consumeModifierValues(args, i+1)
			i = next - 1
			continue
		case "--contains":
			if !scopeHasExplicitTarget {
				return true
			}
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				i++
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
		case "--", "--changed", "--staged", "--unstaged", "--untracked", "--diff", "--snippet":
			if !scopeHasExplicitTarget {
				return true
			}
			continue
		default:
			if strings.HasPrefix(arg, "--contains=") || strings.HasPrefix(arg, "--recent=") {
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
	case "--only":
		return append([]string(nil), args[:len(args)-1]...), startupTrailingActionOnly, true
	case "--exclude":
		return append([]string(nil), args[:len(args)-1]...), startupTrailingActionExclude, true
	case "--recent":
		return append([]string(nil), args[:len(args)-1]...), startupTrailingActionRecent, true
	case "--contains":
		return append([]string(nil), args[:len(args)-1]...), startupTrailingActionContains, true
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
			"--internal-contains-list",
			"--internal-recent-preview", "--internal-recent-data", "--internal-recent-selection":
			return false, nil
		case "--include", "--only", "--exclude", "--contains":
			if arg == "--contains" {
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
				}
				continue
			}
			_, next := consumeModifierValues(args, i+1)
			i = next - 1
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
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-t", "--no-tree",
			"--preview", "--changed", "--staged", "--unstaged", "--untracked", "--diff", "--snippet", "--":
			continue
		}
		if strings.HasPrefix(arg, "--contains=") || strings.HasPrefix(arg, "--recent=") {
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
			parsed.includeQueries = append(parsed.includeQueries, "")
			continue
		}
		if !seenModifier && !strings.HasPrefix(tokens[i], "-") {
			parsed.targets = append(parsed.targets, tokens[i])
			continue
		}

		seenModifier = true
		switch tokens[i] {
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-t", "--no-tree",
			"--preview", "--changed", "--staged", "--unstaged", "--untracked", "--diff", "--snippet":
			parsed.modifiers = append(parsed.modifiers, tokens[i])
		case "--only", "--exclude", "--contains":
			if i+1 >= len(tokens) {
				switch tokens[i] {
				case "--only":
					return startupInputParse{}, newUsageError("Error: --only requires a pattern.\n  Example: catclip src --only '*.ts'")
				case "--exclude":
					return startupInputParse{}, newUsageError("Error: --exclude requires a pattern.\n  Example: catclip src --exclude '*.test.*'")
				default:
					return startupInputParse{}, newUsageError("Error: --contains requires a regex pattern.\n  Example: catclip src --contains 'TODO'")
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
			case strings.HasPrefix(tokens[i], "--"):
				return startupInputParse{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(tokens[i]))
			case strings.HasPrefix(tokens[i], "-") && len(tokens[i]) > 1:
				return startupInputParse{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(tokens[i]))
			default:
				return startupInputParse{}, newUsageError("Error: positional targets must come before modifiers.\n  Add targets first, use --include, or use --then for a new scope.")
			}
		}
	}
	return parsed, nil
}

func resolveStartupScopeInputs(resolver *scopeResolver, targetTokens, includeQueries, alreadySelected []string) ([]string, []string, bool, error) {
	selectedPaths := append([]string(nil), alreadySelected...)
	resolvedArgs := make([]string, 0, len(targetTokens)+len(includeQueries)*2)
	resolvedTargets := make([]string, 0, len(targetTokens)+len(includeQueries))
	usedPicker := false

	if len(targetTokens) == 0 && len(includeQueries) == 0 {
		resolved, used, err := resolveStartupInitialTargets(resolver, nil, selectedPaths)
		if err != nil {
			return nil, nil, true, err
		}
		resolvedArgs = append(resolvedArgs, resolved...)
		resolvedTargets = append(resolvedTargets, startupResolvedTargetPaths(resolved)...)
		return resolvedArgs, resolvedTargets, used, nil
	}

	for _, token := range targetTokens {
		resolved, used, err := resolveStartupInitialTargets(resolver, []string{token}, selectedPaths)
		if err != nil {
			return nil, nil, true, err
		}
		resolvedArgs = append(resolvedArgs, resolved...)
		resolvedPaths := startupResolvedTargetPaths(resolved)
		resolvedTargets = append(resolvedTargets, resolvedPaths...)
		selectedPaths = append(selectedPaths, resolvedPaths...)
		usedPicker = usedPicker || used
	}

	for _, query := range includeQueries {
		resolved, err := resolver.resolveInteractiveIncludeTargets(query, selectedPaths)
		if err != nil {
			return nil, nil, true, err
		}
		for _, target := range resolved {
			resolvedArgs = append(resolvedArgs, "--include", target)
			resolvedTargets = append(resolvedTargets, target)
			selectedPaths = append(selectedPaths, target)
		}
		usedPicker = true
	}

	return resolvedArgs, resolvedTargets, usedPicker, nil
}

func resolveStartupInitialTargets(resolver *scopeResolver, args []string, alreadySelected []string) ([]string, bool, error) {
	if selectionContainsAll(alreadySelected) {
		return nil, true, nil
	}
	if len(args) == 0 {
		selected, err := resolver.chooseRootTargetMatches("", "select> ", true, alreadySelected)
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

		selected, err := resolver.chooseRootTargetMatches(query, "select> ", false, selectedPaths)
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
			if i+1 < len(args) {
				paths = append(paths, args[i+1])
				i++
			}
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
	if len(cfg.Scopes) == 0 {
		return nil, nil
	}
	targets := make([]string, 0, len(cfg.Scopes[len(cfg.Scopes)-1].Targets))
	for _, target := range cfg.Scopes[len(cfg.Scopes)-1].Targets {
		target = normalizeRelPath(target)
		if target == "" {
			target = "."
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
	if len(args) == 0 {
		resolvedArgs, resolvedTargets, usedFzf, err := resolveStartupScopeInputs(resolver, nil, nil, nil)
		return resolvedArgs, resolvedTargets, usedFzf, err
	}

	finalArgs := make([]string, 0, len(args))
	currentScopeTargets := make([]string, 0, len(args))
	usedFzf := false
	modifierMode := false
	hadScopeInput := false

	for i := 0; i < len(args); {
		arg := args[i]

		if !modifierMode && !strings.HasPrefix(arg, "-") {
			resolvedArgs, resolvedTargets, targetUsedFzf, err := resolveStartupScopeInputs(resolver, []string{arg}, nil, currentScopeTargets)
			if err != nil {
				return nil, nil, false, err
			}
			finalArgs = append(finalArgs, resolvedArgs...)
			currentScopeTargets = append(currentScopeTargets, resolvedTargets...)
			usedFzf = usedFzf || targetUsedFzf
			hadScopeInput = true
			i++
			continue
		}

		if requireScopeBeforeModifiers && !hadScopeInput && startupLeadingModifierNeedsInitialScope(arg) {
			resolvedArgs, resolvedTargets, targetUsedFzf, err := resolveStartupScopeInputs(resolver, nil, nil, currentScopeTargets)
			if err != nil {
				return nil, nil, false, err
			}
			finalArgs = append(finalArgs, resolvedArgs...)
			currentScopeTargets = append(currentScopeTargets, resolvedTargets...)
			usedFzf = usedFzf || targetUsedFzf
			hadScopeInput = true
			continue
		}

		switch arg {
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-t", "--no-tree", "--preview":
			finalArgs = append(finalArgs, arg)
			i++
		case "--then":
			if requireScopeBeforeModifiers && len(currentScopeTargets) == 0 {
				resolvedArgs, resolvedTargets, targetUsedFzf, err := resolveStartupScopeInputs(resolver, nil, nil, currentScopeTargets)
				if err != nil {
					return nil, nil, false, err
				}
				finalArgs = append(finalArgs, resolvedArgs...)
				currentScopeTargets = append(currentScopeTargets, resolvedTargets...)
				usedFzf = usedFzf || targetUsedFzf
				hadScopeInput = true
			}
			finalArgs = append(finalArgs, "--then")
			currentScopeTargets = currentScopeTargets[:0]
			modifierMode = false
			hadScopeInput = false
			i++
		case "--":
			choice, err := chooseStartupModifier()
			if err != nil {
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
				modifierMode = false
				hadScopeInput = false
				i++
				continue
			}
			if choice.Mode == startupModifierModeGit {
				argsAfterStage, newScopeTargets, stageUsedFzf, consumed, err := resolveStartupModifierStage(resolver, finalArgs, currentScopeTargets, choice.Args, args[i+1:])
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
			argsAfterStage, newScopeTargets, stageUsedFzf, consumed, err := resolveStartupModifierStage(resolver, finalArgs, currentScopeTargets, choice.Args, args[i+1:])
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
		case "--include", "--only", "--exclude", "--contains", "--recent":
			argsAfterStage, newScopeTargets, stageUsedFzf, consumed, err := resolveStartupModifierStage(resolver, finalArgs, currentScopeTargets, []string{arg}, args[i+1:])
			if err != nil {
				return nil, nil, false, err
			}
			finalArgs = argsAfterStage
			currentScopeTargets = newScopeTargets
			usedFzf = usedFzf || stageUsedFzf
			i += 1 + consumed
			modifierMode = true
			hadScopeInput = true
		case "--changed", "--staged", "--unstaged", "--untracked":
			argsAfterStage, newScopeTargets, stageUsedFzf, consumed, err := resolveStartupModifierStage(resolver, finalArgs, currentScopeTargets, []string{arg}, args[i+1:])
			if err != nil {
				return nil, nil, false, err
			}
			finalArgs = argsAfterStage
			currentScopeTargets = newScopeTargets
			usedFzf = usedFzf || stageUsedFzf
			i += 1 + consumed
			modifierMode = true
			hadScopeInput = true
		case "--diff", "--snippet":
			finalArgs = append(finalArgs, arg)
			modifierMode = true
			hadScopeInput = true
			i++
		default:
			switch {
			case strings.HasPrefix(arg, "--contains="):
				return nil, nil, false, newUsageError("Error: --contains requires a space before the pattern.\n  Use: catclip src --contains 'pattern'\n  Not: catclip src --contains='pattern'")
			case strings.HasPrefix(arg, "--recent="):
				return nil, nil, false, newUsageError("Error: --recent requires a space before the value.\n  Use: catclip src --recent 5\n  Or:  catclip src --recent")
			case strings.HasPrefix(arg, "--"):
				return nil, nil, false, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			case strings.HasPrefix(arg, "-") && len(arg) > 1:
				return nil, nil, false, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			default:
				return nil, nil, false, newUsageError("Error: positional targets must come before modifiers.\n  Add targets first, use --include, or use --then for a new scope.")
			}
		}
	}

	if !hadScopeInput {
		resolvedArgs, resolvedTargets, targetUsedFzf, err := resolveStartupScopeInputs(resolver, nil, nil, currentScopeTargets)
		if err != nil {
			return nil, nil, false, err
		}
		finalArgs = append(finalArgs, resolvedArgs...)
		currentScopeTargets = append(currentScopeTargets, resolvedTargets...)
		usedFzf = usedFzf || targetUsedFzf
	}

	return finalArgs, currentScopeTargets, usedFzf, nil
}

func startupLeadingModifierNeedsInitialScope(arg string) bool {
	switch arg {
	case "--then":
		return false
	case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-t", "--no-tree", "--preview":
		return true
	case "--", "--include", "--only", "--exclude", "--contains", "--recent",
		"--changed", "--staged", "--unstaged", "--untracked", "--diff", "--snippet":
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
	fullArgs := append([]string(nil), prefixArgs...)
	switch action {
	case startupTrailingActionModifierMenu:
		fullArgs = append(fullArgs, "--")
	case startupTrailingActionOnly:
		fullArgs = append(fullArgs, "--only")
	case startupTrailingActionExclude:
		fullArgs = append(fullArgs, "--exclude")
	case startupTrailingActionRecent:
		fullArgs = append(fullArgs, "--recent")
	case startupTrailingActionContains:
		fullArgs = append(fullArgs, "--contains")
	default:
		return append([]string(nil), prefixArgs...), false, nil
	}

	resolvedArgs, _, usedFzf, err := resolveStartupArgs(resolver, fullArgs)
	if err != nil {
		return nil, false, err
	}
	return resolvedArgs, usedFzf, nil
}

func resolveBareStartupModifierArgs(resolver *scopeResolver) ([]string, bool, error) {
	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{"--"})
	return args, usedFzf, err
}

func chooseStartupModifier() (startupModifierChoice, error) {
	lines, index := startupModifierChoiceLines(startupModifierChoices)
	bin, err := fuzzyResolverBinary()
	if err != nil {
		return startupModifierChoice{}, err
	}
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Prompt:  "filter> ",
		WithNth: "1,3",
		Nth:     "1",
		Header:  startupModifierPickerHeader(),
		NoSort:  true,
		Lines:   lines,
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

func resolveStartupModifierArgs(resolver *scopeResolver, currentArgs, currentScopeTargets []string) ([]string, bool, error) {
	choice, err := chooseStartupModifier()
	if err != nil {
		return nil, false, err
	}

	finalArgs := append([]string(nil), currentArgs...)
	switch choice.Mode {
	case startupModifierModeThen:
		return append(finalArgs, "--then"), true, nil
	case startupModifierModeInclude:
		args, _, includeUsedFzf, err := resolveStartupScopeInputs(resolver, nil, []string{""}, currentScopeTargets)
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
	case startupModifierModeContains:
		args, containsUsedFzf, err := resolveStartupContainsArgs(finalArgs, false)
		if err != nil {
			return nil, true || containsUsedFzf, err
		}
		if slices.Contains(choice.Args, "--snippet") {
			args = append(args, "--snippet")
		}
		return args, true || containsUsedFzf, nil
	case startupModifierModeContainsSnippet:
		args, containsUsedFzf, err := resolveStartupContainsArgs(finalArgs, true)
		if err != nil {
			return nil, true || containsUsedFzf, err
		}
		return append(args, "--snippet"), true || containsUsedFzf, nil
	case startupModifierModeGit:
		diffPreview := len(choice.Args) > 1 && choice.Args[1] == "--diff"
		args, gitUsedFzf, err := resolveStartupGitScopeArgs(resolver, append(finalArgs, choice.Args[0]), startupGitStagePrompt(choice.Args[0]), nil, true, diffPreview)
		if err != nil {
			return nil, true || gitUsedFzf, err
		}
		if len(choice.Args) > 1 {
			args = append(args, choice.Args[1:]...)
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

func resolveStartupContainsArgs(currentArgs []string, snippet bool) ([]string, bool, error) {
	result, err := chooseContainsMatchesWithFzf("", currentArgs, snippet)
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(result.Query) == "" {
		return nil, false, errSelectionCancelled
	}

	finalArgs := append([]string(nil), currentArgs...)
	finalArgs = append(finalArgs, "--contains", result.Query)
	if containsSelectionIncludesAllRow(result.Matches) {
		return finalArgs, true, nil
	}
	matchPaths, err := containsMatchPathsForArgs(currentArgs, result.Query)
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
	finalArgs := append([]string(nil), currentArgs...)
	finalArgs = append(finalArgs, flag)
	finalArgs = append(finalArgs, stageValues...)
	return finalArgs, usedFzf, nil
}

func resolveStartupModifierStage(resolver *scopeResolver, currentArgs, currentScopeTargets []string, choiceArgs, remaining []string) ([]string, []string, bool, int, error) {
	if len(choiceArgs) == 0 {
		return nil, nil, false, 0, errSelectionCancelled
	}
	flag := choiceArgs[0]

	switch flag {
	case "--include":
		values, consumed := startupStageValues(remaining)
		if len(values) == 0 {
			resolvedArgs, resolvedTargets, usedFzf, err := resolveStartupScopeInputs(resolver, nil, []string{""}, currentScopeTargets)
			if err != nil {
				return nil, nil, false, 0, err
			}
			finalArgs := append(append([]string(nil), currentArgs...), resolvedArgs...)
			return finalArgs, append(append([]string(nil), currentScopeTargets...), resolvedTargets...), usedFzf, 0, nil
		}
		stageValues := make([]string, 0, len(values))
		usedFzf := false
		selectedPaths := append([]string(nil), currentScopeTargets...)
		for _, value := range values {
			resolved, err := resolver.resolveInteractiveIncludeTargets(value, selectedPaths)
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
		stageValues, usedFzf, err := resolveStartupModifierStageValues(currentArgs, flag, startupStagePrompt(flag), values, len(values) == 0, "")
		if err != nil {
			return nil, nil, false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag)
		finalArgs = append(finalArgs, stageValues...)
		return finalArgs, append([]string(nil), currentScopeTargets...), usedFzf, consumed, nil
	case "--recent":
		if len(remaining) == 0 || isModifierBoundaryToken(remaining[0]) {
			args, err := resolveStartupRecentArgs(currentArgs)
			return args, append([]string(nil), currentScopeTargets...), true, 0, err
		}
		limit, err := parseRecentLimitToken(remaining[0])
		if err != nil {
			return nil, nil, false, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, strconv.Itoa(limit))
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--contains":
		if len(remaining) == 0 || isModifierBoundaryToken(remaining[0]) {
			snippetPreview := currentScopeHasFlag(currentArgs, "--snippet") || slices.Contains(choiceArgs[1:], "--snippet")
			args, usedFzf, err := resolveStartupContainsArgs(currentArgs, snippetPreview)
			return args, append([]string(nil), currentScopeTargets...), usedFzf, 0, err
		}
		finalArgs := append(append([]string(nil), currentArgs...), flag, remaining[0])
		return finalArgs, append([]string(nil), currentScopeTargets...), false, 1, nil
	case "--changed", "--staged", "--unstaged", "--untracked":
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
		diffPreview := len(remaining) > 0 && remaining[0] == "--diff" || slices.Contains(choiceArgs[1:], "--diff")
		argsAfterStage, usedFzf, err := resolveStartupGitScopeArgs(resolver, finalArgs, startupGitStagePrompt(flag), nil, true, diffPreview)
		if err != nil {
			return nil, nil, false, 0, err
		}
		return argsAfterStage, append([]string(nil), currentScopeTargets...), usedFzf, 0, nil
	default:
		return nil, nil, false, 0, errSelectionCancelled
	}
}

func startupValidateGitStageArgs(currentArgs []string, flag string, choiceArgs, remaining []string) error {
	if flag != "--untracked" {
		return nil
	}

	hasDiff := currentScopeHasFlag(currentArgs, "--diff") ||
		slices.Contains(choiceArgs[1:], "--diff") ||
		(len(remaining) > 0 && remaining[0] == "--diff")
	if !hasDiff {
		return nil
	}

	if currentScopeHasFlag(currentArgs, "--changed") ||
		currentScopeHasFlag(currentArgs, "--staged") ||
		currentScopeHasFlag(currentArgs, "--unstaged") {
		return nil
	}

	return newUsageError("Error: --untracked --diff doesn't make sense (untracked files have no diff).\n  Try: catclip --changed --diff    (includes untracked as full content)\n  Try: catclip --staged --diff     (only staged patches)")
}

func resolveStartupGitScopeArgs(resolver *scopeResolver, currentArgs []string, prompt string, values []string, allowInteractiveEmpty bool, diffPreview bool) ([]string, bool, error) {
	if !resolver.gitCtx.Enabled {
		return append([]string(nil), currentArgs...), false, nil
	}

	stageFlag := "--changed"
	for i := len(currentArgs) - 1; i >= 0; i-- {
		switch currentArgs[i] {
		case "--changed", "--staged", "--unstaged", "--untracked":
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
	case "--staged":
		return "staged> "
	case "--unstaged":
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

	return append([]string(nil), values...), false, nil
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
			"File-path matches remove files from the current scope.",
			"Enter continues with the current selection as --exclude.",
			fmt.Sprintf("Tab marks files; %s toggles all visible matches.", multiSelectToggleAllKey()),
			"Use Up/Down arrow keys to move, Esc to cancel.",
		)
	case "--changed":
		return pickerHeader(
			"Start from changed files in the current scope, then narrow by path.",
			"Enter continues with the current selection after --changed.",
			fmt.Sprintf("Tab marks files; %s toggles all visible matches.", multiSelectToggleAllKey()),
			"Select [all changed files] to keep plain --changed.",
		)
	case "--staged":
		return pickerHeader(
			"Start from staged files in the current scope, then narrow by path.",
			"Enter continues with the current selection after --staged.",
			fmt.Sprintf("Tab marks files; %s toggles all visible matches.", multiSelectToggleAllKey()),
			"Select [all staged files] to keep plain --staged.",
		)
	case "--unstaged":
		return pickerHeader(
			"Start from unstaged files in the current scope, then narrow by path.",
			"Enter continues with the current selection after --unstaged.",
			fmt.Sprintf("Tab marks files; %s toggles all visible matches.", multiSelectToggleAllKey()),
			"Select [all unstaged files] to keep plain --unstaged.",
		)
	case "--untracked":
		return pickerHeader(
			"Start from untracked files in the current scope, then narrow by path.",
			"Enter continues with the current selection after --untracked.",
			fmt.Sprintf("Tab marks files; %s toggles all visible matches.", multiSelectToggleAllKey()),
			"Select [all untracked files] to keep plain --untracked.",
		)
	default:
		return pickerHeader(
			"File-path matches keep only those files in the current scope.",
			"Enter continues with the current selection as --only.",
			fmt.Sprintf("Tab marks files; %s toggles all visible matches.", multiSelectToggleAllKey()),
			"Use Up/Down arrow keys to move, Esc to cancel.",
		)
	}
}

func startupScopeFileSetPaths(currentArgs []string) ([]string, error) {
	cfg, err := parseArgs(currentArgs)
	if err != nil {
		return nil, err
	}
	gitCtx := detectGitContext(cfg.WorkingDir)
	baseRules, err := loadIgnoreRules()
	if err != nil {
		return nil, err
	}
	scopeIndex := len(cfg.Scopes) - 1
	entries, _, _, _, err := evaluateScope(cfg, gitCtx, scopeIndex, cfg.Scopes[scopeIndex], baseRules, io.Discard, colorPalette{})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(entries))
	relPaths := make([]string, 0, len(entries))
	for _, entry := range entries {
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
	rows = append(rows, startupFilePathRows(relPaths)...)
	if flag == "--only" || flag == "--exclude" {
		rows = append(rows, startupExtensionPatternRows(relPaths)...)
	}
	return rows
}

func startupFileSetPreviewCommand(currentArgs []string, flag string, diffPreview bool) string {
	switch flag {
	case "--changed", "--staged", "--unstaged":
		if diffPreview || currentScopeHasFlag(currentArgs, "--diff") {
			return fzfDiffFilePreviewCommand(flag)
		}
	}
	return fzfFileSetPreviewCommand()
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

func startupFilePathRows(relPaths []string) []startupFileSetRow {
	rows := make([]startupFileSetRow, 0, len(relPaths))
	for _, relPath := range relPaths {
		rows = append(rows, startupFileSetRow{
			Kind:          startupFileSetRowFile,
			Display:       pathBase(relPath),
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

func containsSelectionIncludesAllRow(selected []string) bool {
	for _, value := range selected {
		if value == containsAllMatchesLabel {
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
	return chooseManyWithFzfOptions(bin, query, prompt, "1,2", "1,2", header, previewCommand, formatStartupFileSetRows(rows))
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
		"Choose the next filter.",
		"Enter inserts the selected filter at this point.",
		"Some filters will ask for a pattern, search, or number next.",
		"Use Up/Down arrow keys to move, Esc to cancel.",
	)
}
