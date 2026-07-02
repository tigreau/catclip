package catclip

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/render"
)

func TestWritePrediscoveredCheckpointCapturesEntrySizes(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("a.txt", "hello\n") // 6 bytes
	write("b.txt", "hi\n")    // 3 bytes

	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, dir, discovery.CheckpointData{
		GitStatus: map[string]string{},
		Entries: []discovery.Entry{
			{RelPath: "a.txt"}, // AbsPath empty -> resolve via workingDir
			{RelPath: "b.txt", AbsPath: filepath.Join(dir, "b.txt")}, // AbsPath already set
			{RelPath: "kept.txt", SizeBytes: 99, SizeKnown: true},    // already known -> preserved, not re-stat'd
			{RelPath: "missing.txt"},                                 // no file -> graceful, stays unknown
		},
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}
	decoded, err := discovery.ReadCheckpoint(checkpointPath)
	if err != nil {
		t.Fatalf("discovery.ReadCheckpoint returned error: %v", err)
	}
	entries := decoded.Entries

	if !entries[0].SizeKnown || entries[0].SizeBytes != 6 {
		t.Errorf("a.txt (abs resolved from workingDir): want 6/known, got %d/%v", entries[0].SizeBytes, entries[0].SizeKnown)
	}
	if !entries[1].SizeKnown || entries[1].SizeBytes != 3 {
		t.Errorf("b.txt (abs preset): want 3/known, got %d/%v", entries[1].SizeBytes, entries[1].SizeKnown)
	}
	if !entries[2].SizeKnown || entries[2].SizeBytes != 99 {
		t.Errorf("kept.txt: already-known size must be preserved untouched, got %d/%v", entries[2].SizeBytes, entries[2].SizeKnown)
	}
	if entries[3].SizeKnown {
		t.Errorf("missing.txt: stat failure must leave SizeKnown=false (graceful), got size %d", entries[3].SizeBytes)
	}
}

func TestPrediscoveredCheckpointRoundtripPreservesData(t *testing.T) {
	modTime := time.Date(2026, 5, 17, 10, 11, 12, 345678901, time.UTC)
	data := discovery.CheckpointData{
		GitContext: git.Context{
			Enabled:    true,
			Root:       "/repo",
			WorkPrefix: "src",
			HasHead:    true,
		},
		GitStatus: map[string]string{
			"src/changed.go": "M",
			"src/new.go":     "?",
			"src/staged.go":  "S",
			"src/mixed.go":   "SM",
		},
		Entries: []discovery.Entry{
			{
				AbsPath:          "/repo/src/changed.go",
				RelPath:          "src/changed.go",
				ModTime:          modTime,
				SizeBytes:        1234,
				SizeKnown:        true,
				TargetRoot:       "src",
				GitVisible:       true,
				Mode:             command.EntryModeSnippet,
				SnippetPattern:   "func",
				Lines:            true,
				LinesStart:       3,
				LinesEnd:         8,
				DiffWantStaged:   true,
				DiffWantUnstaged: true,
				AllowedByInclude: true,
				BlockSource:      ".gitignore",
			},
			{
				AbsPath:    "/repo/src/unknown.go",
				RelPath:    "src/unknown.go",
				ModTime:    modTime.Add(time.Hour),
				TargetRoot: "src",
				GitVisible: true,
				Mode:       command.EntryModeFull,
			},
			{
				AbsPath:    "/repo/src/empty.go",
				RelPath:    "src/empty.go",
				ModTime:    modTime.Add(2 * time.Hour),
				SizeBytes:  0,
				SizeKnown:  true,
				TargetRoot: "src",
				GitVisible: true,
				Mode:       command.EntryModeLines,
				Lines:      true,
			},
			{
				AbsPath:          "/repo/src/diff.go",
				RelPath:          "src/diff.go",
				ModTime:          modTime.Add(3 * time.Hour),
				SizeBytes:        99,
				SizeKnown:        true,
				TargetRoot:       "src",
				GitVisible:       true,
				Mode:             command.EntryModeDiff,
				DiffWantUnstaged: true,
			},
		},
	}

	raw, err := discovery.MarshalCheckpoint(data)
	if err != nil {
		t.Fatalf("discovery.MarshalCheckpoint returned error: %v", err)
	}
	decoded, err := discovery.UnmarshalCheckpoint(raw)
	if err != nil {
		t.Fatalf("discovery.UnmarshalCheckpoint returned error: %v", err)
	}

	// AbsPath is intentionally not serialized — it is re-derived at read by
	// discovery.EnsureEntryAbsPaths (workingDir + RelPath). Reset it on the expected
	// entries so the round-trip comparison reflects that contract.
	for i := range data.Entries {
		data.Entries[i].AbsPath = ""
	}

	if !reflect.DeepEqual(decoded, data) {
		t.Fatalf("decoded checkpoint differs from input\n got: %#v\nwant: %#v\njson:\n%s", decoded, data, string(raw))
	}

	data.GitStatus["src/changed.go"] = "?"
	if decoded.GitStatus["src/changed.go"] != "M" {
		t.Fatalf("decoded git status map was aliased to input: %#v", decoded.GitStatus)
	}
}

