package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
)

func TestFreshTargetTreePreviewMatchesGenericPlanRenderer(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "src/a.ts", "export const a = 1\n")
	writeProjectFile(t, root, "src/nested/b.ts", "export const b = 2\n")

	entries := []discovery.Entry{
		{AbsPath: filepath.Join(root, "src/nested/b.ts"), RelPath: "src/nested/b.ts", TargetRoot: "src", Mode: command.EntryModeFull},
		{AbsPath: filepath.Join(root, "src/a.ts"), RelPath: "src/a.ts", TargetRoot: "src", Mode: command.EntryModeFull},
		// Marking a directory and one of its files produces an overlap. The
		// canonical discovery merge must keep one row and its richer metadata.
		{AbsPath: filepath.Join(root, "src/a.ts"), RelPath: "src/a.ts", TargetRoot: "src/a.ts", IgnoreBypassed: true, BlockSource: ".gitignore", Mode: command.EntryModeFull},
	}
	cfg := RenderConfig{
		TreeTarget: "src",
		TreeKind:   treeTargetKindDir,
		TreeState:  treeTargetStateOK,
		Scopes:     []command.ExecutionScope{{Targets: []string{"src"}}},
	}

	want := renderGenericTargetTree(t, cfg, git.Context{}, entries)
	var got bytes.Buffer
	if err := RenderFreshTargetTreePreview(context.Background(), &got, cfg, git.Context{}, entries); err != nil {
		t.Fatalf("RenderFreshTargetTreePreview() error = %v", err)
	}
	if got.String() != want {
		t.Fatalf("fresh target preview differs from generic renderer\n--- got ---\n%s\n--- want ---\n%s", got.String(), want)
	}
}

func TestFreshTargetTreePreviewMatchesGenericGitBadges(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "src/clean.ts", "clean\n")
	writeProjectFile(t, root, "src/changed.ts", "before\n")
	initGitRepo(t, root)
	writeProjectFile(t, root, "src/changed.ts", "after\n")
	writeProjectFile(t, root, "src/new.ts", "new\n")

	entries := []discovery.Entry{
		{AbsPath: filepath.Join(root, "src/clean.ts"), RelPath: "src/clean.ts", TargetRoot: "src", GitVisible: true, Mode: command.EntryModeFull},
		{AbsPath: filepath.Join(root, "src/changed.ts"), RelPath: "src/changed.ts", TargetRoot: "src", GitVisible: true, Mode: command.EntryModeFull},
		{AbsPath: filepath.Join(root, "src/new.ts"), RelPath: "src/new.ts", TargetRoot: "src", GitVisible: true, Mode: command.EntryModeFull},
	}
	cfg := RenderConfig{
		TreeTarget: "src",
		TreeKind:   treeTargetKindDir,
		TreeState:  treeTargetStateOK,
		Scopes:     []command.ExecutionScope{{Targets: []string{"src"}}},
	}
	gitCtx := git.Detect(root)

	want := renderGenericTargetTree(t, cfg, gitCtx, entries)
	var got bytes.Buffer
	if err := RenderFreshTargetTreePreview(context.Background(), &got, cfg, gitCtx, entries); err != nil {
		t.Fatalf("RenderFreshTargetTreePreview() error = %v", err)
	}
	if got.String() != want {
		t.Fatalf("fresh Git target preview differs from generic renderer\n--- got ---\n%s\n--- want ---\n%s", got.String(), want)
	}
}

func TestFreshTargetTreePreviewMatchesGenericWindowsStylePaths(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "src/a.ts", "a\n")
	writeProjectFile(t, root, "src/nested/b.ts", "b\n")
	entries := []discovery.Entry{
		{AbsPath: filepath.Join(root, "src/nested/b.ts"), RelPath: `src\nested\b.ts`, TargetRoot: `src\nested`, Mode: command.EntryModeFull},
		{AbsPath: filepath.Join(root, "src/a.ts"), RelPath: `src\a.ts`, TargetRoot: `src`, Mode: command.EntryModeFull},
	}
	cfg := RenderConfig{
		TreeTarget: `src`,
		TreeKind:   treeTargetKindDir,
		TreeState:  treeTargetStateOK,
		Scopes:     []command.ExecutionScope{{Targets: []string{`src`}}},
	}

	want := renderGenericTargetTree(t, cfg, git.Context{}, entries)
	var got bytes.Buffer
	if err := RenderFreshTargetTreePreview(context.Background(), &got, cfg, git.Context{}, entries); err != nil {
		t.Fatalf("RenderFreshTargetTreePreview() error = %v", err)
	}
	if got.String() != want {
		t.Fatalf("Windows-style target preview differs from generic renderer\n--- got ---\n%s\n--- want ---\n%s", got.String(), want)
	}
}

