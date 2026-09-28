package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
)

func TestSharedCheckpointPreservesProjectionAndOverrides(t *testing.T) {
	dir := t.TempDir()
	base := []Entry{
		{RelPath: "src/a.go", SizeKnown: true, SizeBytes: 100, ModTime: time.Unix(1700000000, 0).UTC(), Mode: command.EntryModeFull},
		{RelPath: "other.go", SizeKnown: true, SizeBytes: 200, Mode: command.EntryModeFull},
		{RelPath: "ignored.go", SizeKnown: true, SizeBytes: 300, IgnoreBypassed: true, BlockSource: ".hiss", Mode: command.EntryModeFull},
	}
	ref, err := WriteCheckpointInventory(filepath.Join(dir, "inventory.bin"), base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []command.EntryMode{command.EntryModeFull, command.EntryModeLines, command.EntryModeSnippet, command.EntryModeDiff} {
		t.Run(string(mode), func(t *testing.T) {
			entries := []Entry{base[2], base[0], base[0]}
			for i := range entries {
				entries[i].Mode = mode
			}
			entries[1].TargetRoot = "src"
			entries[1].GitVisible = true
			switch mode {
			case command.EntryModeLines:
				entries[1].Lines, entries[1].LinesStart, entries[1].LinesEnd = true, 2, 5
				entries[2].Lines, entries[2].LinesStart, entries[2].LinesEnd = true, 10, 20
			case command.EntryModeSnippet:
				for i := range entries {
					entries[i].SnippetPattern = "needle"
					entries[i].SnippetContextSet = true
					entries[i].SnippetContextLines = 2
					entries[i].SnippetMatchLines = []int{i + 1, i + 5}
				}
			case command.EntryModeDiff:
				entries[0].DiffWantStaged = true
				entries[1].DiffWantUnstaged = true
			}
			data := CheckpointData{Scope: &command.ExecutionScope{Targets: []string{"src", "ignored.go"}, Paths: true}, Entries: entries, GitStatus: map[string]string{"src/a.go": "M"}, NoIgnore: true}
			path := filepath.Join(t.TempDir(), "scope.json")
			if err := WriteSharedCheckpoint(path, ref, base, []uint32{2, 0, 0}, data); err != nil {
				t.Fatal(err)
			}
			got, err := ReadCheckpoint(path)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := MarshalCheckpoint(data)
			if err != nil {
				t.Fatal(err)
			}
			want, err := UnmarshalCheckpoint(legacy)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("shared/standalone mismatch:\ngot %+v\nwant %+v", got, want)
			}
			got.Entries[0].RelPath = "corrupt"
			got.Scope.Targets[0] = "corrupt"
			got.GitStatus["src/a.go"] = "bad"
			if len(got.Entries[0].SnippetMatchLines) > 0 {
				got.Entries[0].SnippetMatchLines[0] = 999
			}
			again, err := ReadCheckpoint(path)
			if err != nil || !reflect.DeepEqual(again, want) {
				t.Fatalf("read ownership changed: %v", err)
			}
		})
	}
}

func TestSharedCheckpointRejectsWrongInventoryAndInvalidIDs(t *testing.T) {
	dir := t.TempDir()
	base := []Entry{{RelPath: "a.go", SizeKnown: true, Mode: command.EntryModeFull}}
	ref, err := WriteCheckpointInventory(filepath.Join(dir, "base.bin"), base)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scope.json")
	if err := WriteSharedCheckpoint(path, ref, base, []uint32{0}, CheckpointData{Entries: base}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"token", "id", "replacement", "snippet", "projection", "version"} {
		t.Run(scenario, func(t *testing.T) {
			var doc checkpointDocument
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "token":
				doc.Inventory.Token = "wrong"
			case "id":
				doc.FileIDs[0] = 100
			case "replacement":
				doc.Replacements = map[int]CheckpointEntry{0: {RelPath: "other.go"}}
			case "snippet":
				doc.SnippetLines = map[int][]int{9: {1}}
			case "projection":
				doc.Projection = nil
			case "version":
				doc.Version = 1
			}
			bad, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := UnmarshalCheckpoint(bad); err == nil {
				t.Fatal("accepted corrupt shared checkpoint")
			}
		})
	}
	if err := os.Remove(ref.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCheckpoint(path); err == nil {
		t.Fatal("missing inventory did not fail")
	}
}
