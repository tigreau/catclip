package command

import (
	"path"
	"strconv"
	"strings"
)

// Private duplicates of root-package helpers used by command-local code
// (RewriteDeepIncludeScope, canonical render). Stdlib-only; duplicating
// keeps internal/command a leaf with no catclip-domain imports. Mirrors
// internal/search/helpers.go and internal/git/helpers.go.
//
// shellQuoteArg and shellEnforceSingleQuote are root copies — the
// authoritative versions live in resolver.go and are used heavily
// outside the parser/canonical render. Per reviewer guidance: dup
// (don't move), so root keeps its versions for fzf preview commands
// and other shell-bound contexts.

func normalizeRelPath(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "./")
	if value == "." || value == "/" {
		return "."
	}
	return value
}

func hasGlobChars(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func includeTargetsContainWildcard(targets []string) bool {
	for _, t := range targets {
		if t == "*" {
			return true
		}
	}
	return false
}

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

// shellQuoteArg wraps arg only when it contains shell-metacharacters or
// whitespace. Default printable identifiers and paths pass through
// unchanged so the canonical command stays readable. Private dup of
// resolver.go's shellQuoteArg.
func shellQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\"'\\*?[]{}()$&;|<>") {
		return arg
	}
	return strconv.Quote(arg)
}

// shellEnforceSingleQuote always wraps arg in POSIX single quotes,
// escaping embedded single quotes. Used for regex modifiers so resolved
// commands show the pattern quoted and copy-paste safely regardless of
// $, *, or spaces in the pattern. Private dup of resolver.go's
// shellEnforceSingleQuote.
func shellEnforceSingleQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
