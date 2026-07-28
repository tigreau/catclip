package command

import (
	"strconv"
	"strings"
)

// Private quoting helpers used by command-local canonical rendering.
// Stdlib-only; keeping them here leaves internal/command dependency-free.
// User-facing POSIX/PowerShell rendering and internal fzf subprocess
// rendering are deliberately separate contracts.

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneStages(in []Stage) []Stage {
	if len(in) == 0 {
		return nil
	}
	out := make([]Stage, 0, len(in))
	for _, stage := range in {
		cloned := Stage{
			Kind:        stage.Kind,
			Values:      cloneStringSlice(stage.Values),
			Nums:        cloneIntSlice(stage.Nums),
			ExactValues: stage.ExactValues,
		}
		if stage.Limit != nil {
			limit := *stage.Limit
			cloned.Limit = &limit
		}
		out = append(out, cloned)
	}
	return out
}

func cloneIntSlice(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	return append([]int(nil), in...)
}

// shellQuoteArg renders one argv value for a POSIX shell. Plain path
// characters stay unquoted; uncomplicated values use readable double
// quotes; values that could expand inside double quotes use literal
// single-quote escaping.
func shellQuoteArg(arg string) string {
	if isPlainShellArg(arg) {
		return arg
	}
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, "$`\\\"!\n\r") {
		return `"` + arg + `"`
	}
	return shellEnforceSingleQuote(arg)
}

// shellEnforceSingleQuote always wraps arg in POSIX single quotes,
// escaping embedded single quotes. Used for regex modifiers so resolved
// commands show the pattern quoted and copy-paste safely regardless of
// $, *, or spaces in the pattern. Private dup of resolver.go's
// shellEnforceSingleQuote.
func shellEnforceSingleQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// internalShellQuoteArg preserves the established CanonicalScopeArgs
// subprocess spelling. CanonicalScopeArgs is used in fzf preview commands,
// whose Windows shell is not the user-facing PowerShell contract.
func internalShellQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\"'\\*?[]{}()$&;|<>") {
		return arg
	}
	return strconv.Quote(arg)
}

func powershellQuoteArg(arg string) string {
	if isPlainShellArg(arg) {
		return arg
	}
	return powershellEnforceSingleQuote(arg)
}

// PowerShell escapes a literal apostrophe by doubling it inside a
// single-quoted string. POSIX's close/escape/reopen sequence is not valid
// PowerShell syntax.
func powershellEnforceSingleQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "''") + "'"
}

func isPlainShellArg(arg string) bool {
	if arg == "" {
		return false
	}
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("_./:-", r):
		default:
			return false
		}
	}
	return true
}
