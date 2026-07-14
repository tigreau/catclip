package output

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Per-language declaration-start patterns. Authored from public language syntax;
// git userdiff (GPLv2) was consulted only as a coverage checklist for which
// keywords each language uses, never copied (catclip is MIT). These SUPPLEMENT
// the language-agnostic recognizer: they add shapes it misses (Rust impl/mod,
// C# namespace, Kotlin object, Swift extension/protocol/actor) without removing
// anything, so recognition is a superset and code behavior cannot regress.
var (
	declGo     = regexp.MustCompile(`^\s*(func\b|type\s+\S+\s+(struct|interface)\b)`)
	declRust   = regexp.MustCompile(`^\s*(pub(\([^)]*\))?\s+)?((async|const|unsafe|default|extern(\s+"[^"]*")?)\s+)*(fn|struct|enum|union|trait|impl|mod|macro_rules!)\b`)
	declJava   = regexp.MustCompile(`^\s*((public|private|protected|static|final|abstract|sealed)\s+)*(class|interface|enum|record)\b`)
	declCSharp = regexp.MustCompile(`^\s*((public|private|protected|internal|static|new|unsafe|sealed|abstract|partial|virtual|override|readonly|async)\s+)*(class|interface|enum|struct|record|namespace)\b`)
	declKotlin = regexp.MustCompile(`^\s*([a-z]+\s+)*(fun|class|interface|object)\b`)
	declPython = regexp.MustCompile(`^\s*(async\s+)?(def|class)\b`)
	declPHP    = regexp.MustCompile(`^\s*((public|private|protected|static|abstract|final)\s+)*(function|class|interface|trait|enum)\b`)
	declSwift  = regexp.MustCompile(`^\s*((public|private|internal|fileprivate|open|static|final|override|dynamic)\s+)*(func|class|struct|enum|protocol|extension|actor)\b`)
	declScala  = regexp.MustCompile(`^\s*((private|protected|final|sealed|abstract|implicit|override|lazy)\s+)*(def|class|trait|object)\b`)
	declCxx    = regexp.MustCompile(`^\s*(class|struct|enum|union|namespace)\b`)
	declTS     = regexp.MustCompile(`^\s*(export\s+)?(default\s+)?(declare\s+)?(abstract\s+)?(class|interface|enum|function|namespace|type|module)\b`)
	// End-delimited (userdiff ruby/elixir coverage): start patterns only; the
	// extent is the indent+`end` reattach strategy (extentEnd).
	declRuby   = regexp.MustCompile(`^\s*(private\s+|protected\s+|public\s+)?(class|module|def)\b`)
	declElixir = regexp.MustCompile(`^\s*(defmodule|defprotocol|defimpl|defmacrop|defmacro|defguardp|defguard|defp|def|test|describe|setup)\b`)
)

// extentStrategy selects how a match expands into a unit for a given file type.
type extentStrategy uint8

const (
	// extentCode: declaration recognizer + blank-line paragraph fallback. Also
	// the default for unknown/extensionless files (preserves prior behavior).
	extentCode extentStrategy = iota
	// extentTag: smallest enclosing multi-line <t>...</t> element (XML/HTML).
	extentTag
	// extentSection: enclosing [header] section (INI/TOML).
	extentSection
	// extentFlat: no nesting; a small fixed context window around the match.
	extentFlat
	// extentParagraph: always the blank-line paragraph; no code recognition
	// (prose/data files that should never be read as code).
	extentParagraph
	// extentEnd: end-delimited declarations (Ruby, Elixir). A start pattern
	// recognizes the declaration; the extent is indentation to the dedent with
	// the closing `end` reattached (git userdiff has no extent to borrow).
	extentEnd
	// extentJSON: brace/bracket-balanced object and array units (JSON, JSONC).
	// A nested key or value resolves to its smallest enclosing {...}/[...].
	extentJSON
)

