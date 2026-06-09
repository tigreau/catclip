package catclip

import (
	"os"
	"reflect"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
)

type positionalGlobNormalizationResult struct {
	Args  []string
	Hints []string
}

type positionalGlobScope struct {
	raw        []string
	positional []positionalGlobToken
	tail       []string
}

type positionalGlobToken struct {
	raw  string
	kind positionalGlobTokenKind
}

type positionalGlobTokenKind string

const (
	positionalGlobPlain       positionalGlobTokenKind = "plain"
	positionalGlobWrapperStar positionalGlobTokenKind = "wrapper-star"
	positionalGlobPattern     positionalGlobTokenKind = "pattern"
	positionalGlobBareExt     positionalGlobTokenKind = "bare-ext"
)

type positionalGlobScopeNormalization struct {
	rewritten          []string
	rewrittenAsTargets []string
	hints              []string
	fixItKind          positionalGlobTokenKind
	fixItRaw           string
	ambiguous          bool
	ambiguityRaw       string
	ambiguityThen      [][]string
	ambiguityOne       []string
}

func normalizePositionalGlobArgs(args []string, quiet bool) (positionalGlobNormalizationResult, error) {
	scopes := splitPositionalGlobScopes(args)
	if len(scopes) == 0 {
		return positionalGlobNormalizationResult{Args: append([]string(nil), args...)}, nil
	}

	normalizedScopes := make([][]string, 0, len(scopes))
	hints := []string{}

	for i, scope := range scopes {
		norm := normalizePositionalGlobScope(scope, quiet)
		if norm.ambiguous {
			suffix := canonicalizePositionalGlobScopes(scopes[i+1:])
			thenCommand := joinPositionalGlobCommand(normalizedScopes, norm.ambiguityThen, suffix)
			oneCommand := joinPositionalGlobCommand(normalizedScopes, [][]string{norm.ambiguityOne}, suffix)
			return positionalGlobNormalizationResult{}, newUsageError(
				"Error: %s is a pattern, not a target.\n  Patterns and targets can't be interleaved in one scope.\n\n  If you want different filters per target, use --then:\n    %s\n\n  If you want one filter across both targets:\n    %s",
				discovery.SingleQuoted(norm.ambiguityRaw),
				cli.FormatResolvedStartupCommand(thenCommand),
				cli.FormatResolvedStartupCommand(oneCommand),
			)
		}

		if norm.fixItKind != "" {
			suffix := canonicalizePositionalGlobScopes(scopes[i+1:])
			onlyCommand := joinPositionalGlobCommand(normalizedScopes, [][]string{norm.rewritten}, suffix)
			targetsCommand := joinPositionalGlobCommand(normalizedScopes, [][]string{norm.rewrittenAsTargets}, suffix)
			noun := "pattern"
			if norm.fixItKind == positionalGlobBareExt {
				noun = "bare extension"
			}
			// When the scope had real targets typed alongside the pattern,
			// the --only form and the targets-list form mean different
			// things: --only narrows to pattern *within* the existing
			// targets; the targets-list form unions the pattern globally
			// with the existing targets. Show both so the user picks the
			// intent. When there were no real targets, the two forms are
			// equivalent and we show just the shorter glob-as-target form.
			if reflect.DeepEqual(onlyCommand, targetsCommand) {
				return positionalGlobNormalizationResult{}, newUsageError(
					"Error: %s is a %s, not a target.\n  Use it as a glob target:\n    %s",
					discovery.SingleQuoted(norm.fixItRaw),
					noun,
					cli.FormatResolvedStartupCommand(targetsCommand),
				)
			}
			return positionalGlobNormalizationResult{}, newUsageError(
				"Error: %s is a %s, not a target.\n  To filter the existing targets to %s:\n    %s\n  To also include %s files anywhere in the project:\n    %s",
				discovery.SingleQuoted(norm.fixItRaw),
				noun,
				norm.fixItRaw,
				cli.FormatResolvedStartupCommand(onlyCommand),
				norm.fixItRaw,
				cli.FormatResolvedStartupCommand(targetsCommand),
			)
		}

		normalizedScopes = append(normalizedScopes, norm.rewritten)
		hints = append(hints, norm.hints...)
	}

	return positionalGlobNormalizationResult{
		Args:  flattenPositionalGlobCommand(normalizedScopes),
		Hints: hints,
	}, nil
}

