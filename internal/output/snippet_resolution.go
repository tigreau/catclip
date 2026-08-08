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
// docs/versions/v0.6.7/reports/RESOLVED_PLAN_snippet_language_profiles.md.
type languageProfile struct {
	lineComments    []string
	extent          extentStrategy
	unitStart       *regexp.Regexp // per-language declaration-start; nil = agnostic only
	literalBindings bool           // extentCode: named JS/TS object/array assignments
	htmlLike        bool           // extentTag: apply HTML void/raw-text/implied-close rules
	caseFold        bool           // extentTag: fold tag-name case (native HTML only, not frameworks)
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
	for _, e := range []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"} {
		p := m[e]
		p.literalBindings = true
		m[e] = p
	}
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
		capacity := len(decls)
		var bindings []SnippetRange
		if profile.literalBindings {
			bindings = literalBindingCandidates(lines)
			capacity += len(bindings)
		}
		out := make([]SnippetRange, 0, capacity)
		for _, d := range decls {
			out = append(out, d.SnippetRange)
		}
		out = append(out, bindings...)
		return out
	}
}

type literalDelimiterFrame struct {
	open      rune
	line      int
	candidate bool
}

// literalBindingCandidates returns multi-line object and array values assigned
// directly to a named JS/TS const/let/var binding. Candidate authorization is
// deliberately narrow; the delimiter scan itself is one pass over the file.
func literalBindingCandidates(lines []string) []SnippetRange {
	var stack []literalDelimiterFrame
	var out []SnippetRange
	quote := rune(0)
	escaped := false
	inBlockComment := false

	for lineIndex, line := range lines {
		runes := []rune(line)
		candidateIndex, candidateOpen, hasCandidate := namedLiteralBindingOpener(runes)
		inLineComment := false
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			switch {
			case inBlockComment:
				if r == '*' && i+1 < len(runes) && runes[i+1] == '/' {
					inBlockComment = false
					i++
				}
			case inLineComment:
				i = len(runes)
			case quote != 0:
				if escaped {
					escaped = false
				} else if r == '\\' {
					escaped = true
				} else if r == quote {
					quote = 0
				}
			case r == '\'' || r == '"' || r == '`':
				quote = r
			case r == '/' && i+1 < len(runes) && runes[i+1] == '/':
				inLineComment = true
			case r == '/' && i+1 < len(runes) && runes[i+1] == '*':
				inBlockComment = true
				i++
			case r == '{' || r == '[':
				stack = append(stack, literalDelimiterFrame{
					open:      r,
					line:      lineIndex + 1,
					candidate: hasCandidate && i == candidateIndex && r == candidateOpen,
				})
			case r == '}' || r == ']':
				if len(stack) == 0 {
					continue
				}
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				want := '{'
				if r == ']' {
					want = '['
				}
				if top.open != want || !top.candidate || top.line == lineIndex+1 {
					continue
				}
				end := lineIndex + 1
				if end < len(lines) && strings.TrimSpace(lines[end]) == ";" {
					end++
				}
				start := attachedDeclarationStart(lines, top.line-1, []string{"//"}) + 1
				out = append(out, SnippetRange{Start: start, End: end})
			}
		}
	}
	return out
}

func namedLiteralBindingOpener(line []rune) (int, rune, bool) {
	i := skipRuneSpace(line, 0)
	if next, ok := consumeRuneWord(line, i, "export"); ok {
		i = skipRuneSpace(line, next)
	}
	next, binding := consumeAnyRuneWord(line, i, "const", "let", "var")
	if !binding {
		return 0, 0, false
	}
	i = skipRuneSpace(line, next)
	if i >= len(line) || !isJSIdentifierStart(line[i]) {
		return 0, 0, false
	}
	i++
	for i < len(line) && isJSIdentifierPart(line[i]) {
		i++
	}
	i = skipRuneSpace(line, i)
	if i >= len(line) || line[i] != '=' || i+1 < len(line) && (line[i+1] == '=' || line[i+1] == '>') {
		return 0, 0, false
	}
	i = skipRuneSpace(line, i+1)
	if i >= len(line) || line[i] != '{' && line[i] != '[' {
		return 0, 0, false
	}
	return i, line[i], true
}

func skipRuneSpace(line []rune, start int) int {
	for start < len(line) && unicode.IsSpace(line[start]) {
		start++
	}
	return start
}

func consumeAnyRuneWord(line []rune, start int, words ...string) (int, bool) {
	for _, word := range words {
		if end, ok := consumeRuneWord(line, start, word); ok {
			return end, true
		}
	}
	return start, false
}

