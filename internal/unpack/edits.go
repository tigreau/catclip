package unpack

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// LineEdit replaces lines Start..End (1-based, inclusive) of the current file
// with Body, which may be shorter or longer than the range. Total is the line
// count of the file the excerpt was copied from; HasTotal tells an explicit
// total="0" (an empty or not yet existing file) from a tag without total.
type LineEdit struct {
	Start, End int
	Total      int
	HasTotal   bool
	Body       []byte
}

// createsFile reports edits that all declare total="0": the agent states the
// file has no lines yet, so a missing file is created from them rather than
// treated as unpacking in the wrong folder.
func createsFile(edits []LineEdit) bool {
	for _, e := range edits {
		if !e.HasTotal || e.Total != 0 {
			return false
		}
	}
	return len(edits) > 0
}

var linesAttrPattern = regexp.MustCompile(`^\s*(\d+)\s*(-\s*(\d*))?\s*$`)

// openRangeEnd stands for "to the end of the file" (lines="A-").
const openRangeEnd = 1 << 30

// parseLineRange reads lines="A-B", "A" or "A-" (to the end, written by
// catclip before v0.7.15 for --lines START). catclip writes one range per tag.
func parseLineRange(value string) (start, end int, ok bool) {
	m := linesAttrPattern.FindStringSubmatch(value)
	if m == nil {
		return 0, 0, false
	}
	start, _ = strconv.Atoi(m[1])
	end = start
	switch {
	case m[3] != "":
		end, _ = strconv.Atoi(m[3])
	case m[2] != "":
		end = openRangeEnd
	}
	if start < 1 || end < start {
		return 0, 0, false
	}
	return start, end, true
}

// checkEditsConsistent refuses overlapping ranges in one file: catclip never
// writes them, so they come from a garbled reply and must not be applied.
func checkEditsConsistent(path string, edits []LineEdit) error {
	sorted := append([]LineEdit(nil), edits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Start <= sorted[i-1].End {
			return fmt.Errorf("%s has overlapping line ranges (lines %d-%d and %d-%d)",
				path, sorted[i-1].Start, sorted[i-1].End, sorted[i].Start, sorted[i].End)
		}
	}
	return nil
}

var (
	numberedLinePattern = regexp.MustCompile(`^ *(\d+)(\t| +)`)
	numberedBlankLine   = regexp.MustCompile(`^ *(\d+)$`)
	lineNumberPrefix    = regexp.MustCompile(`^ *\d+`)
)

// stripLineNumbers removes a --lines number column ("    12<TAB>code") when
// every line carries one and the numbers count up from first. Chat apps turn
// the tab into spaces; then the narrowest gap on any line is taken as the
// separator, so indentation after it is kept. Anything else is left alone.
func stripLineNumbers(body []byte, first int) []byte {
	lines := splitLinesKeepEnds(body)
	if len(lines) == 0 {
		return body
	}
	gaps := make([]string, len(lines))
	for i, line := range lines {
		text := strings.TrimRight(string(line), "\r\n")
		m := numberedLinePattern.FindStringSubmatch(text)
		if m == nil {
			// catclip writes a blank line as the number and a tab only;
			// a chat app may drop the trailing tab.
			if blank := numberedBlankLine.FindStringSubmatch(text); blank != nil {
				m = []string{blank[0], blank[1], ""}
			} else {
				return body
			}
		}
		if n, _ := strconv.Atoi(m[1]); n != first+i {
			return body
		}
		gaps[i] = m[2]
	}
	sep := ""
	for _, gap := range gaps {
		if gap == "" {
			continue
		}
		if sep == "" || len(gap) < len(sep) || (gap == "\t" && sep != "\t") {
			sep = gap
		}
	}
	var out bytes.Buffer
	for _, line := range lines {
		text := string(line)
		m := lineNumberPrefix.FindString(text)
		rest := text[len(m):]
		if strings.HasPrefix(rest, sep) {
			rest = rest[len(sep):]
		} else {
			rest = strings.TrimLeft(rest, " \t")
		}
		out.WriteString(rest)
	}
	return out.Bytes()
}

// splitLinesKeepEnds splits data into lines that keep their "\n" or "\r\n".
func splitLinesKeepEnds(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	parts := bytes.SplitAfter(data, []byte("\n"))
	if len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// LineCount counts lines as an editor shows them: newline bytes, plus one if
// the file does not end with a newline. An empty file has 0 lines. This is
// the number `out-of="N"` carries.
func LineCount(data []byte) int {
	return len(splitLinesKeepEnds(data))
}

// fileUsesCRLF reports whether most of the file's line breaks are CRLF.
func fileUsesCRLF(data []byte) bool {
	lf := bytes.Count(data, []byte("\n"))
	crlf := bytes.Count(data, []byte("\r\n"))
	return crlf > 0 && crlf*2 >= lf
}

// withLineEndings rewrites text's line breaks as CRLF or LF.
func withLineEndings(text []byte, crlf bool) []byte {
	lf := bytes.ReplaceAll(text, []byte("\r\n"), []byte("\n"))
	if !crlf {
		return lf
	}
	return bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
}

// applyLineEdits applies edits to local from the bottom up, so earlier edits
// never shift the lines of later ones. New lines take the file's own line
// ending. Appending past the end adds a line break to an unterminated last
// line first; no blank lines are padded.
func applyLineEdits(local []byte, edits []LineEdit) []byte {
	crlf := fileUsesCRLF(local)
	eol := "\n"
	if crlf {
		eol = "\r\n"
	}
	lines := splitLinesKeepEnds(local)
	sorted := append([]LineEdit(nil), edits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start > sorted[j].Start })
	for _, e := range sorted {
		start := min(e.Start-1, len(lines))
		end := min(e.End, len(lines))
		if start >= len(lines) && len(lines) > 0 {
			last := lines[len(lines)-1]
			if !bytes.HasSuffix(last, []byte("\n")) {
				lines[len(lines)-1] = append(append([]byte(nil), last...), eol...)
			}
		}
		body := splitLinesKeepEnds(withLineEndings(e.Body, crlf))
		// The original's last line had no line break and is replaced: keep
		// the file unterminated rather than adding a final newline.
		if end == len(lines) && end > start && len(body) > 0 && !bytes.HasSuffix(lines[len(lines)-1], []byte("\n")) {
			lastBody := body[len(body)-1]
			body[len(body)-1] = bytes.TrimSuffix(bytes.TrimSuffix(lastBody, []byte("\n")), []byte("\r"))
		}
		next := make([][]byte, 0, len(lines)-(end-start)+len(body))
		next = append(next, lines[:start]...)
		next = append(next, body...)
		next = append(next, lines[end:]...)
		lines = next
	}
	return bytes.Join(lines, nil)
}
