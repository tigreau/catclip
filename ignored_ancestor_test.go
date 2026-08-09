package catclip

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the lookup-miss ignored-ancestor probe.
// See docs/versions/v0.5.7/reports/ACTIVE_PLAN_surface_ignored_ancestor.md.
//
// Each test sets XDG_CONFIG_HOME to a temp dir with an empty .hiss so the
// global ignore overlay can't affect the fixture (no node_modules/ default
// rules leaking in).

func setupAncestorXDG(t *testing.T) {
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

func TestRunSurfacesIgnoredAncestorForBasenameTarget(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":          "myblocked/\n",
		"myblocked/target.md": "hi\n",
		"myblocked/sub/x.txt": "hi\n",
		"visible/keepit.go":   "hi\n",
	})

	cfg := parseInProject(t, project, []string{"--headless", "target.md"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err == nil {
		t.Fatal("expected no-match exit")
	}
	out := stderr.String()

	for _, want := range []string{
		"hidden by an ignored ancestor",
		"./myblocked/target.md",
		"parent ./myblocked ignored by .gitignore",
		"catclip 'myblocked/target.md'",
		"catclip . --no-ignore",
		"catclip --all-ignore-rules",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRunSurfacesIgnoredAncestorForDirTarget(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":                "myblocked/\n",
		"myblocked/somedir/file.go": "hi\n",
		"visible/keepit.go":         "hi\n",
	})

	cfg := parseInProject(t, project, []string{"--headless", "somedir"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err == nil {
		t.Fatal("expected no-match exit")
	}
	out := stderr.String()

	for _, want := range []string{
		"hidden by an ignored ancestor",
		"./myblocked/somedir",
		"parent ./myblocked ignored by .gitignore",
		"catclip 'myblocked/somedir'",
		"catclip . --no-ignore",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRunHiddenExactFileBeatsUnrelatedVisibleFuzzyMatch(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":                       "src/build/\n",
		"src/build/nested.ts":              "hidden exact\n",
		"src/components/layout/Header.tsx": "visible fuzzy\n",
	})

	cfg := parseInProject(t, project, []string{"nested.ts", "--headless", "--quiet", "--print", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err == nil {
		t.Fatal("expected hidden basename shorthand to require a complete path")
	}
	if stdout.Len() != 0 {
		t.Fatalf("hidden exact basename must not fall through to fuzzy output:\n%s", stdout.String())
	}
	for _, want := range []string{
		"hidden by an ignored ancestor",
		"./src/build/nested.ts",
		"catclip 'src/build/nested.ts'",
		"catclip . --no-ignore",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("hidden exact diagnostic missing %q:\n%s", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "Header.tsx") {
		t.Fatalf("diagnostic must not suggest the unrelated fuzzy file:\n%s", stderr.String())
	}
}

func TestRunHiddenExactDirectoryBeatsUnrelatedVisibleFuzzyMatch(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":              "blocked/\n",
		"blocked/secret/value.ts": "hidden exact dir\n",
		"src/SecretPanel.tsx":     "visible fuzzy\n",
	})

	cfg := parseInProject(t, project, []string{"secret", "--headless", "--quiet", "--print", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err == nil {
		t.Fatal("expected hidden directory shorthand to require a complete path")
	}
	if stdout.Len() != 0 {
		t.Fatalf("hidden exact directory must not fall through to fuzzy output:\n%s", stdout.String())
	}
	for _, want := range []string{
		"hidden by an ignored ancestor",
		"./blocked/secret",
		"catclip 'blocked/secret'",
		"catclip . --no-ignore",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("hidden exact directory diagnostic missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestRunVisibleExactBasenameStillBeatsHiddenDuplicate(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":        "blocked/\n",
		"target.md":         "visible exact\n",
		"blocked/target.md": "hidden duplicate\n",
	})

	cfg := parseInProject(t, project, []string{"target.md", "--headless", "--quiet", "--print", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("visible exact basename should retain priority: %v\n%s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "target.md" {
		t.Fatalf("resolved paths = %q, want target.md", got)
	}
}

func TestRunVisibleExactExtensionlessBasenameStillBeatsHiddenDuplicate(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":      "blocked/\n",
		"LICENSE":         "visible exact\n",
		"blocked/LICENSE": "hidden duplicate\n",
	})

	cfg := parseInProject(t, project, []string{"LICENSE", "--headless", "--quiet", "--print", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("visible extensionless exact basename should retain priority: %v\n%s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "LICENSE" {
		t.Fatalf("resolved paths = %q, want LICENSE", got)
	}
}

func TestRunSurfacesIgnoredAncestorMultiHit(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":            "alpha/\nbeta/\n",
		"alpha/somefile.md":     "hi\n",
		"alpha/sub/somefile.md": "hi\n",
		"beta/somefile.md":      "hi\n",
		"visible/keepit.go":     "hi\n",
	})

	cfg := parseInProject(t, project, []string{"--headless", "somefile.md"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err == nil {
		t.Fatal("expected no-match exit")
	}
	out := stderr.String()

	if !strings.Contains(out, "is hidden by ignore rules. Found in") {
		t.Errorf("expected multi-hit header, got:\n%s", out)
	}
	// Both top-level ignored ancestors should appear as blockers.
	if !strings.Contains(out, "./alpha") {
		t.Errorf("missing alpha ancestor:\n%s", out)
	}
	if !strings.Contains(out, "./beta") {
		t.Errorf("missing beta ancestor:\n%s", out)
	}
	// All three hits should be listed in (alpha root, alpha/sub, beta root).
	for _, p := range []string{"./alpha/somefile.md", "./alpha/sub/somefile.md", "./beta/somefile.md"} {
		if !strings.Contains(out, p) {
			t.Errorf("missing path %q in multi-hit listing:\n%s", p, out)
		}
	}
}

func TestRunDoesNotProbeForFilterZeroMatch(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":          "myblocked/\n",
		"myblocked/target.md": "hi\n",
		"visible/keepit.go":   "hi\n",
	})

	// `target.md` exists inside myblocked/; --only narrows the visible set to
	// nothing. The ancestor probe must NOT fire — the target `visible/` resolved
	// fine, and a filter zero-match is semantically different from a target miss.
	cfg := parseInProject(t, project, []string{"--headless", "visible", "--only", "target.md"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err == nil {
		t.Fatal("expected no-match exit")
	}
	out := stderr.String()

	if strings.Contains(out, "hidden by an ignored ancestor") {
		t.Errorf("filter zero-match should NOT trigger ancestor probe, got:\n%s", out)
	}
	if strings.Contains(out, "hidden by ignore rules. Found in") {
		t.Errorf("filter zero-match should NOT trigger multi-hit ancestor message, got:\n%s", out)
	}
	if !strings.Contains(out, "No text files found matching your criteria.") {
		t.Errorf("filter zero-match should produce the generic message, got:\n%s", out)
	}
}

func TestRunUnchangedForTrulyMissingTarget(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		"visible/keepit.go": "hi\n",
	})

	// No file named definitely_not_here.md anywhere → probe finds nothing → fall
	// back to the existing "No file named ... found" warning.
	cfg := parseInProject(t, project, []string{"--headless", "definitely_not_here.md"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err == nil {
		t.Fatal("expected no-match exit")
	}
	out := stderr.String()

	if strings.Contains(out, "hidden by an ignored ancestor") {
		t.Errorf("truly-missing target should NOT trigger ancestor probe, got:\n%s", out)
	}
	if strings.Contains(out, "hidden by ignore rules. Found in") {
		t.Errorf("truly-missing target should NOT trigger multi-hit message, got:\n%s", out)
	}
	if !strings.Contains(out, "No file") {
		t.Errorf("expected existing not-found warning, got:\n%s", out)
	}
}

func TestRunSkipsAncestorProbeUnderNoIgnore(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":          "myblocked/\n",
		"myblocked/target.md": "hi\n",
		"visible/keepit.go":   "hi\n",
	})

	// --no-ignore disables ignore entirely; target.md becomes visible. No
	// ancestor message should fire (and the file should actually be included).
	cfg := parseInProject(t, project, []string{"--headless", "target.md", "--no-ignore"})
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	out := stderr.String() + stdout.String()
	if strings.Contains(out, "hidden by an ignored ancestor") {
		t.Errorf("--no-ignore should disable the ancestor probe, got:\n%s", out)
	}
	_ = err // we don't care about the exit code here, just that the probe didn't fire
}

func TestAncestorProbeRespectsGlobShapedTarget(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":          "myblocked/\n",
		"myblocked/target.md": "hi\n",
		"visible/keepit.go":   "hi\n",
	})

	// Glob targets ("*.md") are routed to resolveGlobTarget, not the basename
	// path the probe hooks into. Whatever message fires, it must NOT be the
	// ancestor message — the value is a pattern, not a filename to attribute.
	cfg := parseInProject(t, project, []string{"--headless", "*.md"})
	var stdout, stderr bytes.Buffer
	_ = run(cfg, &stdout, &stderr)
	out := stderr.String()

	if strings.Contains(out, "hidden by an ignored ancestor") {
		t.Errorf("glob target should NOT trigger ancestor probe, got:\n%s", out)
	}
}