func splitPositionalGlobScopes(args []string) []positionalGlobScope {
	rawScopes := make([][]string, 0, 4)
	current := make([]string, 0, len(args))
	consumeNext := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if consumeNext {
			current = append(current, arg)
			consumeNext = false
			continue
		}
		if arg == "--" {
			current = append(current, args[i:]...)
			rawScopes = append(rawScopes, current)
			return analyzePositionalGlobScopes(rawScopes)
		}
		if arg == "--then" {
			rawScopes = append(rawScopes, current)
			current = make([]string, 0, len(args)-i-1)
			continue
		}
		current = append(current, arg)
		if cli.IsValueTakingFlag(arg) {
			consumeNext = true
		}
	}
	rawScopes = append(rawScopes, current)
	return analyzePositionalGlobScopes(rawScopes)
}

func analyzePositionalGlobScopes(rawScopes [][]string) []positionalGlobScope {
	scopes := make([]positionalGlobScope, 0, len(rawScopes))
	for _, raw := range rawScopes {
		positionalEnd := len(raw)
		for i, token := range raw {
			if isPositionalGlobTailToken(token) {
				positionalEnd = i
				break
			}
		}

		positional := make([]positionalGlobToken, 0, positionalEnd)
		for _, token := range raw[:positionalEnd] {
			positional = append(positional, positionalGlobToken{
				raw:  token,
				kind: classifyPositionalGlobToken(token),
			})
		}

		scopes = append(scopes, positionalGlobScope{
			raw:        append([]string(nil), raw...),
			positional: positional,
			tail:       append([]string(nil), raw[positionalEnd:]...),
		})
	}
	return scopes
}

func normalizePositionalGlobScope(scope positionalGlobScope, quiet bool) positionalGlobScopeNormalization {
	if len(scope.positional) == 0 {
		return positionalGlobScopeNormalization{rewritten: append([]string(nil), scope.raw...)}
	}

	firstPattern := -1
	var firstPatternToken positionalGlobToken
	for i, token := range scope.positional {
		if !token.kind.isPatternLike() {
			continue
		}
		firstPattern = i
		firstPatternToken = token
		break
	}

	if firstPattern == -1 {
		return positionalGlobScopeNormalization{
			rewritten: normalizeWrapperStarScope(scope),
			hints:     wrapperStarHints(scope.positional, quiet),
		}
	}

	// Glob patterns in the target position are first-class targets.
	// They pass through to the resolver, which glob-matches them against
	// all discovered files. Bare extensions (.tsx) are still rejected
	// because they are not valid glob patterns.
	allPatternsAreGlobs := firstPatternToken.kind == positionalGlobPattern
	if allPatternsAreGlobs {
		for _, token := range scope.positional[firstPattern+1:] {
			if token.kind == positionalGlobBareExt {
				allPatternsAreGlobs = false
				break
			}
		}
	}
	if allPatternsAreGlobs {
		return positionalGlobScopeNormalization{rewritten: append([]string(nil), scope.raw...)}
	}

	for _, token := range scope.positional[firstPattern+1:] {
		if !token.kind.isPatternLike() {
			return positionalGlobScopeNormalization{
				ambiguous:     true,
				ambiguityRaw:  firstPatternToken.raw,
				ambiguityThen: ambiguousThenScopes(scope),
				ambiguityOne:  ambiguousOneScope(scope),
			}
		}
	}

	return positionalGlobScopeNormalization{
		rewritten:          buildCanonicalPatternScope(scope),
		rewrittenAsTargets: buildCanonicalPatternScopeAsTargets(scope),
		fixItKind:          firstPatternToken.kind,
		fixItRaw:           firstPatternToken.raw,
	}
}

func normalizeWrapperStarScope(scope positionalGlobScope) []string {
	rewritten := make([]string, 0, len(scope.positional)+len(scope.tail))
	for _, token := range scope.positional {
		if token.kind == positionalGlobWrapperStar {
			core := trimWrapperStars(token.raw)
			if core != "" {
				rewritten = append(rewritten, core)
			}
			continue
		}
		rewritten = append(rewritten, token.raw)
	}
	rewritten = append(rewritten, scope.tail...)
	return rewritten
}