func TestPrediscoveredCheckpointFileEntrySchemaCoversAllFields(t *testing.T) {
	wantSerialized := []string{
		"RelPath",
		"ModTime",
		"SizeBytes",
		"SizeKnown",
		"TargetRoot",
		"GitVisible",
		"Mode",
		"SnippetPattern",
		"SnippetContextSet",
		"SnippetContextLines",
		"Lines",
		"LinesStart",
		"LinesEnd",
		"DiffWantStaged",
		"DiffWantUnstaged",
		"AllowedByInclude",
		"BlockSource",
	}
	// AbsPath is a runtime-derived filesystem handle, intentionally not
	// serialized — re-derived at read by discovery.EnsureEntryAbsPaths.
	wantResetOnRoundtrip := []string{"AbsPath"}

	// discovery.Entry must be fully accounted for: every exported field is either
	// serialized or intentionally reset on round-trip. Compare as sets —
	// declaration order is irrelevant to coverage (AbsPath is declared first
	// but reset, so order would otherwise mismatch).
	covered := append([]string(nil), wantSerialized...)
	covered = append(covered, wantResetOnRoundtrip...)
	got := exportedFieldNames(reflect.TypeOf(discovery.Entry{}))
	slices.Sort(got)
	slices.Sort(covered)
	if !reflect.DeepEqual(got, covered) {
		t.Fatalf("discovery.Entry schema decision list is stale\n got: %v\nwant serialized + reset-on-roundtrip: %v", got, covered)
	}

	if got := exportedFieldNames(reflect.TypeOf(discovery.CheckpointEntry{})); !reflect.DeepEqual(got, wantSerialized) {
		t.Fatalf("discovery.CheckpointEntry fields do not match serialized discovery.Entry fields\n got: %v\nwant: %v", got, wantSerialized)
	}
}

