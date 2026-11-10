package theme

import "github.com/alecthomas/chroma/v2"

const TerminalID = "terminal"

// Pair these canonical ANSI names with Chroma's TTY16 formatter. Emitted SGR
// slots, not these reference RGB values, select the user's terminal colors.
// Use the normal slots (0..7), matching terminal trees and diagnostics. Bright
// slots often have unrelated shades in custom profiles. Unclassified names and
// punctuation stay neutral; color conveys token roles.
func terminalStyle() *chroma.Style {
	neutral := "noinherit"
	return chroma.MustNewStyle(TerminalID, chroma.StyleEntries{
		chroma.Background: neutral, chroma.Text: neutral,
		chroma.Other: neutral, chroma.Name: neutral, chroma.Punctuation: neutral,
		chroma.Keyword: "#ansipurple", chroma.KeywordType: "#ansiteal",
		chroma.KeywordConstant: "#ansibrown", chroma.OperatorWord: "#ansipurple",
		chroma.Operator: neutral, chroma.NameBuiltin: "#ansiteal",
		chroma.NameClass: "#ansiteal", chroma.NameNamespace: "#ansiteal",
		chroma.NameFunction: "#ansidarkblue", chroma.NameTag: "#ansidarkblue",
		chroma.NameAttribute: "#ansiteal", chroma.NameDecorator: "#ansipurple",
		chroma.NameConstant: "#ansibrown", chroma.NameException: "#ansiteal",
		chroma.LiteralString: "#ansidarkgreen", chroma.LiteralStringEscape: "#ansibrown",
		chroma.LiteralStringInterpol: "#ansipurple", chroma.LiteralNumber: "#ansibrown",
		chroma.Comment: "italic " + neutral, chroma.CommentPreproc: "#ansipurple",
		chroma.GenericHeading: "bold #ansidarkblue", chroma.GenericSubheading: "bold #ansiteal",
		chroma.GenericStrong: "bold", chroma.GenericEmph: "italic",
		chroma.GenericInserted: "#ansidarkgreen", chroma.GenericDeleted: "#ansidarkred",
		chroma.GenericError: "#ansidarkred", chroma.GenericTraceback: "#ansidarkred",
		chroma.Error: "underline #ansidarkred",
	})
}
