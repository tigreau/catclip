package output

import (
	"regexp"
	"strings"
	"unicode"
)

// endDelimitedCandidates returns declaration spans for end-delimited languages
// (Ruby, Elixir). A start line matched by unitStart opens a unit; the extent is
// the following blank/more-indented lines plus a reattached `end` at the
// declaration's indent. Relies on conventional indentation (both languages are
// indented by convention though not by grammar); smallest-enclosing selection
// then returns the nearest method for an interior match and the class/module
// for a declaration-level match.
func endDelimitedCandidates(lines []string, unitStart *regexp.Regexp, comments []string) []SnippetRange {
	if unitStart == nil {
		return nil
	}
	var out []SnippetRange
	for i := range lines {
		if !unitStart.MatchString(strings.TrimSpace(lines[i])) {
			continue
		}
		base := indentationWidth(lines[i])
		start := attachedDeclarationStart(lines, i, comments)
		end := i
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" || indentationWidth(lines[j]) > base {
				end = j
				continue
			}
			if isEndCloser(t) {
				end = j
			}
			break
		}
		if end > i {
			out = append(out, SnippetRange{Start: start + 1, End: end + 1})
		}
	}
	return out
}

func isEndCloser(trimmed string) bool {
	return trimmed == "end" || trimmed == "end;" ||
		strings.HasPrefix(trimmed, "end ") || strings.HasPrefix(trimmed, "end.") ||
		strings.HasPrefix(trimmed, "end)") || strings.HasPrefix(trimmed, "end,")
}

// Structural extent strategies for non-code files: tag markup (XML/HTML) and
// section config (INI/TOML). Both return multi-line unit spans (1-indexed) that
// feed the same smallest-enclosing selection the code path uses, so a match on
// a leaf (a <version> or a key line) returns the nearest multi-line ancestor.

// xmlElementCandidates returns the multi-line <tag>...</tag> element spans in a
// file. Single-line elements are omitted so the enclosing selection returns the
// nearest multi-line ancestor (e.g. <dependency> for a <version> match). The
// tokenizer is quote/comment/CDATA/PI aware; a `<`/`>` inside an attribute
// value or a comment does not count. Malformed input degrades to fewer/no
// candidates (the caller then falls back to the paragraph).
func xmlElementCandidates(lines []string, htmlLike, caseFold bool) []SnippetRange {
	type openTag struct {
		name string
		line int
	}
	var stack []openTag
	var out []SnippetRange
	inComment, inCData := false, false
	// Tag-parsing state carried across lines so a multi-line opening tag
	// (<dependency\n  scope="x">) is still recognized with its true start line,
	// instead of being dropped and letting its close tag pop a valid ancestor.
	inTag := false
	tagStartLine := 0
	tagQuote := rune(0)
	var tagBuf []rune
	rawTextTag := "" // when set, inside an HTML <script>/<style> raw-text region

	normName := func(s string) string {
		if caseFold {
			return strings.ToLower(s)
		}
		return s
	}

	// popImplied pops and emits any stack-top elements that HTML implies are
	// closed by `trigger` (a start-tag name, or a close-tag name when isEnd), each
	// ending at `boundary`. Multi-line leaves become candidates; single-line ones
	// are skipped like any other leaf.
	popImplied := func(trigger string, isEnd bool, boundary int) {
		for len(stack) > 0 {
			top := stack[len(stack)-1]
			var closed bool
			if isEnd {
				closed = htmlEndCloses(top.name, trigger)
			} else {
				closed = htmlStartCloses(top.name, trigger)
			}
			if !closed {
				return
			}
			stack = stack[:len(stack)-1]
			if boundary > top.line {
				out = append(out, SnippetRange{Start: top.line, End: boundary})
			}
		}
	}

	flushTag := func(endLine int) {
		inner := strings.TrimSpace(string(tagBuf))
		tagBuf = tagBuf[:0]
		switch {
		case strings.HasPrefix(inner, "/"): // close tag
			name := normName(tagName(strings.TrimSpace(inner[1:])))
			if htmlLike {
				// A closing ancestor (</ul>, </table>, ...) implicitly closes an
				// open optional-end child ending at the line before this close.
				popImplied(name, true, endLine-1)
			}
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if top.name == name {
					if endLine > top.line {
						out = append(out, SnippetRange{Start: top.line, End: endLine})
					}
					break
				}
				// mismatched close: keep popping (lenient)
			}
		case strings.HasSuffix(inner, "/") && !htmlLike: // XML self-closing: no nesting
		default: // open tag (in HTML this also covers <x/>: '/' is not self-closing)
			name := normName(tagName(inner))
			if htmlLike {
				// HTML optional end tags: a new sibling start implicitly closes
				// an open <li>/<p>/<tr>/... at the line before this one. Run this
				// even for void elements (a block-level void like <hr> closes an
				// open <p>) before skipping their push.
				popImplied(name, false, tagStartLine-1)
				if htmlVoidElements[name] {
					return // void element: no content, no close tag, never pushed
				}
			}
			stack = append(stack, openTag{name: name, line: tagStartLine})
			if htmlLike && (name == "script" || name == "style") {
				rawTextTag = name // content is raw until the matching close tag
			}
		}
	}

	for li := range lines {
		runes := []rune(lines[li])
		lineNo := li + 1
		if inTag {
			// A tag continues from the previous line: the newline is
			// attribute-separating whitespace, so insert a space to avoid gluing
			// the element name to the next line's attributes.
			tagBuf = append(tagBuf, ' ')
		}
		i := 0
		for i < len(runes) {
			if rawTextTag != "" {
				// Inside <script>/<style>: skip everything (tag-like text in JS
				// or CSS strings must not nest) until the matching close tag.
				if idx := indexClosingRawTag(runes, i, rawTextTag); idx >= 0 {
					i = idx
					rawTextTag = ""
					// fall through: parse the close tag with the logic below
				} else {
					break
				}
			}
			if inComment {
				if idx := indexRunesFrom(runes, i, "-->"); idx >= 0 {
					i = idx + 3
					inComment = false
					continue
				}
				break // comment continues onto the next line
			}
			if inCData {
				if idx := indexRunesFrom(runes, i, "]]>"); idx >= 0 {
					i = idx + 3
					inCData = false
					continue
				}
				break
			}
			if inTag {
				// Accumulate the tag body until its '>' (quote-aware, may span
				// lines) then classify it as open/close/self-closing.
				r := runes[i]
				if tagQuote != 0 {
					tagBuf = append(tagBuf, r)
					if r == tagQuote {
						tagQuote = 0
					}
					i++
					continue
				}
				switch r {
				case '"', '\'':
					tagQuote = r
					tagBuf = append(tagBuf, r)
				case '>':
					inTag = false
					flushTag(lineNo)
				default:
					tagBuf = append(tagBuf, r)
				}
				i++
				continue
			}
			if runes[i] != '<' {
				i++
				continue
			}
			if hasRunePrefix(runes, i, "<!--") {
				inComment = true
				i += 4
				continue
			}
			if hasRunePrefix(runes, i, "<![CDATA[") {
				inCData = true
				i += 9
				continue
			}
			if hasRunePrefix(runes, i, "<?") || hasRunePrefix(runes, i, "<!") {
				// Processing instruction or DOCTYPE: skip to the closing '>'.
				if idx := indexRuneFrom(runes, i, '>'); idx >= 0 {
					i = idx + 1
					continue
				}
				break
			}
			// Begin a tag; the body accumulates from here (possibly across lines).
			inTag = true
			tagStartLine = lineNo
			tagQuote = 0
			tagBuf = tagBuf[:0]
			i++ // consume '<'
		}
	}
	return out
}