func TestPrediscoveredCheckpointJSONFieldNames(t *testing.T) {
	data := discovery.CheckpointData{
		GitContext: git.Context{
			Enabled:    true,
			Root:       "/repo",
			WorkPrefix: "src",
			HasHead:    true,
		},
		GitStatus: map[string]string{"src/changed.go": "M"},
		Entries: []discovery.Entry{{
			AbsPath:          "/repo/src/changed.go",
			RelPath:          "src/changed.go",
			ModTime:          time.Date(2026, 5, 17, 10, 11, 12, 0, time.UTC),
			SizeBytes:        123,
			SizeKnown:        true,
			TargetRoot:       "src",
			GitVisible:       true,
			Mode:             command.EntryModeDiff,
			SnippetPattern:   "pattern",
			Lines:            true,
			LinesStart:       1,
			LinesEnd:         5,
			DiffWantStaged:   true,
			DiffWantUnstaged: true,
			AllowedByInclude: true,
			BlockSource:      ".gitignore",
		}},
	}

	raw, err := discovery.MarshalCheckpoint(data)
	if err != nil {
		t.Fatalf("discovery.MarshalCheckpoint returned error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	for _, key := range []string{"version", "git_context", "git_status", "entries"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("checkpoint json missing top-level key %q: %s", key, string(raw))
		}
	}

	entries, ok := doc["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("checkpoint json entries = %#v, want one entry", doc["entries"])
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("checkpoint entry has unexpected shape: %#v", entries[0])
	}
	for _, key := range []string{
		"rel",
		"mod_time",
		"size_bytes",
		"size_known",
		"target_root",
		"git_visible",
		"mode",
		"snippet_pattern",
		"lines",
		"lines_start",
		"lines_end",
		"diff_want_staged",
		"diff_want_unstaged",
		"allowed_by_include",
		"block_source",
	} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("checkpoint entry json missing key %q: %s", key, string(raw))
		}
	}
	if _, ok := entry["Mode"]; ok {
		t.Fatalf("checkpoint entry leaked Go field name Mode: %s", string(raw))
	}
}

func TestPrediscoveredCheckpointRejectsUnsupportedVersion(t *testing.T) {
	raw := []byte(`{"version":2,"git_context":{},"git_status":{},"entries":[]}`)
	_, err := discovery.UnmarshalCheckpoint(raw)
	if err == nil {
		t.Fatal("expected unsupported checkpoint version to fail")
	}
	if !strings.Contains(err.Error(), "unsupported prediscovered checkpoint version 2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrediscoveredCheckpointRejectsUnknownJSONFields(t *testing.T) {
	raw := []byte(`{"version":1,"git_context":{},"git_status":{},"entries":[],"extra":true}`)
	_, err := discovery.UnmarshalCheckpoint(raw)
	if err == nil {
		t.Fatal("expected unknown checkpoint field to fail")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrediscoveredCheckpointRejectsTrailingJSONData(t *testing.T) {
	raw := []byte(`{"version":1,"git_context":{},"git_status":{},"entries":[]} {"version":1}`)
	_, err := discovery.UnmarshalCheckpoint(raw)
	if err == nil {
		t.Fatal("expected trailing checkpoint data to fail")
	}
	if !strings.Contains(err.Error(), "trailing JSON data") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunInternalPrediscoveredTreePreviewMatchesFreshEvaluation(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":  "package main\n",
		"src/b.txt": "notes\n",
		"docs/c.md": "docs\n",
	})
	parentCfg := parseInProject(t, project, []string{"src"})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("discovery.EvaluateScope returned error: %v", err)
	}

	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	freshCfg, err := cli.ParseArgs([]string{
		"--quiet",
		"--internal-tree-preview",
		"src",
		"--only", "*.go",
		"--internal-tree-target", "src",
		"--internal-tree-kind", render.TargetKindDir,
		"--internal-tree-state", render.TargetStateOK,
	})
	if err != nil {
		t.Fatalf("parseArgs fresh returned error: %v", err)
	}
	checkpointCfg, err := cli.ParseArgs([]string{
		"--quiet",
		"--internal-tree-preview",
		"--internal-prediscovered", checkpointPath,
		"--only", "*.go",
		"--internal-tree-target", "src",
		"--internal-tree-kind", render.TargetKindDir,
		"--internal-tree-state", render.TargetStateOK,
	})
	if err != nil {
		t.Fatalf("parseArgs checkpoint returned error: %v", err)
	}

	var freshStdout, checkpointStdout bytes.Buffer
	if err := run(freshCfg, &freshStdout, io.Discard); err != nil {
		t.Fatalf("fresh run returned error: %v", err)
	}
	if err := run(checkpointCfg, &checkpointStdout, io.Discard); err != nil {
		t.Fatalf("checkpoint run returned error: %v", err)
	}
	if !bytes.Equal(checkpointStdout.Bytes(), freshStdout.Bytes()) {
		t.Fatalf("checkpoint output differs from fresh output\nfresh:\n%s\ncheckpoint:\n%s", freshStdout.String(), checkpointStdout.String())
	}
}

func TestRunInternalPrediscoveredTreePreviewUsesCheckpointGitStatus(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "hello\n",
	})
	_ = parseInProject(t, project, []string{"."})
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, project, discovery.CheckpointData{
		GitContext: git.Context{
			Enabled: true,
			Root:    "/definitely/missing",
		},
		GitStatus: map[string]string{
			"a.txt": "M",
		},
		Entries: []discovery.Entry{{
			AbsPath:    filepath.Join(project, "a.txt"),
			RelPath:    "a.txt",
			SizeBytes:  int64(len("hello\n")),
			SizeKnown:  true,
			GitVisible: true,
			Mode:       command.EntryModeFull,
		}},
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	cfg, err := cli.ParseArgs([]string{"--quiet", "--internal-tree-preview", "--internal-prediscovered", checkpointPath})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(cfg, &stdout, io.Discard); err != nil {
		t.Fatalf("run returned error; git status was probably recomputed instead of using checkpoint map: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"a.txt", "[M]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview output missing %q:\n%s", want, out)
		}
	}
}

