package catclip

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Tests for the startup-picker gate that distinguishes "multi-hit ambiguous"
// (open picker) from "zero matches anywhere" / "uniquely resolvable via
// --include" (skip picker, let normal flow run).
// See docs/versions/v0.5.7/reports/ACTIVE_PLAN_startup_picker_gated_on_ambiguity.md.

func setupStartupGateXDG(t *testing.T) {
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

func TestStartupCommandCanRunDirectlySkipsPickerForZeroMatchesAnywhere(t *testing.T) {
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		"visible.go": "ok\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"definitely_not_a_real_target"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if !direct {
		t.Fatal("expected zero-match target to bypass the picker (let not-found warning fire)")
	}
}

func TestStartupCommandCanRunDirectlySkipsPickerForBasenameResolvedByInclude(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":       "blocked/\n",
		"blocked/agent.md": "hi\n",
		"visible.go":       "ok\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"agent.md", "--include", "blocked"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if !direct {
		t.Fatal("expected basename + --include parent (1 authorized hit) to bypass the picker")
	}
}

func TestStartupCommandCanRunDirectlyOpensPickerForMultiHitVisibleBasename(t *testing.T) {
	setupStartupGateXDG(t)
	// Two fuzzy-matching "main"-like files so canResolveTargetWithoutPrompt
	// returns false and hasAnyVisibleMatch returns true.
	project := setupTestProject(t, map[string]string{
		"main.go":       "package main\n",
		"cmd/main.go":   "package main\n",
		"lib/main.go":   "package main\n",
		"other.go":      "package other\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"main"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if direct {
		t.Fatal("expected multi-hit visible basename to require the picker")
	}
}

func TestStartupCommandCanRunDirectlySkipsPickerForZeroMatchesWithInclude(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":         "blocked/\n",
		"blocked/something.go": "ok\n",
		"visible.go":         "ok\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	// "nothing" matches neither visible content nor the authorized "blocked"
	// subtree — picker can't help, should skip.
	direct, err := startupCommandCanRunDirectly(resolver, []string{"nothing", "--include", "blocked"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if !direct {
		t.Fatal("expected zero-match target with --include to bypass the picker")
	}
}

// The reachability pre-check: when the target is gitignored and not covered
// by --include, the startup picker should NOT open — let the normal flow
// surface the ignored-target error. See
// ACTIVE_BUG_filter_picker_fires_before_target_check.md.
func TestStartupCommandCanRunDirectlySkipsFilterPickerForIgnoredTarget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":      "blocked/\n",
		"blocked/file.md": "hi\n",
		"visible.go":      "ok\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}

	// --only with a non-precise value would normally trigger a filter-value
	// picker, but the target "blocked" is gitignored with no --include
	// covering it. Skip the picker — let the ignored-target error fire.
	for _, modifierAndValue := range [][]string{
		{"--only", "x"},
		{"--exclude", "x"},
		{"--include", "k"},
	} {
		args := append([]string{"blocked"}, modifierAndValue...)
		direct, err := startupCommandCanRunDirectly(resolver, args)
		if err != nil {
			t.Fatalf("%v: startupCommandCanRunDirectly: %v", args, err)
		}
		if !direct {
			t.Errorf("%v: expected picker to be skipped for unreachable target", args)
		}
	}
}

func TestStartupCommandCanRunDirectlyOpensFilterPickerWhenTargetIsAuthorized(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":      "blocked/\n",
		"blocked/file.md": "hi\n",
		"visible.go":      "ok\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}

	// With --include covering the target, reachability passes; the filter
	// value picker for --only's non-precise value should fire as today.
	direct, err := startupCommandCanRunDirectly(resolver, []string{"blocked", "--include", "blocked", "--only", "x"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if direct {
		t.Fatal("expected filter picker to fire when target is reachable via --include")
	}
}

func TestStartupCommandCanRunDirectlyOpensFilterPickerForVisibleTarget(t *testing.T) {
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		"src/a.go":       "ok\n",
		"src/sub/b.go":   "ok\n",
		"src/main.go":    "ok\n",
		"src/main_test.go": "ok\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}

	// "src" is visible; --only's non-precise glob value needs disambiguation
	// → existing filter picker should fire (unchanged behavior).
	direct, err := startupCommandCanRunDirectly(resolver, []string{"src", "--only", "x"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if direct {
		t.Fatal("expected filter picker to fire for visible target + non-precise --only")
	}
}

func TestStartupCommandCanRunDirectlySkipsFilterPickerForTrulyMissingTarget(t *testing.T) {
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		"visible.go": "ok\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}

	// Truly absent target: no visible match, no fuzzy match, no --include
	// subtree. Skip the picker — let the not-found warning fire.
	direct, err := startupCommandCanRunDirectly(resolver, []string{"definitely_not_a_real_target", "--only", "x"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if !direct {
		t.Fatal("expected picker to be skipped for truly missing target with --only")
	}
}

func TestStartupCommandCanRunDirectlyOpensPickerForMultiHitAuthorizedBasename(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setupStartupGateXDG(t)
	// Two basename hits inside the authorized subtree → genuine ambiguity,
	// picker is the right response.
	project := setupTestProject(t, map[string]string{
		".gitignore":               "blocked/\n",
		"blocked/a/needle.md":      "hi\n",
		"blocked/b/needle.md":      "hi\n",
		"visible.go":               "ok\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"needle.md", "--include", "blocked"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if direct {
		t.Fatal("expected multi-hit basename in --include'd subtree to require the picker")
	}
}
