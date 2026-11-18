package ui

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

func TestRetainedStagesAfterGlobalOptions(t *testing.T) {
	for _, tc := range []struct {
		name           string
		initial, extra []string
	}{
		{name: "control"},
		{name: "verbose", extra: []string{"--verbose"}},
		{name: "quiet", extra: []string{"--quiet"}},
		{name: "raw", extra: []string{"--raw"}},
		{name: "only-tree", extra: []string{"--only-tree"}},
		{name: "no-tree", extra: []string{"--no-tree"}},
		{name: "token-limit", extra: []string{"--token-limit", "4000"}},
		{name: "replace-token-limit", initial: []string{"--token-limit", "8000"}, extra: []string{"--token-limit", "4000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := setupTestProject(t, map[string]string{"src/a.go": "package a\n// KEEP\n", "src/drop.txt": "drop\n"})
			_ = parseInProject(t, project, []string{"."})
			scopeViewMemoReset()
			defer scopeViewMemoReset()
			base := append([]string{"src"}, tc.initial...)
			if err := scopeViewMemoSealEvaluatedTarget(base); err != nil {
				t.Fatal(err)
			}
			first := append(append([]string(nil), base...), "--only", "*.go")
			if _, err := resolvedCurrentScopeViewForArgs(first); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, "src/late.go"), []byte("package late\n// KEEP\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var enumerations int
			restore := search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { enumerations++ })
			defer restore()
			args := appendStartupGlobalOptions(first, tc.extra)
			args = append(args, "--contains", "KEEP")
			view, err := resolvedCurrentScopeViewForArgs(args)
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, entry := range view.Entries {
				paths = append(paths, entry.RelPath)
			}
			late := strings.Contains(strings.Join(paths, ","), "late.go")
			if enumerations != 0 || late {
				t.Fatalf("control rediscovered: %v", paths)
			}
			t.Logf("extra_enumerations=%d late_file_admitted=%v paths=%v", enumerations, late, paths)
		})
	}
}

func TestRepeatedScopeDiagnosticAttribution(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/a.go": "package a\n"})
	_ = parseInProject(t, project, []string{"."})
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	first := []string{"src", "--only", "*/"}
	both := append(append([]string(nil), first...), "--then", "src", "--only", "*/")
	for _, args := range [][]string{first, both} {
		if _, err := resolvedCurrentScopeViewForArgs(args); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := cli.ParseArgsAllowImplicitDot(both)
	if err != nil {
		t.Fatal(err)
	}
	resolved := command.ResolvedFromParsed(cfg)
	retained, _, ok := scopeViewMemoDiscoveryResult(resolved)
	if !ok {
		t.Fatal("retained handoff failed")
	}
	canonical, err := discovery.DiscoverInvocation(resolved, git.Detect(project), io.Discard, platform.ActivePalette())
	if err != nil {
		t.Fatal(err)
	}
	if len(retained.Diagnostics) != len(canonical.Diagnostics) {
		t.Fatalf("diagnostic count: %d != %d", len(retained.Diagnostics), len(canonical.Diagnostics))
	}
	for i, diag := range retained.Diagnostics {
		if diag.Message != canonical.Diagnostics[i].Message || diag.ScopeIndex != canonical.Diagnostics[i].ScopeIndex {
			t.Fatalf("retained attribution differs: %+v / %+v", diag, canonical.Diagnostics[i])
		}
	}
}

func TestDiagnoseGlobalOptionCorpusTiming(t *testing.T) {
	corpus := os.Getenv("CATCLIP_THEN_DIAG_CORPUS")
	if corpus == "" {
		t.Skip("opt-in large corpus diagnosis")
	}
	t.Chdir(corpus)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer scopeViewMemoReset()
	for round := 0; round < 3; round++ {
		for _, late := range []bool{false, true} {
			scopeViewMemoReset()
			base := []string{"drivers"}
			if !late {
				base = append(base, "--verbose")
			}
			if err := scopeViewMemoSealEvaluatedTarget(base); err != nil {
				t.Fatal(err)
			}
			first := append(append([]string(nil), base...), "--only", "*.c")
			if _, err := resolvedCurrentScopeViewForArgs(first); err != nil {
				t.Fatal(err)
			}
			args := first
			if late {
				args = appendStartupGlobalOptions(first, []string{"--verbose"})
			}
			args = append(args, "--contains", "help")
			var scans int
			restore := search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { scans++ })
			start := time.Now()
			view, err := resolvedCurrentScopeViewForArgs(args)
			elapsed := time.Since(start)
			restore()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("late_extra=%v round=%d selected=%d ms=%.3f extra_enumerations=%d", late, round, len(view.Entries), float64(elapsed)/float64(time.Millisecond), scans)
		}
	}
}

