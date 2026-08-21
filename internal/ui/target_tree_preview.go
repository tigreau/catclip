package ui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

// RenderFreshTargetTreePreview renders the target picker's ordinary full-file
// entries without routing them through the generic output plan. The completed
// tree is written once so fzf never observes a partial Catclip frame.
func RenderFreshTargetTreePreview(
	ctx context.Context,
	w io.Writer,
	cfg RenderConfig,
	gitCtx git.Context,
	entries []discovery.Entry,
) error {
	finishBench := platform.InternalBenchSpan("ui.target_tree_preview")

	canonical := append([]discovery.Entry(nil), entries...)
	needsNormalizedSort := false
	for _, entry := range canonical {
		if entry.Mode != command.EntryModeFull {
			err := fmt.Errorf("fresh target preview requires full-file entries, got %q for %q", entry.Mode, entry.RelPath)
			finishBench("err", "true")
			return err
		}
		if strings.ContainsRune(entry.RelPath, '\\') || strings.HasPrefix(entry.RelPath, "./") || strings.Contains(entry.RelPath, "//") {
			needsNormalizedSort = true
		}
	}
	canonical = discovery.DedupeEntriesByPath(canonical)
	if needsNormalizedSort && !sort.SliceIsSorted(canonical, func(i, j int) bool {
		return normalizeRelPath(canonical[i].RelPath) < normalizeRelPath(canonical[j].RelPath)
	}) {
		sort.SliceStable(canonical, func(i, j int) bool {
			return normalizeRelPath(canonical[i].RelPath) < normalizeRelPath(canonical[j].RelPath)
		})
	}
	if err := ctx.Err(); err != nil {
		finishBench("err", "true", "cancelled", "true")
		return err
	}
	platform.InternalBenchLog("ui.target_tree_preview.canonical",
		"entries", platform.InternalBenchInt(len(canonical)),
	)
	if len(canonical) == 0 {
		doc, ok := buildEmptyTreeDocument(TreeDocumentRenderConfig(cfg))
		if !ok {
			finishBench("err", "true", "empty", "true")
			return ErrTreePayloadEmptyNoTarget
		}
		err := writeFreshTargetTreeDocument(ctx, w, doc)
		finishBench("err", platform.InternalBenchError(err), "entries", "0")
		return err
	}

	finishSizes := platform.InternalBenchSpan("ui.target_tree_preview.sizes",
		"entries", platform.InternalBenchInt(len(canonical)),
	)
	sizes, err := output.CollectFileBodySizes(ctx, canonical)
	finishSizes("err", platform.InternalBenchError(err))
	if err != nil {
		finishBench("err", "true", "cancelled", platform.InternalBenchBool(ctx.Err() != nil))
		return err
	}
	for index := range canonical {
		canonical[index].SizeBytes = sizes[canonical[index].RelPath]
		canonical[index].SizeKnown = true
	}

	statuses := map[string]string(nil)
	if gitCtx.Enabled {
		finishGit := platform.InternalBenchSpan("ui.target_tree_preview.git",
			"entries", platform.InternalBenchInt(len(canonical)),
		)
		pathspecs := discovery.GitStatusPathspecsForEntries(gitCtx, canonical)
		if err := ctx.Err(); err != nil {
			finishGit("err", "true", "cancelled", "true")
			finishBench("err", "true", "cancelled", "true")
			return err
		}
		statuses, err = git.StatusMapForPathspecsContext(ctx, gitCtx, pathspecs)
		finishGit("err", platform.InternalBenchError(err))
		if err != nil {
			finishBench("err", "true", "cancelled", platform.InternalBenchBool(ctx.Err() != nil))
			return err
		}
	}

	doc := buildFreshTargetTreeDocument(cfg, canonical, sizes, statuses)
	err = writeFreshTargetTreeDocument(ctx, w, doc)
	finishBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(canonical)),
	)
	return err
}

func writeFreshTargetTreeDocument(ctx context.Context, w io.Writer, doc treeDocument) error {
	var rendered bytes.Buffer
	finishRender := platform.InternalBenchSpan("ui.target_tree_preview.buffer",
		"entries", platform.InternalBenchInt(len(doc.Entries)),
	)
	err := renderTreeDocument(contextWriter{ctx: ctx, w: &rendered}, doc, FzfFilterTreeRenderOptions(), platform.ANSIPalette())
	finishRender(
		"err", platform.InternalBenchError(err),
		"bytes", platform.InternalBenchInt(rendered.Len()),
	)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	finishWrite := platform.InternalBenchSpan("ui.target_tree_preview.write",
		"bytes", platform.InternalBenchInt(rendered.Len()),
	)
	n, err := w.Write(rendered.Bytes())
	if err == nil && n != rendered.Len() {
		err = io.ErrShortWrite
	}
	finishWrite("err", platform.InternalBenchError(err))
	return err
}

func buildFreshTargetTreeDocument(cfg RenderConfig, entries []discovery.Entry, sizes map[string]int64, statuses map[string]string) treeDocument {
	doc := treeDocument{
		Mode:    treeDocumentModeTree,
		Target:  buildTreeDocumentTargetForEntries(cfg, entries),
		Entries: make([]treeDocumentEntry, 0, len(entries)),
	}
	for index := range entries {
		entry := &entries[index]
		doc.Entries = append(doc.Entries, treeDocumentEntry{
			Path:           entry.RelPath,
			Size:           &entry.SizeBytes,
			GitStatus:      statuses[entry.RelPath],
			TargetRoot:     entry.TargetRoot,
			IgnoreBypassed: entry.IgnoreBypassed,
			BlockSource:    entry.BlockSource,
		})
	}
	doc.EntriesSorted = true
	doc.Summary = renderpkg.BuildSummary(sizes, "", 0, "")
	return doc
}

func buildTreeDocumentTargetForEntries(cfg RenderConfig, entries []discovery.Entry) *treeDocumentTarget {
	if target := buildTreeDocumentTarget(cfg); target != nil {
		return target
	}
	if len(cfg.Scopes) != 1 || len(entries) == 0 || len(cfg.Scopes[0].Targets) != 1 {
		return nil
	}
	targetPath := normalizeRelPath(cfg.Scopes[0].Targets[0])
	if targetPath == "" {
		return nil
	}
	if len(entries) == 1 && normalizeRelPath(entries[0].RelPath) == targetPath {
		return renderpkg.BuildTarget(targetPath, TreeTargetKindFile, TreeTargetStateText)
	}
	return renderpkg.BuildTarget(targetPath, treeTargetKindDir, treeTargetStateOK)
}

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}
