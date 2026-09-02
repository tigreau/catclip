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

// extraFixedValueCounts lists fixed-arity value flags OUTSIDE the
// scope-modifier spec table: internal preview plumbing and tree payload
// inputs. Scope-modifier membership derives from spec arity below; only
// non-spec flags belong here.
var extraFixedValueCounts = map[string]int{
	"--input-dir":                   1,
	"--input-stem":                  1,
	"--internal-prediscovered":      1,
	"--internal-target-inventory":   1,
	"--internal-tree-target":        1,
	"--internal-tree-kind":          1,
	"--internal-tree-state":         1,
	"--internal-file-set-selection": 1,
	"--internal-file-set-stage":     1,
	"--internal-file-path":          1,
	"--internal-boundary-source":    1,
	"--internal-boundary-key":       1,
	"--internal-recent-data":        1,
	"--internal-sink-toggle":        1,
	"--internal-sink-preview":       3,
	"--internal-recent-selection":   1,
}

// globalBoundaryFlags lists the non-stage flags that terminate
// optional-value consumption. Kept as an explicit table (they have no
// spec entries); spec flags join the boundary set automatically below.
var globalBoundaryFlags = []string{
	"-v", "--verbose", "-q", "--quiet", "-y", "--yes", "--no", "-p", "--print", "-r", "--raw", "-t", "--no-tree",
	"--no-bundle", "--metadata", "--with-binaries",
	"-h", "--help", "--help-all", "--version", "-V", "--check-update", "--hiss", "--hiss-reset", "--all-ignore-rules",
}

// valueTakingFlags derives from spec arity (one/many) plus the explicit
// extras: adding a value-taking modifier to ScopeModifierFlagSpecs needs
// no edit here. Optional-arity flags (--recent, --size) are deliberately
// NOT value-taking — their lookahead decides per token.
var valueTakingFlags = buildValueTakingFlags()

func buildValueTakingFlags() map[string]struct{} {
	out := make(map[string]struct{}, len(ScopeModifierFlagSpecs)+len(extraFixedValueCounts))
	for _, spec := range ScopeModifierFlagSpecs {
		if spec.Arity == flagArityOne || spec.Arity == flagArityMany {
			out[spec.Flag] = struct{}{}
		}
	}
	for flag := range extraFixedValueCounts {
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
	out := make(map[string]struct{}, len(ScopeModifierFlagSpecs)+len(extraFixedValueCounts)+len(globalBoundaryFlags)+2)
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

// FixedValueCount reports how many following argv tokens a flag always
// consumes. Many-value and optional-value scope modifiers return zero because
// their consumers decide where to stop from the following tokens.
func FixedValueCount(arg string) int {
	for _, spec := range ScopeModifierFlagSpecs {
		if spec.Flag == arg && spec.Arity == flagArityOne {
			return 1
		}
	}
	return extraFixedValueCounts[arg]
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
