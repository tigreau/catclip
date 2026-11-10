package theme

import (
	_ "embed"
	"strings"

	"github.com/alecthomas/chroma/v2"
)

//go:embed darcula.xml
var darculaXML string

// These adapt the upstream editor palettes to Chroma's token categories.
// Chroma lexing cannot reproduce an IDE's semantic highlighting. Sources and
// attribution are recorded in SOURCES.md.
func customStyle(id string) *chroma.Style {
	var bg, fg, keyword, str, number, comment, function, typ string
	switch id {
	case TerminalID:
		return terminalStyle()
	case "solarized-dark", "solarized-light":
		return solarizedStyle(id, false)
	case "darcula":
		return chroma.MustNewXMLStyle(strings.NewReader(darculaXML))
	case "vscode-dark":
		bg, fg, keyword, str, number, comment, function, typ = "#1e1e1e", "#d4d4d4", "#569cd6", "#ce9178", "#b5cea8", "#6a9955", "#dcdcaa", "#4ec9b0"
	case "vscode-light":
		bg, fg, keyword, str, number, comment, function, typ = "#ffffff", "#000000", "#0000ff", "#a31515", "#098658", "#008000", "#795e26", "#267f99"
	case "jetbrains-light":
		bg, fg, keyword, str, number, comment, function, typ = "#ffffff", "#000000", "#000080", "#008000", "#0000ff", "#808080", "#000000", "#000000"
	case "atom-one-light":
		bg, fg, keyword, str, number, comment, function, typ = "#fafafa", "#383a42", "#a626a4", "#50a14f", "#986801", "#a0a1a7", "#4078f2", "#c18401"
	default:
		return nil
	}
	return chroma.MustNewStyle(id, chroma.StyleEntries{
		chroma.Background: fg + " bg:" + bg, chroma.Text: fg, chroma.Other: fg,
		chroma.Keyword: keyword, chroma.KeywordType: typ, chroma.NameBuiltin: typ,
		chroma.NameClass: typ, chroma.NameFunction: function, chroma.NameTag: keyword,
		chroma.NameAttribute: function, chroma.LiteralString: str, chroma.LiteralNumber: number,
		chroma.Comment: comment, chroma.GenericHeading: "bold " + keyword,
		chroma.GenericSubheading: "bold " + function, chroma.GenericStrong: "bold",
		chroma.GenericEmph: "italic", chroma.GenericInserted: str, chroma.GenericDeleted: "#d73a49",
	})
}
