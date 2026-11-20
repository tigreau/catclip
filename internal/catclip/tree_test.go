package catclip

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
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

func TestRunInternalTargetInventoryPreviewMatchesFreshDiscovery(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts":        "export const a = 1\n",
		"src/nested/b.ts": "export const b = 2\n",
		"outside.ts":      "export const outside = true\n",
	})

	inventoryPath := filepath.Join(t.TempDir(), "targets.bin")
	if err := discovery.WriteTargetPreviewInventory(inventoryPath, git.Context{}, []discovery.TargetMatch{
		{Path: "outside.ts", Kind: "file"},
		{Path: "src", Kind: "dir"},
		{Path: "src/a.ts", Kind: "file"},
		{Path: "src/nested", Kind: "dir"},
		{Path: "src/nested/b.ts", Kind: "file"},
	}); err != nil {
		t.Fatalf("WriteTargetPreviewInventory() error = %v", err)
	}

	render := func(args []string) string {
		t.Helper()
		cfg := parseInProject(t, project, args)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if err := run(cfg, &stdout, &stderr); err != nil {
			t.Fatalf("run(%v) error = %v", args, err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("run(%v) stderr = %q", args, stderr.String())
		}
		return stdout.String()
	}

	want := render([]string{"--internal-tree-preview", "src"})
	got := render([]string{
		"--internal-tree-preview",
		"--internal-target-inventory", inventoryPath,
		"--internal-tree-target", "src",
		"--internal-tree-kind", "dir",
		"--internal-tree-state", "ok",
		"src",
	})
	if got != want {
		t.Fatalf("inventory preview differs from fresh discovery\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRunInternalTargetInventoryPreviewUsesCapturedSizeAfterFileDisappears(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "export const a = 1\n",
	})
	inventoryPath := filepath.Join(t.TempDir(), "targets.bin")
	if err := discovery.WriteTargetPreviewInventory(inventoryPath, git.Context{}, []discovery.TargetMatch{
		{Path: "src/a.ts", Kind: "file", SizeBytes: 42, SizeKnown: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(project, "src", "a.ts")); err != nil {
		t.Fatal(err)
	}

	cfg := parseInProject(t, project, []string{
		"--internal-tree-preview",
		"--internal-target-inventory", inventoryPath,
		"--internal-tree-target", "src/a.ts",
		"--internal-tree-kind", "file",
		"--internal-tree-state", "text",
		"src/a.ts",
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "42B") {
		t.Fatalf("preview did not use captured size:\n%s", stdout.String())
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

func TestTargetPickerSessionRejectsFilteredFreshTreePreview(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "export const a = 1\n",
	})
	t.Setenv("CATCLIP_TARGET_PREVIEW_SESSION", t.TempDir())
	cfg := parseInProject(t, project, []string{"--internal-tree-preview", "src", "--only", "*.ts"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "does not accept filter or output stages") {
		t.Fatalf("run error = %v, want target-preview invariant failure", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected preview output: %q", stdout.String())
	}
}
