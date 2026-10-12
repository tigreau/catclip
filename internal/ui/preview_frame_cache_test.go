package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
)

func TestPreviewFrameReplayAndCorruptRecovery(t *testing.T) {
	cache := newPreviewFrameCache(filepath.Join(t.TempDir(), "scope.json"), "test", "query")
	calls := 0
	render := func(w io.Writer) error { calls++; _, err := io.WriteString(w, "complete frame\n"); return err }
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if err := cache.render(context.Background(), &out, render); err != nil || out.String() != "complete frame\n" {
			t.Fatalf("render %d: %q %v", i, out.String(), err)
		}
	}
	if calls != 1 {
		t.Fatalf("revisited renderer %d times", calls)
	}
	if err := os.WriteFile(cache.path, []byte("corrupt partial frame"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cache.render(context.Background(), &out, render); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || out.String() != "complete frame\n" {
		t.Fatalf("corrupt replay leaked: %q calls=%d", out.String(), calls)
	}
}

func TestPreviewFrameDoesNotPublishFailedOrCancelledRender(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			cache := newPreviewFrameCache(filepath.Join(t.TempDir(), "scope.json"), "test", nil)
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			err := cache.render(ctx, io.Discard, func(w io.Writer) error {
				_, _ = io.WriteString(w, "partial")
				if cancel {
					stop()
					return nil
				}
				return errors.New("render failed")
			})
			if err == nil {
				t.Fatal("lost render/cancellation error")
			}
			if _, ok := cache.read(); ok {
				t.Fatal("cached incomplete frame")
			}
		})
	}
}

func TestPreviewFrameStringWritesRespectCaptureLimit(t *testing.T) {
	cache := newPreviewFrameCache(filepath.Join(t.TempDir(), "scope.json"), "test", nil)
	cache.maxFrame = 100
	var out bytes.Buffer
	want := ""
	if err := cache.render(context.Background(), &out, func(w io.Writer) error {
		for i := 0; i < 40; i++ {
			want += "fragment\n"
			if _, err := io.WriteString(w, "fragment\n"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if out.String() != want {
		t.Fatal("oversize string output was truncated")
	}
	if _, ok := cache.read(); ok {
		t.Fatal("string writes bypassed capture limit")
	}
}

func TestPreviewFrameCacheBoundsAndOversizeBypass(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "scope.json")
	for i := 0; i < 12; i++ {
		cache := newPreviewFrameCache(artifact, "test", i)
		cache.maxFrame = 100
		cache.maxBytes = 300
		cache.maxEntries = 2
		var out bytes.Buffer
		body := bytes.Repeat([]byte("x"), 80)
		if err := cache.render(context.Background(), &out, func(w io.Writer) error { _, err := w.Write(body); return err }); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), body) {
			t.Fatal("cache limit truncated output")
		}
		entries, err := os.ReadDir(cache.dir)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		var size int64
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				t.Fatal(err)
			}
			size += info.Size()
			if filepath.Ext(entry.Name()) == ".frame" {
				count++
			}
		}
		if count > 2 || size > 300 {
			t.Fatalf("unbounded cache: count=%d size=%d", count, size)
		}
	}
	cache := newPreviewFrameCache(artifact, "test", "oversize")
	cache.maxFrame = 10
	var out bytes.Buffer
	body := bytes.Repeat([]byte("y"), 100)
	if err := cache.render(context.Background(), &out, func(w io.Writer) error { _, err := w.Write(body); return err }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Fatal("oversize preview truncated")
	}
	if _, ok := cache.read(); ok {
		t.Fatal("cached oversize frame")
	}
}