func consumeRuneWord(line []rune, start int, word string) (int, bool) {
	want := []rune(word)
	if start+len(want) > len(line) || string(line[start:start+len(want)]) != word {
		return start, false
	}
	end := start + len(want)
	if end < len(line) && isJSIdentifierPart(line[end]) {
		return start, false
	}
	return end, true
}

func isJSIdentifierStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_' || r == '$'
}

func isJSIdentifierPart(r rune) bool {
	return isJSIdentifierStart(r) || unicode.IsDigit(r)
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
	for start > 1 && !isBlankSnippetLine(lines[start-2]) {
		start--
	}
	end := matchLine
	for end < len(lines) && !isBlankSnippetLine(lines[end]) {
		end++
	}
	return SnippetRange{Start: start, End: end}
}

// isBlankSnippetLine reports a paragraph-boundary blank line. Snippet lines
// come from SplitLogicalLines, which splits raw bytes on '\n' only, so a blank
// line in a CRLF file arrives as "\r"; without this check, CRLF files never
// hit a paragraph boundary and the fallback silently widens to the whole file.
// Deliberately NOT TrimSpace: whitespace-only lines stay non-boundaries,
// matching the historical behavior for LF files.
func isBlankSnippetLine(line string) bool {
	return line == "" || line == "\r"
}

type declarationKind uint8

const (
	declarationCallable declarationKind = iota
	declarationArrow
	declarationType
)

