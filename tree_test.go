package catclip

import (
	"bytes"
	"strings"
	"testing"
)

// Root tree tests intentionally stay on the catclip side of the boundary.
// Pure renderer and payload coverage lives under internal/render.

func TestRunInternalTreePreviewOutputsTreeDocument(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "export const a = 1\n",
		"src/b.ts": "export const b = 2\n",
	})

	cfg := parseInProject(t, project, []string{"--internal-tree-preview", "src"})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"src/", "a.ts", "b.ts", "Count:", "2 files"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview output missing %q:\n%s", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got:\n%s", stderr.String())
	}
}

func TestRunInternalTreePreviewOutputsNoTextChildrenDirectoryState(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":  "blocked/\n",
		"blocked/bin": "\x00\x01\x02",
	})

	cfg := parseInProject(t, project, []string{
		"--internal-tree-preview",
		"--internal-tree-target", "blocked",
		"--internal-tree-kind", "dir",
		"--internal-tree-state", "no_text_children",
		"blocked",
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"blocked/", "no previewable text files"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview output missing %q:\n%s", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got:\n%s", stderr.String())
	}
}
