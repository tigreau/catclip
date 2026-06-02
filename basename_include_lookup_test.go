package catclip

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the basename + --include resolution fix.
// See docs/versions/v0.5.7/reports/ACTIVE_BUG_basename_target_ignores_include_subtree.md.

func setupBasenameIncludeXDG(t *testing.T) {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	hissDir := filepath.Join(xdg, "catclip")
	if err := os.MkdirAll(hissDir, 0o755); err != nil {
		t.Fatalf("mkdir hiss: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hissDir, ".hiss"), []byte(""), 0o644); err != nil {
		t.Fatalf("write empty .hiss: %v", err)
	}
}

func captureRun(t *testing.T, project string, args []string) (string, string, error) {
	t.Helper()
	cfg := parseInProject(t, project, args)
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	_ = io.Discard
	return stdout.String(), stderr.String(), err
}

// A1: basename file target + --include of its parent ignored dir → bundles the file.
func TestBasenameTargetWithIncludeParentResolves(t *testing.T) {
	setupBasenameIncludeXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":               "blocked/\n",
		"blocked/target.md":        "hello blocked\n",
		"visible/keepit.go":        "ok\n",
	})

	stdout, _, err := captureRun(t, project, []string{"--print", "target.md", "--include", "blocked"})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout, "hello blocked") {
		t.Errorf("expected target.md contents in output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "blocked/target.md") {
		t.Errorf("expected the resolved path in output:\n%s", stdout)
	}
}

// A2: basename + --include of grandparent (the deeper-than-direct case).
func TestBasenameTargetWithIncludeGrandparentResolves(t *testing.T) {
	setupBasenameIncludeXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":                "blocked/\n",
		"blocked/sub/needle.json":   `{"k":"v"}` + "\n",
		"visible/keepit.go":         "ok\n",
	})

	stdout, _, err := captureRun(t, project, []string{"--print", "needle.json", "--include", "blocked"})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout, `"k":"v"`) {
		t.Errorf("expected needle.json contents in output:\n%s", stdout)
	}
}

// A3: dir-shorthand target (no extension) + --include parent → bundles dir contents.
func TestDirShorthandTargetWithIncludeParentResolves(t *testing.T) {
	setupBasenameIncludeXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":              "blocked/\n",
		"blocked/somedir/a.go":    "package somedir\n",
		"blocked/somedir/b.go":    "package somedir\n",
		"visible/keepit.go":       "ok\n",
	})

	stdout, _, err := captureRun(t, project, []string{"--print", "somedir", "--include", "blocked"})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout, "blocked/somedir/a.go") {
		t.Errorf("expected blocked/somedir/a.go in output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "blocked/somedir/b.go") {
		t.Errorf("expected blocked/somedir/b.go in output:\n%s", stdout)
	}
}

// A7 control: path-shaped target + --include parent — the historically-working
// case must not regress.
func TestPathTargetWithIncludeParentStillResolves(t *testing.T) {
	setupBasenameIncludeXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":           "blocked/\n",
		"blocked/target.md":    "still works\n",
	})

	stdout, _, err := captureRun(t, project, []string{"--print", "blocked/target.md", "--include", "blocked"})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout, "still works") {
		t.Errorf("expected path-target case to continue working:\n%s", stdout)
	}
}

// Control: basename target without any --include → still triggers the ancestor
// probe message (the probe is for unauthorized cases).
func TestBasenameTargetWithoutIncludeStillProbes(t *testing.T) {
	setupBasenameIncludeXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":         "blocked/\n",
		"blocked/target.md":  "hi\n",
		"visible/keepit.go":  "ok\n",
	})

	_, stderr, err := captureRun(t, project, []string{"--headless", "target.md"})
	if err == nil {
		t.Fatal("expected no-match exit")
	}
	if !strings.Contains(stderr, "hidden by an ignored ancestor") {
		t.Errorf("expected ancestor probe message, got:\n%s", stderr)
	}
}

// Control: filter zero-match must NOT trigger include-subtree search.
func TestFilterZeroMatchWithIncludeDoesNotResolveBasename(t *testing.T) {
	setupBasenameIncludeXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":           "blocked/\n",
		"blocked/target.md":    "hi\n",
		"visible/keepit.go":    "ok\n",
	})

	// Target is `visible/` (resolves fine), --only narrows to zero. Even
	// though `--include blocked` is active, the filter-miss path must keep the
	// generic "No text files found" message — not bundle the blocked file.
	stdout, stderr, err := captureRun(t, project, []string{"--headless", "visible", "--include", "blocked", "--only", "target.md"})
	if err == nil {
		t.Fatal("expected no-match exit")
	}
	if strings.Contains(stdout, "blocked/target.md") {
		t.Errorf("filter zero-match should NOT have bundled the ignored target:\n%s", stdout)
	}
	if !strings.Contains(stderr, "No text files found matching your criteria.") {
		t.Errorf("expected the generic filter-miss message, got:\n%s", stderr)
	}
}

// A4: --include the exact ignored file path — basename matches the exact
// entry; the file resolves directly.
func TestBasenameTargetWithIncludeExactFilePathResolves(t *testing.T) {
	setupBasenameIncludeXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":          "blocked/\n",
		"blocked/target.md":   "exact path include\n",
	})

	stdout, _, err := captureRun(t, project, []string{"--print", "target.md", "--include", "blocked/target.md"})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout, "exact path include") {
		t.Errorf("expected exact-path --include to resolve the file:\n%s", stdout)
	}
}
