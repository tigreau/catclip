package render

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// WrapTreeRanges wraps only retained numeric range badges, never filenames or
// source text. Unknown width leaves complete ranges on one line for pipes.
func WrapTreeRanges(text string, width int, colors Palette) string {
	if width <= 0 || !strings.Contains(text, ",") {
		return text
	}
	var out strings.Builder
	for line := range strings.SplitAfterSeq(text, "\n") {
		out.WriteString(wrapRangeLine(strings.TrimSuffix(line, "\n"), width, colors))
		if strings.HasSuffix(line, "\n") {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func wrapRangeLine(line string, width int, colors Palette) string {
	start, end := -1, -1
	for i := 0; i < len(line); i++ {
		if line[i] != '[' {
			continue
		}
		j := i + 1
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j == i+1 || j >= len(line) || line[j] != '-' {
			continue
		}
		close := strings.IndexByte(line[j:], ']')
		if close < 0 {
			continue
		}
		start, end = i, j+close
		break
	}
	if start < 0 || !strings.Contains(line[start:end], ",") || visibleTreeWidth(line) <= width {
		return line
	}
	// Continue at this file's branch depth, without another file connector.
	depth := strings.Count(line[:start], "│")
	prefix := strings.Repeat("│   ", depth) + "│ "
	if visibleTreeWidth(prefix)+12 > width {
		prefix = "│ "
	}
	continuation := colors.Tree + prefix + colors.Reset + colors.Git
	var out strings.Builder
	out.WriteString(line[:start+1])
	used := visibleTreeWidth(line[:start+1])
	ranges := strings.Split(line[start+1:end], ",")
	for i, value := range ranges {
		value = strings.TrimSpace(value)
		extra := len(value)
		if i > 0 {
			extra++
		}
		if i < len(ranges)-1 {
			extra++
		} else {
			extra += visibleTreeWidth(line[end:])
		}
		if used+extra > width {
			out.WriteString(colors.Reset + "\n" + continuation)
			used = visibleTreeWidth(prefix)
		} else if i > 0 {
			out.WriteByte(' ')
			used++
		}
		out.WriteString(value)
		used += len(value)
		if i < len(ranges)-1 {
			out.WriteByte(',')
			used++
		}
	}
	out.WriteString(line[end:])
	return out.String()
}

func visibleTreeWidth(s string) int {
	width := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) {
				b := s[i]
				i++
				if b >= 0x40 && b <= 0x7e {
					break
				}
			}
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
			continue
		}
		switch {
		case r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff || r >= 0xfe10 && r <= 0xfe6f || r >= 0xff01 && r <= 0xff60 || r >= 0x1f300):
			width += 2
		default:
			width++
		}
	}
	return width
}
