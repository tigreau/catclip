package catclip

import "fmt"

type validationReason string

const (
	validationReasonRequiredValue                  validationReason = "required_value"
	validationReasonNoValueModifier                validationReason = "no_value_modifier"
	validationReasonBarePlaceholderOrder           validationReason = "bare_placeholder_order"
	validationReasonBarePlaceholderInteractiveOnly validationReason = "bare_placeholder_interactive_only"
	validationReasonDiffStandalone                 validationReason = "diff_standalone"
	validationReasonMissingDiffSelector            validationReason = "missing_diff_selector"
	validationReasonUntrackedDiff                  validationReason = "untracked_diff"
	validationReasonPositionalAfterModifier        validationReason = "positional_after_modifier"
	validationReasonOutputModeConflict             validationReason = "output_mode_conflict"
	validationReasonDiffSnippetConflict            validationReason = "diff_snippet_conflict"
	validationReasonDiffContentFilterOrder         validationReason = "diff_content_filter_order"
	validationReasonDiffGitFilterOrder             validationReason = "diff_git_filter_order"
	validationReasonSnippetContentFilterOrder      validationReason = "snippet_content_filter_order"
	validationReasonRepeatedOutputMode             validationReason = "repeated_output_mode"
	validationReasonTerminalBoundaryOrder          validationReason = "terminal_boundary_order"
	validationReasonIncludeAfterModifier           validationReason = "include_after_modifier"
	validationReasonRepeatedInclude                validationReason = "repeated_include"
)

type validationFailure struct {
	Reason       validationReason
	Flag         string
	BoundaryFlag string
	NextFlag     string
	Suggestion   string
}

func (e validationFailure) Error() string {
	return renderValidationFailure(e)
}

func renderValidationFailure(e validationFailure) string {
	switch e.Reason {
	case validationReasonRequiredValue:
		return renderRequiredValueValidationFailure(e)
	case validationReasonNoValueModifier:
		switch e.Flag {
		case "--changed-diff", "--staged-diff", "--unstaged-diff":
			return fmt.Sprintf("Error: %s takes no value.\n  Use %s by itself, or start a new scope with --then.", e.Flag, e.Flag)
		default:
			return fmt.Sprintf("Error: %s takes no value.\n  Put targets before modifiers, or start a new scope with --then.", e.Flag)
		}
	case validationReasonBarePlaceholderOrder:
		return "Error: bare -- can only be followed by another bare -- in the same scope.\n  It opens interactive modifier selection, so explicit modifiers and values cannot appear to its right."
	case validationReasonBarePlaceholderInteractiveOnly:
		return "Error: -- opens interactive modifier selection.\n  Run catclip in an interactive terminal, or remove the trailing --."
	case validationReasonDiffStandalone:
		return "Error: --diff is no longer a standalone modifier.\n  Use --changed-diff, --staged-diff, or --unstaged-diff."
	case validationReasonMissingDiffSelector:
		return "Error: diff output requires --changed-diff, --staged-diff, or --unstaged-diff.\n  Example: catclip src --changed-diff\n  Example: catclip src --staged-diff"
	case validationReasonUntrackedDiff:
		return "Error: --untracked-diff doesn't make sense (untracked files have no diff).\n  Try: catclip src --changed-diff    (includes untracked as full content)\n  Try: catclip src --staged-diff     (only staged patches)"
	case validationReasonPositionalAfterModifier:
		return "Error: positional targets must come before modifiers.\n  Add targets first, use --include, or use --then for a new scope."
	case validationReasonOutputModeConflict:
		return fmt.Sprintf("Error: %s and %s cannot be combined in the same scope.\n  Use --then to start a new scope for a different output mode.", e.BoundaryFlag, e.Flag)
	case validationReasonDiffSnippetConflict:
		return "Error: --snippet and --diff cannot be combined.\n  Use --snippet to extract blocks around content matches.\n  Use --diff to show unified git patches."
	case validationReasonDiffContentFilterOrder:
		return fmt.Sprintf("Error: %s must come before %s in the same scope.\n  %s commits the scope to patch output, so content filters cannot come after it.\n  Put %s before %s, or start a new scope with --then.", e.Flag, e.BoundaryFlag, e.BoundaryFlag, e.Flag, e.BoundaryFlag)
	case validationReasonDiffGitFilterOrder:
		return fmt.Sprintf("Error: %s must come before %s in the same scope.\n  %s should come after git selection is settled, so later git change filters are not allowed.\n  Put %s before %s, or start a new scope with --then.", e.Flag, e.BoundaryFlag, e.BoundaryFlag, e.Flag, e.BoundaryFlag)
	case validationReasonSnippetContentFilterOrder:
		return fmt.Sprintf("Error: %s must come before --snippet in the same scope.\n  --snippet commits the scope to snippet output, so content filters cannot come after it.\n  Put %s before --snippet, or start a new scope with --then.", e.Flag, e.Flag)
	case validationReasonRepeatedOutputMode:
		return fmt.Sprintf("Error: %s cannot be repeated in the same scope.\n  %s already commits the scope to an output mode.\n  Start a new scope with --then.", e.Flag, e.Flag)
	case validationReasonTerminalBoundaryOrder:
		return fmt.Sprintf("Error: %s finalizes the current scope.\n  No later same-scope modifiers are allowed after %s.\n  Start a new scope with --then.", e.BoundaryFlag, e.BoundaryFlag)
	case validationReasonIncludeAfterModifier:
		return "Error: --include must come before other modifiers in the same scope.\n  --include adds files, so it must appear before filters that narrow them.\n  Move --include before other modifiers, or start a new scope with --then."
	case validationReasonRepeatedInclude:
		return "Error: --include can only appear once per scope.\n  Combine targets in a single --include, or start a new scope with --then."
	default:
		return "Error: invalid command."
	}
}

