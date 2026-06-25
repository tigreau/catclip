package cli

import "fmt"

// Reason identifies the specific validation rule the parser violated.
// Used to dispatch error messages and to drive structured shell
// integration. Renamed from root validationReason during the v0.6.0
// cli/ extraction.
type Reason string

const (
	ReasonRequiredValue                  Reason = "required_value"
	ReasonNoValueModifier                Reason = "no_value_modifier"
	ReasonBarePlaceholderOrder           Reason = "bare_placeholder_order"
	ReasonBarePlaceholderInteractiveOnly Reason = "bare_placeholder_interactive_only"
	ReasonBarePlaceholderHeadlessMode    Reason = "bare_placeholder_headless_mode"
	ReasonDiffStandalone                 Reason = "diff_standalone"
	ReasonMissingDiffSelector            Reason = "missing_diff_selector"
	ReasonUntrackedDiff                  Reason = "untracked_diff"
	ReasonPositionalAfterModifier        Reason = "positional_after_modifier"
	ReasonOutputModeConflict             Reason = "output_mode_conflict"
	ReasonDiffSnippetConflict            Reason = "diff_snippet_conflict"
	ReasonDiffContentFilterOrder         Reason = "diff_content_filter_order"
	ReasonDiffGitFilterOrder             Reason = "diff_git_filter_order"
	ReasonSnippetContentFilterOrder      Reason = "snippet_content_filter_order"
	ReasonRepeatedOutputMode             Reason = "repeated_output_mode"
	ReasonTerminalBoundaryOrder          Reason = "terminal_boundary_order"
	ReasonIncludeAfterModifier           Reason = "include_after_modifier"
	ReasonRepeatedInclude                Reason = "repeated_include"
)

// ValidationFailure is the structured error the parser returns on rule
// violations. Exported so exitWithError at root can errors.As against
// it and bump the exit code to 2.
type ValidationFailure struct {
	Reason       Reason
	Flag         string
	BoundaryFlag string
	NextFlag     string
	Suggestion   string
}

func (e ValidationFailure) Error() string {
	return renderValidationFailure(e)
}

func renderValidationFailure(e ValidationFailure) string {
	switch e.Reason {
	case ReasonRequiredValue:
		return renderRequiredValueValidationFailure(e)
	case ReasonNoValueModifier:
		switch e.Flag {
		case "--changed-diff", "--staged-diff", "--unstaged-diff":
			return fmt.Sprintf("Error: %s takes no value.\n  Use %s by itself, or start a new scope with --then.", e.Flag, e.Flag)
		default:
			return fmt.Sprintf("Error: %s takes no value.\n  Put targets before modifiers, or start a new scope with --then.", e.Flag)
		}
	case ReasonBarePlaceholderOrder:
		return "Error: bare -- can only be followed by another bare -- in the same scope.\n  It opens interactive modifier selection, so explicit modifiers and values cannot appear to its right."
	case ReasonBarePlaceholderInteractiveOnly:
		return "Error: -- opens interactive modifier selection.\n  Run catclip in an interactive terminal, or remove the trailing --."
	case ReasonBarePlaceholderHeadlessMode:
		return "Error: -- opens interactive modifier selection, which is unavailable in headless mode (--headless).\n  Replace -- with explicit modifiers (--only, --recent, --snippet, ...), or drop --headless to run interactively."
	case ReasonDiffStandalone:
		return "Error: --diff is no longer a standalone modifier.\n  Use --changed-diff, --staged-diff, or --unstaged-diff."
	case ReasonMissingDiffSelector:
		return "Error: diff output requires --changed-diff, --staged-diff, or --unstaged-diff.\n  Example: catclip src --changed-diff\n  Example: catclip src --staged-diff"
	case ReasonUntrackedDiff:
		return "Error: --untracked-diff doesn't make sense (untracked files have no diff).\n  Try: catclip src --changed-diff    (includes untracked as full content)\n  Try: catclip src --staged-diff     (only staged patches)"
	case ReasonPositionalAfterModifier:
		return "Error: positional targets must come before modifiers.\n  Add targets first, use --include, or use --then for a new scope."
	case ReasonOutputModeConflict:
		return fmt.Sprintf("Error: %s and %s cannot be combined in the same scope.\n  Use --then to start a new scope for a different output mode.", e.BoundaryFlag, e.Flag)
	case ReasonDiffSnippetConflict:
		return "Error: --snippet and --diff cannot be combined.\n  Use --snippet to extract blocks around content matches.\n  Use --diff to show unified git patches."
	case ReasonDiffContentFilterOrder:
		return fmt.Sprintf("Error: %s must come before %s in the same scope.\n  %s commits the scope to patch output, so content filters cannot come after it.\n  Put %s before %s, or start a new scope with --then.", e.Flag, e.BoundaryFlag, e.BoundaryFlag, e.Flag, e.BoundaryFlag)
	case ReasonDiffGitFilterOrder:
		return fmt.Sprintf("Error: %s must come before %s in the same scope.\n  %s should come after git selection is settled, so later git change filters are not allowed.\n  Put %s before %s, or start a new scope with --then.", e.Flag, e.BoundaryFlag, e.BoundaryFlag, e.Flag, e.BoundaryFlag)
	case ReasonSnippetContentFilterOrder:
		return fmt.Sprintf("Error: %s must come before --snippet in the same scope.\n  --snippet commits the scope to snippet output, so content filters cannot come after it.\n  Put %s before --snippet, or start a new scope with --then.", e.Flag, e.Flag)
	case ReasonRepeatedOutputMode:
		return fmt.Sprintf("Error: %s cannot be repeated in the same scope.\n  %s already commits the scope to an output mode.\n  Start a new scope with --then.", e.Flag, e.Flag)
	case ReasonTerminalBoundaryOrder:
		return fmt.Sprintf("Error: %s finalizes the current scope.\n  No later same-scope modifiers are allowed after %s.\n  Start a new scope with --then.", e.BoundaryFlag, e.BoundaryFlag)
	case ReasonIncludeAfterModifier:
		return "Error: --include must come before other modifiers in the same scope.\n  --include adds files, so it must appear before filters that narrow them.\n  Move --include before other modifiers, or start a new scope with --then."
	case ReasonRepeatedInclude:
		return "Error: --include can only appear once per scope.\n  Combine targets in a single --include, or start a new scope with --then."
	default:
		return "Error: invalid command."
	}
}