func TestRunInternalPrediscoveredContentMatchListMatchesFreshEvaluation(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package main\n// TODO one\n",
		"src/b.go": "package main\n",
		"src/c.md": "TODO docs\n",
	})
	parentCfg := parseInProject(t, project, []string{"src", "--only", "*.go"})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("discovery.EvaluateScope returned error: %v", err)
	}
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	freshCfg, err := cli.ParseArgs([]string{"--quiet", "--internal-content-match-list", "src", "--only", "*.go", "--contains", "TODO"})
	if err != nil {
		t.Fatalf("parseArgs fresh returned error: %v", err)
	}
	checkpointCfg, err := cli.ParseArgs([]string{"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, "--contains", "TODO"})
	if err != nil {
		t.Fatalf("parseArgs checkpoint returned error: %v", err)
	}

	var freshStdout, checkpointStdout bytes.Buffer
	if err := run(freshCfg, &freshStdout, io.Discard); err != nil {
		t.Fatalf("fresh run returned error: %v", err)
	}
	if err := run(checkpointCfg, &checkpointStdout, io.Discard); err != nil {
		t.Fatalf("checkpoint run returned error: %v", err)
	}
	if !bytes.Equal(checkpointStdout.Bytes(), freshStdout.Bytes()) {
		t.Fatalf("checkpoint content-match output differs from fresh output\nfresh:\n%s\ncheckpoint:\n%s", freshStdout.String(), checkpointStdout.String())
	}
}

// fzf substitutes {q} as an empty string before the user types anything,
// so the picker's preview command runs with `--contains ”` on its
// empty-state frame. The internal prediscovered handler must short-
// circuit on empty pattern and return zero rows instead of letting the
// pattern reach the validator (which exits 2 and surfaces as
// "Command failed: ..." in fzf). See
// RESOLVED_BUG_empty_pattern_preview_command_failed.md.
func TestRunInternalPrediscoveredContentMatchListShortCircuitsEmptyPattern(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package main\n// TODO one\n",
		"src/b.go": "package main\n",
	})
	parentCfg := parseInProject(t, project, []string{"src", "--only", "*.go"})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("discovery.EvaluateScope returned error: %v", err)
	}
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	for _, flag := range []string{"--contains", "--snippet"} {
		t.Run(strings.TrimPrefix(flag, "--"), func(t *testing.T) {
			cfg, err := cli.ParseArgs([]string{"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, flag, ""})
			if err != nil {
				t.Fatalf("parseArgs returned error: %v", err)
			}
			var stdout bytes.Buffer
			if err := run(cfg, &stdout, io.Discard); err != nil {
				t.Fatalf("run with empty %s pattern returned error: %v", flag, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout for empty %s pattern, got: %q", flag, stdout.String())
			}
		})
	}
}

func TestParseArgsInternalPrediscoveredRequiresPath(t *testing.T) {
	_, err := cli.ParseArgs([]string{"--internal-tree-preview", "--internal-prediscovered"})
	if err == nil {
		t.Fatal("expected missing --internal-prediscovered path to fail")
	}
	if !strings.Contains(err.Error(), "--internal-prediscovered requires a checkpoint path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunInternalPrediscoveredRequiresPreviewMode(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"--internal-prediscovered", "scope.json"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	err = run(cfg, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected --internal-prediscovered without a preview/reload mode to fail")
	}
	if !strings.Contains(err.Error(), "--internal-prediscovered requires --internal-tree-preview, --internal-content-match-list, --internal-lines-preview, or --internal-file-preview") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRunInternalPrediscoveredContentMatchListDirectModeMatchesChunked
// asserts that the direct-rg fast path produces the same per-file
// {relPath → first-match line} mapping as the chunked path for a bare
// --contains scope (the only flavor where direct mode is eligible).
// Test fixture has no upstream filters, so the prediscovered entries
// equal the filesystem walk; intersection collapses to identity and
// any divergence between direct and chunked is a real bug.
func TestRunInternalPrediscoveredContentMatchListDirectModeMatchesChunked(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":     "package main\n// TODO one\n",
		"src/b.go":     "package main\n// Function comment\n", // smart-case: "function" matches
		"sub/c.go":     "package sub\n// function two\n",
		"d.md":         "TODO docs\n",
		"empty.go":     "",
		"BinaryFile":   "\x00\x00\x00\x00",
		"src/case.go":  "package main\n// Todo mixed case\n",
		"src/upper.go": "package main\n// TODO\n",
	})
	parentCfg := parseInProject(t, project, []string{"."})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("discovery.EvaluateScope returned error: %v", err)
	}
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	patterns := []string{"TODO", "function", "Function", "todo"}
	for _, pattern := range patterns {
		t.Run("pattern_"+pattern, func(t *testing.T) {
			freshCfg, err := cli.ParseArgs([]string{"--quiet", "--internal-content-match-list", ".", "--contains", pattern})
			if err != nil {
				t.Fatalf("parseArgs fresh: %v", err)
			}
			checkpointCfg, err := cli.ParseArgs([]string{"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, "--contains", pattern})
			if err != nil {
				t.Fatalf("parseArgs checkpoint: %v", err)
			}

			var freshOut, ckptOut bytes.Buffer
			if err := run(freshCfg, &freshOut, io.Discard); err != nil {
				t.Fatalf("fresh run: %v", err)
			}
			if err := run(checkpointCfg, &ckptOut, io.Discard); err != nil {
				t.Fatalf("checkpoint run: %v", err)
			}
			if !bytes.Equal(freshOut.Bytes(), ckptOut.Bytes()) {
				t.Fatalf("pattern %q: direct (checkpoint) output differs from chunked (fresh)\nfresh:\n%s\ncheckpoint:\n%s", pattern, freshOut.String(), ckptOut.String())
			}
		})
	}
}

// TestRunInternalPrediscoveredContentMatchListHonorsParentInclude
// reproduces the v0.6.3 direct-mode regression: when the parent scope
// authorized gitignored files via --include, the picker subprocess
// must also bypass gitignore (--no-ignore) so those files appear in
// the match-list. Before the fix, parent discovered 2 entries matching
// the pattern; picker returned 0 because direct rg respected
// .gitignore and never entered the authorized subtree.
//
// See docs/versions/v0.6.4/reports/ACTIVE_PLAN_picker_no_ignore_for_include.md.
func TestRunInternalPrediscoveredContentMatchListHonorsParentInclude(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":     "docs/\n",
		"docs/readme.md": "talks about fzf and other things\n",
		"docs/other.md":  "no relevant content\n",
		"docs/sub/x.md":  "fzf-related notes here\n",
		"src/main.go":    "package main\n",
	})

	parentCfg := parseInProject(t, project, []string{"docs", "--include", "docs", "--contains", "fzf"})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("discovery.EvaluateScope returned error: %v", err)
	}
	if len(discovered.Entries) == 0 {
		t.Fatalf("parent discovery should authorize docs/ entries; got 0")
	}

	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
		NoIgnore:   true,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	freshCfg, err := cli.ParseArgs([]string{"--quiet", "--internal-content-match-list", "docs", "--include", "docs", "--contains", "fzf"})
	if err != nil {
		t.Fatalf("parseArgs fresh: %v", err)
	}
	checkpointCfg, err := cli.ParseArgs([]string{"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, "--contains", "fzf"})
	if err != nil {
		t.Fatalf("parseArgs checkpoint: %v", err)
	}

	var freshOut, ckptOut bytes.Buffer
	if err := run(freshCfg, &freshOut, io.Discard); err != nil {
		t.Fatalf("fresh run: %v", err)
	}
	if err := run(checkpointCfg, &ckptOut, io.Discard); err != nil {
		t.Fatalf("checkpoint run: %v", err)
	}
	if ckptOut.Len() == 0 {
		t.Fatalf("checkpoint picker returned 0 rows for fzf-matching content under --include'd docs/ (the original bug)\nfresh output was:\n%s", freshOut.String())
	}
	if !bytes.Equal(freshOut.Bytes(), ckptOut.Bytes()) {
		t.Fatalf("checkpoint output differs from fresh\nfresh:\n%s\ncheckpoint:\n%s", freshOut.String(), ckptOut.String())
	}
}

// TestRunInternalPrediscoveredContentMatchListLiveNotContains
// asserts the picker hot path handles --not-contains as the LIVE
// (per-keystroke) pattern: each keystroke prunes files matching the
// typed regex from the entry set. Mirrors the existing
// TestRunInternalPrediscoveredContentMatchListMatchesFreshEvaluation
// test for --not-contains live mode.
func TestRunInternalPrediscoveredContentMatchListLiveNotContains(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.go": "package main\n// TODO one\n",
		"b.go": "package main\nfunc f() {}\n",
		"c.go": "package main\ntype X int\n",
		"d.md": "TODO docs\n",
	})
	parentCfg := parseInProject(t, project, []string{"."})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("EvaluateScope: %v", err)
	}
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}

	// Picker reload command: live --not-contains TODO typed in the
	// picker. The handler should prune a.go and d.md (both contain
	// TODO) and keep b.go, c.go.
	cfg, err := cli.ParseArgs([]string{"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, "--not-contains", "TODO"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(cfg, &stdout, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	for _, kept := range []string{"b.go", "c.go"} {
		if !strings.Contains(out, kept) {
			t.Fatalf("expected %s in output (no TODO in it):\n%s", kept, out)
		}
	}
	for _, dropped := range []string{"a.go", "d.md"} {
		if strings.Contains(out, "\t"+dropped+"\t") {
			t.Fatalf("expected %s pruned (contains TODO):\n%s", dropped, out)
		}
	}
}

