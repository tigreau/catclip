package catclip

import "strings"

type flagArity string

const (
	flagArityNone        flagArity = "none"
	flagArityOne         flagArity = "one"
	flagArityMany        flagArity = "many"
	flagArityOptionalOne flagArity = "optional_one"
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
	StageKind      scopeStageKind
	Arity          flagArity
	Family         flagSemanticFamily
	BoundaryPolicy scopeStageBoundaryPolicy
	Recoverability flagInteractiveRecoverability
}

var scopeModifierFlagSpecs = []flagSpec{
	{
		Flag:           "--include",
		StageKind:      scopeStageInclude,
		Arity:          flagArityMany,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--only",
		StageKind:      scopeStageOnly,
		Arity:          flagArityMany,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--exclude",
		StageKind:      scopeStageExclude,
		Arity:          flagArityMany,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--recent",
		StageKind:      scopeStageRecent,
		Arity:          flagArityOptionalOne,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityOptionalValue,
	},
	{
		Flag:           "--depth",
		StageKind:      scopeStageDepth,
		Arity:          flagArityOne,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--contains",
		StageKind:      scopeStageContains,
		Arity:          flagArityOne,
		Family:         flagFamilyContentFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--paths",
		StageKind:      scopeStagePaths,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryTerminal,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--changed",
		StageKind:      scopeStageChanged,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--staged",
		StageKind:      scopeStageStaged,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--unstaged",
		StageKind:      scopeStageUnstaged,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--untracked",
		StageKind:      scopeStageUntracked,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--diff",
		StageKind:      scopeStageDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--changed-diff",
		StageKind:      scopeStageChangedDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--staged-diff",
		StageKind:      scopeStageStagedDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--unstaged-diff",
		StageKind:      scopeStageUnstagedDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		Flag:           "--snippet",
		StageKind:      scopeStageSnippet,
		Arity:          flagArityOne,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundarySnippet,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		Flag:           "--lines",
		StageKind:      scopeStageLines,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryTerminal,
		Recoverability: flagRecoverabilityNoValue,
	},
}

var scopeModifierFlagSpecsByFlag = buildScopeModifierFlagSpecsByFlag()
var scopeModifierFlagSpecsByStageKind = buildScopeModifierFlagSpecsByStageKind()

func buildScopeModifierFlagSpecsByFlag() map[string]flagSpec {
	out := make(map[string]flagSpec, len(scopeModifierFlagSpecs))
	for _, spec := range scopeModifierFlagSpecs {
		out[spec.Flag] = spec
	}
	return out
}

func buildScopeModifierFlagSpecsByStageKind() map[scopeStageKind]flagSpec {
	out := make(map[scopeStageKind]flagSpec, len(scopeModifierFlagSpecs))
	for _, spec := range scopeModifierFlagSpecs {
		out[spec.StageKind] = spec
	}
	return out
}

func scopeModifierFlagSpecForFlag(flag string) (flagSpec, bool) {
	spec, ok := scopeModifierFlagSpecsByFlag[flag]
	return spec, ok
}

func scopeModifierFlagSpecForStageKind(kind scopeStageKind) (flagSpec, bool) {
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

func isValueTakingFlag(arg string) bool {
	switch arg {
	case "--include", "--only", "--exclude", "--depth", "--contains", "--snippet",
		"--internal-tree-target", "--internal-tree-kind", "--internal-tree-state",
		"--internal-file-path":
		return true
	default:
		return false
	}
}

func isModifierBoundaryToken(arg string) bool {
	if arg == "--then" || arg == "--" {
		return true
	}
	if isValueTakingFlag(arg) {
		return true
	}
	switch arg {
	case "--changed", "--staged", "--unstaged", "--untracked",
		"--changed-diff", "--staged-diff", "--unstaged-diff",
		"--recent", "--lines",
		"-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree",
		"--no-bundle", "--preview", "--with-binaries",
		"-h", "--help", "--help-all", "--version", "-V", "--hiss", "--hiss-reset":
		return true
	}
	return strings.HasPrefix(arg, "--")
}

func isKnownScopeModifierToken(arg string) bool {
	if arg == "--then" || arg == "--" {
		return true
	}
	_, ok := scopeStageKindForFlag(arg)
	return ok
}
