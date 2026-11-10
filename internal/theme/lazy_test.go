package theme

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPalettesLoadOnlyWhenSelected(t *testing.T) {
	if os.Getenv("CATCLIP_TEST_LAZY_THEME") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPalettesLoadOnlyWhenSelected$")
		cmd.Env = append(os.Environ(), "CATCLIP_TEST_LAZY_THEME=1", "XDG_CONFIG_HOME="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return
	}
	count := func() int {
		n := 0
		cached.Range(func(_, _ any) bool { n++; return true })
		return n
	}
	if count() != 0 {
		t.Fatal("palettes initialized before presentation")
	}
	_ = Choices()
	if count() != 0 {
		t.Fatal("listing choices loaded their palettes")
	}
	if Current().ID != TerminalID || count() != 1 {
		t.Fatal("default presentation loaded other palettes")
	}
	selected, ok := Lookup("xcode-dark")
	if !ok || count() != 2 {
		t.Fatal("selecting Xcode loaded unrelated palettes")
	}
	if again, _ := Lookup("xcode-dark"); again != selected || count() != 2 {
		t.Fatal("selected palette was rebuilt")
	}
	_ = selected.Formatter()
	_ = Current().Formatter()
	for _, formatter := range []*preparedIndexedFormatter{&preparedTTY16, &preparedTTY256} {
		formatter.styles.Range(func(_, _ any) bool {
			t.Error("syntax mappings prepared before any highlighting")
			return false
		})
	}
}

func BenchmarkThemeFirstLookup(b *testing.B) {
	for _, id := range []string{TerminalID, "xcode-dark", "darcula", "solarized-light"} {
		b.Run(id, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				cached.Delete(id)
				if _, ok := Lookup(id); !ok {
					b.Fatal("missing theme")
				}
			}
		})
	}
}

func BenchmarkPreferenceRead(b *testing.B) {
	for _, saved := range []bool{false, true} {
		name := "absent"
		if saved {
			name = "saved"
		}
		b.Run(name, func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "theme.json")
			if saved {
				if err := savePreference(path, "xcode-dark"); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				_ = readPreference(path)
			}
		})
	}
}