func TestFreshTargetTreePreviewUsesLstatForSymlinkSize(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "target.txt", strings.Repeat("x", 4096))
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink("target.txt", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	entries := []discovery.Entry{{AbsPath: link, RelPath: "link.txt", Mode: command.EntryModeFull}}
	cfg := RenderConfig{TreeTarget: "link.txt", TreeKind: TreeTargetKindFile, TreeState: TreeTargetStateText}

	want := renderGenericTargetTree(t, cfg, git.Context{}, entries)
	var got bytes.Buffer
	if err := RenderFreshTargetTreePreview(context.Background(), &got, cfg, git.Context{}, entries); err != nil {
		t.Fatalf("RenderFreshTargetTreePreview() error = %v", err)
	}
	if got.String() != want {
		t.Fatalf("symlink target preview differs from generic renderer\n--- got ---\n%s\n--- want ---\n%s", got.String(), want)
	}
	if strings.Contains(got.String(), "4.0KB") {
		t.Fatalf("preview followed symlink target size instead of using Lstat:\n%s", got.String())
	}
}

func TestFreshTargetTreePreviewCancelledBeforeWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer := &countingWriter{}
	err := RenderFreshTargetTreePreview(ctx, writer, RenderConfig{}, git.Context{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if writer.calls != 0 {
		t.Fatalf("stdout writes = %d, want 0", writer.calls)
	}
}

func TestFreshTargetTreePreviewWritesCompletedTreeOnce(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "src/a.ts", "a\n")
	writer := &countingWriter{}
	err := RenderFreshTargetTreePreview(context.Background(), writer, RenderConfig{
		TreeTarget: "src",
		TreeKind:   treeTargetKindDir,
		TreeState:  treeTargetStateOK,
	}, git.Context{}, []discovery.Entry{{
		AbsPath: filepath.Join(root, "src/a.ts"),
		RelPath: "src/a.ts",
		Mode:    command.EntryModeFull,
	}})
	if err != nil {
		t.Fatalf("RenderFreshTargetTreePreview() error = %v", err)
	}
	if writer.calls != 1 {
		t.Fatalf("stdout writes = %d, want 1", writer.calls)
	}
	if strings.Contains(writer.buf.String(), "\x1b[2J") {
		t.Fatalf("preview unexpectedly clears the pane: %q", writer.buf.String())
	}
}

func TestFreshTargetTreePreviewSizeErrorUsesCanonicalPathOrder(t *testing.T) {
	root := t.TempDir()
	entries := []discovery.Entry{
		{AbsPath: filepath.Join(root, "z-missing.ts"), RelPath: "z-missing.ts", Mode: command.EntryModeFull},
		{AbsPath: filepath.Join(root, "a-missing.ts"), RelPath: "a-missing.ts", Mode: command.EntryModeFull},
	}
	err := RenderFreshTargetTreePreview(context.Background(), io.Discard, RenderConfig{}, git.Context{}, entries)
	if err == nil {
		t.Fatal("expected size error")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("error = %T %v, want *os.PathError", err, err)
	}
	if filepath.Base(pathErr.Path) != "a-missing.ts" {
		t.Fatalf("first error path = %q, want canonical a-missing.ts", pathErr.Path)
	}
}

func renderGenericTargetTree(t *testing.T, cfg RenderConfig, gitCtx git.Context, entries []discovery.Entry) string {
	t.Helper()
	scope := command.ExecutionScope{Targets: []string{cfg.TreeTarget}}
	plan, err := output.BuildPlanForResolvedScopes(
		gitCtx,
		[]command.ExecutionScope{scope},
		[]output.EvaluatedScope{{Entries: append([]discovery.Entry(nil), entries...)}},
		append([]discovery.Entry(nil), entries...),
	)
	if err != nil {
		t.Fatalf("BuildPlanForResolvedScopes() error = %v", err)
	}
	var rendered bytes.Buffer
	if err := RenderTreePreviewFromPlan(&rendered, cfg, gitCtx, plan, nil, FzfFilterTreeRenderOptions()); err != nil {
		t.Fatalf("RenderTreePreviewFromPlan() error = %v", err)
	}
	return rendered.String()
}

type countingWriter struct {
	calls int
	buf   bytes.Buffer
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.calls++
	return w.buf.Write(p)
}