type declarationCandidate struct {
	SnippetRange
	kind      declarationKind
	headerEnd int
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
	filtered := declarations[:0]
	for i, candidate := range declarations {
		insideBindingHeader := false
		for j, outer := range declarations {
			if i == j || outer.kind != declarationArrow {
				continue
			}
			if outer.Start < candidate.Start && candidate.Start <= outer.headerEnd && candidate.End <= outer.End {
				insideBindingHeader = true
				break
			}
		}
		if !insideBindingHeader {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func declarationStartKind(line string, comments []string, unitStart *regexp.Regexp) (declarationKind, bool) {
	trimmed := strings.TrimSpace(stripLineComment(line, comments))
	if trimmed == "" {
		return 0, false
	}
	if declarationText, annotated := textAfterLeadingAnnotations(trimmed); unitStart == declJava && annotated {
		if declarationText == "" {
			return 0, false
		}
		trimmed = declarationText
	} else if isDeclarationPrefixLine(trimmed) {
		return 0, false
	}
	lower := strings.ToLower(trimmed)
	callableLower := lower
	if strings.HasPrefix(callableLower, "export ") {
		callableLower = strings.TrimSpace(strings.TrimPrefix(callableLower, "export "))
		if strings.HasPrefix(callableLower, "default ") {
			callableLower = strings.TrimSpace(strings.TrimPrefix(callableLower, "default "))
		}
	}
	if strings.HasPrefix(callableLower, "def ") || strings.HasPrefix(callableLower, "async def ") ||
		strings.HasPrefix(callableLower, "func ") || strings.HasPrefix(callableLower, "function ") ||
		strings.HasPrefix(callableLower, "async function ") {
		return declarationCallable, true
	}
	// Per-language declaration starts (userdiff coverage) are recognized as a
	// block declaration; the brace/indent extent then finds the opener. Arrow
	// and generic-callable shapes fall through to the agnostic logic below.
	if unitStart != nil && unitStart.MatchString(trimmed) {
		return declarationType, true
	}
	first := firstIdentifier(lower)
	if declarationControlWords[first] {
		return 0, false
	}
	if hasTypeDeclarationKeyword(lower) {
		return declarationType, true
	}
	// Look past a leading `export` so `export const App = () => {`, the
	// dominant React component shape, is recognized like `const App = ...`.
	// Safe outside JS/TS: shell's `export PATH=$(...)` yields PATH here,
	// which is not const/let/var.
	declWord := first
	if declWord == "export" {
		declWord = firstIdentifier(strings.TrimSpace(lower[len("export"):]))
	}
	isBindingWord := declWord == "const" || declWord == "let" || declWord == "var"
	if isBindingWord {
		assignment := bindingAssignmentIndex(trimmed)
		if assignment >= 0 && identifierIndexAfter(trimmed, "function", assignment+1) >= 0 {
			// Named/anonymous function expression binding, including a generic
			// header whose opening parenthesis arrives on a later line.
			return declarationArrow, true
		}
		if assignment >= 0 && arrowIndexAfter(trimmed, assignment+1) >= 0 {
			// Parentheses are optional for a single arrow parameter:
			// `export const App = props => {` is still a block declaration.
			return declarationArrow, true
		}
		if assignment >= 0 && strings.TrimSpace(string([]rune(trimmed)[assignment+1:])) == "<" {
			// Generic arrow whose type parameters start on the next line:
			// `const Schedule = <\n  T extends FieldValues, ...`.
			return declarationArrow, true
		}
		if assignment >= 0 && isGenericCalleeStart(strings.TrimSpace(string([]rune(trimmed)[assignment+1:]))) {
			// Generic wrapper call split before its type arguments:
			// `const Input = React.forwardRef<\n  HTMLElement, Props\n>(...)`.
			return declarationArrow, true
		}
	}
	open := strings.Index(trimmed, "(")
	if open < 1 {
		// Multi-line generic header: `export const X: React.FC<` (or `...FC<{`)
		// continues on later lines to `> = (props) => {`. Requiring no `=` on
		// the line keeps object literals (`const x = {`) excluded. The `{`
		// suffix additionally requires a `<` on the line (the generic marker):
		// without it, a split typed-object declaration (`const options: {`)
		// would become a false candidate whose later-arrow "opener" truncates
		// the correctly recognized enclosing function.
		if isBindingWord && !strings.Contains(trimmed, "=") &&
			(strings.HasSuffix(trimmed, "<") ||
				(strings.HasSuffix(trimmed, "{") && strings.Contains(trimmed, "<"))) {
			return declarationArrow, true
		}
		return 0, false
	}
	before := strings.TrimSpace(trimmed[:open])
	if strings.Contains(before, "=") {
		if isBindingWord {
			return declarationArrow, true
		}
		return 0, false
	}
	nameStart := trailingIdentifierStart(before)
	if nameStart < 0 {
		return 0, false
	}
	prefix := strings.TrimSpace(before[:nameStart])
	// `]` rejects indexing calls (`arr[i](...)`), but an EMPTY `[]` is an array
	// return type (`public String[] getBeanNames(...)`), which is a declaration.
	if strings.HasSuffix(prefix, ".") || strings.HasSuffix(prefix, ")") ||
		(strings.HasSuffix(prefix, "]") && !strings.HasSuffix(prefix, "[]")) {
		return 0, false
	}
	return declarationCallable, true
}

// textAfterLeadingAnnotations separates same-line Java annotations from the
// declaration that follows them. Annotation-only lines remain
// adornments; `@Nullable Object method(...)` is classified from `Object
// method(...)` while the emitted range still begins on the original line.
func textAfterLeadingAnnotations(line string) (string, bool) {
	rest := strings.TrimSpace(line)
	if !strings.HasPrefix(rest, "@") {
		return line, false
	}
	for strings.HasPrefix(rest, "@") {
		runes := []rune(rest)
		i := 1
		for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) ||
			runes[i] == '_' || runes[i] == '$' || runes[i] == '.') {
			i++
		}
		if i == 1 {
			return "", true
		}
		for i < len(runes) && unicode.IsSpace(runes[i]) {
			i++
		}
		if i < len(runes) && runes[i] == '(' {
			end, ok := balancedAnnotationEnd(runes, i)
			if !ok {
				return "", true
			}
			i = end
		}
		rest = strings.TrimSpace(string(runes[i:]))
		if rest == "" {
			return "", true
		}
		if !strings.HasPrefix(rest, "@") {
			return rest, true
		}
	}
	return rest, true
}

func balancedAnnotationEnd(runes []rune, open int) (int, bool) {
	depth := 0
	quote := rune(0)
	escaped := false
	for i := open; i < len(runes); i++ {
		r := runes[i]
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
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
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
	seenAssignment := false
	seenArrow := false
	seenFunction := false
	typeBraceDepth := 0
	inBlockComment := false
	opener := -1
	limit := min(len(lines), start+maxHeaderLines)

	for i := start; i < limit; i++ {
		cleaned := strings.TrimSpace(stripDeclarationHeaderComments(lines[i], comments, &inBlockComment))
		if cleaned == "" {
			// A comment-only line inside a multi-line header (a // TODO in a
			// generic parameter list) is not a blank line; skip it. A truly
			// blank line still ends the header.
			if i > start && strings.TrimSpace(lines[i]) != "" {
				continue
			}
			if i > start && (parenDepth > 0 || bracketDepth > 0) {
				continue
			}
			// A generic binding type may intentionally group props with blank
			// lines. The recognized `<`/`<{` start and 60-line cap bound this scan.
			if i > start && kind == declarationArrow && !seenAssignment {
				continue
			}
			return declarationCandidate{}, false
		}
		if i > start && indentationWidth(lines[i]) < baseIndent {
			return declarationCandidate{}, false
		}
		if headerClosed && !isHeaderContinuation(cleaned) {
			return declarationCandidate{}, false
		}
		priorParenDepth, priorBracketDepth := parenDepth, bracketDepth
		parenDepth, bracketDepth = updateHeaderDepths(cleaned, parenDepth, bracketDepth)
		if kind == declarationArrow {
			assignment := bindingAssignmentIndex(cleaned)
			arrow := -1
			if assignment >= 0 {
				seenAssignment = true
				arrow = arrowIndexAfter(cleaned, assignment+1)
				seenFunction = seenFunction || identifierIndexAfter(cleaned, "function", assignment+1) >= 0
			} else if seenAssignment {
				arrow = arrowIndexAfter(cleaned, 0)
				seenFunction = seenFunction || identifierIndexAfter(cleaned, "function", 0) >= 0
			}
			if typeBraceDepth > 0 {
				prefixEnd := len([]rune(cleaned))
				if arrow >= 0 {
					prefixEnd = arrow
				}
				typeBraceDepth += curlyDeltaBefore(cleaned, prefixEnd)
				if arrow >= 0 && typeBraceDepth == 0 {
					seenArrow = true
				}
			} else if arrow >= 0 {
				seenArrow = true
			}
		}
		if parenDepth < 0 || bracketDepth < 0 {
			return declarationCandidate{}, false
		}
		if kind == declarationArrow && seenAssignment && !seenArrow && typeBraceDepth == 0 &&
			parenDepth == 0 && bracketDepth == 0 && strings.HasSuffix(cleaned, "{") &&
			strings.Contains(cleaned, "):") {
			typeBraceDepth = curlyDeltaBefore(cleaned, len([]rune(cleaned)))
		}
		// Wrappers such as `memo((props) => {` and `forwardRef(...)(` keep an
		// outer call parenthesis open at the callback's body opener. Accept the
		// direct arrow block without waiting for all wrapper parens to close.
		if kind == declarationArrow && seenAssignment && seenArrow &&
			isArrowBlockOpener(cleaned, priorParenDepth, priorBracketDepth) {
			opener = i
			break
		}
		if kind == declarationArrow && seenAssignment && seenFunction && isFunctionBlockOpener(cleaned) {
			opener = i
			break
		}
		if parenDepth == 0 && bracketDepth == 0 {
			if isInlineDeclaration(cleaned, kind, seenArrow) {
				return declarationCandidate{
					SnippetRange: SnippetRange{Start: attachedDeclarationStart(lines, start, comments) + 1, End: i + 1},
					kind:         kind,
					headerEnd:    i + 1,
				}, true
			}
			if isHeaderOpener(cleaned) && (kind != declarationArrow || seenArrow) {
				opener = i
				break
			}
			if strings.Contains(cleaned, ")") && typeBraceDepth == 0 &&
				(kind != declarationArrow || seenAssignment) {
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
		headerEnd:    opener + 1,
	}, true
}

// bindingAssignmentIndex finds the binding '=' in an arrow declaration while
// ignoring strings, comparisons, compound assignments, and the '=' in '=>'.
// Tracking this boundary keeps arrows in TypeScript prop types (`() => void`)
// from being mistaken for the component implementation arrow.
func bindingAssignmentIndex(line string) int {
	quote := rune(0)
	escaped := false
	runes := []rune(line)
	for i, r := range runes {
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
		if r != '=' {
			continue
		}
		var prev, next rune
		if i > 0 {
			prev = runes[i-1]
		}
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		if next == '>' || next == '=' || strings.ContainsRune("=!<>+-*/%&|^?", prev) {
			continue
		}
		return i
	}
	return -1
}

func isGenericCalleeStart(afterAssignment string) bool {
	if !strings.HasSuffix(afterAssignment, "<") {
		return false
	}
	callee := strings.TrimSuffix(afterAssignment, "<")
	if callee == "" {
		return false
	}
	for _, r := range callee {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '$' && r != '.' {
			return false
		}
	}
	return true
}

func curlyDeltaBefore(line string, end int) int {
	quote := rune(0)
	escaped := false
	delta := 0
	runes := []rune(line)
	end = min(end, len(runes))
	for _, r := range runes[:end] {
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
		switch r {
		case '{':
			delta++
		case '}':
			delta--
		}
	}
	return delta
}

func arrowIndexAfter(line string, start int) int {
	quote := rune(0)
	escaped := false
	runes := []rune(line)
	for i, r := range runes {
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
		if i >= start && r == '=' && i+1 < len(runes) && runes[i+1] == '>' {
			return i
		}
	}
	return -1
}

func identifierIndexAfter(line, identifier string, start int) int {
	quote := rune(0)
	escaped := false
	runes := []rune(line)
	want := []rune(identifier)
	for i, r := range runes {
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
		if i < start || i+len(want) > len(runes) || string(runes[i:i+len(want)]) != identifier {
			continue
		}
		if i > 0 && (unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1]) || runes[i-1] == '_') {
			continue
		}
		end := i + len(want)
		if end < len(runes) && (unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end]) || runes[end] == '_') {
			continue
		}
		return i
	}
	return -1
}

