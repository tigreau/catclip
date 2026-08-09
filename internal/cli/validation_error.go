package cli

import (
	"fmt"
	"strings"
)

// Reason identifies the specific validation rule the parser violated.
// Used to dispatch error messages and to drive structured shell
// integration. Renamed from root validationReason during the v0.6.0
// cli/ extraction.
type Reason string

const (
	ReasonRequiredValue                   Reason = "required_value"
	ReasonNoValueModifier                 Reason = "no_value_modifier"
	ReasonBarePlaceholderOrder            Reason = "bare_placeholder_order"
	ReasonBarePlaceholderInteractiveOnly  Reason = "bare_placeholder_interactive_only"
	ReasonBarePlaceholderHeadlessMode     Reason = "bare_placeholder_headless_mode"
	ReasonDiffStandalone                  Reason = "diff_standalone"
	ReasonMissingDiffSelector             Reason = "missing_diff_selector"
	ReasonUntrackedDiff                   Reason = "untracked_diff"
	ReasonPositionalAfterModifier         Reason = "positional_after_modifier"
	ReasonOutputModeConflict              Reason = "output_mode_conflict"
	ReasonDiffSnippetConflict             Reason = "diff_snippet_conflict"
	ReasonDiffContentFilterOrder          Reason = "diff_content_filter_order"
	ReasonDiffGitFilterOrder              Reason = "diff_git_filter_order"
	ReasonSnippetContentFilterOrder       Reason = "snippet_content_filter_order"
	ReasonRepeatedOutputMode              Reason = "repeated_output_mode"
	ReasonTerminalBoundaryOrder           Reason = "terminal_boundary_order"
	ReasonNoIgnoreAfterModifier           Reason = "no_ignore_after_modifier"
	ReasonRepeatedNoIgnore                Reason = "repeated_no_ignore"
	ReasonNoIgnoreMissingPositionalTarget Reason = "no_ignore_missing_positional_target"
	ReasonUnsupportedDoubleStar           Reason = "unsupported_double_star"
)

// ValidationFailure is the structured error the parser returns on rule
// violations. CatclipExitCode participates in root's structural exit protocol.
type ValidationFailure struct {
	Reason       Reason
	Flag         string
	BoundaryFlag string
	NextFlag     string
	Suggestion   string
	Value        string
}

func (e ValidationFailure) CatclipExitCode() int { return 2 }

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
		return "Error: positional targets must come before modifiers.\n  Add targets first, or use --then for a new scope."
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
	case ReasonNoIgnoreAfterModifier:
		return "Error: --no-ignore must come before other modifiers in the same scope.\n  It changes which files enter the scope, so filters must come after it.\n  Move --no-ignore before other modifiers, or start a new scope with --then."
	case ReasonRepeatedNoIgnore:
		return "Error: --no-ignore can only appear once per scope.\n  Remove the duplicate, or start a new scope with --then."
	case ReasonNoIgnoreMissingPositionalTarget:
		return "Error: --no-ignore requires a positional target.\n\n  Add the folder whose ignored files should be included:\n    catclip . --no-ignore\n    catclip src --no-ignore"
	case ReasonUnsupportedDoubleStar:
		return renderUnsupportedDoubleStarValidationFailure(e.Flag, e.Value)
	default:
		return "Error: invalid command."
	}
}

func renderRequiredValueValidationFailure(e ValidationFailure) string {
	switch e.Flag {
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

func UnknownOptionError(value string) error {
	return newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(value))
}

func ValidateSnippetContext(value int) error {
	if value < 0 || value > SnippetContextMax {
		return newUsageError("Error: --snippet context must be between 0 and %d (got %d).\n  Use: --snippet 'REGEX' N for N lines around each match (0 = matching line only).", SnippetContextMax, value)
	}
	return nil
}

func LinesStartError(value int) error {
	return newUsageError("Error: --lines start must be >= 1 (got %d).\n  Line numbers are 1-based, matching editors and compiler output.", value)
}

func LinesEndBeforeStartError(end, start int) error {
	return newUsageError("Error: --lines end (%d) must be >= start (%d).\n  Use: --lines START END where END >= START.", end, start)
}

func LinesInvalidValueError(value string) error {
	return newUsageError("Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got %q).", value)
}

func NoIgnoreMissingPositionalTargetError() error {
	return ValidationFailure{Reason: ReasonNoIgnoreMissingPositionalTarget, Flag: "--no-ignore"}
}

func IncludeUnsupportedError() error {
	return newUsageError("Error: --include is not a supported option.\n\n" +
		"  Name an ignored file or directory as a target:\n" +
		"    catclip src/generated\n\n" +
		"  To disable ignore rules below a target:\n" +
		"    catclip src --no-ignore")
}

func IsUnsupportedIncludeOption(token string) bool {
	return token == "--include" || strings.HasPrefix(token, "--include=")
}

func UnsupportedDoubleStarError(surface, value string) error {
	return ValidationFailure{
		Reason: ReasonUnsupportedDoubleStar,
		Flag:   surface,
		Value:  value,
	}
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