// languageProfile carries the per-file-extension knobs the snippet boundary
// scanner keys on: the line-comment tokens stripLineComment treats as a trailing
// comment, and the extent strategy. Patterns/strategies are authored from public
// language syntax; git userdiff is a coverage reference only (MIT vs GPLv2). See
// docs/versions/v0.6.7/reports/ACTIVE_PLAN_snippet_language_profiles.md.
type languageProfile struct {
	lineComments []string
	extent       extentStrategy
	unitStart    *regexp.Regexp // per-language declaration-start; nil = agnostic only
	htmlLike     bool           // extentTag: apply HTML void/raw-text/implied-close rules
	caseFold     bool           // extentTag: fold tag-name case (native HTML only, not frameworks)
}

// defaultProfile is the fallback for unknown/unmapped or extensionless files:
// the code recognizer with C-style line comments, matching prior behavior.
var defaultProfile = languageProfile{lineComments: []string{"//"}, extent: extentCode}

var extProfiles = buildExtProfiles()

func buildExtProfiles() map[string]languageProfile {
	m := map[string]languageProfile{}
	add := func(comments []string, extent extentStrategy, exts ...string) {
		for _, e := range exts {
			m[e] = languageProfile{lineComments: comments, extent: extent}
		}
	}
	// Brace / C-family / JS-TS code: `//` line comments.
	add([]string{"//"}, extentCode,
		".go", ".java", ".cs", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh",
		".hxx", ".m", ".mm", ".rs", ".kt", ".kts", ".swift", ".scala", ".sc",
		".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".dart",
		".zig", ".d", ".groovy", ".gradle")
	// JSON/JSONC: brace/bracket-balanced structural units (no declarations).
	add(nil, extentJSON, ".json", ".jsonc")
	// `#` line comment code: Python, Ruby, shell, YAML (indent nesting is a
	// later refinement; for now these fall to the paragraph when no declaration).
	add([]string{"#"}, extentCode,
		".py", ".pyi", ".pyw", ".rb", ".sh", ".bash", ".zsh", ".yaml", ".yml")
	m[".php"] = languageProfile{lineComments: []string{"//", "#"}, extent: extentCode}
	m[".sql"] = languageProfile{lineComments: []string{"--"}, extent: extentCode}
	// Tag markup: enclosing multi-line element. Comments handled by the balancer.
	// Heuristic: .vue/.svelte are treated wholesale as tag markup, so a <script>
	// match returns the entire <script> region (broad but coherent). Region-aware
	// composition is deferred; see the plan's "Documented heuristic boundaries".
	add(nil, extentTag,
		".xml", ".html", ".htm", ".xhtml", ".xsd", ".xsl", ".xslt", ".svg",
		".plist", ".pom", ".vue", ".svelte")
	// HTML-family: apply HTML void/raw-text/implied-close rules (a `</section>`
	// inside a JS string must not pop the element). XML gets none of this.
	for _, e := range []string{".html", ".htm", ".vue", ".svelte"} {
		p := m[e]
		p.htmlLike = true
		m[e] = p
	}
	// Only native HTML folds case (<DIV>...</div>). Vue/Svelte keep case so a
	// PascalCase/kebab component (<Input>) is not mistaken for a native tag and
	// discarded as void; HTML rules then apply only to lowercase native tags.
	for _, e := range []string{".html", ".htm"} {
		p := m[e]
		p.caseFold = true
		m[e] = p
	}
	// Section config: `[header]` units.
	add([]string{"#", ";"}, extentSection, ".ini", ".cfg", ".gitconfig")
	add([]string{"#"}, extentSection, ".toml")
	// End-delimited languages (userdiff-covered): Ruby, Elixir.
	add([]string{"#"}, extentEnd, ".rb", ".rake", ".gemspec")
	add([]string{"#"}, extentEnd, ".ex", ".exs")
	// Flat config: value with a bit of context.
	add([]string{"#"}, extentFlat, ".properties", ".env", ".editorconfig")
	// Prose / data: always the paragraph, never code recognition.
	add(nil, extentParagraph,
		".txt", ".md", ".markdown", ".rst", ".adoc", ".csv", ".tsv", ".log")

	// Layer in per-language declaration-start patterns (userdiff coverage).
	setStart := func(re *regexp.Regexp, exts ...string) {
		for _, e := range exts {
			p := m[e]
			p.unitStart = re
			m[e] = p
		}
	}
	setStart(declGo, ".go")
	setStart(declRust, ".rs")
	setStart(declJava, ".java")
	setStart(declCSharp, ".cs")
	setStart(declKotlin, ".kt", ".kts")
	setStart(declPython, ".py", ".pyi", ".pyw")
	setStart(declPHP, ".php")
	setStart(declSwift, ".swift")
	setStart(declScala, ".scala", ".sc")
	setStart(declCxx, ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx")
	setStart(declTS, ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs")
	setStart(declRuby, ".rb", ".rake", ".gemspec")
	setStart(declElixir, ".ex", ".exs")
	return m
}

func profileForExt(ext string) languageProfile {
	if p, ok := extProfiles[strings.ToLower(ext)]; ok {
		return p
	}
	return defaultProfile
}

func profileForPath(relPath string) languageProfile {
	return profileForExt(filepath.Ext(relPath))
}

type ResolvedSnippet struct {
	Ranges []SnippetRange
	Lines  []string
}

// SnippetBoundaryMode selects how a matched line expands into a snippet range.
type SnippetBoundaryMode string

const (
	// SnippetBoundaryBlock expands each match to its smallest recognized
	// enclosing unit (a declaration, an XML/HTML element, a JSON object, a
	// config section), with a blank-line paragraph fallback.
	SnippetBoundaryBlock SnippetBoundaryMode = "block"
	// SnippetBoundaryContext expands each match to a fixed number of lines
	// before and after (rg/grep -C N), for --snippet REGEX N.
	SnippetBoundaryContext SnippetBoundaryMode = "context"
)

// SnippetOptions carries the boundary mode and, for context mode, the number
// of lines on each side of a match. Context is meaningful only in context mode.
type SnippetOptions struct {
	Mode    SnippetBoundaryMode
	Context int
}

// SnippetOptionsFor builds options from the set/lines fields carried on scopes
// and entries: unset is block mode, set is fixed-context mode.
func SnippetOptionsFor(contextSet bool, contextLines int) SnippetOptions {
	if contextSet {
		return SnippetOptions{Mode: SnippetBoundaryContext, Context: contextLines}
	}
	return SnippetOptions{Mode: SnippetBoundaryBlock}
}

// ResolveSnippetFromSnapshot turns a per-file matched-line list (1-indexed,
// as rg emits) into snippet ranges suitable for emission. The matching step
// is the caller's job — batched via runRipgrepMatchLines for the output
// pipeline, or via a single-file rg call for previews.
//
// Returns a zero ResolvedSnippet when the snapshot isn't text or no lines
// matched. Out-of-range match indices (line numbers beyond the snapshot)
// are silently skipped — they can appear if the snapshot was loaded from
// a different revision than rg saw, which shouldn't happen in normal
// catclip flow but isn't worth crashing over.
func ResolveSnippetFromSnapshot(snapshot TextSnapshot, matchedLines []int, opts SnippetOptions) (ResolvedSnippet, error) {
	if !snapshot.IsText {
		return ResolvedSnippet{}, nil
	}
	return resolveSnippetFromLines(snapshot.SnippetLines(), matchedLines, opts, profileForPath(snapshot.RelPath))
}

// resolveSnippetFromLines is ResolveSnippetFromSnapshot's core over already-split
// lines. Callers resolving several boundary widths for one file split the body
// once (SnippetLines re-splits the whole file on every call) and reuse the
// lines across widths, instead of re-splitting per width.
func resolveSnippetFromLines(lines []string, matchedLines []int, opts SnippetOptions, profile languageProfile) (ResolvedSnippet, error) {
	if len(lines) == 0 || len(matchedLines) == 0 {
		return ResolvedSnippet{}, nil
	}
	var ranges []SnippetRange
	switch opts.Mode {
	case SnippetBoundaryContext:
		ranges = BuildContextSnippetRanges(lines, matchedLines, opts.Context)
	default:
		ranges = buildSnippetRanges(lines, matchedLines, profile)
	}
	if len(ranges) == 0 {
		return ResolvedSnippet{}, nil
	}
	return ResolvedSnippet{Ranges: ranges, Lines: lines}, nil
}

// buildSnippetRanges expands each matched line to the smallest recognized
// enclosing unit for the file's extent strategy (a declaration, an XML/HTML
// element, a JSON object, a config section). Matches outside any unit retain
// the historical blank-line-bounded behavior. Match indices are 1-indexed.
func buildSnippetRanges(lines []string, matchedLines []int, profile languageProfile) []SnippetRange {
	if len(lines) == 0 || len(matchedLines) == 0 {
		return nil
	}
	units := unitCandidates(lines, profile)
	ranges := make([]SnippetRange, 0, len(matchedLines))
	total := len(lines)
	for _, matchLine := range matchedLines {
		if matchLine < 1 || matchLine > total {
			continue
		}
		if unit, ok := smallestEnclosingRange(units, matchLine); ok {
			ranges = append(ranges, unit)
			continue
		}
		ranges = append(ranges, fallbackRange(lines, matchLine, profile))
	}
	return mergeSnippetRanges(ranges)
}

// unitCandidates returns the candidate unit spans for the file's extent
// strategy. Only multi-line units matter for enclosing selection; single-line
// leaves are naturally skipped in favor of the nearest multi-line ancestor.
func unitCandidates(lines []string, profile languageProfile) []SnippetRange {
	switch profile.extent {
	case extentTag:
		return xmlElementCandidates(lines, profile.htmlLike, profile.caseFold)
	case extentSection:
		return sectionCandidates(lines)
	case extentEnd:
		return endDelimitedCandidates(lines, profile.unitStart, profile.lineComments)
	case extentJSON:
		return jsonBraceCandidates(lines)
	case extentFlat, extentParagraph:
		return nil // no structural units; the fallback handles these
	default: // extentCode
		decls := buildDeclarationRanges(lines, profile.lineComments, profile.unitStart)
		out := make([]SnippetRange, len(decls))
		for i, d := range decls {
			out[i] = d.SnippetRange
		}
		return out
	}
}

// fallbackRange handles a match not inside any unit: a small fixed context for
// flat config, otherwise the blank-line paragraph.
func fallbackRange(lines []string, matchLine int, profile languageProfile) SnippetRange {
	if profile.extent == extentFlat {
		return smallContextRange(lines, matchLine)
	}
	return blankLineSnippetRange(lines, matchLine)
}

// smallestEnclosingRange returns the smallest span containing matchLine, ties
// broken toward the later (deeper) start.
func smallestEnclosingRange(ranges []SnippetRange, matchLine int) (SnippetRange, bool) {
	var best SnippetRange
	found := false
	for _, r := range ranges {
		if matchLine < r.Start || matchLine > r.End {
			continue
		}
		if !found || r.End-r.Start < best.End-best.Start ||
			(r.End-r.Start == best.End-best.Start && r.Start > best.Start) {
			best = r
			found = true
		}
	}
	return best, found
}

// smallContextRange returns the match line plus a couple lines on each side.
func smallContextRange(lines []string, matchLine int) SnippetRange {
	const pad = 2
	start := matchLine - pad
	if start < 1 {
		start = 1
	}
	end := matchLine + pad
	if end > len(lines) {
		end = len(lines)
	}
	return SnippetRange{Start: start, End: end}
}

func blankLineSnippetRange(lines []string, matchLine int) SnippetRange {
	start := matchLine
	for start > 1 && lines[start-2] != "" {
		start--
	}
	end := matchLine
	for end < len(lines) && lines[end] != "" {
		end++
	}
	return SnippetRange{Start: start, End: end}
}

type declarationKind uint8

const (
	declarationCallable declarationKind = iota
	declarationArrow
	declarationType
)

type declarationCandidate struct {
	SnippetRange
	kind declarationKind
}

func buildDeclarationRanges(lines []string, comments []string, unitStart *regexp.Regexp) []declarationCandidate {
	declarations := make([]declarationCandidate, 0)
	for i, line := range lines {
		kind, ok := declarationStartKind(line, comments, unitStart)
		if !ok {
			continue
		}
		if declaration, ok := declarationRange(lines, i, kind, comments); ok {
			declarations = append(declarations, declaration)
		}
	}
	return declarations
}

func declarationStartKind(line string, comments []string, unitStart *regexp.Regexp) (declarationKind, bool) {
	trimmed := strings.TrimSpace(stripLineComment(line, comments))
	if trimmed == "" || isDeclarationPrefixLine(trimmed) {
		return 0, false
	}
	// Per-language declaration starts (userdiff coverage) are recognized as a
	// block declaration; the brace/indent extent then finds the opener. Arrow
	// and generic-callable shapes fall through to the agnostic logic below.
	if unitStart != nil && unitStart.MatchString(trimmed) {
		return declarationType, true
	}
	lower := strings.ToLower(trimmed)
	first := firstIdentifier(lower)
	if declarationControlWords[first] {
		return 0, false
	}
	if hasTypeDeclarationKeyword(lower) {
		return declarationType, true
	}
	if strings.HasPrefix(lower, "def ") || strings.HasPrefix(lower, "async def ") ||
		strings.HasPrefix(lower, "func ") || strings.HasPrefix(lower, "function ") ||
		strings.HasPrefix(lower, "async function ") {
		return declarationCallable, true
	}
	open := strings.Index(trimmed, "(")
	if open < 1 {
		return 0, false
	}
	before := strings.TrimSpace(trimmed[:open])
	if strings.Contains(before, "=") {
		if first == "const" || first == "let" || first == "var" {
			return declarationArrow, true
		}
		return 0, false
	}
	nameStart := trailingIdentifierStart(before)
	if nameStart < 0 {
		return 0, false
	}
	prefix := strings.TrimSpace(before[:nameStart])
	if strings.HasSuffix(prefix, ".") || strings.HasSuffix(prefix, "]") || strings.HasSuffix(prefix, ")") {
		return 0, false
	}
	return declarationCallable, true
}

var declarationControlWords = map[string]bool{
	"await": true, "break": true, "case": true, "catch": true,
	"checked": true, "continue": true, "defer": true, "do": true,
	"elif": true, "else": true, "except": true, "finally": true,
	"fixed": true, "for": true, "foreach": true, "go": true, "if": true,
	"lock": true, "loop": true, "match": true, "new": true, "raise": true,
	"return": true, "select": true, "switch": true, "synchronized": true,
	"throw": true, "try": true, "unchecked": true, "using": true,
	"while": true, "with": true, "yield": true,
}

func hasTypeDeclarationKeyword(lower string) bool {
	for _, field := range strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		switch field {
		case "class", "enum", "interface", "record", "struct", "trait":
			return true
		}
	}
	return false
}

func firstIdentifier(s string) string {
	for i, r := range s {
		if unicode.IsLetter(r) || r == '_' {
			start := i
			for j, next := range s[i:] {
				if !unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_' {
					return s[start : i+j]
				}
			}
			return s[start:]
		}
	}
	return ""
}