func renderRequiredValueValidationFailure(e ValidationFailure) string {
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
	case "--not-contains":
		return "Error: --not-contains requires a regex pattern.\n  Example: catclip src --not-contains 'TODO'"
	case "--snippet":
		return "Error: --snippet requires a regex pattern.\n  Example: catclip src --snippet 'TODO'"
	case "--depth":
		return "Error: --depth requires a positive integer.\n  Example: catclip src --depth 2"
	default:
		return "Error: --only requires a pattern.\n  Example: catclip src --only '*.ts'"
	}
}

// Error constructors — exported so root parser-side helpers
// (startup_picker, startup_undo, contains_usage at root, plus cli's
// own parser) can construct the validation failures.

func RequiredStageValueError(flag string) error {
	return ValidationFailure{Reason: ReasonRequiredValue, Flag: flag}
}

// RegexModifierExtraValueError fires when a bare token follows a regex
// modifier (--contains REGEX or --snippet REGEX [N]). The usual cause
// is an unquoted regex with spaces that the shell split into separate
// args, so the hint reconstructs the quoted form.
func RegexModifierExtraValueError(flag, regex, extra string) error {
	takes := "one regex (quote it if it contains spaces)"
	if flag == "--snippet" {
		takes += " and an optional context number"
	}
	return newUsageError("Error: %s takes %s, got extra value %q.\n  Did you mean: %s %s", flag, takes, extra, flag, shellEnforceSingleQuote(regex+" "+extra))
}

func NoValueModifierError(flag string) error {
	return ValidationFailure{Reason: ReasonNoValueModifier, Flag: flag}
}

func BareModifierPlaceholderOrderError() error {
	return ValidationFailure{Reason: ReasonBarePlaceholderOrder}
}

func BareModifierPlaceholderInteractiveOnlyError() error {
	return ValidationFailure{Reason: ReasonBarePlaceholderInteractiveOnly}
}

func BareModifierPlaceholderHeadlessModeError() error {
	return ValidationFailure{Reason: ReasonBarePlaceholderHeadlessMode}
}

func DiffStandaloneError() error {
	return ValidationFailure{Reason: ReasonDiffStandalone}
}

func MissingDiffSelectorError() error {
	return ValidationFailure{Reason: ReasonMissingDiffSelector}
}

func UntrackedDiffError() error {
	return ValidationFailure{Reason: ReasonUntrackedDiff}
}

func PositionalAfterModifierError() error {
	return ValidationFailure{Reason: ReasonPositionalAfterModifier}
}
