package ui

import (
	"io"

	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

type treeDocumentMode = renderpkg.DocumentMode

const (
	treeDocumentModeTree treeDocumentMode = renderpkg.DocumentModeTree
	treeDocumentModeFile treeDocumentMode = renderpkg.DocumentModeFile

	treeTargetKindDir  = renderpkg.TargetKindDir
	TreeTargetKindFile = renderpkg.TargetKindFile

	treeTargetStateOK             = renderpkg.TargetStateOK
	TreeTargetStateText           = renderpkg.TargetStateText
	TreeTargetStateNoTextChildren = renderpkg.TargetStateNoTextChildren
	TreeTargetStateNonText        = renderpkg.TargetStateNonText
)

type treeDocument = renderpkg.Document
type treeDocumentTarget = renderpkg.DocumentTarget
type treeDocumentEntry = renderpkg.DocumentEntry
type treeFilePreview = renderpkg.FilePreview
type treeDocumentSummary = renderpkg.DocumentSummary
type treeRenderOptions = renderpkg.RenderOptions

func buildTreeDocumentFromPreview(cfg RenderConfig, plan output.Plan, report output.Report) treeDocument {
	doc := treeDocument{
		Mode:    treeDocumentModeTree,
		Target:  buildTreeDocumentTargetForPlan(cfg, plan),
		Entries: treeEntriesFromPlan(plan, report),
	}

	doc.Entries = renderpkg.SortedEntries(doc.Entries)
	doc.EntriesSorted = true
	doc.Summary = buildTreeSummaryFromReport(report)
	return doc
}

// treeEntriesFromPlan builds the render-shaped tree-row slice from a
// merged plan + the per-path metadata in report. Was output.Plan.TreeEntries
// before the v0.6.0 output extraction prep; moved here because the
// treeDocumentEntry type lives on the render side, and the planned
// output package must not depend on render.
func treeEntriesFromPlan(plan output.Plan, report output.Report) []treeDocumentEntry {
	merged := plan.MergedItems()
	out := make([]treeDocumentEntry, 0, len(merged))
	for _, entry := range merged {
		row := treeDocumentEntry{
			Path:             entry.RelPath,
			GitStatus:        report.Statuses[entry.RelPath],
			ModeTag:          report.ModeTags[entry.RelPath],
			TargetRoot:       entry.TargetRoot,
			AllowedByInclude: entry.AllowedByInclude,
			BlockSource:      entry.BlockSource,
		}
		if size, ok := report.Sizes[entry.RelPath]; ok {
			sizeCopy := size
			row.Size = &sizeCopy
		}
		out = append(out, row)
	}
	return out
}

func buildTreeDocumentTarget(cfg RenderConfig) *treeDocumentTarget {
	return renderpkg.BuildTarget(cfg.TreeTarget, cfg.TreeKind, cfg.TreeState)
}

func buildTreeDocumentTargetForPlan(cfg RenderConfig, plan output.Plan) *treeDocumentTarget {
	if target := buildTreeDocumentTarget(cfg); target != nil {
		return target
	}
	if len(cfg.Scopes) != 1 || plan.IsEmpty() {
		return nil
	}
	targets := cfg.Scopes[0].Targets
	if len(targets) != 1 {
		return nil
	}

	targetPath := normalizeRelPath(targets[0])
	if targetPath == "" {
		return nil
	}
	if firstRel, ok := plan.FirstRelPath(); plan.Len() == 1 && ok && normalizeRelPath(firstRel) == targetPath {
		return renderpkg.BuildTarget(targetPath, TreeTargetKindFile, TreeTargetStateText)
	}
	return renderpkg.BuildTarget(targetPath, treeTargetKindDir, treeTargetStateOK)
}

func buildEmptyTreeDocument(cfg RenderConfig) (treeDocument, bool) {
	target := buildTreeDocumentTarget(cfg)
	if target == nil {
		return treeDocument{}, false
	}

	return treeDocument{
		Mode:   treeDocumentModeTree,
		Target: target,
	}, true
}

func buildTreeFilePreviewDocument(relPath, highlightPath, content, matchPattern string, truncated bool, focusLines []int) treeDocument {
	return treeDocument{
		Mode: treeDocumentModeFile,
		File: &treeFilePreview{
			Path:          normalizeRelPath(relPath),
			HighlightPath: highlightPath,
			FocusLines:    focusLines,
			Content:       content,
			MatchPattern:  matchPattern,
			Truncated:     truncated,
		},
	}
}

func buildTreeSummaryFromReport(report output.Report) *treeDocumentSummary {
	return renderpkg.BuildSummary(report.Sizes, report.HumanSize, report.Tokens, report.CountWord)
}

func normalizeTreeTargetKind(kind string) string {
	return renderpkg.NormalizeTargetKind(kind)
}

func normalizeTreeTargetState(state string) string {
	return renderpkg.NormalizeTargetState(state)
}

func encodeTreePayload(w io.Writer, doc treeDocument) error {
	return renderpkg.EncodePayload(w, doc)
}

func decodeTreePayload(r io.Reader) (treeDocument, error) {
	return renderpkg.DecodePayload(r)
}

func renderTreeDocument(w io.Writer, doc treeDocument, opts treeRenderOptions, colors platform.Palette) error {
	return renderpkg.RenderDocument(w, doc, opts, treePalette(colors))
}

func renderTreeSummarySection(w io.Writer, summary *treeDocumentSummary, opts treeRenderOptions, colors platform.Palette) error {
	return renderpkg.RenderSummarySection(w, summary, opts, treePalette(colors))
}

func allowedByIncludeTreeDirectoryLabel(entry treeDocumentEntry, relDir string) bool {
	return renderpkg.AllowedByIncludeDirectoryLabel(entry, relDir)
}

func treePalette(colors platform.Palette) renderpkg.Palette {
	return renderpkg.Palette{
		Reset:  colors.Reset,
		Bold:   colors.Bold,
		Dim:    colors.Dim,
		OK:     colors.OK,
		Err:    colors.Err,
		Warn:   colors.Warn,
		Dir:    colors.Dir,
		Label:  colors.Label,
		Value:  colors.Value,
		Tree:   colors.Tree,
		Prompt: colors.Prompt,
		Git:    colors.Git,
	}
}