func wrapperStarHints(tokens []positionalGlobToken, quiet bool) []string {
	if quiet {
		return nil
	}
	hints := []string{}
	for _, token := range tokens {
		if token.kind != positionalGlobWrapperStar {
			continue
		}
		core := trimWrapperStars(token.raw)
		if core == "" {
			continue
		}
		hints = append(hints, "Hint: target "+discovery.SingleQuoted(core)+" is fuzzy by default - no wildcards needed.\n  Searching for "+discovery.SingleQuoted(core)+"...")
	}
	return hints
}

func ambiguousThenScopes(scope positionalGlobScope) [][]string {
	scopes := [][]string{}
	i := 0
	for i < len(scope.positional) {
		targets := []string{}
		for i < len(scope.positional) && !scope.positional[i].kind.isPatternLike() {
			if scope.positional[i].kind == positionalGlobWrapperStar {
				if core := trimWrapperStars(scope.positional[i].raw); core != "" {
					targets = append(targets, core)
				}
			} else {
				targets = append(targets, scope.positional[i].raw)
			}
			i++
		}

		patterns := []string{}
		for i < len(scope.positional) && scope.positional[i].kind.isPatternLike() {
			if canonicalPattern, ok := canonicalPatternForToken(scope.positional[i]); ok {
				patterns = append(patterns, canonicalPattern)
			}
			i++
		}

		if len(targets) == 0 {
			targets = []string{"."}
		}

		scopeArgs := make([]string, 0, len(targets)+len(patterns)+1)
		scopeArgs = append(scopeArgs, targets...)
		if len(patterns) > 0 {
			scopeArgs = append(scopeArgs, "--only")
			scopeArgs = append(scopeArgs, patterns...)
		}
		scopes = append(scopes, scopeArgs)
	}

	if len(scopes) == 0 {
		scopes = append(scopes, append([]string(nil), scope.raw...))
	}
	if len(scope.tail) > 0 {
		scopes[len(scopes)-1] = append(scopes[len(scopes)-1], scope.tail...)
	}
	return scopes
}

func ambiguousOneScope(scope positionalGlobScope) []string {
	return buildCanonicalPatternScope(scope)
}

func buildCanonicalPatternScope(scope positionalGlobScope) []string {
	targets := []string{}
	patterns := []string{}
	for _, token := range scope.positional {
		switch token.kind {
		case positionalGlobPlain:
			targets = append(targets, token.raw)
		case positionalGlobWrapperStar:
			if core := trimWrapperStars(token.raw); core != "" {
				targets = append(targets, core)
			}
		default:
			if canonicalPattern, ok := canonicalPatternForToken(token); ok {
				patterns = append(patterns, canonicalPattern)
			}
		}
	}

	// When the user typed only patterns (no real targets), suggest the
	// shorter form `catclip '*.go'` — globs are first-class targets, so
	// there's no need to wrap with `. --only`. When the user typed both
	// targets and patterns (e.g., `catclip src .go`), keep --only because
	// the scopes are semantically different: `src '*.go'` would expand
	// the scope to *.go files outside src/, while `src --only '*.go'`
	// stays within src/.
	scopeArgs := make([]string, 0, len(targets)+len(patterns)+len(scope.tail)+1)
	if len(targets) == 0 {
		scopeArgs = append(scopeArgs, patterns...)
	} else {
		scopeArgs = append(scopeArgs, targets...)
		if len(patterns) > 0 {
			scopeArgs = append(scopeArgs, "--only")
			scopeArgs = append(scopeArgs, patterns...)
		}
	}
	scopeArgs = append(scopeArgs, scope.tail...)
	return scopeArgs
}

