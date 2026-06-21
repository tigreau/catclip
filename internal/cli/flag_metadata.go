package cli

import (
	"strings"

	"github.com/tigreau/catclip/internal/command"
)

type flagArity string

const (
	flagArityNone        flagArity = "none"
	flagArityOne         flagArity = "one"
	flagArityMany        flagArity = "many"
	flagArityOptionalOne flagArity = "optional_one"
	flagArityOptionalTwo flagArity = "optional_two"
)

type flagSemanticFamily string

const (
	flagFamilyFileSetRefinement flagSemanticFamily = "file_set_refinement"
	flagFamilyContentFilter     flagSemanticFamily = "content_filter"
	flagFamilyGitChangeFilter   flagSemanticFamily = "git_change_filter"
	flagFamilyOutputMode        flagSemanticFamily = "output_mode"
)

type flagInteractiveRecoverability string

const (
	flagRecoverabilityNoValue       flagInteractiveRecoverability = "no_value"
	flagRecoverabilityRequiredValue flagInteractiveRecoverability = "required_value"
	flagRecoverabilityOptionalValue flagInteractiveRecoverability = "optional_value"
)

type flagSpec struct {
	Flag           string
	StageKind      command.StageKind
	Arity          flagArity
	Family         flagSemanticFamily
	BoundaryPolicy scopeStageBoundaryPolicy
	Recoverability flagInteractiveRecoverability
}

var ScopeModifierFlagSpecs = []flagSpec{
	{
		Flag:           "--include",
		StageKind:      command.StageInclude,
		Arity:          flagArityMany,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--only",
		StageKind:      command.StageOnly,
		Arity:          flagArityMany,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--exclude",
		StageKind:      command.StageExclude,
		Arity:          flagArityMany,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--recent",
		StageKind:      command.StageRecent,
		Arity:          flagArityOptionalOne,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityOptionalValue,
	},
	{
		Flag:           "--size",
		StageKind:      command.StageSize,
		Arity:          flagArityOptionalTwo,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityOptionalValue,
	},
	{
		Flag:           "--depth",
		StageKind:      command.StageDepth,
		Arity:          flagArityOne,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--contains",
		StageKind:      command.StageContains,
		Arity:          flagArityOne,
		Family:         flagFamilyContentFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--paths",
		StageKind:      command.StagePaths,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryTerminal,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--changed",
		StageKind:      command.StageChanged,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--staged",
		StageKind:      command.StageStaged,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--unstaged",
		StageKind:      command.StageUnstaged,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--untracked",
		StageKind:      command.StageUntracked,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--diff",
		StageKind:      command.StageDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--changed-diff",
		StageKind:      command.StageChangedDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--staged-diff",
		StageKind:      command.StageStagedDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--unstaged-diff",
		StageKind:      command.StageUnstagedDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--snippet",
		StageKind:      command.StageSnippet,
		Arity:          flagArityOne,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundarySnippet,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--lines",
		StageKind:      command.StageLines,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryTerminal,
		Recoverability: flagRecoverabilityNoValue,
	},
}

var scopeModifierFlagSpecsByFlag = buildScopeModifierFlagSpecsByFlag()
var scopeModifierFlagSpecsByStageKind = buildScopeModifierFlagSpecsByStageKind()

func buildScopeModifierFlagSpecsByFlag() map[string]flagSpec {
	out := make(map[string]flagSpec, len(ScopeModifierFlagSpecs))
	for _, spec := range ScopeModifierFlagSpecs {
		out[spec.Flag] = spec
	}
	return out
}

func buildScopeModifierFlagSpecsByStageKind() map[command.StageKind]flagSpec {
	out := make(map[command.StageKind]flagSpec, len(ScopeModifierFlagSpecs))
	for _, spec := range ScopeModifierFlagSpecs {
		out[spec.StageKind] = spec
	}
	return out
}

func scopeModifierFlagSpecForFlag(flag string) (flagSpec, bool) {
	spec, ok := scopeModifierFlagSpecsByFlag[flag]
	return spec, ok
}

func scopeModifierFlagSpecForStageKind(kind command.StageKind) (flagSpec, bool) {
	spec, ok := scopeModifierFlagSpecsByStageKind[kind]
	return spec, ok
}

func (f flagSemanticFamily) scopeStageCategory() (scopeStageCategory, bool) {
	switch f {
	case flagFamilyFileSetRefinement:
		return scopeStageCategorySetRefinement, true
	case flagFamilyContentFilter:
		return scopeStageCategoryContentFilter, true
	case flagFamilyGitChangeFilter:
		return scopeStageCategoryGitChangeFilter, true
	case flagFamilyOutputMode:
		return scopeStageCategoryOutputMode, true
	default:
		return "", false
	}
}

func IsValueTakingFlag(arg string) bool {
	switch arg {
	case "--include", "--only", "--exclude", "--depth", "--contains", "--snippet",
		"--internal-tree-target", "--internal-tree-kind", "--internal-tree-state",
		"--internal-file-path", "--input-dir", "--input-stem":
		return true
	default:
		return false
	}
}

func IsModifierBoundaryToken(arg string) bool {
	if arg == "--then" || arg == "--" {
		return true
	}
	if IsValueTakingFlag(arg) {
		return true
	}
	switch arg {
	case "--changed", "--staged", "--unstaged", "--untracked",
		"--changed-diff", "--staged-diff", "--unstaged-diff",
		"--recent", "--size", "--lines",
		"-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree",
		"--no-bundle", "--preview", "--with-binaries",
		"-h", "--help", "--help-all", "--version", "-V", "--hiss", "--hiss-reset", "--all-ignore-rules":
		return true
	}
	return strings.HasPrefix(arg, "--")
}

func IsKnownScopeModifierToken(arg string) bool {
	if arg == "--then" || arg == "--" {
		return true
	}
	_, ok := ScopeStageKindForFlag(arg)
	return ok
}
