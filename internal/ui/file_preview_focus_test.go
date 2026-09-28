package ui

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestContentAllMatchesFocusTransportParity(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"keep.go": "package sample\n\nfunc Keep() {}\n",
	})
	t.Chdir(project)
	t.Setenv("FZF_MATCH_COUNT", "1")
	checkpoint := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpoint, project, discovery.CheckpointData{
		Entries: []discovery.Entry{{RelPath: "keep.go", AbsPath: filepath.Join(project, "keep.go")}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--contains", "--not-contains", "--snippet"} {
		for _, state := range []string{"empty-query", "standalone", "checkpoint"} {
			t.Run(flag+"/"+state, func(t *testing.T) {
				pattern := "Keep"
				if flag == "--not-contains" {
					pattern = "absent"
				}
				if state == "empty-query" {
					pattern = ""
				}
				args := []string{"--internal-file-preview", "--internal-searching-preview",
					"--internal-file-path", "", "--internal-tree-target", contentMatchAllMatchesLabel,
					flag, pattern}
				if state == "checkpoint" {
					args = append(args, "--internal-prediscovered", checkpoint)
				}
				old := FilePreviewConfigFromParsedCommand(parseInProject(t, project, args))
				next := old
				next.FilePath = contentMatchAllMatchesFocusPath
				var want, got bytes.Buffer
				if err := RunInternalFilePreview(old, &want); err != nil {
					t.Fatal(err)
				}
				if err := RunInternalFilePreview(next, &got); err != nil {
					t.Fatal(err)
				}
				if got.String() != want.String() {
					t.Fatalf("synthetic focus changed preview:\ngot %s\nwant %s", &got, &want)
				}
				if next.FilePath != contentMatchAllMatchesFocusPath {
					t.Fatal("preview mutated caller config")
				}
				if state == "standalone" && got.Len() != 0 {
					t.Fatalf("standalone all-matches preview should stay empty: %s", &got)
				}
				if state == "checkpoint" && !strings.Contains(got.String(), "keep.go") {
					t.Fatalf("checkpoint tree lost retained file: %s", &got)
				}
				if state == "empty-query" && got.Len() == 0 {
					t.Fatal("empty query lost hint document")
				}
			})
		}
	}
}

func TestContentAllMatchesFocusDoesNotReinterpretRealFile(t *testing.T) {
	project := setupTestProject(t, map[string]string{contentMatchAllMatchesLabel: "unique file body\n"})
	t.Chdir(project)
	cfg := FilePreviewConfigFromParsedCommand(parseInProject(t, project, []string{
		"--internal-file-preview", "--internal-searching-preview",
		"--internal-file-path", contentMatchAllMatchesLabel,
		"--internal-tree-target", contentMatchAllMatchesLabel, "--contains", "unique",
	}))
	var got bytes.Buffer
	if err := RunInternalFilePreview(cfg, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ansiEscape.ReplaceAll(got.Bytes(), nil)), "unique file body") {
		t.Fatalf("real file was interpreted as synthetic row: %s", &got)
	}
	// A dot outside the exact content-picker sentinel must not open a checkpoint.
	for _, contentPicker := range []bool{false, true} {
		cfg.FilePath = "."
		cfg.SearchingHint = contentPicker
		cfg.FocusedLabel = contentMatchAllMatchesLabel
		if contentPicker {
			cfg.FocusedLabel = "ordinary directory"
		}
		cfg.CheckpointPath = filepath.Join(project, "nonexistent-checkpoint.json")
		got.Reset()
		if err := RunInternalFilePreview(cfg, &got); err != nil || got.Len() != 0 {
			t.Fatalf("unrelated dot was normalized: output=%q err=%v", &got, err)
		}
	}
}
