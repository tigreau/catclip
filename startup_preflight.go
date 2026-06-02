package catclip

import (
	"strconv"
	"strings"
)

func validateStartupPreflightArgs(args []string) error {
	_, err := startupPreflightCommandSpec(args)
	return err
}

func startupPreflightCommandSpec(args []string) (commandSpec, error) {
	executionScopes := make([]executionScope, 0, 2)
	var stageState scopeStageTransitionState
	var current executionScopeBuilder
	inModifierMode := false

	resetScope := func() {
		stageState = scopeStageTransitionState{}
		current = executionScopeBuilder{}
		inModifierMode = false
	}

	validateScopeSemantics := func() error {
		switch stageState.outputModeKind {
		case scopeStageSnippet:
			if current.SnippetPattern == "" {
				return requiredStageValueError("--snippet")
			}
		case scopeStageDiff, scopeStageChangedDiff, scopeStageStagedDiff, scopeStageUnstagedDiff:
			if !executionScopeHasGitSelection(current.executionScope) {
				return missingDiffSelectorError()
			}
			if current.Untracked && !current.Staged && !current.Unstaged {
				return untrackedDiffError()
			}
		}
		return nil
	}

	appendCurrentScope := func() error {
		if !current.hasContent() {
			resetScope()
			return nil
		}
		if err := validateScopeSemantics(); err != nil {
			return err
		}

		s := current.executionScope
		if s.Staged || s.Unstaged || s.Untracked {
			s.Changed = true
		}
		if len(s.Targets) == 0 {
			s.Targets = []string{"."}
		}
		executionScopes = append(executionScopes, s)
		resetScope()
		return nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch arg {
		case "-h", "--help", "--help-all", "--version", "-V", "--hiss", "--hiss-reset", "--all-ignore-rules":
			continue
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--preview":
			continue
		case "--then":
			if err := appendCurrentScope(); err != nil {
				return commandSpec{}, err
			}
			continue
		case "--":
			if err := validateScopeSemantics(); err != nil {
				return commandSpec{}, err
			}
			if allRemainingArgsAreBareModifierPlaceholders(args[i:]) {
				if current.hasContent() {
					s := current.executionScope
					if s.Staged || s.Unstaged || s.Untracked {
						s.Changed = true
					}
					if len(s.Targets) == 0 {
						s.Targets = []string{"."}
					}
					executionScopes = append(executionScopes, s)
				}
				if len(executionScopes) == 0 {
					executionScopes = append(executionScopes, executionScope{Targets: []string{"."}})
				}
				return partialCommandSpecFromExecutionScopes(executionScopes), nil
			}
			if i != len(args)-1 {
				return commandSpec{}, bareModifierPlaceholderOrderError()
			}
			return partialCommandSpecFromExecutionScopes(executionScopes), nil
		case "--include":
			inModifierMode = true
			if err := stageState.apply(scopeStageInclude); err != nil {
				return commandSpec{}, err
			}
			next, err := preflightConsumeRequiredStageValues(args, i, arg)
			if err != nil {
				return commandSpec{}, err
			}
			values := cloneStringSlice(args[i+1 : next])
			if err := validateIncludeValues(values); err != nil {
				return commandSpec{}, err
			}
			current.IncludedTargets = append(current.IncludedTargets, values...)
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageInclude, Values: values})
			i = next - 1
			continue
		case "--only":
			inModifierMode = true
			if err := stageState.apply(scopeStageOnly); err != nil {
				return commandSpec{}, err
			}
			next, err := preflightConsumeRequiredStageValues(args, i, arg)
			if err != nil {
				return commandSpec{}, err
			}
			values := cloneStringSlice(args[i+1 : next])
			current.Only = append(current.Only, values...)
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageOnly, Values: values})
			i = next - 1
			continue
		case "--exclude":
			inModifierMode = true
			if err := stageState.apply(scopeStageExclude); err != nil {
				return commandSpec{}, err
			}
			next, err := preflightConsumeRequiredStageValues(args, i, arg)
			if err != nil {
				return commandSpec{}, err
			}
			values := cloneStringSlice(args[i+1 : next])
			current.Exclude = append(current.Exclude, values...)
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageExclude, Values: values})
			i = next - 1
			continue
		case "--recent":
			inModifierMode = true
			if err := stageState.apply(scopeStageRecent); err != nil {
				return commandSpec{}, err
			}
			if i+1 >= len(args) {
				current.Stages = append(current.Stages, scopeStage{Kind: scopeStageRecent})
				continue
			}
			if isModifierBoundaryToken(args[i+1]) {
				current.Stages = append(current.Stages, scopeStage{Kind: scopeStageRecent})
				continue
			}
			limit, err := parseRecentLimitToken(args[i+1])
			if err != nil {
				return commandSpec{}, err
			}
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageRecent, Limit: &limit})
			i++
			continue
		case "--depth":
			inModifierMode = true
			if err := stageState.apply(scopeStageDepth); err != nil {
				return commandSpec{}, err
			}
			if i+1 >= len(args) {
				return commandSpec{}, requiredStageValueError("--depth")
			}
			depth, err := parseDepthToken(args[i+1])
			if err != nil {
				return commandSpec{}, err
			}
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageDepth, Limit: &depth})
			i++
			continue
		case "--paths":
			inModifierMode = true
			if err := stageState.apply(scopeStagePaths); err != nil {
				return commandSpec{}, err
			}
			current.Paths = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStagePaths})
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				return commandSpec{}, noValueModifierError(arg)
			}
			continue
		case "--lines":
			inModifierMode = true
			if err := stageState.apply(scopeStageLines); err != nil {
				return commandSpec{}, err
			}
			current.Lines = true
			var start, end int
			if i+1 < len(args) {
				next := args[i+1]
				if n, err := strconv.Atoi(next); err == nil {
					if n < 1 {
						return commandSpec{}, newUsageError("Error: --lines start must be >= 1 (got %d).\n  Line numbers are 1-based, matching editors and compiler output.", n)
					}
					start = n
					i++
					if i+1 < len(args) {
						next2 := args[i+1]
						if n2, err := strconv.Atoi(next2); err == nil {
							if n2 < start {
								return commandSpec{}, newUsageError("Error: --lines end (%d) must be >= start (%d).\n  Use: --lines START END where END >= START.", n2, start)
							}
							end = n2
							i++
						} else if !strings.HasPrefix(next2, "-") {
							return commandSpec{}, newUsageError("Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got %q).", next2)
						}
					}
				} else if !strings.HasPrefix(next, "-") {
					return commandSpec{}, newUsageError("Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got %q).", next)
				}
			}
			current.LinesStart = start
			current.LinesEnd = end
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageLines})
			continue
		case "--contains", "--snippet":
			inModifierMode = true
			kind, _ := scopeStageKindForFlag(arg)
			if err := stageState.apply(kind); err != nil {
				return commandSpec{}, err
			}
			if i+1 >= len(args) {
				if arg == "--contains" {
					return commandSpec{}, containsMissingPatternError(args, i)
				}
				return commandSpec{}, requiredStageValueError("--snippet")
			}
			i++
			regex := args[i]
			if arg == "--contains" {
				current.Contains = regex
			} else {
				current.Snippet = true
				current.SnippetPattern = regex
				// Optional fixed-context number after the snippet regex; mirrors
				// parseArgs so the startup/interactive path carries it too.
				if i+1 < len(args) {
					if n, err := strconv.Atoi(args[i+1]); err == nil {
						if n < 0 || n > snippetContextMax {
							return commandSpec{}, newUsageError("Error: --snippet context must be between 0 and %d (got %d).\n  Use: --snippet 'REGEX' N for N lines around each match (0 = matching line only).", snippetContextMax, n)
						}
						i++
						current.SnippetContextSet = true
						current.SnippetContextLines = n
					}
				}
			}
			// A bare token after the regex (and snippet's optional N) is almost
			// always an unquoted regex with spaces the shell split; give a quote
			// hint instead of the generic positional-after-modifier error.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return commandSpec{}, regexModifierExtraValueError(arg, regex, args[i+1])
			}
			current.Stages = append(current.Stages, scopeStage{Kind: kind, Values: []string{regex}})
			continue
		case "--changed":
			inModifierMode = true
			if err := stageState.apply(scopeStageChanged); err != nil {
				return commandSpec{}, err
			}
			current.Changed = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageChanged})
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				return commandSpec{}, noValueModifierError(arg)
			}
			continue
		case "--staged":
			inModifierMode = true
			if err := stageState.apply(scopeStageStaged); err != nil {
				return commandSpec{}, err
			}
			current.Staged = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageStaged})
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				return commandSpec{}, noValueModifierError(arg)
			}
			continue
		case "--unstaged":
			inModifierMode = true
			if err := stageState.apply(scopeStageUnstaged); err != nil {
				return commandSpec{}, err
			}
			current.Unstaged = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageUnstaged})
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				return commandSpec{}, noValueModifierError(arg)
			}
			continue
		case "--untracked":
			inModifierMode = true
			if err := stageState.apply(scopeStageUntracked); err != nil {
				return commandSpec{}, err
			}
			current.Untracked = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageUntracked})
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				return commandSpec{}, noValueModifierError(arg)
			}
			continue
		case "--changed-diff":
			inModifierMode = true
			if err := stageState.apply(scopeStageChangedDiff); err != nil {
				return commandSpec{}, err
			}
			current.Changed = true
			current.Diff = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageChangedDiff})
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				return commandSpec{}, noValueModifierError(arg)
			}
			continue
		case "--staged-diff":
			inModifierMode = true
			if err := stageState.apply(scopeStageStagedDiff); err != nil {
				return commandSpec{}, err
			}
			current.Staged = true
			current.Diff = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageStagedDiff})
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				return commandSpec{}, noValueModifierError(arg)
			}
			continue
		case "--unstaged-diff":
			inModifierMode = true
			if err := stageState.apply(scopeStageUnstagedDiff); err != nil {
				return commandSpec{}, err
			}
			current.Unstaged = true
			current.Diff = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageUnstagedDiff})
			if i+1 < len(args) && !isModifierBoundaryToken(args[i+1]) {
				return commandSpec{}, noValueModifierError(arg)
			}
			continue
		}

		switch {
		case arg == "--diff":
			return commandSpec{}, diffStandaloneError()
		case strings.HasPrefix(arg, "--contains="):
			return commandSpec{}, newUsageError("Error: --contains requires a space before the pattern.\n  Use: catclip src --contains 'pattern'\n  Not: catclip src --contains='pattern'")
		case strings.HasPrefix(arg, "--snippet="):
			return commandSpec{}, newUsageError("Error: --snippet requires a space before the pattern.\n  Use: catclip src --snippet 'pattern'\n  Not: catclip src --snippet='pattern'")
		case strings.HasPrefix(arg, "--recent="):
			return commandSpec{}, newUsageError("Error: --recent requires a space before the value.\n  Use: catclip src --recent 5\n  Or:  catclip src --recent")
		case strings.HasPrefix(arg, "--depth="):
			return commandSpec{}, newUsageError("Error: --depth requires a space before the value.\n  Use: catclip src --depth 2")
		case strings.HasPrefix(arg, "--"):
			return commandSpec{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			return commandSpec{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
		default:
			if inModifierMode {
				return commandSpec{}, positionalAfterModifierError()
			}
			current.Targets = append(current.Targets, arg)
			current.explicitTargets++
		}
	}

	if err := appendCurrentScope(); err != nil {
		return commandSpec{}, err
	}
	if len(executionScopes) == 0 {
		executionScopes = append(executionScopes, executionScope{Targets: []string{"."}})
	}
	return finalizedCommandSpecFromExecutionScopes(executionScopes), nil
}

func preflightConsumeRequiredStageValues(args []string, index int, flag string) (int, error) {
	if index+1 >= len(args) {
		return 0, requiredStageValueError(flag)
	}
	if args[index+1] == "--" {
		return 0, requiredStageValueError(flag)
	}
	values, next := consumeModifierValues(args, index+1)
	if len(values) == 0 {
		return 0, requiredStageValueError(flag)
	}
	return next, nil
}

func allRemainingArgsAreBareModifierPlaceholders(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		if arg != "--" {
			return false
		}
	}
	return true
}