func trailingIdentifierStart(s string) int {
	runes := []rune(s)
	i := len(runes) - 1
	for i >= 0 && unicode.IsSpace(runes[i]) {
		i--
	}
	end := i
	for i >= 0 && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_') {
		i--
	}
	if end == i {
		return -1
	}
	return len(string(runes[:i+1]))
}

func declarationRange(lines []string, start int, kind declarationKind, comments []string) (declarationCandidate, bool) {
	const maxHeaderLines = 60
	baseIndent := indentationWidth(lines[start])
	parenDepth, bracketDepth := 0, 0
	headerClosed := false
	seenArrow := false
	opener := -1
	limit := min(len(lines), start+maxHeaderLines)

	for i := start; i < limit; i++ {
		cleaned := strings.TrimSpace(stripLineComment(lines[i], comments))
		if cleaned == "" {
			return declarationCandidate{}, false
		}
		if i > start && indentationWidth(lines[i]) < baseIndent {
			return declarationCandidate{}, false
		}
		if headerClosed && !isHeaderContinuation(cleaned) {
			return declarationCandidate{}, false
		}
		parenDepth, bracketDepth = updateHeaderDepths(cleaned, parenDepth, bracketDepth)
		seenArrow = seenArrow || strings.Contains(cleaned, "=>")
		if parenDepth < 0 || bracketDepth < 0 {
			return declarationCandidate{}, false
		}
		if parenDepth == 0 && bracketDepth == 0 {
			if isInlineDeclaration(cleaned, kind, seenArrow) {
				return declarationCandidate{
					SnippetRange: SnippetRange{Start: attachedDeclarationStart(lines, start, comments) + 1, End: i + 1},
					kind:         kind,
				}, true
			}
			if isHeaderOpener(cleaned) && (kind != declarationArrow || seenArrow) {
				opener = i
				break
			}
			if strings.Contains(cleaned, ")") {
				headerClosed = true
			}
		}
	}
	if opener < 0 {
		return declarationCandidate{}, false
	}

	// Heuristic: the body extent is measured by indentation to the dedent, not by
	// brace balance. A column-zero body line (an unindented body, or a column-zero
	// C preprocessor directive right after the opener) can truncate here or widen
	// to the fallback paragraph. Idiomatic formatted code is consistently
	// indented, so this is rare and fail-safe; see the plan's "Documented
	// heuristic boundaries".
	end := opener
	firstBody := nextNonBlankLine(lines, opener+1)
	if firstBody < 0 {
		end = len(lines) - 1
	} else if indentationWidth(lines[firstBody]) <= baseIndent {
		if isDeclarationCloser(lines[firstBody], comments) {
			end = firstBody
		}
	} else {
		end = firstBody
		for i := firstBody + 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "" || indentationWidth(lines[i]) > baseIndent {
				end = i
				continue
			}
			if isDeclarationCloser(lines[i], comments) {
				end = i
			}
			break
		}
	}
	return declarationCandidate{
		SnippetRange: SnippetRange{Start: attachedDeclarationStart(lines, start, comments) + 1, End: end + 1},
		kind:         kind,
	}, true
}