func TestMenuOutputViewBeforeThenUsesSupportedGrammar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake fzf")
	}
	for _, choice := range []string{"paths", "metadata"} {
		t.Run(choice, func(t *testing.T) {
			project := setupTestProject(t, map[string]string{"src/a.go": "package a\n", "cmd/main.go": "package main\n"})
			_ = parseInProject(t, project, []string{"."})
			t.Setenv("CATCLIP_AUDIT_MENU_CHOICE", choice)
			t.Setenv("CATCLIP_AUDIT_MENU_STEP", filepath.Join(t.TempDir(), "step"))
			installScriptFzf(t, `#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
 case "$1" in
 --prompt) prompt="$2"; shift 2 ;;
 *) shift ;;
 esac
done
input="$(cat)"
case "$prompt" in
 "filter> ")
  if [ -f "$CATCLIP_AUDIT_MENU_STEP" ]; then printf 'then\n'; else touch "$CATCLIP_AUDIT_MENU_STEP"; printf '%s\n' "$CATCLIP_AUDIT_MENU_CHOICE"; fi ;;
 "then> ") printf '%s\n' "$input" | awk -F '\t' '$2 == "cmd"' | head -n 1 ;;
 "output> ") printf '%s\n' "$input" | awk -F '\t' '$2 == "only-tree"' ;;
 *) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)
			resolver, err := newStartupPickerResolver()
			if err != nil {
				t.Fatal(err)
			}
			if err := cli.ValidateStartupPreflightArgs([]string{"src", "--", "--then", "cmd", "--only-tree"}); err == nil {
				t.Fatal("unsupported raw continuation unexpectedly accepted")
			}
			raw := []string{"src", "--", "--"}
			if err := cli.ValidateStartupPreflightArgs(raw); err != nil {
				t.Fatal(err)
			}
			result, err := resolveStartupPickerResultWithUndo(resolver, raw)
			if err != nil {
				t.Fatal(err)
			}
			if result.PreparedOutput == nil || len(result.PreparedOutput.Discovery.Invocation.Scopes) != 2 {
				t.Fatal("missing prepared scopes")
			}
			scope := result.PreparedOutput.Discovery.Invocation.Scopes[0].Scope
			if choice == "paths" && !scope.Paths || choice == "metadata" && !scope.Metadata {
				t.Fatalf("lost selected view: %+v", scope)
			}
		})
	}
}

func TestRetainedParentCompatibilityKeepsSelectionBoundaries(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/a.go": "package a\n"})
	_ = parseInProject(t, project, []string{"."})
	cfg, err := cli.ParseArgsAllowImplicitDot([]string{"src", "--only", "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	resolved := command.ResolvedFromParsed(cfg)
	parent := resolvedScopeView{Invocation: resolved.Config, Scopes: resolved.Scopes, Scope: resolved.Scopes[0]}
	for _, tc := range []struct {
		name    string
		args    []string
		allowed bool
	}{
		{"same selection", []string{"src", "--only", "*.go", "--verbose"}, true},
		{"budget and next stage", []string{"src", "--only", "*.go", "--token-limit", "4000", "--contains", "KEEP"}, true},
		{"new target", []string{"other", "--only", "*.go", "--contains", "KEEP"}, false},
		{"changed previous filter", []string{"src", "--only", "*.txt", "--contains", "KEEP"}, false},
		{"new ignore policy", []string{"src", "--no-ignore", "--only", "*.go", "--contains", "KEEP"}, false},
		{"new binary policy", []string{"src", "--with-binaries", "--only", "*.go", "--contains", "KEEP"}, false},
		{"new scope", []string{"src", "--only", "*.go", "--then", "docs"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := cli.ParseArgsAllowImplicitDot(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			_, _, allowed := scopeViewDerivationStage(parent, command.ResolvedFromParsed(cfg))
			if allowed != tc.allowed {
				t.Fatalf("allowed=%v, want %v", allowed, tc.allowed)
			}
		})
	}
	changed := resolved
	changed.Config.WorkingDir = filepath.Join(project, "other")
	if _, _, allowed := scopeViewDerivationStage(parent, changed); allowed {
		t.Fatal("reused a different working directory")
	}
}

func TestRetainedExtrasCommitWithoutNewStage(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/a.go": "package a\n"})
	_ = parseInProject(t, project, []string{"."})
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	base := []string{"src", "--token-limit", "8000"}
	if err := scopeViewMemoSealEvaluatedTarget(base); err != nil {
		t.Fatal(err)
	}
	var enumerations int
	restore := search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { enumerations++ })
	defer restore()
	args := appendStartupGlobalOptions(base, []string{"--verbose", "--token-limit", "4000"})
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if enumerations != 0 || !view.Invocation.Verbose || view.Invocation.TokenLimit != 4000 {
		t.Fatalf("walks=%d config=%+v", enumerations, view.Invocation)
	}
	if len(view.Entries) != 1 || view.Entries[0].RelPath != "src/a.go" {
		t.Fatal(view.Entries)
	}
}

func TestInteractiveExtrasKeepSealedSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake fzf")
	}
	project := setupTestProject(t, map[string]string{"src/a.go": "package a\n", "src/drop.txt": "drop\n"})
	_ = parseInProject(t, project, []string{"."})
	t.Setenv("CATCLIP_AUDIT_MENU_STEP", filepath.Join(t.TempDir(), "step"))
	installScriptFzf(t, `#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
 case "$1" in
 --prompt) prompt="$2"; shift 2 ;;
 *) shift ;;
 esac
done
input="$(cat)"
case "$prompt" in
 "filter> ")
  if [ -f "$CATCLIP_AUDIT_MENU_STEP" ]; then printf 'paths\n'; else touch "$CATCLIP_AUDIT_MENU_STEP"; printf 'extras\n'; fi ;;
 "extras> ")
  printf 'package late\n' > src/late.go
  printf '%s\n' "$input" | awk -F '\t' '$2 == "verbose"' ;;
 "output> ") printf '%s\n' "$input" | awk -F '\t' '$2 == "only-tree"' ;;
 *) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)
	raw := []string{"src", "--only", "*.go", "--", "--"}
	if err := cli.ValidateStartupPreflightArgs(raw); err != nil {
		t.Fatal(err)
	}
	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolveStartupPickerResultWithUndo(resolver, raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.PreparedOutput == nil {
		t.Fatal("missing prepared output")
	}
	paths := result.PreparedOutput.Plan.DistinctRelPaths()
	if len(paths) != 1 || paths[0] != "src/a.go" {
		t.Fatalf("selection changed after Extras: %v", paths)
	}
	cfg, err := cli.ParseArgs(result.Args)
	if err != nil || !cfg.Verbose {
		t.Fatalf("extras not committed: %v %+v", err, cfg)
	}
}
