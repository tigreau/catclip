package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

type startupModifierMode string

const (
	startupModifierModeFlags       startupModifierMode = "flags"
	startupModifierModeFinish      startupModifierMode = "finish"
	startupModifierModeExtras      startupModifierMode = "extras"
	startupModifierModeThen        startupModifierMode = "then"
	startupModifierModeInclude     startupModifierMode = "include"
	startupModifierModeOnly        startupModifierMode = "only"
	startupModifierModeExclude     startupModifierMode = "exclude"
	startupModifierModeRecent      startupModifierMode = "recent"
	startupModifierModeSize        startupModifierMode = "size"
	startupModifierModeDepth       startupModifierMode = "depth"
	startupModifierModeContains    startupModifierMode = "contains"
	startupModifierModeNotContains startupModifierMode = "not-contains"
	startupModifierModeSnippet     startupModifierMode = "snippet"
	startupModifierModeLines       startupModifierMode = "lines"
	startupModifierModeGit         startupModifierMode = "git"
)

type startupTrailingAction string

const (
	startupTrailingActionNone         startupTrailingAction = ""
	startupTrailingActionModifierMenu startupTrailingAction = "modifier-menu"
	StartupTrailingActionExclude      startupTrailingAction = "exclude"
	startupTrailingActionRecent       startupTrailingAction = "recent"
	StartupTrailingActionContains     startupTrailingAction = "contains"
	StartupTrailingActionNotContains  startupTrailingAction = "not-contains"
	StartupTrailingActionSnippet      startupTrailingAction = "snippet"
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

type StartupModifierChoice struct {
	Key         string
	Label       string
	Description string
	Args        []string
	Mode        startupModifierMode
}

type startupSnippetBoundaryChoice struct {
	Key                 string
	Label               string
	Description         string
	SnippetContextSet   bool
	SnippetContextLines int
}

type StartupPickerResult struct {
	Args                 []string
	UsedFzf              bool
	PreparedOutput       *StartupPreparedOutputState
	ForceResolvedCommand bool
}

type startupCurrentScopeState struct {
	Known                   bool
	Empty                   bool
	NeedsInclude            bool
	HasScopedIgnoredTargets bool
	GitKnown                bool
	AnyChanged              bool
	AllChanged              bool
	AnyStaged               bool
	AllStaged               bool
	AnyUnstaged             bool
	AllUnstaged             bool
	AnyUntracked            bool
	AllUntracked            bool
	Scopes                  []command.ExecutionScope
	// GitStatusMap is the porcelain map (workPath → "S"/"M"/"SM"/"?")
	// collected once in startupCurrentScopeStateForArgs and reused by
	// startupModifierCurrentScopePreviewCommand so the checkpoint write
	// doesn't repeat the git status call. nil when git is disabled or
	// the scope is empty (the caller falls back to a fresh fetch).
	GitStatusMap map[string]string
}

func maybeResolveStartupPickerArgs(args []string) (StartupPickerResult, bool, error) {
	return maybeResolveStartupInteractiveArgs(args, false)
}

func MaybeResolveStartupPickerAndSinkArgs(args []string) (StartupPickerResult, bool, error) {
	return maybeResolveStartupInteractiveArgs(args, true)
}

func maybeResolveStartupInteractiveArgs(args []string, includeSink bool) (StartupPickerResult, bool, error) {
	if rawArgsHasHeadless(args) {
		return StartupPickerResult{}, false, nil
	}
	if rawArgsUseStdinPathValues(args) {
		return StartupPickerResult{}, false, nil
	}
	if !platform.CanPromptInteractively() {
		return StartupPickerResult{}, false, nil
	}

	enabled, err := shouldUseStartupPicker(args)
	if err != nil {
		return StartupPickerResult{}, true, err
	}
	if !enabled {
		return StartupPickerResult{}, false, nil
	}
	if err := cli.ValidateStartupPreflightArgs(args); err != nil {
		return StartupPickerResult{}, true, err
	}

	resolver, err := newStartupPickerResolverForArgs(args)
	if err != nil {
		return StartupPickerResult{}, true, err
	}
	direct, err := startupCommandCanRunDirectly(resolver, args)
	if err != nil {
		return StartupPickerResult{}, true, err
	}
	if direct {
		return StartupPickerResult{}, false, nil
	}

	if includeSink {
		result, err := resolveStartupPickerResultWithUndo(resolver, args)
		if err != nil {
			if errors.Is(err, discovery.ErrSelectionCancelled) {
				return StartupPickerResult{}, true, nil
			}
			return StartupPickerResult{}, true, err
		}
		return result, true, nil
	}

	resolvedArgs, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, args)
	if err != nil {
		if errors.Is(err, discovery.ErrSelectionCancelled) {
			return StartupPickerResult{}, true, nil
		}
		return StartupPickerResult{}, true, err
	}
	return StartupPickerResult{Args: resolvedArgs, UsedFzf: usedFzf}, true, nil
}

func newStartupPickerResolver() (*discovery.Resolver, error) {
	cfg, err := cli.ParseArgs([]string{"."})
	if err != nil {
		return nil, err
	}
	gitCtx := git.Detect(cfg.WorkingDir)
	return &discovery.Resolver{
		Cfg:               invocationConfigFromParsedCommand(cfg),
		GitCtx:            gitCtx,
		AllowFileSymlinks: false,
	}, nil
}

