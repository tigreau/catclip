package output

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

// PrintPresentation is supplied by the application: output owns serialization,
// while the renderer owns syntax colors. Plain serialization and stored sinks
// never invoke Highlight. Headless always wins over terminal presentation.
type PrintPresentation struct {
	Headless  bool
	Quiet     bool
	Highlight func(path, content string, diff bool) string
}

// Bound one serialized file unit, not the bundle. Larger units stream plain
// through the same reader, without restarting or reading the file twice.
const printHighlightLimit = 16 * 1024

func printPresentationEnabled(cfg EmitConfig, p PrintPresentation, stdout io.Writer) bool {
	if p.Highlight == nil {
		return false
	}
	return TerminalPrintPalette(cfg, p.Headless, p.Quiet, stdout).Reset != ""
}

// TerminalPrintPalette is shared by source and metadata payload presentation.
// Resolve it on the original stdout before output buffering hides its type.
func TerminalPrintPalette(cfg EmitConfig, headless, quiet bool, stdout io.Writer) platform.Palette {
	if !printColorPolicy(cfg, headless || quiet, true, os.Getenv("TERM")) {
		return platform.Palette{}
	}
	return platform.ActivePaletteForWriter(stdout)
}

func printColorPolicy(cfg EmitConfig, headless, terminalColor bool, term string) bool {
	return cfg.OutputMode == command.OutputModeStdout && !cfg.Raw && !headless && terminalColor && term != "dumb"
}

type printPresentation struct {
	highlight  func(path, content string, diff bool) string
	limit      int
	addedBytes int64
}

func (p *printPresentation) emitEntry(w io.Writer, unit PreparedFileUnit, index int, prefetcher *emitPrefetcher) error {
	if unit.BodyBytes > int64(p.limit) || len(unit.Payload) > p.limit {
		return emitEntry(w, unit, index, prefetcher)
	}
	capture := printUnitCapture{dest: w, limit: p.limit}
	if err := emitEntry(&capture, unit, index, prefetcher); err != nil {
		return err
	}
	if capture.streaming {
		return nil
	}
	plain := capture.buf.Bytes()
	colored := highlightPrintedUnit(plain, unit, p.highlight)
	n, err := io.WriteString(w, colored)
	if err == nil && n != len(colored) {
		err = io.ErrShortWrite
	}
	if err == nil {
		p.addedBytes += int64(len(colored) - len(plain))
	}
	return err
}

type printUnitCapture struct {
	dest      io.Writer
	limit     int
	buf       bytes.Buffer
	streaming bool
}

func (w *printUnitCapture) Write(data []byte) (int, error) {
	if !w.streaming && len(data) <= w.limit-w.buf.Len() {
		return w.buf.Write(data)
	}
	if !w.streaming {
		if _, err := w.buf.WriteTo(w.dest); err != nil {
			return 0, err
		}
		w.streaming = true
	}
	return w.dest.Write(data)
}

// Interpret only boundaries established by the prepared unit. A literal
// </file> or <file ...> inside source is content, never a delimiter to search.
func highlightPrintedUnit(raw []byte, unit PreparedFileUnit, highlight func(string, string, bool) string) string {
	plain := string(raw)
	if len(raw) == 0 || bytes.ContainsAny(raw, "\x00\x1b") {
		return plain
	}
	entry := unit.Entry
	if len(unit.SnippetRanges) > 0 {
		var out strings.Builder
		rest := raw
		for _, r := range unit.SnippetRanges {
			tag := buildFileOpenTagWithLines(entry.RelPath, "", fmt.Sprintf("%d-%d", r.Start, r.End))
			if !bytes.HasPrefix(rest, tag) {
				return plain
			}
			rest = rest[len(tag):]
			end := 0
			for line := r.Start; line <= r.End; line++ {
				n := bytes.IndexByte(rest[end:], '\n')
				if n < 0 {
					return plain
				}
				end += n + 1
			}
			if !bytes.HasPrefix(rest[end:], fileCloseTag) {
				return plain
			}
			out.Write(tag)
			out.WriteString(highlight(entry.RelPath, string(rest[:end]), false))
			out.Write(fileCloseTag)
			rest = rest[end+len(fileCloseTag):]
		}
		if len(rest) != 0 {
			return plain
		}
		return out.String()
	}
	var tag []byte
	diff := false
	if entry.Mode == command.EntryModeDiff {
		for _, kind := range []string{"diff", "staged-diff", "unstaged-diff", "untracked"} {
			candidate := buildFileOpenTag(entry.RelPath, kind)
			if bytes.HasPrefix(raw, candidate) {
				tag, diff = candidate, kind != "untracked"
				break
			}
		}
	} else if entry.Lines {
		attr := ""
		if entry.LinesStart > 0 {
			attr = fmt.Sprintf("%d-", entry.LinesStart)
			if entry.LinesEnd > 0 {
				attr += fmt.Sprint(entry.LinesEnd)
			}
		}
		tag = buildFileOpenTagWithLines(entry.RelPath, "", attr)
	} else {
		tag = buildFileOpenTag(entry.RelPath, "")
	}
	if len(tag) == 0 || !bytes.HasPrefix(raw, tag) || !bytes.HasSuffix(raw, fileCloseTag) || len(raw) < len(tag)+len(fileCloseTag) {
		return plain
	}
	body := string(raw[len(tag) : len(raw)-len(fileCloseTag)])
	colored := ""
	if entry.Lines {
		colored = highlightNumberedPrintBody(entry.RelPath, body, highlight)
	} else {
		colored = highlight(entry.RelPath, body, diff)
	}
	return string(tag) + colored + string(fileCloseTag)
}

func highlightNumberedPrintBody(path, body string, highlight func(string, string, bool) string) string {
	lines := strings.SplitAfter(body, "\n")
	prefixes := make([]string, 0, len(lines))
	var source strings.Builder
	for _, line := range lines {
		if line == "" {
			continue
		}
		n := strings.IndexByte(line, '\t')
		if n < 0 {
			return body
		}
		prefixes = append(prefixes, line[:n+1])
		source.WriteString(line[n+1:])
	}
	colored := highlight(path, source.String(), false)
	var out strings.Builder
	for _, prefix := range prefixes {
		n := strings.IndexByte(colored, '\n')
		if n < 0 {
			return body
		}
		out.WriteString(prefix)
		out.WriteString(colored[:n+1])
		colored = colored[n+1:]
	}
	// Formatters can place their final reset after the last newline.
	out.WriteString(colored)
	return out.String()
}
