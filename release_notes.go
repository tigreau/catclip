package catclip

import (
	"strings"
	"unicode"

	"github.com/tigreau/catclip/internal/platform"
)

// A small terminal presentation of common release-note Markdown, not a full
// GitHub Markdown renderer. Unknown syntax stays readable as written. Pipes
// retain the original Markdown; no HTML, images or links are fetched.
func formatReleaseNotes(notes string, colors platform.Palette) string {
	if colors.Reset == "" {
		return notes
	}
	lines := strings.Split(notes, "\n")
	fence := byte(0)
	fenceLen := 0
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		if len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			count := 0
			for count < len(trimmed) && trimmed[count] == trimmed[0] {
				count++
			}
			if count >= 3 && fence == 0 {
				fence, fenceLen = trimmed[0], count
				lines[i] = ""
				continue
			}
			if trimmed[0] == fence && count >= fenceLen && strings.TrimSpace(trimmed[count:]) == "" {
				fence = 0
				lines[i] = ""
				continue
			}
		}
		if fence != 0 {
			lines[i] = "  " + colors.Prompt + line + colors.Reset
			continue
		}
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		if level >= 1 && level <= 6 && level < len(trimmed) && trimmed[level] == ' ' {
			heading := strings.TrimSpace(trimmed[level+1:])
			lines[i] = colors.Bold + colors.Prompt + releaseNoteInline(heading, colors, colors.Bold+colors.Prompt) + colors.Reset
			continue
		}
		if strings.HasPrefix(trimmed, "> ") {
			lines[i] = indent + colors.Dim + "│ " + releaseNoteInline(trimmed[2:], colors, colors.Dim) + colors.Reset
			continue
		}
		if len(trimmed) >= 2 && strings.ContainsRune("-*+", rune(trimmed[0])) && trimmed[1] == ' ' {
			lines[i] = indent + colors.Prompt + "•" + colors.Reset + " " + releaseNoteInline(trimmed[2:], colors, "")
			continue
		}
		lines[i] = releaseNoteInline(line, colors, "")
	}
	return strings.Join(lines, "\n")
}

// Scan left-to-right so code spans are literal and inserted ANSI cannot become
// input to another formatting pass. Unmatched markers are left untouched.
func releaseNoteInline(text string, colors platform.Palette, base string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		marker, style := "", ""
		switch {
		case text[i] == '`':
			end := i + 1
			for end < len(text) && text[end] == '`' {
				end++
			}
			marker, style = text[i:end], colors.Prompt
		case strings.HasPrefix(text[i:], "**"):
			marker, style = "**", colors.Bold
		case strings.HasPrefix(text[i:], "__") && (i == 0 || unicode.IsSpace(rune(text[i-1]))):
			marker, style = "__", colors.Bold
		case text[i] == '*':
			marker, style = "*", colors.Bold
		case text[i] == '[':
			if end := strings.Index(text[i+1:], "]("); end >= 0 {
				labelEnd := i + 1 + end
				urlStart := labelEnd + 2
				if urlEnd := strings.IndexByte(text[urlStart:], ')'); urlEnd >= 0 {
					url := text[urlStart : urlStart+urlEnd]
					if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
						out.WriteString(colors.Prompt + text[i+1:labelEnd] + " (" + url + ")" + colors.Reset + base)
						i = urlStart + urlEnd + 1
						continue
					}
				}
			}
		}
		if marker != "" {
			start := i + len(marker)
			if end := strings.Index(text[start:], marker); end > 0 {
				out.WriteString(style + text[start:start+end] + colors.Reset + base)
				i = start + end + len(marker)
				continue
			}
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}
