package theme

import (
	"fmt"
	"strconv"

	"github.com/alecthomas/chroma/v2"
	"github.com/tigreau/catclip/internal/platform"
)

// Canonical sRGB colors and the author's xterm-256 mapping, not a nearest-color
// guess. See SOURCES.md and the upstream MIT notice in solarized.LICENSE.
var solarizedColors = map[string]struct {
	hex   string
	index int
}{
	"base03": {"#002b36", 234}, "base02": {"#073642", 235},
	"base01": {"#586e75", 240}, "base00": {"#657b83", 241},
	"base0": {"#839496", 244}, "base1": {"#93a1a1", 245},
	"base2": {"#eee8d5", 254}, "base3": {"#fdf6e3", 230},
	"yellow": {"#b58900", 136}, "orange": {"#cb4b16", 166},
	"red": {"#dc322f", 160}, "magenta": {"#d33682", 125},
	"violet": {"#6c71c4", 61}, "blue": {"#268bd2", 33},
	"cyan": {"#2aa198", 37}, "green": {"#859900", 64},
}

type solarizedRoles struct{ background, foreground, muted, emphasis, selection string }

func isSolarized(id string) bool { return id == "solarized-dark" || id == "solarized-light" }

func solarizedMode(id string) solarizedRoles {
	if id == "solarized-light" {
		return solarizedRoles{"base3", "base00", "base1", "base01", "base2"}
	}
	return solarizedRoles{"base03", "base0", "base01", "base1", "base02"}
}

func solarizedColour(name string, indexed bool) chroma.Colour {
	value := solarizedColors[name]
	if !indexed {
		return chroma.MustParseColour(value.hex)
	}
	i := value.index
	if i >= 232 {
		v := uint8(8 + (i-232)*10)
		return chroma.NewColour(v, v, v)
	}
	levels := [6]uint8{0, 95, 135, 175, 215, 255}
	n := i - 16
	return chroma.NewColour(levels[n/36], levels[n/6%6], levels[n%6])
}

// Roles follow the original Solarized Vim groups: statements green, types
// yellow, identifiers/functions blue, literals cyan and preprocessors orange.
func solarizedStyle(id string, indexed bool) *chroma.Style {
	roles := solarizedMode(id)
	c := func(name string) string { return solarizedColour(name, indexed).String() }
	return chroma.MustNewStyle(id, chroma.StyleEntries{
		chroma.Background: c(roles.foreground) + " bg:" + c(roles.background),
		chroma.Text:       c(roles.foreground), chroma.Other: c(roles.foreground),
		chroma.Punctuation: c(roles.foreground), chroma.Operator: c(roles.foreground),
		chroma.Keyword: c("green"), chroma.OperatorWord: c("green"), chroma.KeywordType: c("yellow"),
		chroma.Name: c("blue"), chroma.NameFunction: c("blue"), chroma.NameClass: c("yellow"),
		chroma.NameNamespace: c("blue"), chroma.NameBuiltin: c("blue"), chroma.NameAttribute: c("blue"),
		chroma.NameTag: c("blue"), chroma.NameConstant: c("cyan"), chroma.KeywordConstant: c("cyan"),
		chroma.NameDecorator: c("orange"), chroma.NameException: c("green"),
		chroma.Literal: c("cyan"), chroma.LiteralString: c("cyan"), chroma.LiteralNumber: c("cyan"),
		chroma.LiteralStringEscape: c("red"), chroma.LiteralStringRegex: c("red"),
		chroma.Comment: "italic " + c(roles.muted), chroma.CommentPreproc: c("orange"),
		chroma.CommentSpecial: c("magenta"), chroma.LineNumbers: c(roles.muted),
		chroma.GenericHeading: "bold " + c("blue"), chroma.GenericSubheading: "bold " + c("blue"),
		chroma.GenericStrong: "bold " + c(roles.emphasis), chroma.GenericEmph: "italic",
		chroma.GenericInserted: c("green"), chroma.GenericDeleted: c("red"),
		chroma.GenericError: c("red"), chroma.Error: c("red"), chroma.GenericTraceback: c("red"),
	})
}

func solarizedFzfColor(name string) string {
	if trueColor() {
		return solarizedColors[name].hex
	}
	return strconv.Itoa(solarizedColors[name].index)
}

func solarizedANSI(name string, background bool) string {
	if trueColor() {
		return ansi(solarizedColour(name, false), background)
	}
	kind := 38
	if background {
		kind = 48
	}
	return fmt.Sprintf("\x1b[%d;5;%dm", kind, solarizedColors[name].index)
}

func configureSolarized(t *Theme) {
	roles := solarizedMode(t.ID)
	t.BG, t.FG = solarizedANSI(roles.background, true), solarizedANSI(roles.foreground, false)
	t.FzfBackground, t.FzfForeground = solarizedFzfColor(roles.background), solarizedFzfColor(roles.foreground)
	t.FzfBorder, t.FzfLabel = solarizedFzfColor(roles.muted), solarizedFzfColor("cyan")
	t.Palette = platform.Palette{
		Reset: "\x1b[0m", Bold: "\x1b[1m", Value: t.FG,
		Dim: solarizedANSI(roles.muted, false), Tree: solarizedANSI(roles.muted, false), Label: solarizedANSI(roles.muted, false),
		Dir: solarizedANSI("blue", false), Prompt: solarizedANSI("cyan", false), Git: solarizedANSI("magenta", false),
		Warn: solarizedANSI("yellow", false), Err: solarizedANSI("red", false), OK: solarizedANSI("green", false),
	}
	if !trueColor() {
		builder := solarizedStyle(t.ID, true).Builder()
		builder.Add(chroma.Background, "noinherit "+solarizedColour(roles.foreground, true).String())
		var err error
		t.Syntax, err = builder.Build()
		if err != nil {
			panic(err)
		}
	}
}