// htmlVoidElements are HTML elements with no content and no end tag; in HTML
// mode they are never pushed onto the element stack (XML has no void elements).
var htmlVoidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// htmlParagraphAncestors are block-level containers whose end tag implicitly
// closes an open <p> (a <p> holds only phrasing content, so any block ancestor
// closing forces it shut).
var htmlParagraphAncestors = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"body": true, "button": true, "dd": true, "details": true, "dialog": true,
	"div": true, "dt": true, "fieldset": true, "figcaption": true, "figure": true,
	"footer": true, "form": true, "header": true, "li": true, "main": true,
	"map": true, "menu": true, "nav": true, "object": true, "section": true,
	"td": true, "th": true,
}

// htmlParagraphClosers is the set of start tags that implicitly close an open
// <p> (HTML block-level elements that cannot be paragraph content).
var htmlParagraphClosers = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"details": true, "div": true, "dl": true, "fieldset": true,
	"figcaption": true, "figure": true, "footer": true, "form": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hgroup": true, "hr": true, "main": true, "menu": true,
	"nav": true, "ol": true, "p": true, "pre": true, "section": true,
	"table": true, "ul": true,
}

// htmlStartCloses reports whether opening `name` implicitly closes an open
// `top` element under HTML optional-end-tag rules.
func htmlStartCloses(top, name string) bool {
	switch top {
	case "li":
		return name == "li"
	case "dt", "dd":
		return name == "dt" || name == "dd"
	case "p":
		return htmlParagraphClosers[name]
	case "tr":
		return name == "tr" || name == "tbody" || name == "thead" || name == "tfoot"
	case "td", "th":
		return name == "td" || name == "th" || name == "tr" ||
			name == "tbody" || name == "thead" || name == "tfoot"
	case "option":
		return name == "option" || name == "optgroup"
	case "thead", "tbody", "tfoot":
		return name == "thead" || name == "tbody" || name == "tfoot"
	}
	return false
}