func renderRequiredValueValidationFailure(e validationFailure) string {
	switch e.Flag {
	case "--include":
		return "Error: --include requires a target query.\n  Example: catclip . --include node_modules\n  Example: catclip src --include vendor"
	case "--exclude":
		return "Error: --exclude requires a pattern.\n  Example: catclip src --exclude '*.test.*'"
	case "--contains":
		base := "Error: --contains requires a regex pattern.\n  Example: catclip src --contains 'TODO'"
		if e.Suggestion == "" {
			return base
		}
		return fmt.Sprintf("%s\n  Did you mean: %s", base, e.Suggestion)
	case "--snippet":
		return "Error: --snippet requires a regex pattern.\n  Example: catclip src --snippet 'TODO'"
	case "--depth":
		return "Error: --depth requires a positive integer.\n  Example: catclip src --depth 2"
	default:
		return "Error: --only requires a pattern.\n  Example: catclip src --only '*.ts'"
	}
}

func requiredStageValueError(flag string) error {
	return validationFailure{Reason: validationReasonRequiredValue, Flag: flag}
}

func noValueModifierError(flag string) error {
	return validationFailure{Reason: validationReasonNoValueModifier, Flag: flag}
}

func bareModifierPlaceholderOrderError() error {
	return validationFailure{Reason: validationReasonBarePlaceholderOrder}
}

func bareModifierPlaceholderInteractiveOnlyError() error {
	return validationFailure{Reason: validationReasonBarePlaceholderInteractiveOnly}
}

func diffStandaloneError() error {
	return validationFailure{Reason: validationReasonDiffStandalone}
}

func missingDiffSelectorError() error {
	return validationFailure{Reason: validationReasonMissingDiffSelector}
}

func untrackedDiffError() error {
	return validationFailure{Reason: validationReasonUntrackedDiff}
}

func positionalAfterModifierError() error {
	return validationFailure{Reason: validationReasonPositionalAfterModifier}
}
