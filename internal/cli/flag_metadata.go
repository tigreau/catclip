package cli

import (
	"fmt"
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

// ScopeModifierFlagSpecs is the parser-side registry of scope-modifier
// stages. Flag spellings are NOT written here — the builder below joins
// each entry to command's stageFlags table (the single spelling home
// shared with canonical rendering) and panics at init if a kind has no
// entry there.
var ScopeModifierFlagSpecs = buildScopeModifierFlagSpecs()

func buildScopeModifierFlagSpecs() []flagSpec {
	specs := scopeModifierFlagSpecTable
	for i := range specs {
		flag, ok := command.StageFlag(specs[i].StageKind)
		if !ok {
			panic(fmt.Sprintf("cli: no canonical flag registered in command.StageFlags for stage kind %q", specs[i].StageKind))
		}
		specs[i].Flag = flag
	}
	return specs
}

var scopeModifierFlagSpecTable = []flagSpec{
	{
		StageKind:      command.StageNoIgnore,
		Arity:          flagArityNone,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageOnly,
		Arity:          flagArityMany,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		StageKind:      command.StageExclude,
		Arity:          flagArityMany,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		StageKind:      command.StageRecent,
		Arity:          flagArityOptionalOne,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityOptionalValue,
	},
	{
		StageKind:      command.StageSize,
		Arity:          flagArityOptionalTwo,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityOptionalValue,
	},
	{
		StageKind:      command.StageDepth,
		Arity:          flagArityOne,
		Family:         flagFamilyFileSetRefinement,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		StageKind:      command.StageContains,
		Arity:          flagArityOne,
		Family:         flagFamilyContentFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		StageKind:      command.StageNotContains,
		Arity:          flagArityOne,
		Family:         flagFamilyContentFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
		StageKind:      command.StagePaths,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryTerminal,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageChanged,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageStaged,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageUnstaged,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageUntracked,
		Arity:          flagArityNone,
		Family:         flagFamilyGitChangeFilter,
		BoundaryPolicy: scopeStageBoundaryNone,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageChangedDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageStagedDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageUnstagedDiff,
		Arity:          flagArityNone,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundaryDiff,
		Recoverability: flagRecoverabilityNoValue,
	},
	{
		StageKind:      command.StageSnippet,
		Arity:          flagArityOne,
		Family:         flagFamilyOutputMode,
		BoundaryPolicy: scopeStageBoundarySnippet,
		Recoverability: flagRecoverabilityRequiredValue,
	},
	{
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

// extraValueTakingFlags lists value-taking flags OUTSIDE the
// scope-modifier spec table: internal preview plumbing and tree payload
// inputs. Scope-modifier membership derives from spec arity below; only
// non-spec flags belong here.
var extraValueTakingFlags = []string{
	"--internal-tree-target", "--internal-tree-kind", "--internal-tree-state",
	"--internal-file-set-selection", "--internal-file-set-stage",
	"--internal-file-path", "--input-dir", "--input-stem",
}

// globalBoundaryFlags lists the non-stage flags that terminate
// optional-value consumption. Kept as an explicit table (they have no
// spec entries); spec flags join the boundary set automatically below.
var globalBoundaryFlags = []string{
	"-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree",
	"--no-bundle", "--preview", "--with-binaries",
	"-h", "--help", "--help-all", "--version", "-V", "--hiss", "--hiss-reset", "--all-ignore-rules",
}

// valueTakingFlags derives from spec arity (one/many) plus the explicit
// extras: adding a value-taking modifier to ScopeModifierFlagSpecs needs
// no edit here. Optional-arity flags (--recent, --size) are deliberately
// NOT value-taking — their lookahead decides per token.
var valueTakingFlags = buildValueTakingFlags()

func buildValueTakingFlags() map[string]struct{} {
	out := make(map[string]struct{}, len(ScopeModifierFlagSpecs)+len(extraValueTakingFlags))
	for _, spec := range ScopeModifierFlagSpecs {
		if spec.Arity == flagArityOne || spec.Arity == flagArityMany {
			out[spec.Flag] = struct{}{}
		}
	}
	for _, flag := range extraValueTakingFlags {
		out[flag] = struct{}{}
	}
	return out
}

// modifierBoundaryTokens is the derived KNOWN-boundary set: --then, the
// bare -- placeholder, every spec flag regardless of parse policy
// (rejected-standalone --diff is still a boundary — pinned by
// TestOptionalValueConsumersStopAtBoundaryFlags), every value-taking
// flag, and the global table. The strings.HasPrefix("--") fallback in
// IsModifierBoundaryToken remains responsible ONLY for tokens absent
// from every table, so optional-value consumers stop before unknown
// --foo and the parser reports the real unknown-option error.
var modifierBoundaryTokens = buildModifierBoundaryTokens()

func buildModifierBoundaryTokens() map[string]struct{} {
	out := make(map[string]struct{}, len(ScopeModifierFlagSpecs)+len(extraValueTakingFlags)+len(globalBoundaryFlags)+2)
	out["--then"] = struct{}{}
	out["--"] = struct{}{}
	for _, spec := range ScopeModifierFlagSpecs {
		out[spec.Flag] = struct{}{}
	}
	for flag := range valueTakingFlags {
		out[flag] = struct{}{}
	}
	for _, flag := range globalBoundaryFlags {
		out[flag] = struct{}{}
	}
	return out
}

func IsValueTakingFlag(arg string) bool {
	_, ok := valueTakingFlags[arg]
	return ok
}

func IsModifierBoundaryToken(arg string) bool {
	if _, ok := modifierBoundaryTokens[arg]; ok {
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
