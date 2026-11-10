package theme

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

func TestPresentationAccentsUseSelectedSyntaxPalette(t *testing.T) {
	for _, id := range []string{"xcode-dark", "github-dark", "vscode-dark"} {
		selected, _ := Lookup(id)
		want := selected.Palette.Bold + ansi(selected.Style.Get(chroma.NameClass).Colour, false)
		if selected.Palette.Dir != want {
			t.Fatalf("%s directories do not use its class color", id)
		}
	}
	xcode, _ := Lookup("xcode-dark")
	github, _ := Lookup("github-dark")
	if xcode.Palette.Dir == github.Palette.Dir || xcode.Palette.Git == github.Palette.Git {
		t.Fatal("distinct themes still share fixed accents")
	}
}

func TestPresentationAccentsRemainReadableAfterTerminalQuantization(t *testing.T) {
	for _, choice := range Choices() {
		if choice.ID == TerminalID || isSolarized(choice.ID) {
			continue
		} // Terminal owns its colors; Solarized preserves its selective-contrast roles.
		selected, _ := Lookup(choice.ID)
		bg := surfaceTestColour(t, selected.BG)
		for role, sequence := range map[string]string{"directory": selected.Palette.Dir, "tree": selected.Palette.Tree, "warning": selected.Palette.Warn, "error": selected.Palette.Err, "git": selected.Palette.Git, "label": selected.Palette.Prompt, "success": selected.Palette.OK} {
			color := surfaceTestColour(t, sequence)
			if contrast(color, bg) < 2.8 {
				t.Errorf("%s %s lacks contrast after terminal conversion: %s on %s", choice.ID, role, color, bg)
			}
		}
	}
}
func surfaceTestColour(t *testing.T, sequence string) chroma.Colour {
	t.Helper()
	sequence = strings.TrimPrefix(sequence, "\x1b[1m")
	var kind, r, g, b, index int
	if strings.Contains(sequence, ";2;") {
		if _, err := fmt.Sscanf(sequence, "\x1b[%d;2;%d;%d;%dm", &kind, &r, &g, &b); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := fmt.Sscanf(sequence, "\x1b[%d;5;%dm", &kind, &index); err != nil {
			t.Fatal(err)
		}
		if index < 16 || index > 255 {
			t.Fatalf("terminal-remapped color %d", index)
		}
		if index >= 232 {
			r = 8 + (index-232)*10
			g = r
			b = r
		} else {
			n := index - 16
			levels := []int{0, 95, 135, 175, 215, 255}
			r = levels[n/36]
			g = levels[n/6%6]
			b = levels[n%6]
		}
	}
	return chroma.MustParseColour(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}
