package unpack

import (
	"bytes"
	"regexp"
	"strings"
)

// Chat models often shorten code they did not change with a comment such as
// "// ... rest of the function unchanged". Written as received, that comment
// replaces the real code: everything it stands for is deleted. A line counts
// as such a placeholder only when it is a comment (or a bare ellipsis) made of
// an ellipsis and a placeholder word, and the local file does not already
// contain it; spread or variadic syntax (`...args`, `xs...`) is never a whole
// comment line.

var (
	commentLine     = regexp.MustCompile(`^\s*(//+|#+|--|;+|/\*+|\*|<!--|\{/\*)\s*(.*?)\s*(\*+/|-->|\*/\})?\s*$`)
	ellipsisPattern = regexp.MustCompile(`\.\.\.|…`)
	placeholderWord = regexp.MustCompile(`(?i)\b(rest|existing|unchanged|remaining|same|omitted|previous|other|as before|keep|code here|implementation|etc)\b`)
)

// shortenedLine returns the 1-based line of the first placeholder in body
// that local does not already contain, or 0.
func shortenedLine(body, local []byte) int {
	// Fast path: almost no entry contains an ellipsis at all; only lines that
	// do are checked with the patterns (one byte search per entry otherwise).
	pos := 0
	for pos < len(body) {
		hit := nextEllipsis(body[pos:])
		if hit < 0 {
			return 0
		}
		at := pos + hit
		start := bytes.LastIndexByte(body[:at], '\n') + 1
		end := len(body)
		if nl := bytes.IndexByte(body[at:], '\n'); nl >= 0 {
			end = at + nl
		}
		text := strings.TrimRight(string(body[start:end]), "\r")
		if isPlaceholderLine(text) && (local == nil || !bytes.Contains(local, []byte(strings.TrimSpace(text)))) {
			return bytes.Count(body[:start], []byte{'\n'}) + 1
		}
		pos = end + 1
	}
	return 0
}

// nextEllipsis returns the index of the first "..." or "…" in b, or -1.
func nextEllipsis(b []byte) int {
	dots := bytes.Index(b, []byte("..."))
	single := bytes.Index(b, []byte("…"))
	switch {
	case dots < 0:
		return single
	case single < 0 || dots < single:
		return dots
	}
	return single
}

func isPlaceholderLine(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "..." || trimmed == "…" {
		return true
	}
	m := commentLine.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	inner := m[2]
	if !ellipsisPattern.MatchString(inner) {
		return false
	}
	// "// ..." alone, or "// ... rest unchanged", "# existing code ...".
	rest := strings.TrimSpace(ellipsisPattern.ReplaceAllString(inner, ""))
	return rest == "" || placeholderWord.MatchString(rest)
}

// shortenedPlaceholder returns the placeholder line that shortened the entry,
// quoted ("\"// ... rest unchanged\""), or "".
func shortenedPlaceholder(e Entry, local []byte) string {
	bodies := [][]byte{e.Body}
	if e.Edits != nil {
		bodies = bodies[:0]
		for _, ed := range e.Edits {
			bodies = append(bodies, ed.Body)
		}
	}
	for _, body := range bodies {
		if n := shortenedLine(body, local); n > 0 {
			line := strings.TrimSpace(strings.TrimRight(string(splitLinesKeepEnds(body)[n-1]), "\r\n"))
			return "\"" + clip(line) + "\""
		}
	}
	return ""
}