// TestRunInternalPrediscoveredContentMatchListAppliesNotContains
// asserts the picker hot path (per-keystroke reload via
// --internal-content-match-list --internal-prediscovered) applies
// --not-contains correctly when it's set on the parent scope.
//
// Direct-mode dispatch is gated out for scopes with NotContains;
// this test exercises the chunked-path fallback to confirm it
// honors the --not-contains stage. Otherwise picker reloads for
// mixed `--contains FOO --not-contains BAR` flows would silently
// include files matching BAR.
func TestRunInternalPrediscoveredContentMatchListAppliesNotContains(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.go": "package main\nfunc f() { /* TODO */ }\n",
		"b.go": "package main\nfunc g() {}\n",
		"c.go": "package main\ntype X int\n",
		"d.go": "package main\nfunc h() { /* TODO */ }\n",
	})
	parentCfg := parseInProject(t, project, []string{".", "--not-contains", "TODO"})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("EvaluateScope: %v", err)
	}
	// Parent's discovery already applied --not-contains TODO → only
	// b.go and c.go survive. The checkpoint mirrors that.
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("WriteCheckpoint: %v", err)
	}

	// Picker reload command: live --contains func plus the fixed
	// --not-contains TODO. The picker hot path must prune any file
	// matching TODO from the result, even if it matches the live
	// --contains.
	cfg, err := cli.ParseArgs([]string{"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, "--not-contains", "TODO", "--contains", "func"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(cfg, &stdout, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Look for the file rows in the output. b.go matches "func" and
	// has no TODO → included. a.go and d.go match "func" but have
	// TODO → excluded.
	out := stdout.String()
	if !strings.Contains(out, "b.go") {
		t.Fatalf("expected b.go in picker reload output:\n%s", out)
	}
	for _, banned := range []string{"a.go", "d.go"} {
		// Look for the row form `<basename>  <relpath>\t...`. Bare
		// matches in any column would incorrectly flag the row.
		if strings.Contains(out, "\t"+banned+"\t") {
			t.Fatalf("--not-contains TODO should have pruned %s from picker output:\n%s", banned, out)
		}
	}
}

// TestNotContainsPrunesFiles asserts the headless --not-contains
// flow drops files matching the regex, mirroring the math
// full_set − contains(P) on the same fixture.
func TestNotContainsPrunesFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.go":   "package main\n// TODO one\n",
		"b.go":   "package main\n// nothing here\n",
		"c.md":   "TODO docs\n",
		"d.md":   "just docs\n",
	})

	captureAll := func(t *testing.T, args []string) []string {
		t.Helper()
		cfg, err := cli.ParseArgs(args)
		if err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		var stdout bytes.Buffer
		if err := run(cfg, &stdout, io.Discard); err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		paths := []string{}
		for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
			if line != "" {
				paths = append(paths, line)
			}
		}
		sort.Strings(paths)
		return paths
	}

	// Switch to project dir so the relative target "." resolves.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir project: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	full := captureAll(t, []string{".", "--paths", "--headless", "--quiet"})
	contains := captureAll(t, []string{".", "--contains", "TODO", "--paths", "--headless", "--quiet"})
	without := captureAll(t, []string{".", "--not-contains", "TODO", "--paths", "--headless", "--quiet"})

	// full == contains ∪ without, disjoint.
	containsSet := map[string]struct{}{}
	for _, p := range contains {
		containsSet[p] = struct{}{}
	}
	for _, p := range without {
		if _, dup := containsSet[p]; dup {
			t.Fatalf("path %q appeared in both --contains and --not-contains output", p)
		}
	}
	if len(contains)+len(without) != len(full) {
		t.Fatalf("contains(%d) + without(%d) != full(%d)\nfull: %v\ncontains: %v\nwithout: %v",
			len(contains), len(without), len(full), full, contains, without)
	}
}

// TestNotContainsCombinedWithContains asserts the argv-ordered
// pipeline produces a strict subset of --contains and excludes
// any file matching the --not-contains regex.
func TestNotContainsCombinedWithContains(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.go": "package main\nimport \"x\"\n// TODO one\n",
		"b.go": "package main\nimport \"y\"\n",
		"c.go": "package main\n// no deps\n",
		"d.go": "package main\nimport \"z\"\n// TODO\n",
	})

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir project: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg, err := cli.ParseArgs([]string{".", "--contains", "import", "--not-contains", "TODO", "--paths", "--headless", "--quiet"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(cfg, &stdout, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	sort.Strings(got)
	want := []string{"b.go"} // has import, doesn't have TODO
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--contains import --not-contains TODO\n  got:  %v\n  want: %v", got, want)
	}
}

func exportedFieldNames(typ reflect.Type) []string {
	names := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		names = append(names, field.Name)
	}
	return names
}
