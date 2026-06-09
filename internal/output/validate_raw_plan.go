package output

import "github.com/tigreau/catclip/internal/command"

// ValidateRawPlan rejects --raw paired with output kinds that can't
// flow through the bareword path: --paths (which is a separate
// section, not a file emission) and snippet/diff modes (which produce
// multi-block payloads that need the <file ...> wrapper to stay
// attributable).
//
// Returns a typed UsageError so root exitWithError classifies the
// failure as exit code 2. Was root cli.go's validateRawOutputPlan
// before the v0.6.0 output extraction; moved here to keep Plan.items
// access internal to the package.
func ValidateRawPlan(cfg EmitConfig, plan Plan) error {
	if !cfg.Raw {
		return nil
	}
	for _, item := range plan.items {
		if item.kind != SectionKindFiles {
			return newUsageError("Error: --raw cannot be combined with --paths.")
		}
		switch item.mode {
		case command.EntryModeSnippet:
			return newUsageError("Error: --raw cannot be combined with snippet output.\n  Snippets can emit multiple regex-derived ranges per file; <file ... lines=\"...\"> wrappers keep those ranges attributable.\n  Omit --raw, or use --lines with --raw when you want one explicit raw line slice.")
		case command.EntryModeDiff:
			return newUsageError("Error: --raw cannot be combined with diff output.")
		case command.EntryModeFull, command.EntryModeLines:
			// OK
		default:
			return newUsageError("Error: --raw cannot be combined with this output kind.")
		}
	}
	return nil
}
