package discovery

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

func TestEvaluateScopeRetainsResolvedTargetsAcrossEmptyFilter(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeResolvedTargetFixture(t, root, "nested/unique-scope/keep.go", "package scope\n")
	result, err := EvaluateScope(
		command.Invocation{WorkingDir: root, Headless: true},
		git.Context{},
		0,
		command.ExecutionScope{
			Targets: []string{"unique-scope"},
			Stages:  []command.Stage{{Kind: command.StageOnly, Values: []string{"definitely-no-match-*"}}},
		},
		io.Discard,
		platform.Palette{},
	)
	if err != nil {
		t.Fatalf("EvaluateScope: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Fatalf("filtered entries = %#v, want none", result.Entries)
	}
	if len(result.ResolvedTargets) != 1 || result.ResolvedTargets[0] != (ResolvedTarget{Path: "nested/unique-scope", Kind: ResolvedTargetDir}) {
		t.Fatalf("resolved targets = %#v", result.ResolvedTargets)
	}
}

func TestEvaluateScopeRetainsCanonicalFuzzyFileTarget(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeResolvedTargetFixture(t, root, "nested/unique-report.go", "package report\n")
	result, err := EvaluateScope(
		command.Invocation{WorkingDir: root, Headless: true},
		git.Context{},
		0,
		command.ExecutionScope{Targets: []string{"unique-report.go"}},
		io.Discard,
		platform.Palette{},
	)
	if err != nil {
		t.Fatalf("EvaluateScope: %v", err)
	}
	if len(result.ResolvedTargets) != 1 || result.ResolvedTargets[0] != (ResolvedTarget{Path: "nested/unique-report.go", Kind: ResolvedTargetFile}) {
		t.Fatalf("resolved targets = %#v", result.ResolvedTargets)
	}
}

func writeResolvedTargetFixture(t *testing.T, root, rel, contents string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
