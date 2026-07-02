package cli

import (
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/command"
)

func ValidateStartupPreflightArgs(args []string) error {
	_, err := StartupPreflightCommandSpec(args)
	return err
}

func StartupPreflightCommandSpec(args []string) (command.Spec, error) {
	executionScopes := make([]command.ExecutionScope, 0, 2)
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
		case command.StageSnippet:
			if current.SnippetPattern == "" {
				return RequiredStageValueError("--snippet")
			}
		case command.StageDiff, command.StageChangedDiff, command.StageStagedDiff, command.StageUnstagedDiff:
			if !current.ExecutionScope.HasGitSelection() {
				return MissingDiffSelectorError()
			}
			if current.Untracked && !current.Staged && !current.Unstaged {
				return UntrackedDiffError()
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

		s := current.ExecutionScope
		if s.Staged || s.Unstaged || s.Untracked {
			s.Changed = true
		}
		// Effect 5: --include with no positional target. See the twin
		// check in parse.go for the rationale (ambiguity between
		// "just <include>" and "everything + <include>").
		if len(s.Targets) == 0 && len(s.IncludedTargets) > 0 {
			return BareIncludeMissingTargetError(s.IncludedTargets[0])
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
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--preview", "--with-binaries":
			continue
		case "--then":
			if err := appendCurrentScope(); err != nil {
				return command.Spec{}, err
			}
			continue
		case "--":
			if err := validateScopeSemantics(); err != nil {
				return command.Spec{}, err
			}
			if allRemainingArgsAreBareModifierPlaceholders(args[i:]) {
				if current.hasContent() {
					s := current.ExecutionScope
					if s.Staged || s.Unstaged || s.Untracked {
						s.Changed = true
					}
					if len(s.Targets) == 0 {
						s.Targets = []string{"."}
					}
					executionScopes = append(executionScopes, s)
				}
				if len(executionScopes) == 0 {
					executionScopes = append(executionScopes, command.ExecutionScope{Targets: []string{"."}})
				}
				return command.PartialSpecFromExecutionScopes(executionScopes), nil
			}
			if i != len(args)-1 {
				return command.Spec{}, BareModifierPlaceholderOrderError()
			}
			return command.PartialSpecFromExecutionScopes(executionScopes), nil
		case "--include":
			inModifierMode = true
			if err := stageState.apply(command.StageInclude); err != nil {
				return command.Spec{}, err
			}
			next, err := preflightConsumeRequiredStageValues(args, i, arg)
			if err != nil {
				return command.Spec{}, err
			}
			values := cloneSliceStrings(args[i+1 : next])
			if err := ValidateIncludeValues(values); err != nil {
				return command.Spec{}, err
			}
			current.IncludedTargets = append(current.IncludedTargets, values...)
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageInclude, Values: values})
			i = next - 1
			continue
		case "--only":
			inModifierMode = true
			if err := stageState.apply(command.StageOnly); err != nil {
				return command.Spec{}, err
			}
			next, err := preflightConsumeRequiredStageValues(args, i, arg)
			if err != nil {
				return command.Spec{}, err
			}
			values := cloneSliceStrings(args[i+1 : next])
			current.Only = append(current.Only, values...)
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageOnly, Values: values})
			i = next - 1
			continue
		case "--exclude":
			inModifierMode = true
			if err := stageState.apply(command.StageExclude); err != nil {
				return command.Spec{}, err
			}
			next, err := preflightConsumeRequiredStageValues(args, i, arg)
			if err != nil {
				return command.Spec{}, err
			}
			values := cloneSliceStrings(args[i+1 : next])
			current.Exclude = append(current.Exclude, values...)
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageExclude, Values: values})
			i = next - 1
			continue
		case "--recent":
			inModifierMode = true
			if err := stageState.apply(command.StageRecent); err != nil {
				return command.Spec{}, err
			}
			if i+1 >= len(args) {
				current.Stages = append(current.Stages, command.Stage{Kind: command.StageRecent})
				continue
			}
			if IsModifierBoundaryToken(args[i+1]) {
				current.Stages = append(current.Stages, command.Stage{Kind: command.StageRecent})
				continue
			}
			limit, err := parseRecentLimitToken(args[i+1])
			if err != nil {
				return command.Spec{}, err
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageRecent, Limit: &limit})
			i++
			continue
		case "--size":
			inModifierMode = true
			if err := stageState.apply(command.StageSize); err != nil {
				return command.Spec{}, err
			}
			nums, next, err := consumeOptionalSizeBounds(args, i+1)
			if err != nil {
				return command.Spec{}, err
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageSize, Nums: nums})
			i = next - 1
			continue
		case "--depth":
			inModifierMode = true
			if err := stageState.apply(command.StageDepth); err != nil {
				return command.Spec{}, err
			}
			if i+1 >= len(args) {
				return command.Spec{}, RequiredStageValueError("--depth")
			}
			depth, err := parseDepthToken(args[i+1])
			if err != nil {
				return command.Spec{}, err
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageDepth, Limit: &depth})
			i++
			continue
		case "--paths":
			inModifierMode = true
			if err := stageState.apply(command.StagePaths); err != nil {
				return command.Spec{}, err
			}
			current.Paths = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StagePaths})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				return command.Spec{}, NoValueModifierError(arg)
			}
			continue
		case "--lines":
			inModifierMode = true
			if err := stageState.apply(command.StageLines); err != nil {
				return command.Spec{}, err
			}
			current.Lines = true
			var start, end int
			if i+1 < len(args) {
				next := args[i+1]
				if n, err := strconv.Atoi(next); err == nil {
					if n < 1 {
						return command.Spec{}, newUsageError("Error: --lines start must be >= 1 (got %d).\n  Line numbers are 1-based, matching editors and compiler output.", n)
					}
					start = n
					i++
					if i+1 < len(args) {
						next2 := args[i+1]
						if n2, err := strconv.Atoi(next2); err == nil {
							if n2 < start {
								return command.Spec{}, newUsageError("Error: --lines end (%d) must be >= start (%d).\n  Use: --lines START END where END >= START.", n2, start)
							}
							end = n2
							i++
						} else if !strings.HasPrefix(next2, "-") {
							return command.Spec{}, newUsageError("Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got %q).", next2)
						}
					}
				} else if !strings.HasPrefix(next, "-") {
					return command.Spec{}, newUsageError("Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got %q).", next)
				}
			}
			current.LinesStart = start
			current.LinesEnd = end
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageLines})
			continue
		case "--contains", "--snippet":
			inModifierMode = true
			kind, _ := ScopeStageKindForFlag(arg)
			if err := stageState.apply(kind); err != nil {
				return command.Spec{}, err
			}
			if i+1 >= len(args) {
				if arg == "--contains" {
					return command.Spec{}, ContainsMissingPatternError(args, i)
				}
				return command.Spec{}, RequiredStageValueError("--snippet")
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
							return command.Spec{}, newUsageError("Error: --snippet context must be between 0 and %d (got %d).\n  Use: --snippet 'REGEX' N for N lines around each match (0 = matching line only).", snippetContextMax, n)
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
				return command.Spec{}, RegexModifierExtraValueError(arg, regex, args[i+1])
			}
			current.Stages = append(current.Stages, command.Stage{Kind: kind, Values: []string{regex}})
			continue
		case "--not-contains":
			inModifierMode = true
			if err := stageState.apply(command.StageNotContains); err != nil {
				return command.Spec{}, err
			}
			if i+1 >= len(args) {
				return command.Spec{}, NotContainsMissingPatternError(args, i)
			}
			i++
			regex := args[i]
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return command.Spec{}, RegexModifierExtraValueError(arg, regex, args[i+1])
			}
			current.NotContains = append(current.NotContains, regex)
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageNotContains, Values: []string{regex}})
			continue
		case "--changed":
			inModifierMode = true
			if err := stageState.apply(command.StageChanged); err != nil {
				return command.Spec{}, err
			}
			current.Changed = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageChanged})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				return command.Spec{}, NoValueModifierError(arg)
			}
			continue
		case "--staged":
			inModifierMode = true
			if err := stageState.apply(command.StageStaged); err != nil {
				return command.Spec{}, err
			}
			current.Staged = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageStaged})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				return command.Spec{}, NoValueModifierError(arg)
			}
			continue
		case "--unstaged":
			inModifierMode = true
			if err := stageState.apply(command.StageUnstaged); err != nil {
				return command.Spec{}, err
			}
			current.Unstaged = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageUnstaged})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				return command.Spec{}, NoValueModifierError(arg)
			}
			continue
		case "--untracked":
			inModifierMode = true
			if err := stageState.apply(command.StageUntracked); err != nil {
				return command.Spec{}, err
			}
			current.Untracked = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageUntracked})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				return command.Spec{}, NoValueModifierError(arg)
			}
			continue
		case "--changed-diff":
			inModifierMode = true
			if err := stageState.apply(command.StageChangedDiff); err != nil {
				return command.Spec{}, err
			}
			current.Changed = true
			current.Diff = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageChangedDiff})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				return command.Spec{}, NoValueModifierError(arg)
			}
			continue
		case "--staged-diff":
			inModifierMode = true
			if err := stageState.apply(command.StageStagedDiff); err != nil {
				return command.Spec{}, err
			}
			current.Staged = true
			current.Diff = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageStagedDiff})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				return command.Spec{}, NoValueModifierError(arg)
			}
			continue
		case "--unstaged-diff":
			inModifierMode = true
			if err := stageState.apply(command.StageUnstagedDiff); err != nil {
				return command.Spec{}, err
			}
			current.Unstaged = true
			current.Diff = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageUnstagedDiff})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				return command.Spec{}, NoValueModifierError(arg)
			}
			continue
		}

		switch {
		case arg == "--diff":
			return command.Spec{}, DiffStandaloneError()
		case strings.HasPrefix(arg, "--contains="):
			return command.Spec{}, newUsageError("Error: --contains requires a space before the pattern.\n  Use: catclip src --contains 'pattern'\n  Not: catclip src --contains='pattern'")
		case strings.HasPrefix(arg, "--not-contains="):
			return command.Spec{}, newUsageError("Error: --not-contains requires a space before the pattern.\n  Use: catclip src --not-contains 'pattern'\n  Not: catclip src --not-contains='pattern'")
		case strings.HasPrefix(arg, "--snippet="):
			return command.Spec{}, newUsageError("Error: --snippet requires a space before the pattern.\n  Use: catclip src --snippet 'pattern'\n  Not: catclip src --snippet='pattern'")
		case strings.HasPrefix(arg, "--recent="):
			return command.Spec{}, newUsageError("Error: --recent requires a space before the value.\n  Use: catclip src --recent 5\n  Or:  catclip src --recent")
		case strings.HasPrefix(arg, "--size="):
			return command.Spec{}, SizeEqualsFormError()
		case strings.HasPrefix(arg, "--depth="):
			return command.Spec{}, newUsageError("Error: --depth requires a space before the value.\n  Use: catclip src --depth 2")
		case strings.HasPrefix(arg, "--"):
			return command.Spec{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			return command.Spec{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
		default:
			if inModifierMode {
				return command.Spec{}, PositionalAfterModifierError()
			}
			current.Targets = append(current.Targets, arg)
			current.explicitTargets++
		}
	}

	if err := appendCurrentScope(); err != nil {
		return command.Spec{}, err
	}
	if len(executionScopes) == 0 {
		executionScopes = append(executionScopes, command.ExecutionScope{Targets: []string{"."}})
	}
	return command.FinalizedSpecFromExecutionScopes(executionScopes), nil
}

func preflightConsumeRequiredStageValues(args []string, index int, flag string) (int, error) {
	if index+1 >= len(args) {
		return 0, RequiredStageValueError(flag)
	}
	if args[index+1] == "--" {
		return 0, RequiredStageValueError(flag)
	}
	values, next := consumeModifierValues(args, index+1)
	if len(values) == 0 {
		return 0, RequiredStageValueError(flag)
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
