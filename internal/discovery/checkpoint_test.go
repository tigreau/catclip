package discovery

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
)

func TestMarshalCheckpointUsesCompactJSON(t *testing.T) {
	raw, err := MarshalCheckpoint(CheckpointData{
		Entries: []Entry{{RelPath: "src/main.go", Mode: command.EntryModeFull}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("\n  ")) || bytes.Count(raw, []byte("\n")) != 1 || raw[len(raw)-1] != '\n' {
		t.Fatalf("checkpoint is not one compact JSON document: %q", raw)
	}
	if _, err := UnmarshalCheckpoint(raw); err != nil {
		t.Fatalf("compact checkpoint did not round-trip: %v", err)
	}
}

func TestCheckpointNoIgnoreRoundtrip(t *testing.T) {
	cases := []struct {
		name     string
		noIgnore bool
	}{
		{"noIgnore=false (default)", false},
		{"noIgnore=true (parent bypassed ignore rules)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scope.json")
			in := CheckpointData{
				GitContext: git.Context{},
				GitStatus:  map[string]string{},
				Entries:    []Entry{{RelPath: "a.txt"}},
				NoIgnore:   tc.noIgnore,
			}
			if err := WriteCheckpoint(path, t.TempDir(), in); err != nil {
				t.Fatalf("WriteCheckpoint: %v", err)
			}
			out, err := ReadCheckpoint(path)
			if err != nil {
				t.Fatalf("ReadCheckpoint: %v", err)
			}
			if out.NoIgnore != tc.noIgnore {
				t.Fatalf("NoIgnore round-trip: got %v, want %v", out.NoIgnore, tc.noIgnore)
			}
		})
	}
}

