package theme

import (
	"math"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/tigreau/catclip/internal/platform"
)

// File/tree-preview and report roles share the selected syntax palette. Missing or low-contrast
// entries fall through to another theme token, then the normal foreground.
// Derive these once alongside the cached style, never for each rendered row.
func presentationPalette(style *chroma.Style, background, foreground chroma.Colour) (platform.Palette, string, string) {
	surface := background
	if !trueColor() {
		_, surface = indexedColour(background)
	}
	pick := func(tokens ...chroma.TokenType) chroma.Colour {
		for _, token := range tokens {
			color := style.Get(token).Colour
			visible := color
			if color.IsSet() && !trueColor() {
				_, visible = indexedColour(color)
			}
			if color.IsSet() && color != foreground && contrast(visible, surface) >= 3 {
				return color
			}
		}
		return foreground
	}
	dim := pick(chroma.Comment, chroma.LineNumbers)
	dir := pick(chroma.NameClass, chroma.NameFunction, chroma.KeywordType, chroma.Keyword)
	label := pick(chroma.NameBuiltin, chroma.NameClass, chroma.Keyword)
	palette := platform.Palette{
		Reset: "\x1b[0m", Bold: "\x1b[1m", Value: ansi(foreground, false),
		Dim: ansi(dim, false), Tree: ansi(dim, false), Label: ansi(dim, false),
		Dir: ansi(dir, false), Prompt: ansi(label, false),
		Git:  ansi(pick(chroma.NameDecorator, chroma.Keyword, chroma.NameBuiltin), false),
		Warn: ansi(pick(chroma.LiteralNumber, chroma.CommentPreproc, chroma.Keyword), false),
		Err:  ansi(pick(chroma.GenericDeleted, chroma.Error, chroma.LiteralString, chroma.Keyword), false),
		OK:   ansi(pick(chroma.GenericInserted, chroma.NameFunction, chroma.NameTag, chroma.LiteralString), false),
	}
	return palette, fzfColor(dim), fzfColor(label)
}
func fzfColor(color chroma.Colour) string {
	if trueColor() {
		return color.String()
	}
	return strings.TrimSuffix(strings.TrimPrefix(ansi(color, false), "\x1b[38;5;"), "m")
}
func contrast(a, b chroma.Colour) float64 {
	luminance := func(c chroma.Colour) float64 {
		channel := func(n uint8) float64 {
			v := float64(n) / 255
			if v <= 0.04045 {
				return v / 12.92
			}
			return math.Pow((v+0.055)/1.055, 2.4)
		}
		return .2126*channel(c.Red()) + .7152*channel(c.Green()) + .0722*channel(c.Blue())
	}
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + .05) / (y + .05)
}
