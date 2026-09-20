package catclip

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

// Exercise the actual helper process on every CI OS, including native Windows.
// This checks CLI/descriptor/render transport, not fzf's separate shell layer.
func TestDiffPreviewStateNativeProcess(t *testing.T) {
	project := setupTestProject(t, map[string]string{"source folder/a.go": "package before\n"})
	initGitRepo(t, project)
	if err := os.WriteFile(filepath.Join(project, "source folder/a.go"), []byte("package after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "diff state.json")
	for _, targets := range [][]string{{"source folder/a.go"}, nil} {
		data, err := json.Marshal(struct {
			Version int                    `json:"version"`
			Scope   command.ExecutionScope `json:"scope"`
		}{1, command.ExecutionScope{Targets: targets, Diff: true, Changed: true, Unstaged: true}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, focus := range []string{"source folder/a.go", ""} {
			args := []string{"--quiet", "--internal-file-preview", "--internal-diff-preview-state", statePath, "--internal-file-path", focus}
			var want bytes.Buffer
			if err := run(parseInProject(t, project, args), &want, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if (focus != "" || len(targets) == 1) && want.Len() == 0 {
				t.Fatal("fixture produced no diff")
			}
			if focus == "" && len(targets) == 0 && want.Len() != 0 {
				t.Fatal("multi-target all row must not acquire a fallback")
			}
			child := exec.Command(os.Args[0], args...)
			child.Dir = project
			child.Env = nativePreviewTestEnv(t)
			var stderr bytes.Buffer
			child.Stderr = &stderr
			got, err := child.Output()
			if err != nil || !bytes.Equal(got, want.Bytes()) {
				t.Fatalf("native diff preview mismatch: err=%v stderr=%s\ngot=%s\nwant=%s", err, &stderr, got, &want)
			}
		}
	}
}

func TestDiffPreviewStateRejectsOtherPreviewModes(t *testing.T) {
	for _, cfg := range []internalCommandConfig{
		{DiffPreviewStatePath: "state.json"},
		{DiffPreviewStatePath: "state.json", FilePreview: true, TreePreview: true},
		{DiffPreviewStatePath: "state.json", FilePreview: true, ContentMatchList: true},
		{DiffPreviewStatePath: "state.json", FilePreview: true, PrediscoveredPath: "scope.json"},
		{DiffPreviewStatePath: "state.json", FilePreview: true, FileSearchingPreview: true},
		{DiffPreviewStatePath: "state.json", FilePreview: true, SnippetBoundaryPreview: true},
		{DiffPreviewStatePath: "state.json", FilePreview: true, RecentPreview: true},
		{DiffPreviewStatePath: "state.json", FilePreview: true, LinesPreview: true},
		{DiffPreviewStatePath: "state.json", FilePreview: true, SinkTogglePath: "toggle"},
		{DiffPreviewStatePath: "state.json", FilePreview: true, SinkPreviewModePath: "mode"},
	} {
		if err := validateImplementedFeatureSet(cfg); err == nil {
			t.Fatalf("accepted conflicting internal preview config: %+v", cfg)
		}
	}
}
