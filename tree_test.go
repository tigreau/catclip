package catclip

import (
	"bytes"
	"testing"
)

// Root tree tests intentionally stay on the catclip side of the boundary.
// Pure renderer, payload, and catclip-tree CLI coverage lives under internal/tree.

func TestRunInternalTreePayloadOutputsStructuredPreview(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "export const a = 1\n",
		"src/b.ts": "export const b = 2\n",
	})

	cfg := parseInProject(t, project, []string{"--internal-tree-payload", "src"})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}

	if doc.Mode != treeDocumentModeTree {
		t.Fatalf("payload mode = %q, want tree", doc.Mode)
	}
	if doc.Target == nil || doc.Target.Path != "src" || doc.Target.Kind != treeTargetKindDir || doc.Target.State != treeTargetStateOK {
		t.Fatalf("payload target = %#v, want src dir ok", doc.Target)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("payload entries = %d, want 2", len(doc.Entries))
	}
	if doc.Summary == nil || doc.Summary.Count != 2 {
		t.Fatalf("payload summary = %#v, want count 2", doc.Summary)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got:\n%s", stderr.String())
	}
}

func TestRunInternalTreePayloadOutputsStructuredNoTextChildrenDirectoryState(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":  "blocked/\n",
		"blocked/bin": "\x00\x01\x02",
	})

	cfg := parseInProject(t, project, []string{
		"--internal-tree-payload",
		"--internal-tree-target", "blocked",
		"--internal-tree-kind", "dir",
		"--internal-tree-state", "no_text_children",
		"blocked",
		"--include", "blocked",
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.Target == nil || doc.Target.Path != "blocked" || doc.Target.Kind != treeTargetKindDir || doc.Target.State != treeTargetStateNoTextChildren {
		t.Fatalf("payload target = %#v, want blocked dir no_text_children", doc.Target)
	}
	if len(doc.Entries) != 0 {
		t.Fatalf("payload entries = %d, want 0", len(doc.Entries))
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got:\n%s", stderr.String())
	}
}
