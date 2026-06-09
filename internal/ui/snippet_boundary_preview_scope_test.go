package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBuildSnippetBoundaryPreviewForScopeStreamsSnippetOutput and
// TestBuildSnippetBoundaryPreviewForScopePreservesRecentOrder used to
// live at the root in main_test.go. They are white-box tests on the
// snippet-boundary preview pipeline — no run()/Main() calls — so they
// moved into internal/ui in commit 3D to release
// resolvedCurrentScopeViewForArgs / snippetBoundaryPreviewMatchedEntries
// / buildSnippetBoundaryPreviewForScope from public API.

func TestBuildSnippetBoundaryPreviewForScopeStreamsSnippetOutput(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "before main\nTODO: one\nafter main\n",
		"src/util.ts": "before util\nTODO: two\nafter util\n",
	})
	_ = parseInProject(t, project, []string{"."})
	view, err := resolvedCurrentScopeViewForArgs([]string{"src"})
	if err != nil {
		t.Fatalf("resolvedCurrentScopeViewForArgs returned error: %v", err)
	}

	matched, err := snippetBoundaryPreviewMatchedEntries(view, "TODO", nil)
	if err != nil {
		t.Fatalf("snippetBoundaryPreviewMatchedEntries: %v", err)
	}
	cmd, tmpdir := buildSnippetBoundaryPreviewForScope(view, "TODO", matched, nil)
	if cmd == "" || tmpdir == "" {
		t.Fatal("expected snippet boundary preview command and tmpdir")
	}
	defer os.RemoveAll(tmpdir)

	if strings.Contains(cmd, "catclip-tree") {
		t.Fatalf("streamed boundary preview must not pipe to catclip-tree, got %q", cmd)
	}
	for _, want := range []string{"--internal-snippet-boundary-preview", "--internal-boundary-source", "--internal-boundary-key {2}"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("preview command missing %q, got %q", want, cmd)
		}
	}

	sourcePath := filepath.Join(tmpdir, "source.json")

	stripANSI := func(b []byte) string { return string(ansiEscape.ReplaceAll(b, nil)) }

	var blockBuf bytes.Buffer
	if err := RunInternalSnippetBoundaryPreview(sourcePath, "block", &blockBuf); err != nil {
		t.Fatalf("stream block boundary: %v", err)
	}
	blockContent := stripANSI(blockBuf.Bytes())
	for _, want := range []string{`<file path="src/main.ts"`, `TODO: one`, `before main`, `after main`, `<file path="src/util.ts"`, `TODO: two`, `before util`, `after util`} {
		if !strings.Contains(blockContent, want) {
			t.Fatalf("block preview missing %q in:\n%s", want, blockContent)
		}
	}

	var zeroBuf bytes.Buffer
	if err := RunInternalSnippetBoundaryPreview(sourcePath, "0", &zeroBuf); err != nil {
		t.Fatalf("stream zero-context boundary: %v", err)
	}
	zeroContent := stripANSI(zeroBuf.Bytes())
	if !strings.Contains(zeroContent, "TODO: one") || !strings.Contains(zeroContent, "TODO: two") {
		t.Fatalf("zero-context preview should include both matching lines, got %q", zeroContent)
	}
	if strings.Contains(zeroContent, "before main") || strings.Contains(zeroContent, "after main") ||
		strings.Contains(zeroContent, "before util") || strings.Contains(zeroContent, "after util") {
		t.Fatalf("zero-context preview should not include neighboring lines, got %q", zeroContent)
	}
}

func TestBuildSnippetBoundaryPreviewForScopePreservesRecentOrder(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a_old.go":    "package main\n\nfunc oldMatch() {}\n",
		"src/z_recent.go": "package main\n\nfunc recentMatch() {}\n",
	})
	now := time.Date(2026, time.May, 25, 12, 0, 0, 0, time.UTC)
	setProjectModTime(t, project, "src/a_old.go", now.Add(-1*time.Hour))
	setProjectModTime(t, project, "src/z_recent.go", now)
	_ = parseInProject(t, project, []string{"."})

	view, err := resolvedCurrentScopeViewForArgs([]string{"src", "--only", "*.go", "--recent", "2"})
	if err != nil {
		t.Fatalf("resolvedCurrentScopeViewForArgs returned error: %v", err)
	}
	matched, err := snippetBoundaryPreviewMatchedEntries(view, "func", nil)
	if err != nil {
		t.Fatalf("snippetBoundaryPreviewMatchedEntries: %v", err)
	}
	cmd, tmpdir := buildSnippetBoundaryPreviewForScope(view, "func", matched, nil)
	if cmd == "" || tmpdir == "" {
		t.Fatal("expected snippet boundary preview command and tmpdir")
	}
	defer os.RemoveAll(tmpdir)

	var zeroBuf bytes.Buffer
	if err := RunInternalSnippetBoundaryPreview(filepath.Join(tmpdir, "source.json"), "0", &zeroBuf); err != nil {
		t.Fatalf("stream zero-context boundary: %v", err)
	}
	content := zeroBuf.String()
	recentIndex := strings.Index(content, `path="src/z_recent.go"`)
	oldIndex := strings.Index(content, `path="src/a_old.go"`)
	if recentIndex < 0 || oldIndex < 0 {
		t.Fatalf("preview missing expected files:\n%s", content)
	}
	if recentIndex > oldIndex {
		t.Fatalf("preview should preserve --recent order, got:\n%s", content)
	}
}