func newStartupPickerResolverForArgs(args []string) (*discovery.Resolver, error) {
	resolver, err := newStartupPickerResolver()
	if err != nil {
		return nil, err
	}
	for _, arg := range args {
		if arg != "--with-binaries" {
			continue
		}
		resolver.WithBinaries = true
		resolver.Cfg.WithBinaries = true
		break
	}
	return resolver, nil
}

func startupCommandCanRunDirectly(resolver *discovery.Resolver, args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if _, action, ok := detectStartupTrailingAction(args); ok && action != startupTrailingActionNone {
		return false, nil
	}
	if startupHasUnresolvedScope(args) {
		return false, nil
	}
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return false, nil
	}
	// Probe every target once before resolving modifier values. Missing targets
	// bypass startup pickers immediately because no modifier can make an absent
	// path selectable. Blocked targets are decided after include resolution:
	// an unresolved --include query may open the ignored-target picker and
	// authorize an existing blocked target. Ambiguous targets retain the mixed
	// ranked matches that require a picker. The complete inventory built by a
	// scope copy is safe to publish back to the base resolver for the eventual
	// picker.
	scopeSpecs := cfg.Command.Scopes()
	probesByScope := make([][]discovery.StartupTargetProbe, len(scopeSpecs))
	for scopeIndex, scopeSpec := range scopeSpecs {
		scopeResolverCopy := *resolver
		scopeTargets := scopeSpec.Targets()
		if len(scopeSpec.IncludedTargets()) > 0 {
			scopeResolverCopy.IncludedTargets = discovery.BuildIncludedTargetSet(scopeResolverCopy.Cfg.WorkingDir, scopeSpec.IncludedTargets())
		}
		for _, target := range scopeTargets {
			probe, err := scopeResolverCopy.ProbeStartupTarget(target)
			if err != nil {
				return false, err
			}
			resolver.AdoptVisibleTargetInventoryFrom(&scopeResolverCopy)
			probesByScope[scopeIndex] = append(probesByScope[scopeIndex], probe)
			if probe.Outcome == discovery.StartupTargetMissing ||
				(probe.Outcome == discovery.StartupTargetBlocked && len(scopeSpec.IncludedTargets()) == 0) {
				return true, nil
			}
		}
	}
	needsFileSetResolution, err := startupArgsNeedFileSetResolution(args)
	if err != nil {
		return false, err
	}
	if needsFileSetResolution {
		return false, nil
	}
	for scopeIndex, scopeSpec := range scopeSpecs {
		scopeResolverCopy := *resolver
		scopeTargets := scopeSpec.Targets()
		if len(scopeSpec.IncludedTargets()) > 0 && !discovery.IncludeTargetsContainWildcard(scopeSpec.IncludedTargets()) {
			includeTargets := scopeSpec.IncludedTargets()
			_, unresolvedIncludeQueries, err := scopeResolverCopy.ResolveExactIgnoredIncludeTargets(includeTargets, scopeTargets)
			if err != nil {
				return false, err
			}
			// Salvage unresolved queries that are concrete on-disk paths.
			// `ResolveExactIgnoredIncludeTargets`'s scope-target filter
			// drops include values unrelated to the scope target — it
			// assumes the include lives inside (or is an ancestor of)
			// the scope. For the basename + `--include` case, the include
			// names a *separate* ignored dir that *authorizes* finding
			// the basename elsewhere — the filter rejects it as
			// "unrelated" and the picker would open even though the
			// include is concrete. A query that exists on disk as a
			// regular file or directory is concrete enough to treat as
			// an exact ignored target; queries that don't exist at the
			// working-dir level (the truly ambiguous, picker-needs-help
			// case) stay unresolved and continue to route through the
			// picker.
			stillUnresolved := unresolvedIncludeQueries[:0]
			for _, q := range unresolvedIncludeQueries {
				normalized := normalizeRelPath(q)
				if normalized == "" {
					stillUnresolved = append(stillUnresolved, q)
					continue
				}
				abs := filepath.Join(scopeResolverCopy.Cfg.WorkingDir, filepath.FromSlash(normalized))
				if info, err := os.Stat(abs); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
					continue
				}
				stillUnresolved = append(stillUnresolved, q)
			}
			if len(stillUnresolved) > 0 {
				return false, nil
			}
		}
		for _, probe := range probesByScope[scopeIndex] {
			if probe.BypassesPicker() {
				return true, nil
			}
			if probe.RequiresPicker() {
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
			values, next := cli.ConsumeModifierValues(args, i+1)
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
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--preview", "--with-binaries":
			continue
		case "--then":
			if !scopeHasExplicitTarget {
				return true
			}
			scopeHasExplicitTarget = false
			continue
		case "--include", "--only", "--exclude", "--contains", "--not-contains", "--snippet", "--depth":
			if !scopeHasExplicitTarget {
				return true
			}
			if args[i] == "--include" || args[i] == "--only" || args[i] == "--exclude" {
				_, next := cli.ConsumeModifierValues(args, i+1)
				i = next - 1
				continue
			}
			if args[i] == "--depth" {
				if i+1 < len(args) {
					if _, err := cli.ParseDepthToken(args[i+1]); err == nil {
						i++
					}
				}
				continue
			}
			if i+1 < len(args) {
				i++
				if args[i-1] == "--snippet" && i+1 < len(args) {
					if _, err := strconv.Atoi(args[i+1]); err == nil {
						i++
					}
				}
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
			if i+1 < len(args) && !cli.IsModifierBoundaryToken(args[i+1]) {
				if _, err := cli.ParseRecentLimitToken(args[i+1]); err == nil {
					i++
				}
			}
			continue
		case "--size":
			if !scopeHasExplicitTarget {
				return true
			}
			for consumed := 0; consumed < 2 && i+1 < len(args) && !cli.IsModifierBoundaryToken(args[i+1]); consumed++ {
				if _, err := cli.ParseSizeBoundToken(args[i+1]); err != nil {
					break
				}
				i++
			}
			continue
		case "--lines":
			if !scopeHasExplicitTarget {
				return true
			}
			for i+1 < len(args) {
				if _, err := strconv.Atoi(args[i+1]); err != nil {
					break
				}
				i++
			}
			continue
		default:
			if cli.EqualsFormRejectionError(arg) != nil {
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
		if strings.HasPrefix(arg, "--internal-") {
			return false, nil
		}
		switch arg {
		case "-h", "--help", "--help-all", "--version", "-V", "--hiss", "--hiss-reset", "--all-ignore-rules",
			"--input-dir", "--input-stem":
			return false, nil
		case "--include", "--only", "--exclude", "--contains", "--not-contains", "--snippet", "--depth":
			if arg == "--depth" {
				if i+1 < len(args) {
					if _, err := cli.ParseDepthToken(args[i+1]); err == nil {
						i++
					}
				}
				continue
			}
			if arg == "--contains" || arg == "--not-contains" || arg == "--snippet" {
				if i+1 < len(args) {
					i++
					if arg == "--snippet" && i+1 < len(args) {
						if _, err := strconv.Atoi(args[i+1]); err == nil {
							i++
						}
					}
				}
				continue
			}
			_, next := cli.ConsumeModifierValues(args, i+1)
			i = next - 1
			continue
		case "--size":
			for consumed := 0; consumed < 2 && i+1 < len(args) && !cli.IsModifierBoundaryToken(args[i+1]); consumed++ {
				if _, err := cli.ParseSizeBoundToken(args[i+1]); err != nil {
					return false, nil
				}
				i++
			}
			continue
		case "--paths":
			continue
		case "--lines":
			for i+1 < len(args) {
				if _, err := strconv.Atoi(args[i+1]); err != nil {
					break
				}
				i++
			}
			continue
		case "--recent":
			if i+1 < len(args) && !cli.IsModifierBoundaryToken(args[i+1]) {
				if _, err := cli.ParseRecentLimitToken(args[i+1]); err != nil {
					return false, nil
				}
				i++
			}
			continue
		case "--then":
			continue
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree",
			"--no-bundle", "--preview", "--with-binaries", "--changed", "--staged", "--unstaged", "--untracked",
			"--changed-diff", "--staged-diff", "--unstaged-diff", "--", "--diff":
			continue
		}
		if cli.EqualsFormRejectionError(arg) != nil {
			return true, nil
		}
		if strings.HasPrefix(arg, "--") || (strings.HasPrefix(arg, "-") && len(arg) > 1) {
			return false, nil
		}
		if filepath.IsAbs(arg) {
			return false, newUsageError("Error: Absolute paths not allowed: %s\n  Use a relative path from your project root instead.", discovery.SingleQuoted(arg))
		}
		if discovery.ContainsParentTraversal(arg) {
			return false, newUsageError("Error: Cannot traverse above working directory: %s\n  catclip only operates within the current directory tree.\n  Use a relative path from your project root instead.\n  Example: catclip config/", discovery.SingleQuoted(arg))
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
				if err := cli.ValidateIncludeValues([]string{tokens[i]}); err != nil {
					return startupInputParse{}, err
				}
				parsed.includeQueries = append(parsed.includeQueries, tokens[i])
				continue
			}
			return startupInputParse{}, cli.RequiredStageValueError("--include")
		}
		if !seenModifier && !strings.HasPrefix(tokens[i], "-") {
			parsed.targets = append(parsed.targets, tokens[i])
			continue
		}

		seenModifier = true
		switch tokens[i] {
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree",
			"--no-bundle", "--preview", "--with-binaries", "--changed", "--staged", "--unstaged", "--untracked",
			"--changed-diff", "--staged-diff", "--unstaged-diff", "--paths":
			parsed.modifiers = append(parsed.modifiers, tokens[i])
		case "--lines":
			parsed.modifiers = append(parsed.modifiers, tokens[i])
			for i+1 < len(tokens) {
				if _, err := strconv.Atoi(tokens[i+1]); err != nil {
					break
				}
				i++
				parsed.modifiers = append(parsed.modifiers, tokens[i])
			}
		case "--only", "--exclude", "--contains", "--not-contains", "--snippet", "--depth":
			flag := tokens[i]
			if i+1 >= len(tokens) {
				switch flag {
				case "--only":
					return startupInputParse{}, cli.RequiredStageValueError("--only")
				case "--exclude":
					return startupInputParse{}, cli.RequiredStageValueError("--exclude")
				case "--snippet":
					return startupInputParse{}, cli.RequiredStageValueError("--snippet")
				case "--depth":
					return startupInputParse{}, cli.RequiredStageValueError("--depth")
				case "--not-contains":
					return startupInputParse{}, cli.NotContainsMissingPatternError(tokens, i)
				default:
					return startupInputParse{}, cli.ContainsMissingPatternError(tokens, i)
				}
			}
			if flag == "--depth" {
				if _, err := cli.ParseDepthToken(tokens[i+1]); err != nil {
					return startupInputParse{}, err
				}
			}
			parsed.modifiers = append(parsed.modifiers, flag, tokens[i+1])
			i++
			if flag == "--snippet" && i+1 < len(tokens) {
				if n, err := strconv.Atoi(tokens[i+1]); err == nil {
					if n < 0 || n > snippetContextMax {
						return startupInputParse{}, newUsageError("Error: --snippet context must be between 0 and %d (got %d).\n  Use: --snippet 'REGEX' N for N lines around each match (0 = matching line only).", snippetContextMax, n)
					}
					i++
					parsed.modifiers = append(parsed.modifiers, strconv.Itoa(n))
				}
			}
		case "--recent":
			parsed.modifiers = append(parsed.modifiers, tokens[i])
			if i+1 >= len(tokens) || cli.IsModifierBoundaryToken(tokens[i+1]) {
				continue
			}
			if _, err := cli.ParseRecentLimitToken(tokens[i+1]); err != nil {
				return startupInputParse{}, err
			}
			parsed.modifiers = append(parsed.modifiers, tokens[i+1])
			i++
		case "--size":
			parsed.modifiers = append(parsed.modifiers, tokens[i])
			consumed := 0
			nums := make([]int, 0, 2)
			for consumed < 2 && i+1 < len(tokens) && !cli.IsModifierBoundaryToken(tokens[i+1]) {
				i++
				n, err := cli.ParseSizeBoundToken(tokens[i])
				if err != nil {
					return startupInputParse{}, err
				}
				nums = append(nums, n)
				parsed.modifiers = append(parsed.modifiers, tokens[i])
				consumed++
			}
			if i+1 < len(tokens) && !cli.IsModifierBoundaryToken(tokens[i+1]) {
				return startupInputParse{}, cli.SizeTooManyValuesError(tokens[i+1])
			}
			if err := cli.ValidateSizeBounds(nums); err != nil {
				return startupInputParse{}, err
			}
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
			case strings.HasPrefix(tokens[i], "--not-contains="):
				return startupInputParse{}, newUsageError("Error: --not-contains requires a space before the pattern.\n  Use: --not-contains 'pattern'")
			case strings.HasPrefix(tokens[i], "--snippet="):
				return startupInputParse{}, newUsageError("Error: --snippet requires a space before the pattern.\n  Use: --snippet 'pattern'")
			case strings.HasPrefix(tokens[i], "--recent="):
				return startupInputParse{}, newUsageError("Error: --recent requires a space before the value.\n  Use: --recent 5\n  Or:  --recent")
			case strings.HasPrefix(tokens[i], "--size="):
				return startupInputParse{}, cli.SizeEqualsFormError()
			case strings.HasPrefix(tokens[i], "--depth="):
				return startupInputParse{}, newUsageError("Error: --depth requires a space before the value.\n  Use: --depth 2")
			case strings.HasPrefix(tokens[i], "--"):
				return startupInputParse{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", discovery.SingleQuoted(tokens[i]))
			case strings.HasPrefix(tokens[i], "-") && len(tokens[i]) > 1:
				return startupInputParse{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", discovery.SingleQuoted(tokens[i]))
			default:
				return startupInputParse{}, cli.PositionalAfterModifierError()
			}
		}
	}
	return parsed, nil
}

func resolveStartupScopeInputs(resolver *discovery.Resolver, targetTokens, includeQueries, alreadySelected, explicitTargets []string) ([]string, []string, []string, bool, error) {
	return resolveStartupScopeInputsWithPrompt(resolver, targetTokens, includeQueries, alreadySelected, explicitTargets, "select> ")
}

func resolveStartupScopeInputsWithPrompt(resolver *discovery.Resolver, targetTokens, includeQueries, alreadySelected, explicitTargets []string, prompt string) ([]string, []string, []string, bool, error) {
	selectedPaths := append([]string(nil), alreadySelected...)
	previouslyIncluded := selectionPathsExcludingExplicitTargets(alreadySelected, explicitTargets)
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
	exactIncludedTargets, unresolvedIncludeQueries, err := resolver.ResolveExactIgnoredIncludeTargets(includeQueries, scopeTargetsForInclude)
	if err != nil {
		return nil, nil, nil, false, err
	}
	resolvedGroups := make([][]string, len(unresolvedIncludeQueries))
	resolveFrom := 0
	var includedTargets []string
resolveIncludes:
	for {
		includedTargets = append([]string(nil), exactIncludedTargets...)
		selectedIncludes := append(append([]string(nil), previouslyIncluded...), exactIncludedTargets...)
		for i := 0; i < resolveFrom; i++ {
			includedTargets = append(includedTargets, resolvedGroups[i]...)
			selectedIncludes = append(selectedIncludes, resolvedGroups[i]...)
		}
		for resolveFrom < len(unresolvedIncludeQueries) {
			query := unresolvedIncludeQueries[resolveFrom]
			covered, err := resolver.InteractiveIgnoredQueryCoveredBySelection(query, selectedIncludes, selectedExplicitTargets, scopeTargetsForInclude)
			if err != nil {
				return nil, nil, nil, usedPicker, err
			}
			if covered {
				resolvedGroups[resolveFrom] = nil
				resolveFrom++
				continue
			}
			resolved, err := resolver.ResolveInteractiveIncludeTargets(query, selectedIncludes, selectedExplicitTargets, scopeTargetsForInclude)
			if err != nil {
				if errors.Is(err, discovery.ErrSelectionCancelled) {
					back := previousResolvedIncludeGroup(resolvedGroups, resolveFrom)
					if back >= 0 {
						clearResolvedIncludeGroupsFrom(resolvedGroups, back)
						resolveFrom = back
						continue resolveIncludes
					}
				}
				return nil, nil, nil, true, err
			}
			if len(resolved) == 0 {
				back := previousResolvedIncludeGroup(resolvedGroups, resolveFrom)
				if back >= 0 {
					clearResolvedIncludeGroupsFrom(resolvedGroups, back)
					resolveFrom = back
					continue resolveIncludes
				}
				return nil, nil, nil, true, discovery.ErrSelectionCancelled
			}
			resolvedGroups[resolveFrom] = append([]string(nil), resolved...)
			includedTargets = append(includedTargets, resolved...)
			selectedIncludes = append(selectedIncludes, resolved...)
			usedPicker = true
			resolveFrom++
		}
		break
	}
	resolvedTargets = append(resolvedTargets, includedTargets...)
	if len(includedTargets) > 0 {
		resolvedArgs = append(resolvedArgs, "--include")
		resolvedArgs = append(resolvedArgs, includedTargets...)
	}

	return resolvedArgs, resolvedTargets, resolvedExplicitTargets, usedPicker, nil
}

// selectionPathsExcludingExplicitTargets keeps prior include selections while
// removing one occurrence of each positional target. The startup frame stores
// both in one ordered list; include pickers must not hide a blocked positional
// target that the current include query is supposed to authorize.
func selectionPathsExcludingExplicitTargets(selected, explicit []string) []string {
	remainingExplicit := make(map[string]int, len(explicit))
	for _, value := range explicit {
		remainingExplicit[normalizeRelPath(value)]++
	}
	includes := make([]string, 0, len(selected))
	for _, value := range selected {
		normalized := normalizeRelPath(value)
		if remainingExplicit[normalized] > 0 {
			remainingExplicit[normalized]--
			continue
		}
		includes = append(includes, value)
	}
	return includes
}

func resolveStartupInitialTargets(resolver *discovery.Resolver, args []string, alreadySelected []string, prompt string) ([]string, bool, error) {
	if discovery.SelectionContainsAll(alreadySelected) {
		return nil, true, nil
	}
	if len(args) == 0 {
		selected, err := resolver.ChooseRootTargetMatches("", prompt, true, alreadySelected)
		if err != nil {
			return nil, true, err
		}
		if len(selected) == 0 {
			return nil, true, discovery.ErrSelectionCancelled
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

		if cli.HasGlobChars(arg) {
			resolved = append(resolved, arg)
			continue
		}

		exists, err := resolver.TargetPathExists(normalized)
		if err != nil {
			return nil, usedPicker, err
		}
		if exists {
			if discovery.CoveredBySelection(normalized, selectedPaths) {
				continue
			}
			resolved = append(resolved, normalized)
			continue
		}

		covered, err := resolver.InteractiveQueryCoveredBySelection(query, selectedPaths)
		if err != nil {
			return nil, usedPicker, err
		}
		if covered {
			continue
		}

		selected, err := resolver.ChooseRootTargetMatches(query, prompt, false, selectedPaths)
		if err != nil {
			return nil, true, err
		}
		if len(selected) == 0 {
			return nil, true, discovery.ErrSelectionCancelled
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

func targetMatchArgs(matches []discovery.TargetMatch) []string {
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
			values, next := cli.ConsumeModifierValues(args, i+1)
			paths = append(paths, values...)
			i = next - 1
			continue
		}
		paths = append(paths, args[i])
	}
	return paths
}

func startupCurrentScopeIncludedTargetPaths(args []string) ([]string, error) {
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return nil, err
	}
	scopeSpecs := cfg.Command.Scopes()
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

func resolveStartupArgs(resolver *discovery.Resolver, args []string) ([]string, []string, bool, error) {
	return resolveStartupArgsWithMode(resolver, args, false)
}

func resolveInteractiveStartupArgs(resolver *discovery.Resolver, args []string) ([]string, []string, bool, error) {
	return resolveStartupArgsWithUndo(resolver, args)
}

func resolveStartupArgsWithMode(resolver *discovery.Resolver, args []string, requireScopeBeforeModifiers bool) ([]string, []string, bool, error) {
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
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--preview", "--with-binaries":
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
			if choice.Mode == startupModifierModeFinish {
				i = len(args)
				modifierMode = true
				hadScopeInput = true
				continue
			}
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
		case "--include", "--only", "--exclude", "--contains", "--not-contains", "--snippet", "--recent", "--size", "--depth", "--paths", "--lines":
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
			if err := cli.EqualsFormRejectionError(arg); err != nil {
				return nil, nil, false, err
			}
			switch {
			case strings.HasPrefix(arg, "--"):
				return nil, nil, false, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", discovery.SingleQuoted(arg))
			case strings.HasPrefix(arg, "-") && len(arg) > 1:
				return nil, nil, false, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", discovery.SingleQuoted(arg))
			default:
				return nil, nil, false, cli.PositionalAfterModifierError()
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
	case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--preview", "--with-binaries":
		return true
	case "--", "--include", "--only", "--exclude", "--contains", "--not-contains", "--snippet", "--recent", "--size", "--depth", "--paths",
		"--changed", "--staged", "--unstaged", "--untracked",
		"--changed-diff", "--staged-diff", "--unstaged-diff":
		return true
	default:
		return strings.HasPrefix(arg, "--")
	}
}

func pathBase(relPath string) string {
	parts := strings.Split(relPath, "/")
	return parts[len(parts)-1]
}

func resolveStartupTrailingActionArgs(resolver *discovery.Resolver, prefixArgs []string, action startupTrailingAction) ([]string, bool, error) {
	for len(prefixArgs) > 0 && prefixArgs[len(prefixArgs)-1] == "--" {
		prefixArgs = prefixArgs[:len(prefixArgs)-1]
	}
	switch action {
	case startupTrailingActionModifierMenu:
		args, _, usedFzf, err := resolveStartupArgs(resolver, append(append([]string(nil), prefixArgs...), "--"))
		return args, usedFzf, err
	case StartupTrailingActionExclude:
		args, usedFzf, err := resolveStartupScopeFileSetArgs(prefixArgs, "--exclude", "exclude> ")
		return args, usedFzf, err
	case startupTrailingActionRecent:
		args, err := resolveStartupRecentArgs(prefixArgs)
		return args, true, err
	case StartupTrailingActionContains:
		args, usedFzf, err := resolveStartupContentArgs(prefixArgs, "--contains")
		return args, usedFzf, err
	case StartupTrailingActionNotContains:
		args, usedFzf, err := resolveStartupContentArgs(prefixArgs, "--not-contains")
		return args, usedFzf, err
	case StartupTrailingActionSnippet:
		args, usedFzf, err := resolveStartupContentArgs(prefixArgs, "--snippet")
		return args, usedFzf, err
	default:
		return append([]string(nil), prefixArgs...), false, nil
	}
}

func resolveBareStartupModifierArgs(resolver *discovery.Resolver) ([]string, bool, error) {
	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{"--"})
	return args, usedFzf, err
}

func trimTrailingModifierPlaceholders(args []string) []string {
	for len(args) > 0 && args[len(args)-1] == "--" {
		args = args[:len(args)-1]
	}
	return args
}

func chooseStartupModifier(currentArgs []string) (StartupModifierChoice, error) {
	return chooseStartupModifierWithEscHint(currentArgs, "")
}

func chooseStartupModifierWithEscHint(currentArgs []string, escHint string) (StartupModifierChoice, error) {
	for {
		choice, err := chooseStartupModifierChoiceWithEscHint(currentArgs, escHint)
		if err != nil {
			return StartupModifierChoice{}, err
		}
		if choice.Mode != startupModifierModeExtras {
			return choice, nil
		}
		args, err := chooseStartupExtrasWithEscHint(escHint)
		if errors.Is(err, discovery.ErrSelectionCancelled) {
			// extras> is a submenu. Esc returns to filter>; Esc from filter>
			// itself still exits/undoes through the normal frame handling.
			continue
		}
		if err != nil {
			return StartupModifierChoice{}, err
		}
		return StartupModifierChoice{
			Key:         "extras-selected",
			Label:       "[extras]",
			Description: "selected extras",
			Args:        args,
			Mode:        startupModifierModeFlags,
		}, nil
	}
}

func chooseStartupModifierChoiceWithEscHint(currentArgs []string, escHint string) (StartupModifierChoice, error) {
	finishBench := platform.InternalBenchSpan("ui.startup.modifier_picker")
	finishStateBench := platform.InternalBenchSpan("ui.startup.modifier_picker.scope_state")
	state, view, err := startupCurrentScopeStateForArgs(currentArgs)
	finishStateBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(view.Entries)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return StartupModifierChoice{}, err
	}
	if state.Known && state.Empty && !state.NeedsInclude {
		finishBench("err", "false", "no_files", "true")
		return StartupModifierChoice{}, startupNoFilesMatchedError(state.Scopes)
	}
	lines, index := startupModifierChoiceLines(startupAvailableModifierChoicesWithState(currentArgs, state))
	if len(lines) == 0 {
		finishBench("err", "false", "no_choices", "true")
		return StartupModifierChoice{}, discovery.ErrSelectionCancelled
	}
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return StartupModifierChoice{}, err
	}
	finishPrepBench := platform.InternalBenchSpan("ui.startup.modifier_picker.prepare_preview",
		"entries", platform.InternalBenchInt(len(view.Entries)),
	)
	previewCmd, previewTmpdir := startupModifierCurrentScopePreviewCommand(state, view)
	finishPrepBench(
		"has_preview", platform.InternalBenchBool(previewCmd != ""),
	)
	if previewTmpdir != "" {
		defer os.RemoveAll(previewTmpdir)
	}
	req := picker.Request{
		Prompt:  "filter> ",
		WithNth: "1,3",
		Nth:     "1",
		Header:  startupModifierPickerHeaderWithEscHint(escHint),
		NoSort:  true,
		Lines:   lines,
	}
	if previewCmd != "" {
		// The modifier menu's preview is static — the scope cannot change
		// while the menu is open. Pin it via start:preview(...) so fzf
		// renders it once at startup instead of re-evaluating on every
		// focus change.
		req.PreviewWindow = picker.DefaultPreviewWindow
		req.Bindings = append(req.Bindings, "start:preview("+previewCmd+")")
	}
	result, err := picker.Run(bin, themedFzfRequest(req))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		finishBench("err", "false", "cancelled", "true")
		return StartupModifierChoice{}, discovery.ErrSelectionCancelled
	}
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return StartupModifierChoice{}, err
	}
	if len(result.Matches) == 0 {
		finishBench("err", "false", "no_match", "true")
		return StartupModifierChoice{}, discovery.ErrSelectionCancelled
	}
	selected := result.Matches[0]
	choice, ok := index[selected]
	if !ok {
		finishBench("err", "false", "unmapped_match", "true")
		return StartupModifierChoice{}, discovery.ErrSelectionCancelled
	}
	finishBench("err", "false", "choice", choice.Key)
	return choice, nil
}

