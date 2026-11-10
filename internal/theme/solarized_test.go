package theme

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

func TestSolarizedCanonicalPaletteAndRoles(t *testing.T) {
	for _, tc := range []struct{ id, bg, fg string }{
		{"solarized-dark", "#002b36", "#839496"},
		{"solarized-light", "#fdf6e3", "#657b83"},
	} {
		selected, _ := Lookup(tc.id)
		root := selected.Style.Get(chroma.Background)
		if root.Background.String() != tc.bg || root.Colour.String() != tc.fg {
			t.Fatalf("%s wrong Solarized surface: %v", tc.id, root)
		}
		for kind, want := range map[chroma.TokenType]string{
			chroma.Keyword: "#859900", chroma.KeywordType: "#b58900", chroma.NameFunction: "#268bd2",
			chroma.LiteralString: "#2aa198", chroma.LiteralNumber: "#2aa198", chroma.CommentPreproc: "#cb4b16",
			chroma.GenericDeleted: "#dc322f", chroma.GenericInserted: "#859900",
		} {
			if got := selected.Style.Get(kind).Colour.String(); got != want {
				t.Fatalf("%s %s=%s want %s", tc.id, kind, got, want)
			}
		}
	}
	for _, choice := range Choices() {
		if choice.ID == "terminal-dark" {
			t.Fatal("removed Terminal Dark still listed")
		}
	}
}

func TestSolarizedUsesOfficialTerminalMappings(t *testing.T) {
	if mode := os.Getenv("CATCLIP_TEST_SOLARIZED_MODE"); mode != "" {
		dark, _ := Lookup("solarized-dark")
		light, _ := Lookup("solarized-light")
		var out bytes.Buffer
		if err := dark.Formatter().Format(&out, dark.Syntax, chroma.Literator(chroma.Token{Type: chroma.NameFunction, Value: "function"})); err != nil {
			t.Fatal(err)
		}
		if mode == "256" {
			if dark.FzfBackground != "234" || dark.FzfForeground != "244" || light.FzfBackground != "230" || light.FzfForeground != "241" {
				t.Fatal("wrong official fallback indices")
			}
			if !strings.Contains(out.String(), "\x1b[38;5;33mfunction") {
				t.Fatalf("blue did not use official index 33: %q", out.String())
			}
		} else {
			if dark.FzfBackground != "#002b36" || light.FzfBackground != "#fdf6e3" {
				t.Fatal("canonical RGB background changed")
			}
			if !strings.Contains(out.String(), "\x1b[38;2;38;139;210mfunction") {
				t.Fatalf("wrong canonical blue: %q", out.String())
			}
		}
		return
	}
	for _, mode := range []string{"256", "rgb"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSolarizedUsesOfficialTerminalMappings$")
		capability := ""
		if mode == "rgb" {
			capability = "truecolor"
		}
		cmd.Env = append(os.Environ(), "CATCLIP_TEST_SOLARIZED_MODE="+mode, "COLORTERM="+capability, "TERM=xterm-256color")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", mode, err, out)
		}
	}
}