func TestPreviewFrameCleanupCannotResurrectOwner(t *testing.T) {
	dir := t.TempDir()
	cache := newPreviewFrameCache(filepath.Join(dir, "scope.json"), "test", nil)
	err := cache.render(context.Background(), io.Discard, func(w io.Writer) error {
		removePreviewSession(dir)
		_, err := io.WriteString(w, "completed after close")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("owner resurrected: %v", err)
	}
}

func TestPreviewFrameKeysKeepStateAndProjectionSeparate(t *testing.T) {
	dir := t.TempDir()
	base := filePreviewConfig{WorkingDir: dir, FilePath: "a.go", CheckpointPath: filepath.Join(dir, "a.json"), Scopes: []command.ExecutionScope{{Contains: "foo"}}}
	keys := []string{newPreviewFrameCache(base.CheckpointPath, "file", base).path}
	mutations := []func(*filePreviewConfig){
		func(c *filePreviewConfig) { c.FilePath = "b.go" },
		func(c *filePreviewConfig) { c.Scopes[0].Contains = "foo|bar" },
		func(c *filePreviewConfig) { c.Scopes[0].Snippet = true; c.Scopes[0].SnippetPattern = "foo" },
		func(c *filePreviewConfig) { c.Scopes[0].SnippetContextSet = true; c.Scopes[0].SnippetContextLines = 3 },
		func(c *filePreviewConfig) {
			c.Scopes[0].Lines = true
			c.Scopes[0].LinesStart = 2
			c.Scopes[0].LinesEnd = 4
		},
		func(c *filePreviewConfig) { c.CheckpointPath = filepath.Join(dir, "other.json") },
		func(c *filePreviewConfig) { c.SearchingHint = true },
	}
	for _, mutate := range mutations {
		c := base
		c.Scopes = append([]command.ExecutionScope(nil), base.Scopes...)
		mutate(&c)
		keys = append(keys, newPreviewFrameCache(c.CheckpointPath, "file", c).path)
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if key == "" || seen[key] {
			t.Fatalf("different requests share %q", key)
		}
		seen[key] = true
	}
}

func TestPreviewFrameConcurrentPublicationStaysBounded(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "scope.json")
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			cache := newPreviewFrameCache(artifact, "test", i)
			cache.maxFrame = 100
			cache.maxBytes = 512
			cache.maxEntries = 3
			var out bytes.Buffer
			err := cache.render(context.Background(), &out, func(w io.Writer) error { _, err := io.WriteString(w, "complete"); return err })
			if err != nil || out.String() != "complete" {
				t.Errorf("preview %d: %q %v", i, out.String(), err)
			}
		}(i)
	}
	group.Wait()
	cache := newPreviewFrameCache(artifact, "test", nil)
	entries, err := os.ReadDir(cache.dir)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	var frames int
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
		if filepath.Ext(entry.Name()) == ".frame" {
			frames++
		}
	}
	if total > 512 || frames > 3 {
		t.Fatalf("concurrent cache: bytes=%d frames=%d", total, frames)
	}
}

func TestPreviewFrameShortWriterStillFailsOnHit(t *testing.T) {
	cache := newPreviewFrameCache(filepath.Join(t.TempDir(), "scope.json"), "test", nil)
	render := func(w io.Writer) error { _, err := io.WriteString(w, "complete"); return err }
	if err := cache.render(context.Background(), io.Discard, render); err != nil {
		t.Fatal(err)
	}
	if err := cache.render(context.Background(), shortPreviewWriter{}, render); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer: %v", err)
	}
}

type shortPreviewWriter struct{}

func (shortPreviewWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestPreviewFrameDoesNotCacheSwallowedWriteFailure(t *testing.T) {
	cache := newPreviewFrameCache(filepath.Join(t.TempDir(), "scope.json"), "test", nil)
	writer := &failingPreviewWriter{}
	err := cache.render(context.Background(), writer, func(w io.Writer) error {
		_, _ = io.WriteString(w, "first")
		_, _ = io.WriteString(w, "second")
		return nil // existing streaming handlers may swallow a closed pipe
	})
	if err != nil {
		t.Fatalf("changed swallowed-error contract: %v", err)
	}
	if _, ok := cache.read(); ok {
		t.Fatal("published partial frame after swallowed write failure")
	}
}

func TestPreviewFramesShareSessionBudgetAcrossArtifactOwners(t *testing.T) {
	root := t.TempDir()
	t.Setenv(previewFrameSessionEnv, root)
	for i := 0; i < 12; i++ {
		cache := newPreviewFrameCache(filepath.Join(t.TempDir(), "scope.json"), "test", i)
		cache.maxBytes = 300
		cache.maxEntries = 2
		if cache.dir != filepath.Join(root, "preview-frames") {
			t.Fatal("owner acquired a separate session budget")
		}
		if err := cache.render(context.Background(), io.Discard, func(w io.Writer) error { _, err := w.Write(bytes.Repeat([]byte("x"), 80)); return err }); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "preview-frames"))
	if err != nil {
		t.Fatal(err)
	}
	var bytes int64
	var count int
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		bytes += info.Size()
		if filepath.Ext(entry.Name()) == ".frame" {
			count++
		}
	}
	if bytes > 300 || count > 2 {
		t.Fatalf("session budget grew across owners: %d bytes, %d frames", bytes, count)
	}
}