// htmlEndCloses reports whether closing ancestor `name` implicitly closes an
// open optional-end child `top`.
func htmlEndCloses(top, name string) bool {
	switch top {
	case "li":
		return name == "ul" || name == "ol" || name == "menu"
	case "dt", "dd":
		return name == "dl"
	case "tr":
		return name == "table" || name == "thead" || name == "tbody" || name == "tfoot"
	case "td", "th":
		return name == "tr" || name == "table" || name == "thead" || name == "tbody" || name == "tfoot"
	case "option":
		return name == "select" || name == "optgroup" || name == "datalist"
	case "thead", "tbody", "tfoot":
		return name == "table"
	case "p":
		return htmlParagraphAncestors[name]
	}
	return false
}

// indexClosingRawTag returns the index of the '<' beginning the close tag
// </name> (case-insensitive, name already lowercased) at or after `from`, or -1
// if it is not on this line. Used to skip HTML <script>/<style> raw text.
func indexClosingRawTag(runes []rune, from int, name string) int {
	nameRunes := []rune(name)
	for i := from; i+2+len(nameRunes) <= len(runes); i++ {
		if runes[i] != '<' || runes[i+1] != '/' {
			continue
		}
		match := true
		for j, nr := range nameRunes {
			if unicode.ToLower(runes[i+2+j]) != nr {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		k := i + 2 + len(nameRunes)
		if k >= len(runes) {
			return i // </name at end of line
		}
		switch runes[k] {
		case '>', ' ', '\t', '/':
			return i
		}
	}
	return -1
}

// tagName extracts the element name from a tag body (text between < and >),
// stopping at whitespace or '/'.
func tagName(inner string) string {
	inner = strings.TrimSpace(inner)
	for i, r := range inner {
		if r == ' ' || r == '\t' || r == '/' || r == '>' || r == '\n' {
			return inner[:i]
		}
	}
	return inner
}

// jsonBraceCandidates returns the multi-line {...} and [...] spans in a JSON or
// JSONC file, so a match on a nested key or value resolves to its smallest
// enclosing object/array. The scanner is string-aware (escaped quotes handled)
// and skips // and /* */ comments so a brace inside a string or comment is not
// counted; single-line leaves are omitted and unbalanced input simply yields
// fewer candidates (the caller then falls back to the paragraph). Closers are
// matched leniently against the nearest opener rather than by exact {/[ kind,
// which is correct for well-formed input and degrades gracefully otherwise.
func jsonBraceCandidates(lines []string) []SnippetRange {
	type jsonFrame struct {
		line int
		open rune // '{' or '[' to match the closer kind
	}
	var stack []jsonFrame
	var out []SnippetRange
	inString := false
	escaped := false
	inBlockComment := false
	for li := range lines {
		runes := []rune(lines[li])
		lineNo := li + 1
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
				i = len(runes) // rest of line is comment
			case inString:
				if escaped {
					escaped = false
				} else if r == '\\' {
					escaped = true
				} else if r == '"' {
					inString = false
				}
			case r == '"':
				inString = true
			case r == '/' && i+1 < len(runes) && runes[i+1] == '/':
				inLineComment = true
			case r == '/' && i+1 < len(runes) && runes[i+1] == '*':
				inBlockComment = true
				i++
			case r == '{' || r == '[':
				stack = append(stack, jsonFrame{line: lineNo, open: r})
			case r == '}' || r == ']':
				if len(stack) > 0 {
					top := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					want := '{'
					if r == ']' {
						want = '['
					}
					// Mismatched delimiter kinds (`[` closed by `}`): malformed
					// input. Drop the pairing so the caller falls back to the
					// paragraph rather than emitting a misleading unit.
					if top.open != want {
						continue
					}
					if lineNo > top.line {
						out = append(out, SnippetRange{Start: top.line, End: lineNo})
					}
				}
			}
		}
	}
	return out
}

// sectionCandidates returns the spans of `[header]` sections: from a header
// line to the line before the next header (or EOF). A match inside a section
// returns the whole section.
func sectionCandidates(lines []string) []SnippetRange {
	var headers []int // 1-indexed header line numbers
	for i, line := range lines {
		if isSectionHeader(line) {
			headers = append(headers, i+1)
		}
	}
	if len(headers) == 0 {
		return nil
	}
	total := len(lines)
	out := make([]SnippetRange, 0, len(headers))
	for idx, h := range headers {
		end := total
		if idx+1 < len(headers) {
			end = headers[idx+1] - 1
		}
		if end > h {
			out = append(out, SnippetRange{Start: h, End: end})
		}
	}
	return out
}

func isSectionHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") && len(trimmed) > 2
}

func hasRunePrefix(runes []rune, i int, prefix string) bool {
	p := []rune(prefix)
	if i+len(p) > len(runes) {
		return false
	}
	for j := range p {
		if runes[i+j] != p[j] {
			return false
		}
	}
	return true
}

func indexRuneFrom(runes []rune, from int, target rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

func indexRunesFrom(runes []rune, from int, target string) int {
	t := []rune(target)
	for i := from; i+len(t) <= len(runes); i++ {
		if hasRunePrefix(runes, i, target) {
			return i
		}
	}
	return -1
}