func isInlineDeclaration(line string, kind declarationKind, seenArrow bool) bool {
	if kind == declarationArrow && !seenArrow {
		return false
	}
	openBrace := strings.Index(line, "{")
	if openBrace >= 0 && strings.LastIndex(line, "}") > openBrace {
		if kind == declarationType {
			return true
		}
		return strings.LastIndex(line[:openBrace], ")") >= 0
	}
	lower := strings.ToLower(strings.TrimSpace(line))
	if strings.HasPrefix(lower, "def ") || strings.HasPrefix(lower, "async def ") {
		colon := strings.LastIndex(line, ":")
		return colon >= 0 && strings.TrimSpace(line[colon+1:]) != ""
	}
	return false
}

func isHeaderContinuation(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "=>") ||
		strings.HasPrefix(trimmed, "throws ") || strings.HasPrefix(trimmed, "where ")
}

func isHeaderOpener(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, ":")
}

func updateHeaderDepths(line string, parenDepth, bracketDepth int) (int, int) {
	quote := rune(0)
	escaped := false
	for _, r := range line {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			continue
		}
		switch r {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
		}
	}
	return parenDepth, bracketDepth
}

func stripLineComment(line string, comments []string) string {
	if len(comments) == 0 {
		comments = defaultProfile.lineComments
	}
	quote := rune(0)
	escaped := false
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			continue
		}
		for _, tok := range comments {
			if commentTokenAt(runes, i, tok) {
				return string(runes[:i])
			}
		}
	}
	return line
}

