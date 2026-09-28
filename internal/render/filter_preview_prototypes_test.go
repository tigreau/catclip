package render

// Test-only prototypes: production rendering is unchanged.
import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

func benchCombinedMatches(lines, raw []string, pattern string, focus []int) ([]string, map[int]struct{}) {
	numbers := make(map[int]struct{}, len(focus))
	for _, n := range focus {
		if n > 0 {
			numbers[n] = struct{}{}
		}
	}
	if len(raw) == 0 || strings.TrimSpace(pattern) == "" {
		return lines, numbers
	}
	if isSmartCaseInsensitive(pattern) {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return lines, numbers
	}
	for i, line := range raw {
		spans := re.FindAllStringIndex(line, -1)
		if len(spans) > 0 {
			numbers[i+1] = struct{}{}
		}
		if i < len(lines) {
			lines[i] = benchPaintSpans(lines[i], line, spans)
		}
	}
	return lines, numbers
}

// Hypothetical NEW 200-line policy, not an optimization of today's unlimited
// filter preview. Cutting text before lexing can also change multiline colors.
func benchEarlyCapPreview(w io.Writer, file *FilePreview, opts RenderOptions, colors Palette) error {
	copy := *file
	lines := splitPreviewLines(copy.Content)
	if opts.MaxLines > 0 && len(lines) > opts.MaxLines {
		copy.Content = strings.Join(lines[:opts.MaxLines], "\n") + "\n"
		copy.Truncated = true
	}
	return renderFilePreview(w, &copy, opts, colors)
}

func benchCombinedFilePreview(w io.Writer, file *FilePreview, opts RenderOptions, colors Palette) error {
	if file == nil {
		return nil
	}
	if file.Path != "" && file.Path != "." {
		if _, err := fmt.Fprintf(w, "%s%s%s\n", colors.Bold, file.Path, colors.Reset); err != nil {
			return err
		}
	}

	content := file.Content
	rawLines := splitPreviewLines(file.Content)
	if shouldHighlightFilePreview(colors) {
		highlightPath := file.HighlightPath
		if strings.TrimSpace(highlightPath) == "" {
			highlightPath = file.Path
		}
		content = highlightFilePreview(highlightPath, file.Content, opts)
	}

	lines := splitPreviewLines(content)
	lines, highlightedLineNumbers := benchCombinedMatches(lines, rawLines, file.MatchPattern, file.FocusLines)
	lines = overlayPreviewFocusLineHighlights(lines, file.FocusLines)
	truncated := file.Truncated
	if opts.MaxLines > 0 && len(lines) > opts.MaxLines {
		lines = lines[:opts.MaxLines]
		truncated = true
	}

	width := max(len(strconv.Itoa(len(lines))), 1)
	for i, line := range lines {
		lineNo := i + 1
		lineNumber := stylePreviewLineNumber(fmt.Sprintf("%*d", width, lineNo), lineNo, highlightedLineNumbers, opts, colors)
		if _, err := fmt.Fprintf(w, "%s %s│%s %s\n", lineNumber, colors.Tree, colors.Reset, line); err != nil {
			return err
		}
	}

	if truncated {
		_, err := fmt.Fprintf(w, "%s… truncated%s\n", colors.Dim, colors.Reset)
		return err
	}
	return nil
}

func benchPaintSpans(ansiLine, rawLine string, spans [][]int) string {
	if ansiLine == "" || rawLine == "" {
		return ansiLine
	}

	if len(spans) == 0 {
		return ansiLine
	}

	filtered := spans[:0]
	for _, span := range spans {
		if len(span) != 2 || span[0] >= span[1] {
			continue
		}
		filtered = append(filtered, span)
	}
	if len(filtered) == 0 {
		return ansiLine
	}

	var b strings.Builder
	b.Grow(len(ansiLine) + len(filtered)*(len(previewMatchStart)+len(previewMatchEnd)))

	rawPos := 0
	spanIndex := 0
	inMatch := false

	for i := 0; i < len(ansiLine); {
		if ansiLine[i] == '\x1b' {
			end := ansiSequenceEnd(ansiLine, i)
			seq := ansiLine[i:end]
			b.WriteString(seq)
			if inMatch && isANSIResetSequence(seq) {
				b.WriteString(previewMatchStart)
			}
			i = end
			continue
		}

		for spanIndex < len(filtered) && rawPos >= filtered[spanIndex][1] {
			if inMatch {
				b.WriteString(previewMatchEnd)
				inMatch = false
			}
			spanIndex++
		}
		if spanIndex < len(filtered) && rawPos == filtered[spanIndex][0] && !inMatch {
			b.WriteString(previewMatchStart)
			inMatch = true
		}

		_, ansiSize := utf8.DecodeRuneInString(ansiLine[i:])
		if ansiSize <= 0 {
			break
		}
		if rawPos < len(rawLine) {
			_, rawSize := utf8.DecodeRuneInString(rawLine[rawPos:])
			if rawSize > 0 {
				rawPos += rawSize
			}
		}
		b.WriteString(ansiLine[i : i+ansiSize])
		i += ansiSize

		if inMatch && spanIndex < len(filtered) && rawPos >= filtered[spanIndex][1] {
			b.WriteString(previewMatchEnd)
			inMatch = false
			spanIndex++
		}
	}

	if inMatch {
		b.WriteString(previewMatchEnd)
	}
	return b.String()
}