func chooseStartupExtrasWithEscHint(escHint string) ([]string, error) {
	lines, index := startupModifierChoiceLines(startupExtrasChoices)
	if len(lines) == 0 {
		return nil, discovery.ErrSelectionCancelled
	}
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return nil, err
	}
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Prompt:   "extras> ",
		WithNth:  "1,3",
		Nth:      "1",
		Header:   startupExtrasPickerHeaderWithEscHint(escHint),
		Multi:    true,
		Bindings: discovery.MultiSelectPickerBindings(),
		NoSort:   true,
		Lines:    lines,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return nil, discovery.ErrSelectionCancelled
	}
	if err != nil {
		return nil, err
	}
	if len(result.Matches) == 0 {
		return nil, discovery.ErrSelectionCancelled
	}
	out := make([]string, 0, len(result.Matches))
	seen := make(map[string]struct{}, len(result.Matches))
	for _, match := range result.Matches {
		choice, ok := index[match]
		if !ok || len(choice.Args) == 0 {
			continue
		}
		for _, arg := range choice.Args {
			if _, dup := seen[arg]; dup {
				continue
			}
			seen[arg] = struct{}{}
			out = append(out, arg)
		}
	}
	if len(out) == 0 {
		return nil, discovery.ErrSelectionCancelled
	}
	return out, nil
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

