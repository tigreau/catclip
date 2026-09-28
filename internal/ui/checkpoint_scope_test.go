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
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/search"
)

func TestCheckpointScopeTreeAndContentParity(t *testing.T) {
	for _, args := range [][]string{
		{"src"}, {"src", "--paths"}, {"src", "--lines", "1", "1"},
		{"src", "--snippet", "TODO"}, {"src", "--only", "*.go", "--not-contains", "skip"},
		{"src", "--no-ignore", "--only", "*.go", "--contains", "TODO", "--lines", "1", "2"},
		{"src", "--recent", "2"}, {"src", "--paths", "--then", "src", "--lines", "1", "1"},
		{"src", "--changed-diff"}, {"src", "--staged-diff"}, {"src", "--unstaged-diff"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			project := setupTestProject(t, map[string]string{
				".gitignore": "hidden/\n", "src/a.go": "package a\n// TODO work\n",
				"src/b.go": "package b\n// TODO skip\n", "src/c.txt": "plain\n",
				"src/hidden/secret.go": "// TODO hidden\n", "outside.go": "// TODO outside\n",
			})
			t.Chdir(project)
			if strings.HasSuffix(args[len(args)-1], "-diff") {
				initGitRepo(t, project)
				if err := os.WriteFile(filepath.Join(project, "src/a.go"), []byte("package a\n// TODO staged\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				runGit(t, project, "add", "src/a.go")
				if err := os.WriteFile(filepath.Join(project, "src/a.go"), []byte("package a\n// TODO unstaged\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			scopeViewMemoReset()
			defer scopeViewMemoReset()
			view, err := resolvedCurrentScopeViewForArgs(args)
			if err != nil {
				t.Fatal(err)
			}
			data := discovery.CheckpointData{Scope: &view.Scope, Entries: view.Entries, GitContext: view.GitContext, NoIgnore: view.Scope.NoIgnore}
			path := filepath.Join(t.TempDir(), "scope.json")
			if err := discovery.WriteCheckpoint(path, project, data); err != nil {
				t.Fatal(err)
			}
			want, err := output.BuildPlanForResolvedScopes(view.GitContext, []command.ExecutionScope{view.Scope},
				[]output.EvaluatedScope{{Paths: view.Scope.Paths, Entries: view.Entries}}, append([]discovery.Entry(nil), view.Entries...))
			if err != nil {
				t.Fatal(err)
			}
			cfg := PrediscoveredCommandConfigFromParsedCommand(parseInProject(t, project, []string{"--internal-tree-preview", "--internal-prediscovered", path, "--internal-checkpoint-scope"}))
			benchLog := filepath.Join(t.TempDir(), "static-preview.log")
			t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", benchLog)
			var events []search.MembershipEnumerationEvent
			restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) { events = append(events, e) })
			got, _, err := buildPrediscoveredTreePlan(cfg)
			restore()
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 0 {
				t.Fatalf("static preview enumerated membership: %+v", events)
			}
			log, err := os.ReadFile(benchLog)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(log), `event="search.rg.`) {
				t.Fatalf("static preview repeated a content scan:\n%s", log)
			}
			var actual, expected bytes.Buffer
			for i, plan := range []output.Plan{got, want} {
				buf := &actual
				if i == 1 {
					buf = &expected
				}
				if err := RenderTreePreviewFromPlan(buf, cfg.Render, view.GitContext, plan, nil, FzfFilterTreeRenderOptions(), nil); err != nil {
					t.Fatal(err)
				}
			}
			if actual.String() != expected.String() {
				t.Fatalf("retained tree differs:\n%s\nwant:\n%s", &actual, &expected)
			}
			if !reflect.DeepEqual(got.DistinctRelPaths(), want.DistinctRelPaths()) {
				t.Fatal("lost selected order")
			}
			if view.Scope.OutputMode() == command.EntryModeDiff {
				return
			} // content pickers are unavailable after diffs
			for _, flag := range []string{"--contains", "--not-contains", "--snippet"} {
				var rendered [2]bytes.Buffer
				for i := 0; i < 2; i++ {
					live := []string{"--internal-content-match-list", "--internal-prediscovered", path}
					if i == 0 {
						live = append(live, view.Scope.Targets...)
					} else {
						live = append(live, "--internal-checkpoint-scope")
					}
					live = append(live, flag, "TODO")
					liveCfg := PrediscoveredCommandConfigFromParsedCommand(parseInProject(t, project, live))
					if err := RunInternalPrediscoveredContentMatchList(liveCfg, &rendered[i]); err != nil {
						t.Fatal(err)
					}
				}
				if rendered[0].String() != rendered[1].String() {
					t.Fatalf("%s transport changed rows:\n%s\nwant:\n%s", flag, &rendered[1], &rendered[0])
				}
			}
		})
	}
}

func TestCheckpointScopeRejectsMissingOrConflictingState(t *testing.T) {
	scope := command.ExecutionScope{Targets: []string{"src"}}
	for _, tc := range []struct {
		live    command.ExecutionScope
		data    discovery.CheckpointData
		content bool
	}{
		{live: command.ExecutionScope{Targets: []string{"."}}},
		{live: command.ExecutionScope{Targets: []string{"elsewhere"}}, data: discovery.CheckpointData{Scope: &scope}},
		{live: command.ExecutionScope{Targets: []string{"."}, Stages: []command.Stage{{Kind: command.StageNoIgnore}}}, data: discovery.CheckpointData{Scope: &scope}},
		{live: command.ExecutionScope{Targets: []string{"."}}, data: discovery.CheckpointData{Scope: &scope}, content: true},
	} {
		if _, err := scopeFromCheckpoint(tc.live, tc.data, tc.content); err == nil {
			t.Fatal("accepted missing/conflicting retained scope")
		}
	}
}

func TestLargeModifierTargetRootsAreRetainedAndCleaned(t *testing.T) {
	t.Chdir(t.TempDir())
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	var entries []discovery.Entry
	var args []string
	metadata := make(map[string]search.FileMetadata)
	for i := 0; i < 10000; i++ {
		path := fmt.Sprintf("selected/file-%05d.go", i)
		args = append(args, path)
		entries = append(entries, discovery.Entry{RelPath: path, SizeKnown: true, SizeBytes: 1, Mode: command.EntryModeFull})
		metadata[path] = search.FileMetadata{SizeBytes: 1}
	}
	if !scopeViewMemoAdoptTargetSelection(args, git.Context{}, entries, metadata) {
		t.Fatal("adoption failed")
	}
	state, view, err := startupCurrentScopeStateForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd, dir := startupModifierCurrentScopePreviewCommand(args, state, view)
	if dir != "" || len(cmd) > 4096 || !strings.Contains(cmd, "--internal-target-roots") || strings.Contains(cmd, "selected/file-") {
		t.Fatalf("unbounded modifier handoff: %d bytes, %s", len(cmd), cmd)
	}
	path, ok := scopeViewMemoTargetRoots(args)
	if !ok {
		t.Fatal("missing retained roots")
	}
	got, err := discovery.ReadTargetRoots(path)
	if err != nil || !reflect.DeepEqual(got, args) {
		t.Fatalf("lost roots: %d, %v", len(got), err)
	}
	again, cleanup := startupModifierCurrentScopePreviewCommand(args, state, view)
	if again != cmd || cleanup != "" {
		t.Fatal("same-state preview did not reuse transport")
	}
	scopeViewMemoReset()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("descriptor outlived its state: %v", err)
	}
}