// buildCanonicalPatternScopeAsTargets renders the same scope as
// buildCanonicalPatternScope but emits the pattern(s) as targets alongside
// the user's plain targets instead of wrapping them in --only. This produces
// the semantically *broader* form: `catclip src '*.go'` matches both `src/`
// and `*.go` files anywhere in the project, whereas `catclip src --only
// '*.go'` stays within `src/`. The two forms are offered side-by-side in
// the bare-extension error so the user can pick the intent that matches.
func buildCanonicalPatternScopeAsTargets(scope positionalGlobScope) []string {
	targets := []string{}
	patterns := []string{}
	for _, token := range scope.positional {
		switch token.kind {
		case positionalGlobPlain:
			targets = append(targets, token.raw)
		case positionalGlobWrapperStar:
			if core := trimWrapperStars(token.raw); core != "" {
				targets = append(targets, core)
			}
		default:
			if canonicalPattern, ok := canonicalPatternForToken(token); ok {
				patterns = append(patterns, canonicalPattern)
			}
		}
	}
	scopeArgs := make([]string, 0, len(targets)+len(patterns)+len(scope.tail))
	scopeArgs = append(scopeArgs, targets...)
	scopeArgs = append(scopeArgs, patterns...)
	scopeArgs = append(scopeArgs, scope.tail...)
	return scopeArgs
}

func canonicalizePositionalGlobScopes(scopes []positionalGlobScope) [][]string {
	if len(scopes) == 0 {
		return nil
	}
	out := make([][]string, 0, len(scopes))
	for _, scope := range scopes {
		out = append(out, buildCanonicalPatternScope(scope))
	}
	return out
}

func joinPositionalGlobCommand(prefix [][]string, replacement [][]string, suffix [][]string) []string {
	parts := make([][]string, 0, len(prefix)+len(replacement)+len(suffix))
	parts = append(parts, prefix...)
	parts = append(parts, replacement...)
	parts = append(parts, suffix...)
	return flattenPositionalGlobCommand(parts)
}

func flattenPositionalGlobCommand(scopes [][]string) []string {
	args := []string{}
	for i, scope := range scopes {
		if i > 0 {
			args = append(args, "--then")
		}
		args = append(args, scope...)
	}
	return args
}

func classifyPositionalGlobToken(token string) positionalGlobTokenKind {
	if isWrapperStarGlob(token) {
		return positionalGlobWrapperStar
	}
	if isBareExtensionToken(token) {
		return positionalGlobBareExt
	}
	if cli.HasGlobChars(token) {
		return positionalGlobPattern
	}
	return positionalGlobPlain
}

func isWrapperStarGlob(token string) bool {
	if len(token) < 2 || token[0] != '*' || token[len(token)-1] != '*' {
		return false
	}
	core := trimWrapperStars(token)
	if core == "" {
		return false
	}
	return !cli.HasGlobChars(core) && !strings.Contains(core, "]")
}

func trimWrapperStars(token string) string {
	return strings.Trim(token, "*")
}

func isBareExtensionToken(token string) bool {
	if !strings.HasPrefix(token, ".") || len(token) < 2 {
		return false
	}
	if strings.Contains(token, "/") || strings.Contains(token, "\\") || cli.HasGlobChars(token) {
		return false
	}
	// `.foo` is ambiguous: a hidden filename (`.env`, `.htaccess`) or a
	// bare extension the user meant as a glob (`.go`, `.ts`). Two checks:
	// real file on disk wins, else fall back to a length/shape heuristic
	// so the answer doesn't depend on the working directory (otherwise
	// tests are flaky across environments). Real extensions are short
	// alphanumeric tails; longer or non-alphanumeric tails are dotfiles.
	if _, err := os.Stat(token); err == nil {
		return false
	}
	tail := token[1:]
	if len(tail) > 6 {
		return false
	}
	for _, r := range tail {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func canonicalPatternForToken(token positionalGlobToken) (string, bool) {
	switch token.kind {
	case positionalGlobPattern:
		if token.raw == "*" {
			return "", false
		}
		return token.raw, true
	case positionalGlobBareExt:
		return "*" + token.raw, true
	default:
		return "", false
	}
}

func isPositionalGlobTailToken(token string) bool {
	if token == "--" {
		return true
	}
	if strings.HasPrefix(token, "--") {
		return true
	}
	return strings.HasPrefix(token, "-") && len(token) > 1
}

func (k positionalGlobTokenKind) isPatternLike() bool {
	return k == positionalGlobPattern || k == positionalGlobBareExt
}

func positionalGlobArgsQuiet(args []string) bool {
	for _, arg := range args {
		if arg == "-q" || arg == "--quiet" {
			return true
		}
	}
	return false
}
