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
		if len(s.Targets) == 0 {
			s.Targets = []string{"."}
		}
		executionScopes = append(executionScopes, s)
		resetScope()
		return nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if IsUnsupportedIncludeOption(arg) {
			return command.Spec{}, IncludeUnsupportedError()
		}

		switch arg {
		case "-h", "--help", "--help-all", "--version", "-V", "--check-update", "--hiss", "--hiss-reset", "--all-ignore-rules":
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
		case "--no-ignore":
			inModifierMode = true
			if err := stageState.apply(command.StageNoIgnore); err != nil {
				return command.Spec{}, err
			}
			current.NoIgnore = true
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageNoIgnore})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				return command.Spec{}, NoValueModifierError(arg)
			}
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
			if err := validateDirectPathPatterns("--only", values, false); err != nil {
				return command.Spec{}, err
			}
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
			if err := validateDirectPathPatterns("--exclude", values, false); err != nil {
				return command.Spec{}, err
			}
			current.Exclude = append(current.Exclude, values...)
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageExclude, Values: values})
			i = next - 1
			continue
		case "--recent":
			inModifierMode = true
			if err := stageState.apply(command.StageRecent); err != nil {
				return command.Spec{}, err
			}
			limit, next, err := consumeOptionalRecentLimit(args, i+1)
			if err != nil {
				return command.Spec{}, err
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageRecent, Limit: limit})
			i = next - 1
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
			limit, next, err := consumeDepthLimit(args, i+1)
			if err != nil {
				return command.Spec{}, err
			}
			current.Stages = append(current.Stages, command.Stage{Kind: command.StageDepth, Limit: limit})
			i = next - 1
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
						return command.Spec{}, LinesStartError(n)
					}
					start = n
					i++
					if i+1 < len(args) {
						next2 := args[i+1]
						if n2, err := strconv.Atoi(next2); err == nil {
							if n2 < start {
								return command.Spec{}, LinesEndBeforeStartError(n2, start)
							}
							end = n2
							i++
						} else if !strings.HasPrefix(next2, "-") {
							return command.Spec{}, LinesInvalidValueError(next2)
						}
					}
				} else if !strings.HasPrefix(next, "-") {
					return command.Spec{}, LinesInvalidValueError(next)
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
						if err := ValidateSnippetContext(n); err != nil {
							return command.Spec{}, err
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

		if err := EqualsFormRejectionError(arg); err != nil {
			return command.Spec{}, err
		}
		switch {
		case arg == "--diff":
			return command.Spec{}, DiffStandaloneError()
		case strings.HasPrefix(arg, "--"):
			return command.Spec{}, UnknownOptionError(arg)
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			return command.Spec{}, UnknownOptionError(arg)
		default:
			if inModifierMode {
				return command.Spec{}, PositionalAfterModifierError()
			}
			if err := validateDirectPathPatterns("target", []string{arg}, false); err != nil {
				return command.Spec{}, err
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