// startupCurrentScopeStateForArgs returns both the menu-driving state and
// the underlying resolved view. The view is returned so the modifier-menu
// preview-command builder can write a checkpoint without re-running
// discovery (the bug the Windows latency trace surfaced in v0.6.0: the
// modifier-menu tree preview child was repeating ~1.3s of rg.text_files
// work the parent had just finished). Callers that only need the state
// can discard the view with `_`. A zero view is returned when the state
// is unknown (no scopes yet).
func startupCurrentScopeStateForArgs(currentArgs []string) (startupCurrentScopeState, resolvedScopeView, error) {
	view, ok, err := startupResolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil {
		return startupCurrentScopeState{}, resolvedScopeView{}, err
	}
	if !ok {
		return startupCurrentScopeState{}, resolvedScopeView{}, nil
	}

	state := startupCurrentScopeState{
		Known:  true,
		Empty:  len(view.Entries) == 0,
		Scopes: append([]command.ExecutionScope(nil), view.Scopes...),
	}
	if state.Empty {
		needsInclude, err := startupCurrentScopeNeedsInclude(view.Scopes)
		if err != nil {
			return startupCurrentScopeState{}, resolvedScopeView{}, err
		}
		state.NeedsInclude = needsInclude
	}
	if len(view.Scopes) > 0 {
		current := view.Scopes[len(view.Scopes)-1]
		finishIgnoredBench := platform.InternalBenchSpan("ui.startup.scope_state.has_scoped_ignored",
			"targets", platform.InternalBenchInt(len(current.Targets)),
		)
		hasScopedIgnored, err := startupHasScopedIgnoredTargets(current.Targets)
		finishIgnoredBench("err", platform.InternalBenchError(err))
		if err == nil {
			state.HasScopedIgnoredTargets = hasScopedIgnored
		}
	}
	if state.Empty || !view.GitContext.Enabled {
		return state, view, nil
	}
	state.GitKnown = true

	currentRepoPaths := make(map[string]struct{}, len(view.Entries))
	for _, entry := range view.Entries {
		repoPath := normalizeRelPath(view.GitContext.ToRepoPath(entry.RelPath))
		if repoPath == "" {
			continue
		}
		currentRepoPaths[repoPath] = struct{}{}
	}
	total := len(currentRepoPaths)
	if total == 0 {
		return state, view, nil
	}

	// Single `git status --porcelain` covers staged + unstaged + untracked.
	// Replaces three sequential git invocations (one each for staged /
	// unstaged / untracked); per the v0.6.2 Windows trace each separate
	// invocation cost ~150 ms (Defender intercepts every git subprocess
	// .git/ read), so the consolidation saves ~300 ms on warm boot. The
	// resulting map is also reused by startupModifierCurrentScopePreviewCommand
	// to write the modifier-menu checkpoint without a second porcelain
	// call (state.GitStatusMap).
	finishGitSelBench := platform.InternalBenchSpan("ui.startup.scope_state.git_selection_sets",
		"repo_paths", platform.InternalBenchInt(total),
	)
	pathspecs := discovery.GitStatusPathspecsForEntries(view.GitContext, view.Entries)
	statusMap, err := git.StatusMapForPathspecs(view.GitContext, pathspecs)
	if err != nil {
		finishGitSelBench("err", platform.InternalBenchError(err))
		return startupCurrentScopeState{}, resolvedScopeView{}, err
	}
	state.GitStatusMap = statusMap

	staged := make(map[string]struct{})
	unstaged := make(map[string]struct{})
	untracked := make(map[string]struct{})
	for workPath, status := range statusMap {
		repoPath := normalizeRelPath(view.GitContext.ToRepoPath(workPath))
		if _, inScope := currentRepoPaths[repoPath]; !inScope {
			continue
		}
		switch status {
		case "S":
			staged[repoPath] = struct{}{}
		case "M":
			unstaged[repoPath] = struct{}{}
		case "SM":
			staged[repoPath] = struct{}{}
			unstaged[repoPath] = struct{}{}
		case "?":
			untracked[repoPath] = struct{}{}
		}
	}
	finishGitSelBench(
		"err", "false",
		"statuses", platform.InternalBenchInt(len(statusMap)),
		"staged", platform.InternalBenchInt(len(staged)),
		"unstaged", platform.InternalBenchInt(len(unstaged)),
		"untracked", platform.InternalBenchInt(len(untracked)),
	)
	// `Changed:true` (no sub-flag) on discovery.CollectChangedRepoPaths returns
	// `staged ∪ unstaged ∪ untracked` — its extra `git diff HEAD` call
	// is a subset of `staged ∪ unstaged` for tracked files. Recompute
	// the union here instead of paying for the redundant call.
	changed := unionRepoPathSets(staged, unstaged, untracked)

	state.AnyChanged, state.AllChanged = startupAnyAllForCurrentScope(total, changed)
	state.AnyStaged, state.AllStaged = startupAnyAllForCurrentScope(total, staged)
	state.AnyUnstaged, state.AllUnstaged = startupAnyAllForCurrentScope(total, unstaged)
	state.AnyUntracked, state.AllUntracked = startupAnyAllForCurrentScope(total, untracked)
	return state, view, nil
}