// commentTokenAt reports whether the comment token tok begins at runes[i].
func commentTokenAt(runes []rune, i int, tok string) bool {
	t := []rune(tok)
	if len(t) == 0 || i+len(t) > len(runes) {
		return false
	}
	for j := range t {
		if runes[i+j] != t[j] {
			return false
		}
	}
	return true
}

func indentationWidth(line string) int {
	width := 0
	for _, r := range line {
		switch r {
		case ' ':
			width++
		case '\t':
			width += 8 - width%8
		default:
			return width
		}
	}
	return width
}

func nextNonBlankLine(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return -1
}

func isDeclarationCloser(line string, comments []string) bool {
	trimmed := strings.TrimSpace(stripLineComment(line, comments))
	if trimmed == "" || !strings.ContainsAny(trimmed, "}])") {
		return false
	}
	for _, r := range trimmed {
		if !strings.ContainsRune("}]);,", r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func attachedDeclarationStart(lines []string, start int, comments []string) int {
	attached := start
	const maxAdornmentLines = 40
	for attached > 0 {
		// Resolve one adornment above `attached`. Scan upward balancing brackets
		// so a decorator/annotation whose argument list wraps across lines
		// (@app.route(\n  methods=[...])) is absorbed as a unit instead of being
		// dropped, while a mere wrapped statement above the declaration (whose
		// balancing line is not an adornment/comment) is left alone.
		depth := 0
		k := attached
		commit := false
		for k > 0 && attached-k < maxAdornmentLines {
			k--
			depth += closerDelta(lines[k], comments)
			if depth < 0 {
				break // a dangling opener above: not an adornment tail
			}
			if depth == 0 {
				commit = isDeclarationPrefixLine(strings.TrimSpace(lines[k]))
				break
			}
		}
		if !commit {
			break
		}
		attached = k
	}
	return attached
}

// closerDelta reports the count of closing minus opening brackets (parens and
// square brackets) on a line, ignoring brackets inside strings, trailing line
// comments (`# note )`), and inline block comments (`/* ) */`, in `//`-comment
// languages). attachedDeclarationStart uses it to balance a wrapped
// decorator/annotation argument list. Block-comment tracking is per line; a
// bracket inside a block comment that spans lines is a rare, accepted gap.
func closerDelta(line string, comments []string) int {
	blockAware := false
	for _, c := range comments {
		if c == "//" {
			blockAware = true
			break
		}
	}
	openParen, openBracket := 0, 0
	quote := rune(0)
	escaped := false
	inBlock := false
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inBlock {
			if r == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				inBlock = false
				i++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			continue
		}
		if blockAware && r == '/' && i+1 < len(runes) && runes[i+1] == '*' {
			inBlock = true
			i++
			continue
		}
		commented := false
		for _, tok := range comments {
			if commentTokenAt(runes, i, tok) {
				commented = true
				break
			}
		}
		if commented {
			break // rest of line is a line comment
		}
		switch r {
		case '(':
			openParen++
		case ')':
			openParen--
		case '[':
			openBracket++
		case ']':
			openBracket--
		}
	}
	return -(openParen + openBracket)
}

func isDeclarationPrefixLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "@") || strings.HasPrefix(trimmed, "[") ||
		strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "///") ||
		strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "##") ||
		strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") ||
		strings.HasSuffix(trimmed, "*/")
}

