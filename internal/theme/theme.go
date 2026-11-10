// Package theme owns presentation preferences and compiled-in color palettes.
// It never enumerates targets or opens source files.
package theme

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/tigreau/catclip/internal/platform"
)

const DefaultID = TerminalID

type Choice struct{ ID, Name string }

var choices = []Choice{
	{TerminalID, "Terminal"},
	{"xcode-dark", "Xcode Dark"}, {"xcode", "Xcode Light"},
	{"github-dark", "GitHub Dark"}, {"github", "GitHub Light"},
	{"vscode-dark", "VS Code Dark+"}, {"vscode-light", "VS Code Light+"},
	{"darcula", "JetBrains Darcula"}, {"jetbrains-light", "JetBrains Light"},
	{"solarized-dark", "Solarized Dark"}, {"solarized-light", "Solarized Light"},
	{"catppuccin-latte", "Catppuccin Latte"}, {"catppuccin-frappe", "Catppuccin Frappe"},
	{"catppuccin-macchiato", "Catppuccin Macchiato"}, {"catppuccin-mocha", "Catppuccin Mocha"},
	{"onedark", "Atom One Dark"}, {"atom-one-light", "Atom One Light"},
	{"monokai", "Sublime Monokai"}, {"monokailight", "Sublime Monokai Light"},
}

func Choices() []Choice { return append([]Choice(nil), choices...) }

// Theme is immutable after creation. Cache styles and terminal sequences once,
// rather than rebuilding a Chroma style or reading config for each source unit.
type Theme struct {
	Choice
	Style, Syntax                *chroma.Style
	Background, Foreground       string
	FzfBackground, FzfForeground string
	FzfBorder, FzfLabel          string
	BG, FG                       string
	Palette                      platform.Palette
	interactiveColors            []string
}

var cached sync.Map

// Resolve preferences only when presentation needs them. Calling this during
// startup would add settings I/O and palette loading to plain/headless commands.
var current = sync.OnceValue(func() *Theme {
	id := DefaultID
	if path, err := ConfigPath(); err == nil {
		id = readPreference(path)
	}
	t, _ := Lookup(id)
	return t
})

func Current() *Theme { return current() }

func Lookup(id string) (*Theme, bool) {
	var choice Choice
	for _, c := range choices {
		if c.ID == id {
			choice = c
			break
		}
	}
	if choice.ID == "" {
		return nil, false
	}
	if t, ok := cached.Load(id); ok {
		return t.(*Theme), true
	}
	style := loadStyle(id)
	if style == nil {
		return nil, false
	}
	if id == TerminalID {
		t := &Theme{
			Choice: choice, Style: style, Syntax: style,
			Background: "default", Foreground: "default",
			BG: "\x1b[49m", FG: "\x1b[39m",
			FzfBackground: "-1", FzfForeground: "-1", FzfBorder: "8", FzfLabel: "6",
			Palette: platform.ANSIPalette(),
		}
		t.Palette.Value = t.FG
		t.buildInteractiveColors()
		actual, _ := cached.LoadOrStore(id, t)
		return actual.(*Theme), true
	}
	bg := style.Get(chroma.Background)
	background := bg.Background
	if !background.IsSet() {
		background = chroma.MustParseColour("#1f1f24")
	}
	light := int(background.Red())*299+int(background.Green())*587+int(background.Blue())*114 > 128000
	fg := bg.Colour
	if !fg.IsSet() {
		fg = style.Get(chroma.Text).Colour
	}
	if !fg.IsSet() {
		fg = style.Get(chroma.Name).Colour
	}
	if !fg.IsSet() {
		fg = style.Get(chroma.Other).Colour
	}
	if !fg.IsSet() {
		if light {
			fg = chroma.MustParseColour("#000000")
		} else {
			fg = chroma.MustParseColour("#ffffff")
		}
	}
	builder := style.Builder()
	bg.Background, bg.Colour, bg.NoInherit = 0, fg, true
	builder.AddEntry(chroma.Background, bg)
	syntax, err := builder.Build()
	if err != nil {
		panic(err)
	} // Compiled-in palettes are validated by catalog tests.
	t := &Theme{Choice: choice, Style: style, Syntax: syntax, Background: background.String(), Foreground: fg.String(), BG: ansi(background, true), FG: ansi(fg, false)}

	t.FzfBackground, t.FzfForeground = t.Background, t.Foreground
	if !trueColor() {
		t.FzfBackground = strings.TrimSuffix(strings.TrimPrefix(t.BG, "\x1b[48;5;"), "m")
		t.FzfForeground = strings.TrimSuffix(strings.TrimPrefix(t.FG, "\x1b[38;5;"), "m")
	}

	t.Palette, t.FzfBorder, t.FzfLabel = presentationPalette(style, background, fg)

	if isSolarized(id) {
		configureSolarized(t)
	}

	// Directory emphasis belongs to Catclip's UI, independently of the syntax
	// theme. Terminal's ANSI palette already includes bold directories.
	t.Palette.Dir = t.Palette.Bold + t.Palette.Dir
	t.buildInteractiveColors()
	actual, _ := cached.LoadOrStore(id, t)
	return actual.(*Theme), true
}

// Truecolor follows the advertised terminal capability without a terminal query.
// Other terminals get fixed xterm colors 16..255, never user-remapped ANSI 0..15.
var trueColor = sync.OnceValue(func() bool {
	c := strings.ToLower(os.Getenv("COLORTERM"))
	return c == "truecolor" || c == "24bit" || strings.Contains(os.Getenv("TERM"), "direct")
})

func Formatter() chroma.Formatter { return Current().Formatter() }

func (t *Theme) Formatter() chroma.Formatter {
	if t.ID == TerminalID {
		return &preparedTTY16
	}
	if trueColor() {
		return formatters.TTY16m
	}
	return &preparedTTY256
}
func ansi(c chroma.Colour, background bool) string {
	kind := 38
	if background {
		kind = 48
	}
	r, g, b := int(c.Red()), int(c.Green()), int(c.Blue())
	if trueColor() {
		return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", kind, r, g, b)
	}
	index, _ := indexedColour(c)
	return fmt.Sprintf("\x1b[%d;5;%dm", kind, index)
}

func indexedColour(c chroma.Colour) (int, chroma.Colour) {
	r, g, b := int(c.Red()), int(c.Green()), int(c.Blue())
	var closest chroma.Colour
	best, distance := 16, int(^uint(0)>>1)
	levels := []int{0, 95, 135, 175, 215, 255}
	for i := 16; i < 256; i++ {
		var cr, cg, cb int
		if i >= 232 {
			cr = 8 + (i-232)*10
			cg = cr
			cb = cr
		} else {
			n := i - 16
			cr = levels[n/36]
			cg = levels[n/6%6]
			cb = levels[n%6]
		}
		d := (r-cr)*(r-cr) + (g-cg)*(g-cg) + (b-cb)*(b-cb)
		if d < distance {
			best, distance = i, d
			closest = chroma.NewColour(uint8(cr), uint8(cg), uint8(cb))
		}
	}
	return best, closest
}

func PreviewPalette() platform.Palette {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return platform.Palette{}
	}
	return Current().Palette
}

// PreviewColorSpecs configures the entire fzf pane once, including its frame
// and scrollbar background. Ordinary pickers keep this palette for the session.
func (t *Theme) PreviewColorSpecs() []string {
	return []string{"preview-bg:" + t.FzfBackground, "preview-fg:" + t.FzfForeground,
		"preview-border:" + t.FzfBorder, "preview-label:" + t.FzfLabel, "preview-scrollbar:" + t.FzfBorder}
}
