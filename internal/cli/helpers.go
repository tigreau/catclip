package cli

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// Private duplicates of root-package helpers used by parser / validation /
// canonical render machinery. Per reviewer guidance on the v0.6.0 cli/
// extraction: duplicate, do not export, do not move — the root copies
// stay where they are because of heavy non-cli use (resolver fzf
// commands, stage runtime, picker runtime). Mirrors the dup pattern in
// internal/search/helpers.go, internal/git/helpers.go, and
// internal/command/helpers.go.
//
// Drift risk: each helper is small (5-20 lines) with no domain logic and
// is unlikely to change. If any grows substantially, promote to a
// shared package instead of letting the implementations diverge.

const snippetContextMax = 200

// UsageError is the CLI parsing failure shape. Exported so root
// exitWithError can errors.As against it and bump exit code to 2.
type UsageError struct {
	Message string
}

func (e UsageError) Error() string {
	return e.Message
}

func newUsageError(format string, args ...any) error {
	return UsageError{Message: fmt.Sprintf(format, args...)}
}

// normalizeRelPath is the path-string utility used across the parser
// for canonicalizing positional targets and modifier values. Identical
// to root discovery.go's normalizeRelPath.
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

// hasGlobChars reports whether pattern contains any glob metacharacter.
// Identical to root cli.go's hasGlobChars (kept at root because
// resolver, stage runtime, and picker all use it).
func hasGlobChars(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// parseRecentLimitToken parses --recent's integer argument. Identical
// to root recent_stage.go's parseRecentLimitToken (kept at root because
// runtime stage code calls it too).
func parseRecentLimitToken(token string) (int, error) {
	limit, err := strconv.Atoi(token)
	if err != nil || limit <= 0 {
		return 0, newUsageError("Error: --recent takes an optional positive integer.\n  Example: catclip src --recent\n  Example: catclip src --recent 5")
	}
	return limit, nil
}

// parseDepthToken parses --depth's integer argument. Identical to root
// depth_stage.go's parseDepthToken.
func parseDepthToken(token string) (int, error) {
	depth, err := strconv.Atoi(token)
	if err != nil || depth <= 0 {
		return 0, newUsageError("Error: --depth takes a positive integer.\n  Example: catclip src --depth 2")
	}
	return depth, nil
}

// shellQuoteArg / shellEnforceSingleQuote dup the resolver.go helpers
// used by canonical render and parser error messages. See
// internal/command/helpers.go for the same pattern.

func shellQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\"'\\*?[]{}()$&;|<>") {
		return arg
	}
	return strconv.Quote(arg)
}

func shellEnforceSingleQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// singleQuoted unconditionally wraps arg in single quotes (no
// escaping). Used by some validation error message templates.
func singleQuoted(value string) string {
	return "'" + value + "'"
}

// containsParentTraversal is a private dup of root discovery.go's
// containsParentTraversal. Used by the parser's --include validation.
// Root also uses this in resolver.go and startup_picker.go, so it
// stays at root with a dup here per reviewer guidance.
func containsParentTraversal(value string) bool {
	if value == ".." {
		return true
	}
	if strings.HasPrefix(value, "../") {
		return true
	}
	if strings.HasSuffix(value, "/..") {
		return true
	}
	return strings.Contains(value, "/../")
}

// intPtr is a tiny dup of the root recent_stage.go helper. Parser uses
// it for --depth's Stage.Limit.
func intPtr(v int) *int {
	return &v
}

// consumeOptionalRecentLimit consumes an optional integer arg for
// --recent. Returns nil limit when no integer follows. Dup of
// recent_stage.go's helper because root stage code uses it too.
func consumeOptionalRecentLimit(args []string, start int) (*int, int, error) {
	if start >= len(args) {
		return nil, start, nil
	}
	next := args[start]
	if IsModifierBoundaryToken(next) {
		return nil, start, nil
	}
	limit, err := parseRecentLimitToken(next)
	if err != nil {
		return nil, start, err
	}
	return intPtr(limit), start + 1, nil
}

func cloneSliceStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}