func mergeSnippetRanges(ranges []SnippetRange) []SnippetRange {
	if len(ranges) == 0 {
		return nil
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start != ranges[j].Start {
			return ranges[i].Start < ranges[j].Start
		}
		return ranges[i].End < ranges[j].End
	})
	merged := make([]SnippetRange, 0, len(ranges))
	current := ranges[0]
	for _, candidate := range ranges[1:] {
		if candidate.Start <= current.End+1 {
			current.End = max(current.End, candidate.End)
			continue
		}
		merged = append(merged, current)
		current = candidate
	}
	return append(merged, current)
}

// BuildContextSnippetRanges expands each matched line to [match-context,
// match+context], clamped to file bounds, then sorts and merges ranges that
// overlap or are adjacent (rg/grep -C N semantics). context 0 yields the
// matching line only. Match indices are 1-indexed.
func BuildContextSnippetRanges(lines []string, matchedLines []int, context int) []SnippetRange {
	if len(lines) == 0 || len(matchedLines) == 0 {
		return nil
	}
	total := len(lines)
	windows := make([]SnippetRange, 0, len(matchedLines))
	for _, matchLine := range matchedLines {
		if matchLine < 1 || matchLine > total {
			continue
		}
		start := matchLine - context
		if start < 1 {
			start = 1
		}
		end := matchLine + context
		if end > total {
			end = total
		}
		windows = append(windows, SnippetRange{Start: start, End: end})
	}
	if len(windows) == 0 {
		return nil
	}
	sort.Slice(windows, func(i, j int) bool {
		if windows[i].Start != windows[j].Start {
			return windows[i].Start < windows[j].Start
		}
		return windows[i].End < windows[j].End
	})
	merged := make([]SnippetRange, 0, len(windows))
	current := windows[0]
	for _, w := range windows[1:] {
		// Merge overlapping or adjacent windows (gap of zero lines).
		if w.Start <= current.End+1 {
			if w.End > current.End {
				current.End = w.End
			}
			continue
		}
		merged = append(merged, current)
		current = w
	}
	merged = append(merged, current)
	return merged
}
