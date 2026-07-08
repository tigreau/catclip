package cli

import (
	"fmt"

	"github.com/tigreau/catclip/internal/command"
)

type scopeStageCategory string

const (
	scopeStageCategorySetRefinement   scopeStageCategory = "set_refinement"
	scopeStageCategoryContentFilter   scopeStageCategory = "content_filter"
	scopeStageCategoryGitChangeFilter scopeStageCategory = "git_change_filter"
	scopeStageCategoryOutputMode      scopeStageCategory = "output_mode"
)

type scopeStageBoundaryPolicy string

const (
	scopeStageBoundaryNone     scopeStageBoundaryPolicy = ""
	scopeStageBoundaryDiff     scopeStageBoundaryPolicy = "diff"
	scopeStageBoundarySnippet  scopeStageBoundaryPolicy = "snippet"
	scopeStageBoundaryTerminal scopeStageBoundaryPolicy = "terminal"
)

type scopeStageSemantics struct {
	Category       scopeStageCategory
	BoundaryPolicy scopeStageBoundaryPolicy
	Flag           string
}

type scopeStageTransitionState struct {
	outputModeKind     command.StageKind
	activeBoundaryKind command.StageKind
	activeBoundary     scopeStageBoundaryPolicy
	hasInclude         bool
	hasNonIncludeStage bool
}

func ValidateScopeStageOrder(stages []command.Stage) error {
	_, err := buildScopeStageTransitionState(stages)
	return err
}

func ValidateCurrentScopeFlagAddition(currentArgs []string, flag string) error {
	kinds, err := scopeStageKindsForFlags([]string{flag})
	if err != nil {
		return err
	}
	return validateCurrentScopeStageSequence(currentArgs, kinds)
}

func ValidateCurrentScopeFlagSequence(currentArgs []string, flags []string) error {
	kinds, err := scopeStageKindsForFlags(flags)
	if err != nil {
		return err
	}
	return validateCurrentScopeStageSequence(currentArgs, kinds)
}

func validateCurrentScopeStageSequence(currentArgs []string, additions []command.StageKind) error {
	stages := currentScopeStagesFromArgs(currentArgs)
	state, err := buildScopeStageTransitionState(stages)
	if err != nil {
		return err
	}
	for _, kind := range additions {
		if err := state.apply(kind); err != nil {
			return err
		}
	}
	return nil
}

func buildScopeStageTransitionState(stages []command.Stage) (scopeStageTransitionState, error) {
	var state scopeStageTransitionState
	for _, stage := range stages {
		if err := state.apply(stage.Kind); err != nil {
			return scopeStageTransitionState{}, err
		}
	}
	return state, nil
}

func (s *scopeStageTransitionState) apply(kind command.StageKind) error {
	semantics, ok := scopeStageSemanticsForKind(kind)
	if !ok {
		return fmt.Errorf("internal error: missing scope stage semantics for %q", kind)
	}
	if err := s.validate(kind, semantics); err != nil {
		return err
	}
	if semantics.Category == scopeStageCategoryOutputMode && s.outputModeKind == "" {
		s.outputModeKind = kind
	}
	if semantics.BoundaryPolicy != scopeStageBoundaryNone {
		s.activeBoundary = semantics.BoundaryPolicy
		s.activeBoundaryKind = kind
	}
	if kind == command.StageInclude {
		s.hasInclude = true
	} else {
		s.hasNonIncludeStage = true
	}
	return nil
}

func (s scopeStageTransitionState) validate(kind command.StageKind, semantics scopeStageSemantics) error {
	if s.activeBoundary == scopeStageBoundaryTerminal {
		return terminalBoundaryOrderError(scopeStageFlagLabel(s.activeBoundaryKind), semantics.Flag)
	}
	if kind == command.StageInclude {
		if s.hasInclude {
			return repeatedIncludeError()
		}
		if s.hasNonIncludeStage {
			return includeAfterModifierError()
		}
	}
	if err := validateOutputModeTransition(s.outputModeKind, kind, semantics); err != nil {
		return err
	}
	switch s.activeBoundary {
	case scopeStageBoundaryDiff:
		switch semantics.Category {
		case scopeStageCategoryContentFilter:
			return diffContentFilterOrderError(semantics.Flag, scopeStageFlagLabel(s.activeBoundaryKind))
		case scopeStageCategoryGitChangeFilter:
			return diffGitFilterOrderError(semantics.Flag, scopeStageFlagLabel(s.activeBoundaryKind))
		}
	case scopeStageBoundarySnippet:
		switch semantics.Category {
		case scopeStageCategoryContentFilter:
			return snippetContentFilterOrderError(semantics.Flag)
		}
	}
	return nil
}

func validateOutputModeTransition(current command.StageKind, next command.StageKind, nextSemantics scopeStageSemantics) error {
	if current == "" || nextSemantics.Category != scopeStageCategoryOutputMode {
		return nil
	}
	if current == next {
		return repeatedOutputModeError(scopeStageFlagLabel(next))
	}
	if (isDiffOutputStage(current) && next == command.StageSnippet) ||
		(current == command.StageSnippet && isDiffOutputStage(next)) {
		return diffSnippetConflictError()
	}
	return outputModeConflictError(scopeStageFlagLabel(current), scopeStageFlagLabel(next))
}

