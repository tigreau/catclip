package catclip

import "strings"

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
	rewritten     []string
	hints         []string
	fixItKind     positionalGlobTokenKind
	fixItRaw      string
	ambiguous     bool
	ambiguityRaw  string
	ambiguityThen [][]string
	ambiguityOne  []string
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
				singleQuoted(norm.ambiguityRaw),
				formatResolvedStartupCommand(thenCommand),
				formatResolvedStartupCommand(oneCommand),
			)
		}

		if norm.fixItKind != "" {
			fullCommand := joinPositionalGlobCommand(normalizedScopes, [][]string{norm.rewritten}, canonicalizePositionalGlobScopes(scopes[i+1:]))
			switch norm.fixItKind {
			case positionalGlobBareExt:
				return positionalGlobNormalizationResult{}, newUsageError(
					"Error: %s is a bare extension, not a target.\n  Use --only to filter by extension:\n    %s",
					singleQuoted(norm.fixItRaw),
					formatResolvedStartupCommand(fullCommand),
				)
			default:
				return positionalGlobNormalizationResult{}, newUsageError(
					"Error: %s is a pattern, not a target.\n  Use --only to filter by pattern:\n    %s",
					singleQuoted(norm.fixItRaw),
					formatResolvedStartupCommand(fullCommand),
				)
			}
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
		if isValueTakingFlag(arg) {
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
		rewritten: buildCanonicalPatternScope(scope),
		fixItKind: firstPatternToken.kind,
		fixItRaw:  firstPatternToken.raw,
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
		hints = append(hints, "Hint: target "+singleQuoted(core)+" is fuzzy by default - no wildcards needed.\n  Searching for "+singleQuoted(core)+"...")
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
	if len(targets) == 0 {
		targets = []string{"."}
	}

	scopeArgs := make([]string, 0, len(targets)+len(patterns)+len(scope.tail)+1)
	scopeArgs = append(scopeArgs, targets...)
	if len(patterns) > 0 {
		scopeArgs = append(scopeArgs, "--only")
		scopeArgs = append(scopeArgs, patterns...)
	}
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
	if hasGlobChars(token) {
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
	return !hasGlobChars(core) && !strings.Contains(core, "]")
}

func trimWrapperStars(token string) string {
	return strings.Trim(token, "*")
}

func isBareExtensionToken(token string) bool {
	if !strings.HasPrefix(token, ".") || len(token) < 2 {
		return false
	}
	if strings.Contains(token, "/") || strings.Contains(token, "\\") || hasGlobChars(token) {
		return false
	}
	ext := strings.ToLower(token[1:])
	if _, ok := knownTextExts[ext]; ok {
		return true
	}
	if _, ok := knownBinaryExts[ext]; ok {
		return true
	}
	return false
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
