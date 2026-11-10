package theme

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

func TestCatalogPalettes(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Choices() {
		if seen[c.ID] {
			t.Fatalf("duplicate theme %s", c.ID)
		}
		seen[c.ID] = true
		selected, ok := Lookup(c.ID)
		if !ok || selected == nil {
			t.Fatalf("missing palette %s", c.ID)
		}
		if selected.Style.Name != c.ID {
			t.Fatalf("wrong palette %s: %s", c.ID, selected.Style.Name)
		}
		if (c.ID != TerminalID && !selected.Style.Get(chroma.Background).Background.IsSet()) || selected.Syntax.Get(chroma.Background).Background.IsSet() {
			t.Fatalf("invalid surface styles %s", c.ID)
		}
		if (c.ID != TerminalID && selected.Background == selected.Foreground) || selected.BG == "" || selected.FG == "" {
			t.Fatalf("unreadable surface %s", c.ID)
		}
		again, _ := Lookup(c.ID)
		if again != selected {
			t.Fatal("style was rebuilt")
		}
	}
	if len(seen) != 19 {
		t.Fatalf("catalog size %d", len(seen))
	}
	if _, ok := Lookup("not-a-theme"); ok {
		t.Fatal("unknown theme accepted")
	}
}
func TestPreferencePersistenceAndFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catclip", "theme.json")
	if readPreference(path) != DefaultID {
		t.Fatal("missing preference did not use default")
	}
	for _, id := range []string{TerminalID, "xcode", "github-dark", "catppuccin-latte"} {
		if err := savePreference(path, id); err != nil {
			t.Fatal(err)
		}
		if readPreference(path) != id {
			t.Fatal("preference did not round trip")
		}
	}
	for _, bad := range []string{`{`, `{"theme":"missing"}`, `{"theme":"terminal-dark"}`, `{"theme":"github"}junk`, `{"theme":"github"}` + strings.Repeat(" ", 4096), strings.Repeat("x", 8000)} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if readPreference(path) != DefaultID {
			t.Fatal("bad preference did not use default")
		}
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("temporary preference files leaked")
	}
}
func TestCurrentLoadsOnce(t *testing.T) {
	if os.Getenv("CATCLIP_THEME_TEST_CHILD") == "1" {
		if Current().ID != DefaultID {
			t.Fatal("incorrect default")
		}
		if err := Save("github"); err != nil {
			t.Fatal(err)
		}
		if Current().ID != DefaultID {
			t.Fatal("preference reread within invocation")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCurrentLoadsOnce$")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir(), "CATCLIP_THEME_TEST_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
func BenchmarkCurrent(b *testing.B) {
	_ = Current()
	b.ReportAllocs()
	for b.Loop() {
		_ = Current()
	}
}

func TestFzfColorsMatchTerminalCapability(t *testing.T) {
	if mode := os.Getenv("CATCLIP_THEME_COLOR_TEST"); mode != "" {
		selected, _ := Lookup("xcode-dark")
		if mode == "rgb" {
			if selected.FzfBackground != "#1f1f24" || selected.FzfForeground != "#ffffff" {
				t.Fatal("missing RGB palette")
			}
		} else {
			if strings.HasPrefix(selected.FzfBackground, "#") || strings.HasPrefix(selected.FzfForeground, "#") {
				t.Fatal("RGB forced on a 256-color terminal")
			}
			if selected.BG != "\x1b[48;5;"+selected.FzfBackground+"m" || selected.FG != "\x1b[38;5;"+selected.FzfForeground+"m" {
				t.Fatal("fzf surface differs from print")
			}
		}
		return
	}
	for _, mode := range []string{"rgb", "256"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFzfColorsMatchTerminalCapability$")
		colorterm := ""
		if mode == "rgb" {
			colorterm = "truecolor"
		}
		cmd.Env = append(os.Environ(), "CATCLIP_THEME_COLOR_TEST="+mode, "COLORTERM="+colorterm, "TERM=xterm-256color")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", mode, err, out)
		}
	}
}

func TestPreviewFrameSharesSavedThemeSurface(t *testing.T) {
	for _, id := range []string{TerminalID, "xcode", "xcode-dark", "github-dark"} {
		selected, _ := Lookup(id)
		joined := strings.Join(selected.PreviewColorSpecs(), ",")
		for _, want := range []string{"preview-bg:" + selected.FzfBackground, "preview-fg:" + selected.FzfForeground, "preview-scrollbar:" + selected.FzfBorder} {
			if !strings.Contains(joined, want) {
				t.Fatalf("%s missing %s", id, want)
			}
		}
	}
}

func TestInteractiveThemeCoversEveryPane(t *testing.T) {
	for _, choice := range Choices() {
		selected, _ := Lookup(choice.ID)
		specs := selected.InteractiveColorSpecs(true)
		for _, want := range []string{"bg:" + selected.FzfBackground, "list-bg:" + selected.FzfBackground, "header-bg:" + selected.FzfBackground, "input-bg:" + selected.FzfBackground, "footer-bg:" + selected.FzfBackground, "preview-bg:" + selected.FzfBackground, "fg:" + selected.FzfForeground} {
			found := false
			for _, spec := range specs {
				if spec == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s missing %s", choice.ID, want)
			}
		}
		specs[0] = "mutated"
		if selected.InteractiveColorSpecs(false)[0] == "mutated" {
			t.Fatal("caller modified cached theme")
		}
	}
}