func isDiffOutputStage(kind command.StageKind) bool {
	switch kind {
	case command.StageDiff, command.StageChangedDiff, command.StageStagedDiff, command.StageUnstagedDiff:
		return true
	default:
		return false
	}
}

func outputModeConflictError(currentFlag, nextFlag string) error {
	return ValidationFailure{
		Reason:       ReasonOutputModeConflict,
		BoundaryFlag: currentFlag,
		Flag:         nextFlag,
	}
}

func currentScopeStagesFromArgs(args []string) []command.Stage {
	if stages, ok := currentScopeStagesFromCommandSpec(args); ok {
		return stages
	}
	return currentScopeStagesFromArgsLegacy(args)
}

func currentScopeStagesFromCommandSpec(args []string) ([]command.Stage, bool) {
	spec, err := StartupPreflightCommandSpec(args)
	if err != nil {
		return nil, false
	}
	scopes := spec.Scopes()
	if len(scopes) == 0 {
		return nil, true
	}
	return scopes[len(scopes)-1].Stages(), true
}

func currentScopeStagesFromArgsLegacy(args []string) []command.Stage {
	stages := make([]command.Stage, 0, 8)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--then":
			stages = stages[:0]
		case "--":
			return stages
		case "--include", "--only", "--exclude":
			if kind, ok := ScopeStageKindForFlag(args[i]); ok {
				stages = append(stages, command.Stage{Kind: kind})
			}
			_, next := consumeModifierValues(args, i+1)
			i = next - 1
		case "--recent":
			stages = append(stages, command.Stage{Kind: command.StageRecent})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				if _, err := ParseRecentLimitToken(args[i+1]); err == nil {
					i++
				}
			}
		case "--size":
			stages = append(stages, command.Stage{Kind: command.StageSize})
			for consumed := 0; consumed < 2 && i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]); consumed++ {
				if _, err := ParseSizeBoundToken(args[i+1]); err != nil {
					break
				}
				i++
			}
		case "--contains", "--not-contains", "--snippet", "--depth":
			kind, _ := ScopeStageKindForFlag(args[i])
			stages = append(stages, command.Stage{Kind: kind})
			if i+1 < len(args) && !IsModifierBoundaryToken(args[i+1]) {
				i++
			}
		default:
			if kind, ok := ScopeStageKindForFlag(args[i]); ok {
				stages = append(stages, command.Stage{Kind: kind})
			}
		}
	}
	return stages
}

func scopeStageKindsForFlags(flags []string) ([]command.StageKind, error) {
	kinds := make([]command.StageKind, 0, len(flags))
	for _, flag := range flags {
		kind, ok := ScopeStageKindForFlag(flag)
		if !ok {
			continue
		}
		kinds = append(kinds, kind)
	}
	return kinds, nil
}

func ScopeStageKindForFlag(flag string) (command.StageKind, bool) {
	spec, ok := scopeModifierFlagSpecForFlag(flag)
	if !ok {
		return "", false
	}
	return spec.StageKind, true
}

func scopeStageFlagLabel(kind command.StageKind) string {
	semantics, ok := scopeStageSemanticsForKind(kind)
	if !ok || semantics.Flag == "" {
		return fmt.Sprintf("--%s", kind)
	}
	return semantics.Flag
}

func scopeStageSemanticsForKind(kind command.StageKind) (scopeStageSemantics, bool) {
	spec, ok := scopeModifierFlagSpecForStageKind(kind)
	if !ok {
		return scopeStageSemantics{}, false
	}
	category, ok := spec.Family.scopeStageCategory()
	if !ok {
		return scopeStageSemantics{}, false
	}
	return scopeStageSemantics{
		Category:       category,
		BoundaryPolicy: spec.BoundaryPolicy,
		Flag:           spec.Flag,
	}, true
}

func diffSnippetConflictError() error {
	return ValidationFailure{Reason: ReasonDiffSnippetConflict}
}

func diffContentFilterOrderError(flag, boundaryFlag string) error {
	return ValidationFailure{Reason: ReasonDiffContentFilterOrder, Flag: flag, BoundaryFlag: boundaryFlag}
}

func diffGitFilterOrderError(flag, boundaryFlag string) error {
	return ValidationFailure{Reason: ReasonDiffGitFilterOrder, Flag: flag, BoundaryFlag: boundaryFlag}
}

func snippetContentFilterOrderError(flag string) error {
	return ValidationFailure{Reason: ReasonSnippetContentFilterOrder, Flag: flag}
}

func repeatedOutputModeError(flag string) error {
	return ValidationFailure{Reason: ReasonRepeatedOutputMode, Flag: flag}
}

func terminalBoundaryOrderError(boundaryFlag, nextFlag string) error {
	return ValidationFailure{Reason: ReasonTerminalBoundaryOrder, BoundaryFlag: boundaryFlag, NextFlag: nextFlag}
}

func includeAfterModifierError() error {
	return ValidationFailure{Reason: ReasonIncludeAfterModifier}
}

func repeatedIncludeError() error {
	return ValidationFailure{Reason: ReasonRepeatedInclude}
}
