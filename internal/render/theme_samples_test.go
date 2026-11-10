package render

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/tigreau/catclip/internal/theme"
)

func TestThemeConfirmationUsesExplicitSavedChoice(t *testing.T) {
	previous := theme.Current()
	strip := regexp.MustCompile(`\x1b\[[0-9;]*[mK]`)
	for _, choice := range theme.Choices() {
		selected, _ := theme.Lookup(choice.ID)
		var plain, colored bytes.Buffer
		if err := WriteThemeConfirmation(&plain, selected, false); err != nil {
			t.Fatal(err)
		}
		language, want := themeConfirmationSource(selected)
		if strings.Count(want, strconv.Quote(selected.Name)) != 1 || strings.Count(want, "saved.") != 1 || strings.Count(want, "Applies to interactive mode and terminal --print.") != 1 || strings.Count(want, "\n") != 3 {
			t.Fatalf("%s confirmation lacks its declaration and two output statements: %q", choice.ID, want)
		}
		lexer := lexers.Get(language)
		if lexer == nil {
			t.Fatalf("%s confirmation language %s is unsupported", choice.ID, language)
		}
		tokens, err := lexer.Tokenise(nil, want)
		if err != nil {
			t.Fatal(err)
		}
		for token := tokens(); token != chroma.EOF; token = tokens() {
			if token.Type == chroma.Error {
				t.Fatalf("%s confirmation contains invalid %s syntax: %q", choice.ID, language, token.Value)
			}
		}
		if plain.String() != want {
			t.Fatalf("%s plain confirmation changed: %q", choice.ID, plain.String())
		}
		if err := WriteThemeConfirmation(&colored, selected, true); err != nil {
			t.Fatal(err)
		}
		if clean := strip.ReplaceAllString(colored.String(), ""); clean != want {
			t.Fatalf("%s colored confirmation changed text: %q", choice.ID, clean)
		}
		if choice.ID != theme.TerminalID && !strings.HasPrefix(colored.String(), selected.FG+selected.BG) {
			t.Fatalf("%s confirmation used a different theme's surface", choice.ID)
		}
		if !strings.HasSuffix(colored.String(), "\x1b[0m\n") {
			t.Fatalf("%s confirmation lacks its final reset", choice.ID)
		}
	}
	if theme.Current() != previous {
		t.Fatal("confirmation changed the invocation's cached theme")
	}
}

func TestThemeSamplesKeepTextAndPaintViewport(t *testing.T) {
	strip := regexp.MustCompile(`\x1b\[[0-9;]*[mK]`)
	for _, choice := range theme.Choices() {
		selected, _ := theme.Lookup(choice.ID)
		var plain, colored bytes.Buffer
		if err := WriteThemeSample(&plain, selected, 88, 180, false); err != nil {
			t.Fatal(err)
		}
		if err := WriteThemeSample(&colored, selected, 88, 180, true); err != nil {
			t.Fatal(err)
		}
		clean := strip.ReplaceAllString(colored.String(), "")
		trimLines := func(s string) string {
			lines := strings.Split(s, "\n")
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], " ")
			}
			return strings.TrimRight(strings.Join(lines, "\n"), "\n")
		}
		if trimLines(clean) != trimLines(plain.String()) {
			t.Fatalf("theme %s changed sample text", choice.ID)
		}
		if choice.ID == theme.TerminalID {
			continue // Inherits the terminal; there is no painted canvas.
		}
		if !strings.HasPrefix(colored.String(), selected.FG+selected.BG) || !strings.HasSuffix(colored.String(), "\x1b[0m\n") {
			t.Fatalf("missing surface/reset: %s", choice.ID)
		}
		if !strings.Contains(plain.String(), "Markdown\n") {
			t.Fatal("missing Markdown")
		}
	}
}
func BenchmarkThemePreview(b *testing.B) {
	for _, id := range []string{"xcode-dark", "github", "vscode-dark", "catppuccin-latte"} {
		b.Run(id, func(b *testing.B) {
			selected, _ := theme.Lookup(id)
			b.ReportAllocs()
			for b.Loop() {
				if err := WriteThemeSample(io.Discard, selected, 100, 40, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestNamedBodyPreviewKeepsItsSurfaceOnTerminalPane(t *testing.T) {
	selected, _ := theme.Lookup("xcode-dark")
	body := []byte("\x1b[35mfunc\x1b[0m main() {}\n")
	var out bytes.Buffer
	if err := WriteThemedPreview(&out, body, selected, 100, 40); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), selected.FG+selected.BG) || !strings.HasSuffix(out.String(), "\x1b[0m\n") {
		t.Fatal("code preview lost its own theme surface on the terminal-colored tree pane")
	}
	clean := regexp.MustCompile(`\x1b\[[0-9;]*[mK]`).ReplaceAllString(out.String(), "")
	if strings.TrimSpace(clean) != "func main() {}" {
		t.Fatal("preview canvas changed code text")
	}
}
func TestNonTerminalPreviewCanvasHasBoundedExpansion(t *testing.T) {
	selected, _ := theme.Lookup("xcode")
	var out bytes.Buffer
	if err := WriteThemedPreview(&out, []byte(strings.Repeat("\n", 128*1024)), selected, 1000, 40); err != nil {
		t.Fatal(err)
	}
	if out.Len() > previewCanvasLimit || !strings.Contains(out.String(), "[preview truncated]") {
		t.Fatalf("unbounded canvas expansion: %d", out.Len())
	}
}

func TestPreviewBackgroundDoesNotDependOnReportedWidth(t *testing.T) {
	selected, _ := theme.Lookup("xcode")
	body := []byte("short\n\n\x1b[35mcolored\x1b[0m\n")
	var narrow, wide bytes.Buffer
	if err := WriteThemedPreview(&narrow, body, selected, 20, 10); err != nil {
		t.Fatal(err)
	}
	if err := WriteThemedPreview(&wide, body, selected, 200, 10); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(narrow.Bytes(), wide.Bytes()) {
		t.Fatal("viewport width changed preview bytes")
	}
	if strings.Contains(wide.String(), strings.Repeat(" ", 20)) || strings.Count(wide.String(), "\x1b[0K") != 10 {
		t.Fatal("preview did not delegate full-line background fill to fzf")
	}
}

func TestTerminalPreviewDoesNotPaintCanvas(t *testing.T) {
	selected, _ := theme.Lookup(theme.TerminalID)
	body := []byte("\x1b[35mpackage\x1b[0m main\n")
	var out bytes.Buffer
	if err := WriteThemedPreview(&out, body, selected, 80, 40); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Fatal("terminal preview painted a surface or padded the body")
	}
}

func TestThemeTreeSampleUsesSelectedPalette(t *testing.T) {
	for _, id := range []string{theme.TerminalID, "xcode-dark", "github"} {
		selected, _ := theme.Lookup(id)
		var out bytes.Buffer
		if err := WriteThemeSample(&out, selected, 100, 40, true); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Tree preview\n", selected.Palette.Dir + "src/", selected.Palette.Warn + "[M]", selected.Palette.OK + "[S]", "Markdown\n"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%s sample missing %q", id, want)
			}
		}
	}
}
