package catclip

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
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
	if err := writePrediscoveredCheckpoint(checkpointPath, dir, prediscoveredCheckpointData{
		GitStatus: map[string]string{},
		Entries: []fileEntry{
			{RelPath: "a.txt"}, // AbsPath empty -> resolve via workingDir
			{RelPath: "b.txt", AbsPath: filepath.Join(dir, "b.txt")}, // AbsPath already set
			{RelPath: "kept.txt", SizeBytes: 99, SizeKnown: true},    // already known -> preserved, not re-stat'd
			{RelPath: "missing.txt"},                                 // no file -> graceful, stays unknown
		},
	}); err != nil {
		t.Fatalf("writePrediscoveredCheckpoint returned error: %v", err)
	}
	decoded, err := readPrediscoveredCheckpoint(checkpointPath)
	if err != nil {
		t.Fatalf("readPrediscoveredCheckpoint returned error: %v", err)
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
	data := prediscoveredCheckpointData{
		GitContext: gitContext{
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
		Entries: []fileEntry{
			{
				AbsPath:          "/repo/src/changed.go",
				RelPath:          "src/changed.go",
				ModTime:          modTime,
				SizeBytes:        1234,
				SizeKnown:        true,
				TargetRoot:       "src",
				GitVisible:       true,
				Mode:             entryModeSnippet,
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
				Mode:       entryModeFull,
			},
			{
				AbsPath:    "/repo/src/empty.go",
				RelPath:    "src/empty.go",
				ModTime:    modTime.Add(2 * time.Hour),
				SizeBytes:  0,
				SizeKnown:  true,
				TargetRoot: "src",
				GitVisible: true,
				Mode:       entryModeLines,
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
				Mode:             entryModeDiff,
				DiffWantUnstaged: true,
			},
		},
	}

	raw, err := marshalPrediscoveredCheckpoint(data)
	if err != nil {
		t.Fatalf("marshalPrediscoveredCheckpoint returned error: %v", err)
	}
	decoded, err := unmarshalPrediscoveredCheckpoint(raw)
	if err != nil {
		t.Fatalf("unmarshalPrediscoveredCheckpoint returned error: %v", err)
	}

	// AbsPath is intentionally not serialized — it is re-derived at read by
	// ensureEntryAbsPaths (workingDir + RelPath). Reset it on the expected
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
	// serialized — re-derived at read by ensureEntryAbsPaths.
	wantResetOnRoundtrip := []string{"AbsPath"}

	// fileEntry must be fully accounted for: every exported field is either
	// serialized or intentionally reset on round-trip. Compare as sets —
	// declaration order is irrelevant to coverage (AbsPath is declared first
	// but reset, so order would otherwise mismatch).
	covered := append([]string(nil), wantSerialized...)
	covered = append(covered, wantResetOnRoundtrip...)
	got := exportedFieldNames(reflect.TypeOf(fileEntry{}))
	slices.Sort(got)
	slices.Sort(covered)
	if !reflect.DeepEqual(got, covered) {
		t.Fatalf("fileEntry schema decision list is stale\n got: %v\nwant serialized + reset-on-roundtrip: %v", got, covered)
	}

	if got := exportedFieldNames(reflect.TypeOf(prediscoveredCheckpointEntry{})); !reflect.DeepEqual(got, wantSerialized) {
		t.Fatalf("prediscoveredCheckpointEntry fields do not match serialized fileEntry fields\n got: %v\nwant: %v", got, wantSerialized)
	}
}

func TestPrediscoveredCheckpointJSONFieldNames(t *testing.T) {
	data := prediscoveredCheckpointData{
		GitContext: gitContext{
			Enabled:    true,
			Root:       "/repo",
			WorkPrefix: "src",
			HasHead:    true,
		},
		GitStatus: map[string]string{"src/changed.go": "M"},
		Entries: []fileEntry{{
			AbsPath:          "/repo/src/changed.go",
			RelPath:          "src/changed.go",
			ModTime:          time.Date(2026, 5, 17, 10, 11, 12, 0, time.UTC),
			SizeBytes:        123,
			SizeKnown:        true,
			TargetRoot:       "src",
			GitVisible:       true,
			Mode:             entryModeDiff,
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

	raw, err := marshalPrediscoveredCheckpoint(data)
	if err != nil {
		t.Fatalf("marshalPrediscoveredCheckpoint returned error: %v", err)
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
	_, err := unmarshalPrediscoveredCheckpoint(raw)
	if err == nil {
		t.Fatal("expected unsupported checkpoint version to fail")
	}
	if !strings.Contains(err.Error(), "unsupported prediscovered checkpoint version 2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrediscoveredCheckpointRejectsUnknownJSONFields(t *testing.T) {
	raw := []byte(`{"version":1,"git_context":{},"git_status":{},"entries":[],"extra":true}`)
	_, err := unmarshalPrediscoveredCheckpoint(raw)
	if err == nil {
		t.Fatal("expected unknown checkpoint field to fail")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrediscoveredCheckpointRejectsTrailingJSONData(t *testing.T) {
	raw := []byte(`{"version":1,"git_context":{},"git_status":{},"entries":[]} {"version":1}`)
	_, err := unmarshalPrediscoveredCheckpoint(raw)
	if err == nil {
		t.Fatal("expected trailing checkpoint data to fail")
	}
	if !strings.Contains(err.Error(), "trailing JSON data") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunInternalPrediscoveredTreePayloadMatchesFreshEvaluation(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":  "package main\n",
		"src/b.txt": "notes\n",
		"docs/c.md": "docs\n",
	})
	parentCfg := parseInProject(t, project, []string{"src"})
	gitCtx := detectGitContext(parentCfg.WorkingDir)
	discovered, err := evaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, colorPalette{})
	if err != nil {
		t.Fatalf("evaluateScope returned error: %v", err)
	}

	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := writePrediscoveredCheckpoint(checkpointPath, parentCfg.WorkingDir, prediscoveredCheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("writePrediscoveredCheckpoint returned error: %v", err)
	}

	freshCfg, err := parseArgs([]string{
		"--quiet",
		"--internal-tree-payload",
		"src",
		"--only", "*.go",
		"--internal-tree-target", "src",
		"--internal-tree-kind", treeTargetKindDir,
		"--internal-tree-state", treeTargetStateOK,
	})
	if err != nil {
		t.Fatalf("parseArgs fresh returned error: %v", err)
	}
	checkpointCfg, err := parseArgs([]string{
		"--quiet",
		"--internal-tree-payload",
		"--internal-prediscovered", checkpointPath,
		"--only", "*.go",
		"--internal-tree-target", "src",
		"--internal-tree-kind", treeTargetKindDir,
		"--internal-tree-state", treeTargetStateOK,
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

func TestRunInternalPrediscoveredTreePreviewMatchesPayloadRenderer(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":  "package main\n",
		"src/b.go":  "package b\n",
		"src/c.txt": "notes\n",
	})
	parentCfg := parseInProject(t, project, []string{"src"})
	gitCtx := detectGitContext(parentCfg.WorkingDir)
	discovered, err := evaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, colorPalette{})
	if err != nil {
		t.Fatalf("evaluateScope returned error: %v", err)
	}

	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := writePrediscoveredCheckpoint(checkpointPath, parentCfg.WorkingDir, prediscoveredCheckpointData{
		GitContext: gitCtx,
		GitStatus: map[string]string{
			"src/a.go": "M",
		},
		Entries: discovered.Entries,
	}); err != nil {
		t.Fatalf("writePrediscoveredCheckpoint returned error: %v", err)
	}

	commonArgs := []string{
		"--internal-prediscovered", checkpointPath,
		"--only", "*.go",
		"--internal-tree-target", "src",
		"--internal-tree-kind", treeTargetKindDir,
		"--internal-tree-state", treeTargetStateOK,
	}
	payloadCfg, err := parseArgs(append([]string{"--quiet", "--internal-tree-payload"}, commonArgs...))
	if err != nil {
		t.Fatalf("parseArgs payload returned error: %v", err)
	}
	previewCfg, err := parseArgs(append([]string{"--quiet", "--internal-tree-preview"}, commonArgs...))
	if err != nil {
		t.Fatalf("parseArgs preview returned error: %v", err)
	}

	var payloadStdout bytes.Buffer
	if err := run(payloadCfg, &payloadStdout, io.Discard); err != nil {
		t.Fatalf("payload run returned error: %v", err)
	}
	doc, err := decodeTreePayload(bytes.NewReader(payloadStdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	var renderedPayload bytes.Buffer
	if err := renderTreeDocument(&renderedPayload, doc, fzfFilterTreeRenderOptions(), ansiColorPalette()); err != nil {
		t.Fatalf("renderTreeDocument returned error: %v", err)
	}

	var previewStdout bytes.Buffer
	if err := run(previewCfg, &previewStdout, io.Discard); err != nil {
		t.Fatalf("preview run returned error: %v", err)
	}
	if !bytes.Equal(previewStdout.Bytes(), renderedPayload.Bytes()) {
		t.Fatalf("direct tree preview differs from payload renderer\npayload-rendered:\n%s\npreview:\n%s", renderedPayload.String(), previewStdout.String())
	}
}

func TestRunInternalPrediscoveredTreePayloadUsesCheckpointGitStatus(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "hello\n",
	})
	_ = parseInProject(t, project, []string{"."})
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := writePrediscoveredCheckpoint(checkpointPath, project, prediscoveredCheckpointData{
		GitContext: gitContext{
			Enabled: true,
			Root:    "/definitely/missing",
		},
		GitStatus: map[string]string{
			"a.txt": "M",
		},
		Entries: []fileEntry{{
			AbsPath:    filepath.Join(project, "a.txt"),
			RelPath:    "a.txt",
			SizeBytes:  int64(len("hello\n")),
			SizeKnown:  true,
			GitVisible: true,
			Mode:       entryModeFull,
		}},
	}); err != nil {
		t.Fatalf("writePrediscoveredCheckpoint returned error: %v", err)
	}

	cfg, err := parseArgs([]string{"--quiet", "--internal-tree-payload", "--internal-prediscovered", checkpointPath})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(cfg, &stdout, io.Discard); err != nil {
		t.Fatalf("run returned error; git status was probably recomputed instead of using checkpoint map: %v", err)
	}
	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if got, want := len(doc.Entries), 1; got != want {
		t.Fatalf("doc.Entries length = %d, want %d", got, want)
	}
	if got := doc.Entries[0].GitStatus; got != "M" {
		t.Fatalf("doc.Entries[0].GitStatus = %q, want M", got)
	}
}

func TestRunInternalPrediscoveredContentMatchListMatchesFreshEvaluation(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package main\n// TODO one\n",
		"src/b.go": "package main\n",
		"src/c.md": "TODO docs\n",
	})
	parentCfg := parseInProject(t, project, []string{"src", "--only", "*.go"})
	gitCtx := detectGitContext(parentCfg.WorkingDir)
	discovered, err := evaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, colorPalette{})
	if err != nil {
		t.Fatalf("evaluateScope returned error: %v", err)
	}
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := writePrediscoveredCheckpoint(checkpointPath, parentCfg.WorkingDir, prediscoveredCheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("writePrediscoveredCheckpoint returned error: %v", err)
	}

	freshCfg, err := parseArgs([]string{"--quiet", "--internal-content-match-list", "src", "--only", "*.go", "--contains", "TODO"})
	if err != nil {
		t.Fatalf("parseArgs fresh returned error: %v", err)
	}
	checkpointCfg, err := parseArgs([]string{"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, "--contains", "TODO"})
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
	gitCtx := detectGitContext(parentCfg.WorkingDir)
	discovered, err := evaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, colorPalette{})
	if err != nil {
		t.Fatalf("evaluateScope returned error: %v", err)
	}
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := writePrediscoveredCheckpoint(checkpointPath, parentCfg.WorkingDir, prediscoveredCheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("writePrediscoveredCheckpoint returned error: %v", err)
	}

	for _, flag := range []string{"--contains", "--snippet"} {
		t.Run(strings.TrimPrefix(flag, "--"), func(t *testing.T) {
			cfg, err := parseArgs([]string{"--quiet", "--internal-content-match-list", "--internal-prediscovered", checkpointPath, flag, ""})
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
	_, err := parseArgs([]string{"--internal-tree-payload", "--internal-prediscovered"})
	if err == nil {
		t.Fatal("expected missing --internal-prediscovered path to fail")
	}
	if !strings.Contains(err.Error(), "--internal-prediscovered requires a checkpoint path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunInternalPrediscoveredRequiresTreePayload(t *testing.T) {
	cfg, err := parseArgs([]string{"--internal-prediscovered", "scope.json"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	err = run(cfg, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected --internal-prediscovered without --internal-tree-payload to fail")
	}
	if !strings.Contains(err.Error(), "--internal-prediscovered requires --internal-tree-payload, --internal-tree-preview, --internal-content-match-list, --internal-lines-preview, or --internal-file-preview") {
		t.Fatalf("unexpected error: %v", err)
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
