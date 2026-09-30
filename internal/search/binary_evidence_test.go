package search

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassificationBinaryEvidence(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	root := t.TempDir()
	for name, body := range map[string]string{
		"text.unknown": "hello", "nul.unknown": "a\x00b", "image.png": "\x00png",
		"empty.png": "", "empty.unknown": "", "other.unknown": "hello",
		"utf16.unknown": "\xff\xfea\x00", "utf16nul.unknown": "\xff\xfe\x00\x00",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		paths  []string
		binary bool
		texts  int
	}{
		{"known binary", []string{"image.png"}, true, 0},
		{"tiny NUL", []string{"nul.unknown"}, true, 0},
		{"rg NUL", []string{"nul.unknown", "text.unknown", "other.unknown"}, true, 2},
		{"UTF16 text", []string{"utf16.unknown"}, false, 1},
		{"UTF16 NUL", []string{"utf16nul.unknown"}, true, 0},
		{"empty binary name", []string{"empty.png"}, false, 1},
		{"empty residue", []string{"empty.unknown"}, false, 1},
		{"empty rg", []string{"empty.unknown", "text.unknown", "other.unknown"}, false, 3},
		{"missing residue", []string{"missing.unknown"}, false, 0},
		{"missing binary name", []string{"missing.png"}, false, 0},
		{"rg read errors", []string{"missing.unknown", "text.unknown", "other.unknown"}, false, 2},
		{"independent binary evidence despite unreadable neighbor", []string{"missing.unknown", "nul.unknown", "text.unknown"}, true, 1},
		{"no candidates", nil, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set, binary, err := ClassifyTextPathsWithBinaryEvidence(root, tc.paths)
			if err != nil || (len(binary) > 0) != tc.binary || len(set) != tc.texts {
				t.Fatalf("set=%v binary=%v err=%v; want %d text paths, binary=%v", set, binary, err, tc.texts, tc.binary)
			}
		})
	}
}

func TestClassifierIgnoresRipgrepConfigGlobs(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "rg-config")
	if err := os.WriteFile(config, []byte("--glob\n*.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIPGREP_CONFIG_PATH", config)
	if err := os.WriteFile(filepath.Join(root, "file.unknown"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	text, binary, err := ClassifyTextPathsWithBinaryEvidence(root, []string{"file.unknown"})
	if err != nil || len(binary) > 0 || len(text) != 1 {
		t.Fatalf("rg glob affected classifier: text=%v binary=%v err=%v", text, binary, err)
	}
}

func TestUnreadableResidueIsNotPositiveBinaryEvidence(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	root := t.TempDir()
	file := filepath.Join(root, "unreadable.unknown")
	if err := os.WriteFile(file, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0000); err != nil {
		t.Skipf("cannot restrict fixture permissions: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0600) })
	if f, err := os.Open(file); err == nil {
		f.Close()
		t.Skip("host can still read mode-000 fixture")
	}
	set, binary, err := ClassifyTextPathsWithBinaryEvidence(root, []string{"unreadable.unknown"})
	if err != nil || len(set) != 0 || len(binary) != 0 {
		t.Fatalf("unreadable file inferred binary: set=%v binary=%v err=%v", set, binary, err)
	}
}