func isFunctionBlockOpener(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasSuffix(trimmed, "{") {
		return false
	}
	openBrace := strings.LastIndex(trimmed, "{")
	return strings.LastIndex(trimmed[:openBrace], ")") >= 0
}

func isArrowBlockOpener(line string, initialParenDepth, initialBracketDepth int) bool {
	arrow := arrowIndexAfter(line, 0)
	for arrow >= 0 {
		next := arrowIndexAfter(line, arrow+2)
		if next < 0 {
			break
		}
		arrow = next
	}
	runes := []rune(line)
	if arrow < 0 || strings.TrimSpace(string(runes[arrow+2:])) != "{" {
		return false
	}

	// A top-level colon before the arrow identifies a TypeScript property
	// signature such as `onBuild: () => {`, not an implementation callback.
	// Colons inside callback parameters (`({ value }: Props) => {`) are safe.
	parenDepth, bracketDepth := initialParenDepth, initialBracketDepth
	quote := rune(0)
	escaped := false
	start := 0
	if assignment := bindingAssignmentIndex(line); assignment >= 0 {
		start = assignment + 1
	}
	for _, r := range runes[start:arrow] {
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
		switch r {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
		case ':':
			if parenDepth == 0 && bracketDepth == 0 {
				return false
			}
		}
	}
	return true
}

