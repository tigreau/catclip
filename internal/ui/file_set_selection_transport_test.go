package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

func TestReadFzfFileSetSelectionExtractsValuesFromRows(t *testing.T) {
	selectionPath := filepath.Join(t.TempDir(), "selection.tsv")
	rows := strings.Join([]string{
		"Button.tsx\tsrc/components/Button.tsx\tsrc\tdir\tok",
		"index.js\tnode_modules/pkg/index.js\t.\tdir\tok",
		"duplicate\tsrc/components/Button.tsx\tsrc\tdir\tok",
	}, "\n") + "\n"
	if err := os.WriteFile(selectionPath, []byte(rows), 0o600); err != nil {
		t.Fatalf("write selection: %v", err)
	}

	got, err := readFzfFileSetSelection(selectionPath)
	if err != nil {
		t.Fatalf("readFzfFileSetSelection returned error: %v", err)
	}
	want := []string{"src/components/Button.tsx", "node_modules/pkg/index.js"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("selection values = %q, want %q", got, want)
	}
}

func TestBuildPrediscoveredTreePlanAppliesFileBackedExcludeSelection(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"node_modules/pkg-a/index.js": "module.exports = 'a'\n",
		"node_modules/pkg-b/index.js": "module.exports = 'b'\n",
		"src/main.ts":                 "export const main = true\n",
	})
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	entries := []discovery.Entry{
		{RelPath: "node_modules/pkg-a/index.js"},
		{RelPath: "node_modules/pkg-b/index.js"},
		{RelPath: "src/main.ts"},
	}
	if err := discovery.WriteCheckpoint(checkpointPath, project, discovery.CheckpointData{Entries: entries}); err != nil {
		t.Fatalf("WriteCheckpoint returned error: %v", err)
	}

	selectionPath := filepath.Join(t.TempDir(), "selection.tsv")
	rows := "index.js\tnode_modules/pkg-a/index.js\t.\tdir\tok\n" +
		"index.js\tnode_modules/pkg-b/index.js\t.\tdir\tok\n"
	if err := os.WriteFile(selectionPath, []byte(rows), 0o600); err != nil {
		t.Fatalf("write selection: %v", err)
	}

	plan, _, err := buildPrediscoveredTreePlan(prediscoveredCommandConfig{
		CheckpointPath:        checkpointPath,
		FileSetSelectionPath:  selectionPath,
		FileSetSelectionStage: "exclude",
		Invocation:            command.Invocation{WorkingDir: project, Quiet: true, Internal: true},
		Scopes:                []command.ExecutionScope{{Targets: []string{"."}}},
	})
	if err != nil {
		t.Fatalf("buildPrediscoveredTreePlan returned error: %v", err)
	}
	if got, want := strings.Join(plan.DistinctRelPaths(), "\n"), "src/main.ts"; got != want {
		t.Fatalf("preview paths = %q, want %q", got, want)
	}
}
