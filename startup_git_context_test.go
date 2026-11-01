package catclip

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/ui"
)

func TestRunReusesKnownDisabledStartupGitContext(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := parseInProject(t, root, []string{"a.go", "--paths", "--headless"})
	trace := filepath.Join(t.TempDir(), "git.jsonl")
	t.Setenv("GIT_TRACE2_EVENT", filepath.ToSlash(trace))
	var stdout, stderr bytes.Buffer
	known := &ui.StartupGitContext{WorkingDir: cfg.WorkingDir}
	if err := runFromParsedCommand(cfg, &stdout, &stderr, known); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "a.go\n" {
		t.Fatal(stdout.String())
	}
	if data, err := os.ReadFile(trace); err == nil && len(data) > 0 {
		t.Fatalf("redetected Git despite retained non-repository observation: %s", data)
	}
	// A context from another cwd must not suppress actual detection.
	known.WorkingDir = filepath.Join(root, "elsewhere")
	stdout.Reset()
	stderr.Reset()
	if err := runFromParsedCommand(cfg, &stdout, &stderr, known); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(trace); err != nil || len(data) == 0 {
		t.Fatalf("did not detect Git after cwd mismatch: %v", err)
	}
}
