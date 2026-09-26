package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/search"
)

func diffPreviewTestTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	return dir
}

func diffPreviewTestPath(t *testing.T, dir string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "catclip-diff-preview-*", "state.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("expected one preview state, got %v: %v", paths, err)
	}
	return paths[0]
}

func TestDiffPreviewTransportParity(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/a.go": "package old\n", "src/clean.go": "package clean\n"})
	t.Chdir(project)
	initGitRepo(t, project)
	if err := os.WriteFile(filepath.Join(project, "src/a.go"), []byte("package staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, project, "add", "src/a.go")
	for path, content := range map[string]string{"src/a.go": "package unstaged\n", "src/new.go": "package untracked\n"} {
		if err := os.WriteFile(filepath.Join(project, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, flag := range []string{"--changed-diff", "--staged-diff", "--unstaged-diff"} {
		for _, base := range [][]string{{"src"}, {"."}, {"src/a.go"}, {"src/a.go", "src/clean.go"}, {"src", "--paths", "--then", "src"}} {
			args := append(append([]string(nil), base...), "--no-ignore", flag)
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				dir := diffPreviewTestTemp(t)
				var events []search.MembershipEnumerationEvent
				restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) { events = append(events, e) })
				defer restore()
				cmd, cleanup := startupCheckpointFileSetPreviewCommand(args, "--only", false)
				defer cleanup()
				if cmd == "" || strings.Contains(cmd, "{+") {
					t.Fatalf("invalid bounded preview command: %q", cmd)
				}
				path := diffPreviewTestPath(t, dir)
				for _, focus := range []string{"src/a.go", "src/new.go", "src/clean.go", ""} {
					legacyArgs := append([]string{"--internal-file-preview", "--internal-file-path", focus}, args...)
					legacyArgs = append(legacyArgs, "--only", "src/a.go", "src/new.go")
					legacy := FilePreviewConfigFromParsedCommand(parseInProject(t, project, legacyArgs))
					next := FilePreviewConfigFromParsedCommand(parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", focus, "--internal-diff-preview-state", path}))
					var want, got bytes.Buffer
					if err := RunInternalFilePreview(legacy, &want); err != nil {
						t.Fatal(err)
					}
					if err := RunInternalFilePreview(next, &got); err != nil {
						t.Fatal(err)
					}
					if got.String() != want.String() {
						t.Fatalf("focus %q changed preview:\n%s\nwant:\n%s", focus, &got, &want)
					}
					if focus == "src/a.go" && got.Len() == 0 {
						t.Fatal("diff fixture did not produce output")
					}
				}
				if len(events) != 0 {
					t.Fatalf("diff preview enumerated membership: %+v", events)
				}
				cleanup()
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("preview state survived cleanup: %v", err)
				}
			})
		}
	}
}

func TestDiffPreviewTransportLargeSelectionAndFailure(t *testing.T) {
	project := setupTestProject(t, nil)
	t.Chdir(project)
	dir := diffPreviewTestTemp(t)
	args := make([]string, 10000)
	for i := range args {
		args[i] = fmt.Sprintf("selected folder/file-%05d.go", i)
	}
	args = append(args, "--staged-diff")
	var events []search.MembershipEnumerationEvent
	restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) { events = append(events, e) })
	defer restore()
	cmd, cleanup := prepareDiffPreview(args)
	defer cleanup()
	if len(cmd) > 4096 || !strings.Contains(cmd, "--internal-diff-preview-state") || strings.Contains(cmd, "selected folder") || strings.Contains(cmd, "{+") {
		t.Fatalf("selection leaked into command: %q", cmd)
	}
	path := diffPreviewTestPath(t, dir)
	cfg := FilePreviewConfigFromParsedCommand(parseInProject(t, project, []string{"--internal-file-preview", "--internal-diff-preview-state", path}))
	got, err := filePreviewWithDiffState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	scope := internalPreviewScope(got)
	if !scope.Diff || !scope.Staged || scope.Unstaged || internalPreviewRelPath(got) != "" {
		t.Fatalf("lost diff semantics or invented all-row fallback: %+v", scope)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 4096 {
		t.Fatalf("unused targets retained in state: %d bytes, %v", len(data), err)
	}
	if !reflect.DeepEqual(cfg.Scopes[0].Targets, []string{"."}) {
		t.Fatal("loading preview state mutated caller")
	}
	// A descriptor write failure must omit the preview, never expand targets.
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, blocker)
	}
	cmd, failedCleanup := prepareDiffPreview(args)
	failedCleanup()
	if cmd != "" {
		t.Fatalf("unsafe fallback on write failure: %q", cmd)
	}
	if len(events) != 0 {
		t.Fatalf("large preview setup enumerated membership: %+v", events)
	}
}

func TestDiffPreviewTransportRejectsInvalidState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	base := filePreviewConfig{DiffStatePath: path, Scopes: []command.ExecutionScope{{Targets: []string{"."}}}}
	for _, data := range []string{
		`{`, `{"version":2,"scope":{"Diff":true}}`, `{"version":1,"scope":{}}`,
		`{"version":1,"scope":{"Diff":true,"Targets":["a","b"]}}`,
		`{"version":1,"scope":{"Diff":true,"Targets":[""]}}`,
		`{"version":1,"scope":{"Diff":true,"Targets":["../outside"]}}`,
		`{"version":1,"scope":{"Diff":true},"unknown":1}`,
		`{"version":1,"scope":{"Diff":true}} {}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := filePreviewWithDiffState(base); err == nil {
			t.Fatalf("accepted malformed state: %s", data)
		}
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"scope":{"Diff":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := filePreviewWithDiffState(base); err != nil {
		t.Fatalf("valid state failed: %v", err)
	}
	for _, mutate := range []func(*filePreviewConfig){
		func(c *filePreviewConfig) { c.CheckpointPath = "scope.json" },
		func(c *filePreviewConfig) { c.SearchingHint = true },
		func(c *filePreviewConfig) { c.Scopes = []command.ExecutionScope{{Targets: []string{"src"}}} },
		func(c *filePreviewConfig) {
			c.Scopes = []command.ExecutionScope{{Targets: []string{"."}, NoIgnore: true}}
		},
		func(c *filePreviewConfig) {
			c.Scopes = []command.ExecutionScope{{Targets: []string{"."}, Stages: []command.Stage{{Kind: command.StageChangedDiff}}}}
		},
	} {
		cfg := base
		mutate(&cfg)
		if _, err := filePreviewWithDiffState(cfg); err == nil {
			t.Fatalf("accepted conflicting inputs: %+v", cfg)
		}
	}
}