func unionRepoPathSets(sets ...map[string]struct{}) map[string]struct{} {
	total := 0
	for _, s := range sets {
		total += len(s)
	}
	out := make(map[string]struct{}, total)
	for _, s := range sets {
		for k := range s {
			out[k] = struct{}{}
		}
	}
	return out
}

func startupCurrentScopeNeedsInclude(scopes []command.ExecutionScope) (bool, error) {
	if len(scopes) == 0 {
		return false, nil
	}

	resolver, err := newStartupPickerResolver()
	if err != nil {
		return false, err
	}

	current := scopes[len(scopes)-1]
	if len(current.IncludedTargets) > 0 {
		exactIncludedTargets, unresolvedIncludeQueries, err := resolver.ResolveExactIgnoredIncludeTargets(current.IncludedTargets, current.Targets)
		if err != nil {
			return false, err
		}
		if len(unresolvedIncludeQueries) > 0 {
			return false, nil
		}
		resolver.IncludedTargets = discovery.BuildIncludedTargetSet(resolver.Cfg.WorkingDir, exactIncludedTargets)
	}

	for _, target := range current.Targets {
		target = normalizeRelPath(target)
		if target == "" || target == "." {
			continue
		}
		needsInclude, err := resolver.TargetNeedsInclude(target)
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
	wd, err := os.Getwd()
	if err != nil {
		return false, err
	}
	hissPath, err := discovery.ReadableHissPath()
	if err != nil {
		return false, err
	}
	return search.HasScopedIgnoredTargetsStreaming(context.Background(), wd, scopeTargets, hissPath)
}

func startupAnyAllForCurrentScope(total int, set map[string]struct{}) (bool, bool) {
	if total == 0 || len(set) == 0 {
		return false, false
	}
	return true, len(set) == total
}

func startupNoFilesMatchedError(scopes []command.ExecutionScope) error {
	var b strings.Builder
	if err := discovery.WriteNoFilesMatchedMessage(scopes, &b, platform.ActivePalette(), false); err != nil {
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
			PreviewKind:   TreeTargetKindFile,
			PreviewState:  TreeTargetStateText,
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
		ext := discovery.ShellStyleExtension(relPath)
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
	bin, err := discovery.FuzzyResolverBinary()
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
		Bindings:       discovery.MultiSelectPickerBindings(),
		NoSort:         startupFileSetRowsNeedStableOrder(rows),
		Lines:          formatStartupFileSetRows(rows),
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return nil, discovery.ErrSelectionCancelled
	}
	if err != nil {
		return nil, err
	}
	if len(result.Matches) == 0 {
		return nil, discovery.ErrSelectionCancelled
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
