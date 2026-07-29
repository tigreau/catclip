package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
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

func TestStartupCommandCanRunDirectlySkipsPickerForHiddenExactDirectory(t *testing.T) {
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":              "blocked/\n",
		"blocked/secret/value.ts": "hidden exact dir\n",
		"src/SecretPanel.tsx":     "visible fuzzy\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	probe, err := resolver.ProbeStartupTarget("secret")
	if err != nil {
		t.Fatalf("ProbeStartupTarget: %v", err)
	}
	if probe.Outcome != discovery.StartupTargetBlocked {
		t.Fatalf("hidden exact probe outcome = %v, want blocked", probe.Outcome)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"secret"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if !direct {
		t.Fatal("hidden exact directory must bypass the visible fuzzy picker")
	}
}

func TestStartupProbeKeepsVisibleExactDirectoryAmbiguityAheadOfHiddenDuplicate(t *testing.T) {
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":               "blocked/\n",
		"src/base/common/a.ts":     "visible exact one\n",
		"src/editor/common/b.ts":   "visible exact two\n",
		"blocked/common/hidden.ts": "hidden duplicate\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	probe, err := resolver.ProbeStartupTarget("common")
	if err != nil {
		t.Fatalf("ProbeStartupTarget: %v", err)
	}
	if probe.Outcome != discovery.StartupTargetAmbiguousFuzzy {
		t.Fatalf("visible exact-directory probe outcome = %v, want fuzzy ambiguity", probe.Outcome)
	}
}

func TestStartupCommandCanRunDirectlyForWrapperStarGlob(t *testing.T) {
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		"src/utils.ts":              "ok\n",
		"src/components/utils/a.ts": "ok\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"*utils*"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if !direct {
		t.Fatal("wrapper-star glob must bypass fuzzy target selection")
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
	// Several fuzzy-matching "main"-like files produce one ambiguous probe.
	project := setupTestProject(t, map[string]string{
		"main.go":     "package main\n",
		"cmd/main.go": "package main\n",
		"lib/main.go": "package main\n",
		"other.go":    "package other\n",
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

func TestStartupCommandCanRunDirectlyOpensPickerForMixedFuzzyTargets(t *testing.T) {
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		"src/components/ui/Button.tsx":           "ok\n",
		"matcher-cases/beta/leaf-token/beta.txt": "fixture\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"btn"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if direct {
		t.Fatal("mixed fuzzy file/directory candidates must require the picker")
	}
}

func TestStartupCommandCanRunDirectlyUsesOneMixedFzfProbe(t *testing.T) {
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		"src/components/ui/Button.tsx":           "ok\n",
		"matcher-cases/beta/leaf-token/beta.txt": "fixture\n",
		"public/manifest.json":                   "{}\n",
	})
	_ = parseInProject(t, project, []string{"."})

	realFzf, err := discovery.FuzzyResolverBinary()
	if err != nil {
		t.Fatalf("FuzzyResolverBinary: %v", err)
	}
	callLog := filepath.Join(t.TempDir(), "fzf-filter-calls")
	t.Setenv("CATCLIP_TEST_REAL_FZF", realFzf)
	t.Setenv("CATCLIP_TEST_FZF_CALL_LOG", callLog)
	installScriptFzf(t, `#!/bin/sh
for arg in "$@"; do
	if [ "$arg" = "--filter" ]; then
		printf 'filter\n' >> "$CATCLIP_TEST_FZF_CALL_LOG"
		break
	fi
done
exec "$CATCLIP_TEST_REAL_FZF" "$@"
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"btn"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if direct {
		t.Fatal("expected mixed fuzzy candidates to require the picker")
	}

	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read fzf call log: %v", err)
	}
	if got := strings.Count(string(data), "filter\n"); got != 1 {
		t.Fatalf("startup target probe used %d fzf filter calls, want 1\n%s", got, data)
	}
}

func TestStartupCommandCanRunDirectlyOpensPickerForFuzzyDirAndDescendantFiles(t *testing.T) {
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		"src/components/App.tsx": "ok\n",
		"src/other/Card.tsx":     "ok\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"cmpnts"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if direct {
		t.Fatal("fuzzy directory and descendant file matches must remain separate picker candidates")
	}
}

func TestStartupCommandCanRunDirectlySkipsPickerForZeroMatchesWithInclude(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":           "blocked/\n",
		"blocked/something.go": "ok\n",
		"visible.go":           "ok\n",
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

func TestStartupCommandCanRunDirectlyLetsIncludePickerAuthorizeBlockedTarget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":    "docs/\n",
		"docs/guide.md": "ignored documentation\n",
		"visible.go":    "package visible\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	direct, err := startupCommandCanRunDirectly(resolver, []string{"docs", "--include", "doc"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly: %v", err)
	}
	if direct {
		t.Fatal("unresolved include query must be allowed to authorize an existing blocked target")
	}
}

func TestResolveStartupArgsIncludePickerCanSelectBlockedTargetItAuthorizes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setupStartupGateXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":    "docs/\n",
		"docs/guide.md": "ignored documentation\n",
		"visible.go":    "package visible\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
query=""
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--query)
		query="$2"
		shift 2
		;;
	--prompt)
		prompt="$2"
		shift 2
		;;
	*)
		shift
		;;
	esac
done
input="$(cat)"
if [ "$prompt" = "include> " ] && [ "$query" = "doc" ]; then
	printf '%s\n' "$input" | awk -F '\t' '$2 == "docs"' | head -n 1
	exit 0
fi
echo "unexpected prompt/query: $prompt / $query" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver: %v", err)
	}
	args, _, usedPicker, err := resolveStartupArgs(resolver, []string{"docs", "--include", "doc"})
	if err != nil {
		t.Fatalf("resolveStartupArgs: %v", err)
	}
	if !usedPicker {
		t.Fatal("expected unresolved include to use the ignored-target picker")
	}
	if got, want := strings.Join(args, "\n"), "docs\n--include\ndocs"; got != want {
		t.Fatalf("resolved args = %q, want %q", got, want)
	}
}

// The reachability pre-check: when the target is gitignored and there is no
// --include query that could authorize it, a modifier picker should NOT open —
// let the normal flow surface the ignored-target error. See
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
		"src/a.go":         "ok\n",
		"src/sub/b.go":     "ok\n",
		"src/main.go":      "ok\n",
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
		".gitignore":          "blocked/\n",
		"blocked/a/needle.md": "hi\n",
		"blocked/b/needle.md": "hi\n",
		"visible.go":          "ok\n",
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
