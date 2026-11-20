package discovery

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

// A --snippet that matches no line names the pattern and the regex rules,
// instead of suggesting the folder is empty or ignored.
func TestNoFilesMatchedMessageNamesTheSnippetPattern(t *testing.T) {
	var out bytes.Buffer
	scopes := []command.ExecutionScope{{Snippet: true, SnippetPattern: `Main(`}}
	if err := WriteNoFilesMatchedMessage(scopes, &out, platform.Palette{}, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"No lines matched --snippet 'Main('.", "regular expression", `'Main\('`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "No text files found") || strings.Contains(got, "Directory is empty") || strings.Contains(got, "--all-ignore-rules") {
		t.Errorf("generic causes and ignore-rule tips should not appear:\n%s", got)
	}

	out.Reset()
	if err := WriteNoFilesMatchedMessage([]command.ExecutionScope{{Contains: "zzz"}}, &out, platform.Palette{}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No text files found matching your criteria.") {
		t.Errorf("other filters keep the generic message:\n%s", out.String())
	}
}

// A guessed path that does not exist (a chat agent asking for code) lists
// what its folder has instead of suggesting --no-ignore.
func TestTargetNotFoundListsTheFolderOfAMissingPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join("internal", "store"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("internal", "store", "store.go"), []byte("package store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := targetNotFoundWarning("internal/store/user.go", 0, platform.Palette{})
	if !strings.Contains(got, "internal/store/ has: store.go") || !strings.Contains(got, "catclip internal/store") || strings.Contains(got, "--no-ignore") {
		t.Fatalf("got:\n%s", got)
	}
	got = targetNotFoundWarning("elsewhere/user.go", 0, platform.Palette{})
	if !strings.Contains(got, "catclip user.go") {
		t.Fatalf("got:\n%s", got)
	}
}
