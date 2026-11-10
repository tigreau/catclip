package render

import "strings"

// SearchHelpHighlight identifies authored help, never a source language or
// filename. Its role-based colors come from the supplied UI theme palette.
// A language lexer treats prose and regex notation as invalid code, producing
// error backgrounds or underlines that vary with the syntax theme.
const SearchHelpHighlight = "catclip:search-help"

func highlightSearchHelp(content string, colors Palette) string {
	if colors.Reset == "" {
		return content
	}
	var out strings.Builder
	for index, line := range strings.SplitAfter(content, "\n") {
		text := strings.TrimSuffix(line, "\n")
		trimmed := strings.TrimSpace(text)
		switch {
		case trimmed == "":
			out.WriteString(text)
		case index == 0 || strings.HasSuffix(trimmed, ":"):
			out.WriteString(colors.Bold + colors.Value + text + colors.Reset)
		case strings.HasPrefix(text, "    ") && strings.Contains(text, "→"):
			pattern, description, _ := strings.Cut(text, "→")
			out.WriteString(highlightHelpRegex(pattern, colors))
			out.WriteString(colors.Dim + "→" + colors.Reset)
			out.WriteString(highlightHelpDescription(description, colors))
		default:
			out.WriteString(colors.Value + text + colors.Reset)
		}
		if strings.HasSuffix(line, "\n") {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// This styles the small, compiled-in examples, not arbitrary PCRE2 input. It
// does not compile a regex, validate syntax or run a programming-language lexer.
// Keywords from every language remain ordinary example text; regex operators
// and escape pairs have the same role wherever they appear in the cheat sheet.
func highlightHelpRegex(pattern string, colors Palette) string {
	var out strings.Builder
	inClass, classStart, inCount := false, false, false
	activeColor := ""
	for i := 0; i < len(pattern); i++ {
		text := pattern[i : i+1]
		color := colors.Prompt
		ch := pattern[i]
		switch {
		case ch == '\\' && i+1 < len(pattern):
			text = pattern[i : i+2]
			i++
			color = colors.Git
			classStart = false
		case inClass:
			if ch == ']' {
				color, inClass = colors.Git, false
			} else if ch == '^' && classStart {
				color = colors.Git
			}
			classStart = false
		case ch == '[':
			color, inClass, classStart = colors.Git, true, true
		case strings.ContainsRune(".*+?^$|(){}", rune(ch)):
			color = colors.Git
			if ch == '{' {
				inCount = true
			} else if ch == '}' {
				inCount = false
			}
		case inCount && ch >= '0' && ch <= '9':
			color = colors.Warn
		case ch == ' ' || ch == '\t':
			color = colors.Value
		}
		if color != activeColor {
			if activeColor != "" {
				out.WriteString(colors.Reset)
			}
			out.WriteString(color)
			activeColor = color
		}
		out.WriteString(text)
	}
	if activeColor != "" {
		out.WriteString(colors.Reset)
	}
	return out.String()
}

// Parenthesized "PATTERN matches ..." examples use the same pattern colors
// as their left-hand counterparts. Other explanation text stays neutral.
func highlightHelpDescription(description string, colors Palette) string {
	before, parenthetical, hasExample := strings.Cut(description, "(")
	pattern, explanation, hasMatch := strings.Cut(parenthetical, " matches ")
	if !hasExample || !hasMatch {
		return colors.Value + description + colors.Reset
	}
	prefix := ""
	if strings.HasPrefix(pattern, "plain ") {
		prefix, pattern = "plain ", strings.TrimPrefix(pattern, "plain ")
	}
	return colors.Value + before + "(" + prefix + colors.Reset + highlightHelpRegex(pattern, colors) +
		colors.Value + " matches " + explanation + colors.Reset
}
