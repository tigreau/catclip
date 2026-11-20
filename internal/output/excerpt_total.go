package output

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"

	"github.com/tigreau/catclip/internal/discovery"
)

// Excerpt tags (--snippet and ranged --lines) carry the file's line count,
// out-of="N", since v0.7.15: an agent appends to a file with lines="N+1-N+1",
// and --unpack skips an edit whose file no longer has N lines (it changed
// since the excerpt was copied). Lines count as an editor shows them: newline
// bytes, plus one when the file does not end with a newline; this equals
// len(SplitLogicalLines(data)) and unpack.LineCount.

// fileLineTotals caches counts per file for one run: the payload, the
// --print highlighter and previews must agree on the same tag.
var fileLineTotals sync.Map

// fileLineTotal returns the line count of the file at abs, or 0 when it cannot
// be read (the tag then simply has no total).
func fileLineTotal(abs string) int {
	if v, ok := fileLineTotals.Load(abs); ok {
		return v.(int)
	}
	f, err := os.Open(abs)
	if err != nil {
		return 0
	}
	defer f.Close()
	buf := make([]byte, 256*1024)
	lines, last := 0, byte('\n')
	for {
		n, err := f.Read(buf)
		if n > 0 {
			lines += bytes.Count(buf[:n], []byte{'\n'})
			last = buf[n-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0
		}
	}
	if last != '\n' {
		lines++
	}
	fileLineTotals.Store(abs, lines)
	return lines
}

// withTotal adds out-of="N" to an open tag ("<file ...>\n"). A count of 0
// (unreadable or empty file) leaves the tag unchanged.
func withTotal(tag []byte, total int) []byte {
	if total <= 0 || !bytes.HasSuffix(tag, []byte(">\n")) {
		return tag
	}
	out := make([]byte, 0, len(tag)+16)
	out = append(out, tag[:len(tag)-2]...)
	out = append(out, ` out-of="`...)
	out = strconv.AppendInt(out, int64(total), 10)
	out = append(out, "\">\n"...)
	return out
}

// excerptOpenTag opens one excerpt with its range and the file's total.
func excerptOpenTag(relPath, typeAttr string, r SnippetRange, total int) []byte {
	return withTotal(buildFileOpenTagWithLines(relPath, typeAttr, fmt.Sprintf("%d-%d", r.Start, r.End)), total)
}

// rangedLinesAttr is the lines="..." value of a ranged --lines entry, or ""
// for a whole numbered file. The end is the last line actually included: an
// open end (--lines START) or an END past the file is written as the file's
// last line, so the tag is always the real range, in previews and copies.
func rangedLinesAttr(entry discovery.Entry, total int) string {
	if entry.LinesStart <= 0 {
		return ""
	}
	end := entry.LinesEnd
	if end <= 0 || (total > 0 && end > total) {
		end = total
	}
	if end <= 0 {
		return fmt.Sprintf("%d-", entry.LinesStart)
	}
	return fmt.Sprintf("%d-%d", entry.LinesStart, end)
}

// rangedLinesOpenTag opens --lines output: numbered="true" for a whole file,
// lines="A-B" out-of="N" for a range.
func rangedLinesOpenTag(entry discovery.Entry, typeAttr string) []byte {
	if entry.LinesStart <= 0 {
		return buildNumberedOpenTag(entry.RelPath, typeAttr, "")
	}
	total := fileLineTotal(entry.AbsPath)
	return withTotal(buildNumberedOpenTag(entry.RelPath, typeAttr, rangedLinesAttr(entry, total)), total)
}
