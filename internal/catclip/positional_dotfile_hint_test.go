package catclip

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
)

func TestDotfileHintOnlyOnBareExtensionError(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(".env", []byte("synthetic=yes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(".git", 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{".env"}, {".git"}, {"."}, {"src"}, {"src/.env"}, {"*.env"}, {".env.local"},
		{"src", "--only", ".local"}, {"src", "--snippet", ".local"},
		{"src", "--then", ".env"},
	} {
		got, err := normalizePositionalGlobArgsWithLookup(args, func(string) []string { t.Fatalf("unexpected lookup for %q", args); return nil })
		if err != nil || !reflect.DeepEqual(got.Args, args) {
			t.Fatalf("args=%q got=%q err=%v", args, got.Args, err)
		}
	}
	// Interleaving uses a separate existing error; do not broaden this change.
	_, err := normalizePositionalGlobArgsWithLookup([]string{".local", "src"}, func(string) []string { t.Fatal("unexpected interleaving probe"); return nil })
	if err == nil || !strings.Contains(err.Error(), "can't be interleaved") {
		t.Fatalf("err=%v", err)
	}
}

func TestDotfileHintPreservesCommandAndCapsSuggestions(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args    []string
		name    string
		matches []string
	}{
		{[]string{".env"}, ".env", []string{"services/api/.env"}},
		{[]string{"src", ".local", "--lines", "1", "3"}, ".local", []string{"a b/.local"}},
		{[]string{"README.md", "--then", ".", ".secret", "--snippet", ".secret", "--then", "docs", "--paths"}, ".secret", []string{"config/.secret"}},
		{[]string{".key"}, ".key", []string{"a/.key", "b/.key", "c/.key", "d/.key", "e/.key", "f/.key", "g/.key", "h/.key", "i/.key", "j/.key"}},
	} {
		calls := 0
		_, err := normalizePositionalGlobArgsWithLookup(tc.args, func(name string) []string {
			calls++
			if name != tc.name {
				t.Fatalf("name=%q", name)
			}
			return tc.matches
		})
		if err == nil || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
		message := err.Error()
		if !strings.Contains(message, "bare extension, not a target") || !strings.Contains(message, "Found") {
			t.Fatal(message)
		}
		for _, match := range tc.matches[:min(5, len(tc.matches))] {
			want := append([]string(nil), tc.args...)
			for i, arg := range want {
				if arg == tc.name {
					want[i] = match
					break
				}
			}
			if !strings.Contains(message, cli.FormatResolvedStartupCommand(want)) {
				t.Fatalf("missing command %q in %s", want, message)
			}
		}
		if len(tc.matches) > 5 {
			if strings.Contains(message, tc.matches[5]) || !strings.Contains(message, "and 5 more matching ignored files") {
				t.Fatal(message)
			}
		}
	}
	_, err := normalizePositionalGlobArgsWithLookup([]string{".go"}, func(string) []string { return nil })
	want := fmt.Sprintf("Error: '.go' is a bare extension, not a target.\n  Use it as a glob target:\n    %s", cli.FormatResolvedStartupCommand([]string{"*.go"}))
	if err == nil || err.Error() != want {
		t.Fatalf("err=%v want=%s", err, want)
	}
}

func TestDotfileHintIntegratedIgnoredNestedFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join("services", "api"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("services", "api", ".env"), []byte("synthetic=yes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := normalizePositionalGlobArgs([]string{".env"})
	if err == nil || !strings.Contains(err.Error(), "catclip services/api/.env") {
		t.Fatalf("missing ignored hint: %v", err)
	}
}
