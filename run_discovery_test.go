package catclip

import (
	"bytes"
	"testing"
)

func TestDiscoverInvocationAggregatesScopes(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":  "package src\n",
		"docs/a.md": "hello\n",
	})
	cfg := parseInProject(t, project, []string{"--print", "src", "--then", "docs"})

	result, err := discoverInvocation(
		resolvedInvocationFromParsedCommand(cfg),
		detectGitContext(cfg.WorkingDir),
		&bytes.Buffer{},
		colorPalette{},
	)
	if err != nil {
		t.Fatalf("discoverInvocation returned error: %v", err)
	}
	if got, want := len(result.Invocation.Scopes), 2; got != want {
		t.Fatalf("scope count mismatch: got %d want %d", got, want)
	}
	if got, want := len(result.ScopeStats), 2; got != want {
		t.Fatalf("scope stats count mismatch: got %d want %d", got, want)
	}
	if got, want := result.ScopeStats[0].Count, 1; got != want {
		t.Fatalf("first scope count mismatch: got %d want %d", got, want)
	}
	if got, want := result.ScopeStats[1].Count, 1; got != want {
		t.Fatalf("second scope count mismatch: got %d want %d", got, want)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", result.Diagnostics)
	}
	if result.HadSelectionCancel {
		t.Fatal("expected no selection cancel")
	}
}
