package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/search"
)

// The checkpoint content-match reload command must embed the parent
// scope's positional targets: without them the child parses an implicit
// "." scope and direct-mode rg walks the whole working dir instead of
// the target (live failure 2026-07-04: cwd=Desktop, target=vscode-main,
// per-keystroke rg over the entire Desktop).
func TestFzfCheckpointContentMatchListCommandEmbedsScopeTargets(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vscode-main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vscode-main", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := scopeViewResolverFn
	defer func() { scopeViewResolverFn = prev }()
	SetScopeViewResolver(func(args []string) (ScopeView, bool) {
		return ScopeView{
			WorkingDir: dir,
			Entries:    []Entry{{RelPath: "vscode-main/a.go"}},
			Targets:    []string{"vscode-main"},
		}, true
	})

	command, checkpointPath, cleanup := fzfCheckpointContentMatchListCommand([]string{"vscode-main"}, "--contains")
	defer cleanup()
	if command == "" {
		t.Fatal("expected a checkpoint-backed command, got empty")
	}
	if checkpointPath == "" {
		t.Fatal("expected a checkpoint path")
	}
	if !strings.Contains(command, "--internal-prediscovered") {
		t.Fatalf("expected checkpoint form, got: %s", command)
	}
	// The target must sit between the checkpoint path and the flag so the
	// child parses it as a positional scope target.
	flagIdx := strings.Index(command, " --contains ")
	targetIdx := strings.Index(command, " vscode-main ")
	if targetIdx == -1 {
		t.Fatalf("expected scope target embedded in reload command, got: %s", command)
	}
	if flagIdx == -1 || targetIdx > flagIdx {
		t.Fatalf("expected target before %s flag, got: %s", "--contains", command)
	}
}

// FilterEntriesBySnippetContent is the snippet stage's one-pass producer:
// membership and pinned match-lines from a single rg call. Pins must be
// exactly the pattern's matched line numbers; non-matching entries drop.
func TestFilterEntriesBySnippetContentPinsMatchLines(t *testing.T) {
	if _, ok := search.RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hit.go"), []byte("a\nTODO x\nb\nTODO y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "miss.go"), []byte("nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{RelPath: "hit.go", AbsPath: filepath.Join(dir, "hit.go")},
		{RelPath: "miss.go", AbsPath: filepath.Join(dir, "miss.go")},
	}
	out, err := FilterEntriesBySnippetContent(entries, "TODO")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].RelPath != "hit.go" {
		t.Fatalf("expected only hit.go to survive, got %#v", out)
	}
	if got, want := out[0].SnippetMatchLines, []int{2, 4}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected pinned lines [2 4], got %v", got)
	}
}