func TestPreviewFrameRemovedOwnerCannotPublishIntoSharedSession(t *testing.T) {
	t.Setenv(previewFrameSessionEnv, t.TempDir())
	owner := t.TempDir()
	cache := newPreviewFrameCache(filepath.Join(owner, "scope.json"), "test", nil)
	if err := cache.render(context.Background(), io.Discard, func(w io.Writer) error {
		_, _ = io.WriteString(w, "completed too late")
		removePreviewSession(owner)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.read(); ok {
		t.Fatal("removed owner published into surviving session")
	}
}

type failingPreviewWriter struct{ writes int }

func (w *failingPreviewWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestFilePreviewRevisitUsesSessionObservation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte("package main\nfunc old() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := filePreviewConfig{WorkingDir: dir, FilePath: "a.go", CheckpointPath: filepath.Join(t.TempDir(), "scope.json"), Scopes: []command.ExecutionScope{{Contains: "func"}}}
	var first, second bytes.Buffer
	if err := RunInternalFilePreview(cfg, &first); err != nil {
		t.Fatal(err)
	}
	if first.Len() == 0 {
		t.Fatal("empty first preview")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := RunInternalFilePreview(cfg, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("revisit reopened deleted source instead of replaying observation")
	}
	cfg.CheckpointPath = filepath.Join(t.TempDir(), "scope.json")
	var next bytes.Buffer
	if err := RunInternalFilePreview(cfg, &next); err != nil {
		t.Fatal(err)
	}
	if next.Len() != 0 {
		t.Fatal("new owner reused old owner's body")
	}
}

func TestPreviewRetainedRoutesReplayWithoutDecodingTransport(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.go": "package a\n// TODO one\nfunc first() {}\n",
		"b.go": "package b\n// TODO two\nfunc second() {}\n",
	})
	for _, kind := range []string{"tree", "lines", "snippet-boundary"} {
		t.Run(kind, func(t *testing.T) {
			owner := t.TempDir()
			artifact := filepath.Join(owner, "scope.json")
			entries := discovery.FillEntrySizes(project, discovery.EnsureEntryAbsPaths([]discovery.Entry{{RelPath: "a.go"}, {RelPath: "b.go"}}, project))
			if err := discovery.WriteCheckpoint(artifact, project, discovery.CheckpointData{Entries: entries}); err != nil {
				t.Fatal(err)
			}
			cfg := PrediscoveredCommandConfigFromParsedCommand(parseInProject(t, project, []string{"--quiet", "--internal-tree-preview", "--internal-prediscovered", artifact, "."}))
			var render, uncached func(io.Writer) error
			switch kind {
			case "tree":
				selection := filepath.Join(owner, "first-selection.tsv")
				if err := os.WriteFile(selection, []byte("a.go\ta.go\n"), 0600); err != nil {
					t.Fatal(err)
				}
				cfg.FileSetSelectionPath = selection
				cfg.FileSetSelectionStage = "only"
				render = func(w io.Writer) error { return RunInternalPrediscoveredTreePreview(cfg, w) }
				uncached = func(w io.Writer) error { return runInternalPrediscoveredTreePreview(cfg, w) }
			case "lines":
				cfg.Scopes[0].Lines = true
				cfg.Scopes[0].LinesStart, cfg.Scopes[0].LinesEnd = 2, 3
				render = func(w io.Writer) error { return RunInternalLinesPreview(cfg, output.EmitConfig{}, w) }
				uncached = func(w io.Writer) error { return runInternalLinesPreview(cfg, output.EmitConfig{}, w) }
			case "snippet-boundary":
				if err := writeSnippetBoundarySource(artifact, snippetBoundarySource{Pattern: "TODO", Entries: []snippetBoundarySourceEntry{
					{RelPath: "a.go", AbsPath: filepath.Join(project, "a.go"), Lines: []int{2}},
					{RelPath: "b.go", AbsPath: filepath.Join(project, "b.go"), Lines: []int{2}},
				}}); err != nil {
					t.Fatal(err)
				}
				render = func(w io.Writer) error { return RunInternalSnippetBoundaryPreview(artifact, "0", w) }
				uncached = func(w io.Writer) error { return runInternalSnippetBoundaryPreview(artifact, "0", w) }
			}
			var expected, first, replay bytes.Buffer
			if err := uncached(&expected); err != nil {
				t.Fatal(err)
			}
			if err := render(&first); err != nil {
				t.Fatal(err)
			}
			if expected.Len() == 0 || !bytes.Equal(expected.Bytes(), first.Bytes()) {
				t.Fatal("cache miss changed output")
			}
			if err := os.Remove(artifact); err != nil {
				t.Fatal(err)
			}
			if kind == "tree" {
				// fzf gives each refresh a different temporary selection filename.
				// Equivalent selected values must still hit the same frame.
				cfg.FileSetSelectionPath = filepath.Join(owner, "next-selection.tsv")
				if err := os.WriteFile(cfg.FileSetSelectionPath, []byte("a.go\ta.go\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := render(&replay); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first.Bytes(), replay.Bytes()) {
				t.Fatal("replay changed output")
			}
			if kind == "tree" {
				if err := os.WriteFile(cfg.FileSetSelectionPath, []byte("b.go\tb.go\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := render(io.Discard); err == nil {
					t.Fatal("different selection reused cached frame")
				}
			}
		})
	}
}

func TestPreviewDiffReplaySkipsStateDecode(t *testing.T) {
	project := setupTestProject(t, map[string]string{"a.go": "package old\n"})
	t.Chdir(project)
	initGitRepo(t, project)
	if err := os.WriteFile(filepath.Join(project, "a.go"), []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := diffPreviewTestTemp(t)
	_, cleanup := prepareDiffPreview([]string{"a.go", "--changed-diff"})
	defer cleanup()
	path := diffPreviewTestPath(t, dir)
	cfg := FilePreviewConfigFromParsedCommand(parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "a.go", "--internal-diff-preview-state", path}))
	var first, replay bytes.Buffer
	if err := RunInternalFilePreview(cfg, &first); err != nil {
		t.Fatal(err)
	}
	if first.Len() == 0 {
		t.Fatal("empty diff fixture")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := RunInternalFilePreview(cfg, &replay); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), replay.Bytes()) {
		t.Fatal("diff replay changed output")
	}
}