func TestCheckpointRoundTripsEveryOutputProjection(t *testing.T) {
	in := CheckpointData{
		GitContext: git.Context{Enabled: true, Root: "/repo", WorkPrefix: "sub", HasHead: true},
		GitStatus:  map[string]string{"full.txt": "M"},
		NoIgnore:   true,
		Entries: []Entry{
			{RelPath: "full.txt", Mode: command.EntryModeFull, GitVisible: true, IgnoreBypassed: true, BlockSource: ".gitignore"},
			{RelPath: "lines.txt", Mode: command.EntryModeLines, Lines: true, LinesStart: 2, LinesEnd: 4},
			{
				RelPath:             "snippet.txt",
				Mode:                command.EntryModeSnippet,
				SnippetPattern:      "TODO|FIXME",
				SnippetContextSet:   true,
				SnippetContextLines: 3,
				SnippetMatchLines:   []int{5, 11},
			},
			{RelPath: "diff.txt", Mode: command.EntryModeDiff, DiffWantStaged: true, DiffWantUnstaged: true},
		},
	}
	raw, err := MarshalCheckpoint(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnmarshalCheckpoint(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("checkpoint changed output projection:\ngot  %+v\nwant %+v", out, in)
	}

	// Decoded collections belong to that read. Mutating one decode must not
	// alter the serialized source or a later decode.
	out.GitStatus["full.txt"] = "?"
	out.Entries[2].SnippetMatchLines[0] = 999
	again, err := UnmarshalCheckpoint(raw)
	if err != nil {
		t.Fatal(err)
	}
	if again.GitStatus["full.txt"] != "M" || !reflect.DeepEqual(again.Entries[2].SnippetMatchLines, []int{5, 11}) {
		t.Fatalf("checkpoint decode retained mutable aliases: %+v", again)
	}
}

func TestApplyPrediscoveredScopeTailNoIgnorePreservesIgnoreAttribution(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	for rel, contents := range map[string]string{
		".gitignore":                    "src/build/\noutside/\n",
		"outside/secret.txt":            "must stay outside src scope\n",
		"src/main.ts":                   "export const main = true\n",
		"src/build/generated/client.ts": "export const generated = true\n",
	} {
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	scope := command.ExecutionScope{
		Targets:  []string{"src"},
		NoIgnore: true,
		Stages:   []command.Stage{{Kind: command.StageNoIgnore}},
	}
	entries, err := ApplyPrediscoveredScopeTail(
		command.Invocation{WorkingDir: project},
		git.Context{},
		scope,
		[]Entry{{RelPath: "src/main.ts", GitVisible: true}},
	)
	if err != nil {
		t.Fatalf("ApplyPrediscoveredScopeTail: %v", err)
	}
	attribution := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		attribution[entry.RelPath] = entry
	}
	visible := attribution["src/main.ts"]
	if visible.IgnoreBypassed || visible.BlockSource != "" {
		t.Fatalf("visible checkpoint entry was marked ignored: %+v", visible)
	}
	ignored := attribution["src/build/generated/client.ts"]
	if !ignored.IgnoreBypassed || ignored.BlockSource != ".gitignore" {
		t.Fatalf("ignored checkpoint entry attribution = %+v", ignored)
	}
	if outside, ok := attribution["outside/secret.txt"]; ok {
		t.Fatalf("no-ignore checkpoint replay escaped src target: %+v", outside)
	}
}

func TestApplyPrediscoveredScopeTailNoIgnoreRecoversEmptyIgnoredTarget(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	for rel, contents := range map[string]string{
		".gitignore":        "secret/\n",
		"secret/config.txt": "ignored configuration\n",
	} {
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	scope := command.ExecutionScope{
		Targets:  []string{"secret"},
		NoIgnore: true,
		Stages:   []command.Stage{{Kind: command.StageNoIgnore}},
	}
	entries, err := ApplyPrediscoveredScopeTail(command.Invocation{WorkingDir: project}, git.Context{}, scope, nil)
	if err != nil {
		t.Fatalf("ApplyPrediscoveredScopeTail: %v", err)
	}
	if len(entries) != 1 || entries[0].RelPath != "secret/config.txt" {
		t.Fatalf("empty checkpoint no-ignore recovery = %+v", entries)
	}
	if !entries[0].IgnoreBypassed || entries[0].BlockSource != ".gitignore" {
		t.Fatalf("recovered ignored attribution = %+v", entries[0])
	}
}

func TestApplyPrediscoveredScopeTailNoIgnoreRespectsGlobTarget(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	for rel, contents := range map[string]string{
		".gitignore":          "generated/\n",
		"generated/hidden.js": "export const hidden = true\n",
		"generated/hidden.ts": "export const hidden = true\n",
		"src/main.ts":         "export const main = true\n",
	} {
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	scope := command.ExecutionScope{
		Targets:  []string{"*.ts"},
		NoIgnore: true,
		Stages:   []command.Stage{{Kind: command.StageNoIgnore}},
	}
	entries, err := ApplyPrediscoveredScopeTail(
		command.Invocation{WorkingDir: project},
		git.Context{},
		scope,
		[]Entry{{RelPath: "src/main.ts", GitVisible: true}},
	)
	if err != nil {
		t.Fatalf("ApplyPrediscoveredScopeTail: %v", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.RelPath)
	}
	if got, want := paths, []string{"src/main.ts", "generated/hidden.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("no-ignore glob checkpoint expansion = %v, want %v", got, want)
	}
	if !entries[1].IgnoreBypassed || entries[1].BlockSource != ".gitignore" {
		t.Fatalf("glob-recovered ignored attribution = %+v", entries[1])
	}
}

func TestApplyPrediscoveredScopeTailNoIgnoreAppliesLaterStagesFromEmptyCheckpoint(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	for rel, contents := range map[string]string{
		".gitignore":          "secret/\n",
		"secret/keep.md":      "keep\n",
		"secret/drop.txt":     "drop\n",
		"secret/also-drop.md": "drop by exclude\n",
	} {
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	scope := command.ExecutionScope{
		Targets:  []string{"secret"},
		NoIgnore: true,
		Stages: []command.Stage{
			{Kind: command.StageNoIgnore},
			{Kind: command.StageOnly, Values: []string{"*.md"}},
			{Kind: command.StageExclude, Values: []string{"also-drop.md"}},
		},
	}
	entries, err := ApplyPrediscoveredScopeTail(command.Invocation{WorkingDir: project}, git.Context{}, scope, nil)
	if err != nil {
		t.Fatalf("ApplyPrediscoveredScopeTail: %v", err)
	}
	if len(entries) != 1 || entries[0].RelPath != "secret/keep.md" {
		t.Fatalf("empty checkpoint stage replay = %+v, want only secret/keep.md", entries)
	}
	if !entries[0].IgnoreBypassed || entries[0].BlockSource != ".gitignore" {
		t.Fatalf("filtered ignored entry attribution = %+v", entries[0])
	}
}

func TestApplyPrediscoveredScopeTailNoIgnoreKeepsMultipleTargetsBounded(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	for rel, contents := range map[string]string{
		".gitignore":           "src/generated/\ndocs/private/\noutside/\n",
		"src/main.ts":          "visible src\n",
		"src/generated/a.ts":   "ignored src\n",
		"docs/guide.md":        "visible docs\n",
		"docs/private/note.md": "ignored docs\n",
		"outside/secret.txt":   "must stay outside\n",
	} {
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	scope := command.ExecutionScope{
		Targets:  []string{"src", "docs"},
		NoIgnore: true,
		Stages:   []command.Stage{{Kind: command.StageNoIgnore}},
	}
	entries, err := ApplyPrediscoveredScopeTail(
		command.Invocation{WorkingDir: project},
		git.Context{},
		scope,
		[]Entry{
			{RelPath: "src/main.ts", TargetRoot: "src", GitVisible: true},
			{RelPath: "docs/guide.md", TargetRoot: "docs", GitVisible: true},
		},
	)
	if err != nil {
		t.Fatalf("ApplyPrediscoveredScopeTail: %v", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.RelPath)
	}
	want := []string{"src/main.ts", "docs/guide.md", "src/generated/a.ts", "docs/private/note.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("multi-target no-ignore replay = %v, want %v", paths, want)
	}
	for _, entry := range entries[2:] {
		if !entry.IgnoreBypassed || entry.BlockSource != ".gitignore" {
			t.Fatalf("ignored multi-target entry attribution = %+v", entry)
		}
	}
}

func TestApplyPrediscoveredScopeTailNoIgnoreSupportsIgnoredFileTarget(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	for rel, contents := range map[string]string{
		".gitignore": "secret.txt\nother.txt\n",
		"secret.txt": "selected\n",
		"other.txt":  "must stay outside\n",
	} {
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	scope := command.ExecutionScope{
		Targets:  []string{"secret.txt"},
		NoIgnore: true,
		Stages:   []command.Stage{{Kind: command.StageNoIgnore}},
	}
	entries, err := ApplyPrediscoveredScopeTail(command.Invocation{WorkingDir: project}, git.Context{}, scope, nil)
	if err != nil {
		t.Fatalf("ApplyPrediscoveredScopeTail: %v", err)
	}
	if len(entries) != 1 || entries[0].RelPath != "secret.txt" {
		t.Fatalf("ignored file target replay = %+v", entries)
	}
	if !entries[0].IgnoreBypassed || entries[0].BlockSource != ".gitignore" {
		t.Fatalf("ignored file target attribution = %+v", entries[0])
	}
}

func TestApplyPrediscoveredScopeTailNoIgnoreKeepsCaseCollidingFilesDistinct(t *testing.T) {
	project := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	if err := os.WriteFile(filepath.Join(project, ".gitignore"), []byte("Case.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Case.txt"), []byte("ignored uppercase\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "case.txt"), []byte("visible lowercase\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	upperInfo, err := os.Stat(filepath.Join(project, "Case.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lowerInfo, err := os.Stat(filepath.Join(project, "case.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(upperInfo, lowerInfo) {
		t.Skip("filesystem does not support distinct case-colliding fixture paths")
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	scope := command.ExecutionScope{
		Targets:  []string{"."},
		NoIgnore: true,
		Stages:   []command.Stage{{Kind: command.StageNoIgnore}},
	}
	entries, err := ApplyPrediscoveredScopeTail(
		command.Invocation{WorkingDir: project},
		git.Context{},
		scope,
		[]Entry{{RelPath: "case.txt", GitVisible: true}},
	)
	if err != nil {
		t.Fatalf("ApplyPrediscoveredScopeTail: %v", err)
	}
	byPath := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		byPath[entry.RelPath] = entry
	}
	if got := byPath["case.txt"]; got.IgnoreBypassed || got.BlockSource != "" {
		t.Fatalf("visible lowercase path attribution = %+v", got)
	}
	if got := byPath["Case.txt"]; !got.IgnoreBypassed || got.BlockSource != ".gitignore" {
		t.Fatalf("ignored uppercase path attribution = %+v", got)
	}
}
