package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/search"
)

func TestContentReloadLargeScopeAndSetupFailure(t *testing.T) {
	dir := t.TempDir()
	previous := scopeViewResolverFn
	defer func() { scopeViewResolverFn = previous }()
	var targets []string
	for i := 0; i < 10000; i++ {
		targets = append(targets, fmt.Sprintf("selected/file-%05d.go", i))
	}
	SetScopeViewResolver(func([]string) (ScopeView, bool) {
		return ScopeView{WorkingDir: dir, Targets: targets, NoIgnore: true,
			Entries: []Entry{{RelPath: targets[0], SizeKnown: true, SizeBytes: 1}}}, true
	})
	for _, flag := range []string{"--contains", "--not-contains", "--snippet"} {
		cmd, path, cleanup, err := fzfCheckpointContentMatchListCommand(targets, flag)
		if err != nil {
			t.Fatal(err)
		}
		data, err := ReadCheckpoint(path)
		if err != nil || data.Scope == nil || !reflect.DeepEqual(data.Scope.Targets, targets) || !data.NoIgnore {
			t.Fatalf("scope lost in checkpoint: %v", err)
		}
		if len(cmd) > 4096 || strings.Contains(cmd, "selected/file-") || !strings.HasSuffix(cmd, flag+" {q}") {
			t.Fatalf("unbounded or quoted query: %s", cmd)
		}
		cleanup()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("checkpoint not cleaned: %v", err)
		}
	}
	badTemp := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(badTemp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, badTemp)
	}
	cmd, path, cleanup, err := fzfCheckpointContentMatchListCommand(targets, "--contains")
	defer cleanup()
	if err == nil || cmd != "" || path != "" {
		t.Fatalf("failed checkpoint restored a fallback: %s, %s, %v", cmd, path, err)
	}
}

// The content-match checkpoint must retain the parent's positional targets:
// without them the child parses an implicit
// "." scope and direct-mode rg walks the whole working dir instead of
// the target (live failure 2026-07-04: cwd=Desktop, target=vscode-main,
// per-keystroke rg over the entire Desktop).
func TestFzfCheckpointContentMatchListCommandRetainsScopeTargets(t *testing.T) {
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

	command, checkpointPath, cleanup, err := fzfCheckpointContentMatchListCommand([]string{"vscode-main"}, "--contains")
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if command == "" {
		t.Fatal("expected a checkpoint-backed command, got empty")
	}
	if checkpointPath == "" {
		t.Fatal("expected a checkpoint path")
	}
	if !strings.Contains(command, "--internal-prediscovered") {
		t.Fatalf("expected checkpoint form, got: %s", command)
	}
	if !strings.HasSuffix(command, " --internal-checkpoint-scope --internal-query-env --contains {q}") || strings.Contains(command, " vscode-main ") {
		t.Fatalf("expected raw live query and bounded scope transport: %s", command)
	}
	data, err := ReadCheckpoint(checkpointPath)
	if err != nil || data.Scope == nil || len(data.Scope.Targets) != 1 || data.Scope.Targets[0] != "vscode-main" {
		t.Fatalf("checkpoint lost scope targets: %+v, %v", data.Scope, err)
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
