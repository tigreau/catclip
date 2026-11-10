package render

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/theme"
)

var helpSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestSearchHelpUsesConsistentThemeRoles(t *testing.T) {
	const text = "Search help\n\nDefinitions:\n" +
		"    func\\s+Handler     → Go functions\n" +
		"    def\\s+handler      → Python functions\n" +
		"    class\\s+Handler    → class definitions\n" +
		"    type\\s+Handler     → type definitions\n" +
		"    [.*]|.{120,}        → examples (ab* matches a, ab, abb)\n" +
		"    \\.                 → literal dot (plain . matches any char)"
	for _, choice := range theme.Choices() {
		t.Run(choice.ID, func(t *testing.T) {
			selected, _ := theme.Lookup(choice.ID)
			colors := Palette(selected.Palette)
			got := highlightSearchHelp(text, colors)
			if helpSGR.ReplaceAllString(got, "") != text {
				t.Fatal("help styling changed text or whitespace")
			}
			for _, keyword := range []string{"func", "def", "class", "type"} {
				index := strings.Index(text, "    "+keyword) + 4
				for i := range len(keyword) {
					if styleAtHelpByte(got, index+i) != colors.Prompt {
						t.Fatalf("%s did not use the common example color", keyword)
					}
				}
			}
			for _, prefix := range []string{"func", "def", "class", "type"} {
				index := strings.Index(text, prefix+"\\s+") + len(prefix)
				for i := range 3 {
					if styleAtHelpByte(got, index+i) != colors.Git {
						t.Fatal("regex escapes or quantifiers changed role across languages")
					}
				}
			}
			for _, pattern := range []string{"[", "]", "|", ".{", "* matches", ". matches"} {
				index := strings.Index(text, pattern)
				if styleAtHelpByte(got, index) != colors.Git {
					t.Fatalf("operator %q has an inconsistent color", pattern)
				}
			}
			if styleAtHelpByte(got, strings.Index(text, "[.*]")+1) != colors.Prompt {
				t.Fatal("literal dot inside a character class styled as a wildcard")
			}
			if styleAtHelpByte(got, strings.Index(text, "120")) != colors.Warn {
				t.Fatal("quantifier count lost its numeric role")
			}
			if styleAtHelpByte(got, strings.Index(text, "class definitions")) != colors.Value {
				t.Fatal("explanation was lexed as source")
			}
			for _, sgr := range helpSGR.FindAllString(got, -1) {
				if strings.Contains(sgr, "48;") || sgr == "\x1b[4m" || sgr == "\x1b[7m" {
					t.Fatalf("help gained an error background/underline: %q", sgr)
				}
			}
		})
	}
	if got := highlightSearchHelp(text, Palette{}); got != text {
		t.Fatal("disabled help styling changed content")
	}
}

func TestSearchHelpRenderingSurvivesPayloadTransport(t *testing.T) {
	doc := Document{Mode: DocumentModeFile, File: &FilePreview{
		HighlightPath: SearchHelpHighlight, Content: "Search help\n    def\\s+name → definition\n",
	}}
	var payload bytes.Buffer
	if err := EncodePayload(&payload, doc); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePayload(&payload)
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := theme.Lookup("xcode-dark")
	var got bytes.Buffer
	if err := RenderDocument(&got, decoded, RenderOptions{}, Palette(selected.Palette)); err != nil {
		t.Fatal(err)
	}
	if styleAtHelpByte(highlightSearchHelp(doc.File.Content, Palette(selected.Palette)), len("Search help\n    ")) != selected.Palette.Prompt {
		t.Fatal("help styling was lost")
	}
	if !strings.Contains(got.String(), selected.Palette.Prompt+"d") || !strings.Contains(got.String(), selected.Palette.Git+"\\s") {
		t.Fatal("transported help took the source-lexer path")
	}
}

func styleAtHelpByte(styled string, offset int) string {
	style, plain := "", 0
	for i := 0; i < len(styled); {
		if styled[i] == '\x1b' {
			end := strings.IndexByte(styled[i:], 'm') + i + 1
			if end <= i {
				return "invalid SGR"
			}
			sequence := styled[i:end]
			if sequence == "\x1b[0m" {
				style = ""
			} else {
				style += sequence
			}
			i = end
			continue
		}
		if plain == offset {
			return style
		}
		plain++
		i++
	}
	return "missing byte"
}
