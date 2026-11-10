package theme

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

func TestTerminalThemeInheritsDefaultsAndKeepsSyntaxRoles(t *testing.T) {
	selected, ok := Lookup(TerminalID)
	if !ok || DefaultID != TerminalID || Choices()[0].ID != TerminalID {
		t.Fatal("Terminal is not the default first choice")
	}
	if selected.FG != "\x1b[39m" || selected.BG != "\x1b[49m" || selected.FzfForeground != "-1" || selected.FzfBackground != "-1" {
		t.Fatal("Terminal stopped inheriting terminal defaults")
	}
	for _, capability := range []string{"", "truecolor"} {
		t.Setenv("COLORTERM", capability)
		var out bytes.Buffer
		err := selected.Formatter().Format(&out, selected.Syntax, chroma.Literator(
			chroma.Token{Type: chroma.Keyword, Value: "keyword"},
			chroma.Token{Type: chroma.NameFunction, Value: "function"},
			chroma.Token{Type: chroma.LiteralString, Value: "string"},
			chroma.Token{Type: chroma.Name, Value: "identifier"},
			chroma.Token{Type: chroma.Comment, Value: "comment"},
		))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"\x1b[35mkeyword", "\x1b[34mfunction", "\x1b[32mstring", "identifier", "comment"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("missing role %q in %q", want, out.String())
			}
		}
		for _, forced := range []string{"\x1b[37m", "\x1b[40m", "\x1b[38;", "\x1b[48;"} {
			if strings.Contains(out.String(), forced) {
				t.Fatalf("Terminal forced a surface color: %q", out.String())
			}
		}
	}
}