// stripDeclarationHeaderComments removes line and block comments while
// preserving quoted text. Unlike stripLineComment, it carries block-comment
// state between lines because React prop types commonly contain doc comments.
func stripDeclarationHeaderComments(line string, comments []string, inBlock *bool) string {
	if len(comments) == 0 {
		comments = defaultProfile.lineComments
	}
	blockAware := false
	for _, tok := range comments {
		if tok == "//" {
			blockAware = true
			break
		}
	}
	quote := rune(0)
	escaped := false
	runes := []rune(line)
	var kept strings.Builder
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if *inBlock {
			if r == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				*inBlock = false
				i++
			}
			continue
		}
		if quote != 0 {
			kept.WriteRune(r)
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
			kept.WriteRune(r)
			continue
		}
		if blockAware && r == '/' && i+1 < len(runes) && runes[i+1] == '*' {
			*inBlock = true
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
			break
		}
		kept.WriteRune(r)
	}
	return kept.String()
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
		previous := strings.TrimSpace(lines[attached-1])
		if strings.HasSuffix(previous, "*/") {
			commentStart, ok := attachedBlockCommentStart(lines, attached, maxAdornmentLines)
			if !ok {
				break
			}
			attached = commentStart
			continue
		}
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

// attachedBlockCommentStart treats a contiguous /* ... */ comment immediately
// above a declaration as one adornment. This avoids attaching only a dangling
// closing line when punctuation inside earlier JSDoc rows confuses the generic
// bracket balancer.
func attachedBlockCommentStart(lines []string, declarationStart, limit int) (int, bool) {
	if declarationStart <= 0 {
		return 0, false
	}
	end := declarationStart - 1
	if !strings.HasSuffix(strings.TrimSpace(lines[end]), "*/") {
		return 0, false
	}
	for start := end; start >= 0 && declarationStart-start <= limit; start-- {
		trimmed := strings.TrimSpace(lines[start])
		if opener := strings.Index(trimmed, "/*"); opener >= 0 {
			if strings.TrimSpace(trimmed[:opener]) != "" {
				return 0, false
			}
			return start, true
		}
	}
	return 0, false
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
