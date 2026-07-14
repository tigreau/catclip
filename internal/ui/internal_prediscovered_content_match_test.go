package ui

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestPrediscoveredContentMatchListUsesCheckpointForGlobTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"keep.go":      "package sample\n\nfunc Keep() {}\n",
		"drop_test.go": "package sample\n\nfunc Drop() {}\n",
	})
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	keepPath := filepath.Join(project, "keep.go")
	if err := discovery.WriteCheckpoint(checkpointPath, project, discovery.CheckpointData{
		Entries: []discovery.Entry{{
			RelPath: "keep.go",
			AbsPath: keepPath,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := parseInProject(t, project, []string{
		"--internal-content-match-list",
		"--internal-prediscovered", checkpointPath,
		"*.go",
		"--snippet", "func",
	})
	var stdout bytes.Buffer
	if err := RunInternalPrediscoveredContentMatchList(PrediscoveredCommandConfigFromParsedCommand(cfg), &stdout); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "keep.go\tkeep.go\tkeep.go\tfile\ttext") {
		t.Fatalf("expected checkpoint match row, got %q", out)
	}
	if strings.Contains(out, "drop_test.go") {
		t.Fatalf("reload widened beyond checkpoint entries: %q", out)
	}
}
