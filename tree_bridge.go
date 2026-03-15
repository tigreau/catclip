package catclip

import (
	"errors"
	"io"
	"os"
	"strings"

	treepkg "github.com/tigreau/catclip/internal/tree"
)

type treeDocumentMode = treepkg.DocumentMode

const (
	treeDocumentModeTree treeDocumentMode = treepkg.DocumentModeTree
	treeDocumentModeFile treeDocumentMode = treepkg.DocumentModeFile

	treeTargetKindDir  = treepkg.TargetKindDir
	treeTargetKindFile = treepkg.TargetKindFile

	treeTargetStateOK             = treepkg.TargetStateOK
	treeTargetStateText           = treepkg.TargetStateText
	treeTargetStateEmpty          = treepkg.TargetStateEmpty
	treeTargetStateNoTextChildren = treepkg.TargetStateNoTextChildren
	treeTargetStateNonText        = treepkg.TargetStateNonText
)

type treeDocument = treepkg.Document
type treeDocumentTarget = treepkg.DocumentTarget
type treeDocumentEntry = treepkg.DocumentEntry
type treeFilePreview = treepkg.FilePreview
type treeDocumentSummary = treepkg.DocumentSummary
type treeRenderOptions = treepkg.RenderOptions

var errEmptyTreePayload = treepkg.ErrEmptyPayload

func defaultTreeRenderOptions() treeRenderOptions {
	return treepkg.DefaultRenderOptions()
}

func buildTreeDocumentFromPreview(cfg runConfig, entries []fileEntry, report outputReport) treeDocument {
	doc := treeDocument{
		Mode:    treeDocumentModeTree,
		Target:  buildTreeDocumentTargetForEntries(cfg, entries),
		Entries: make([]treeDocumentEntry, 0, len(entries)),
	}

	for _, entry := range entries {
		treeEntry := treeDocumentEntry{
			Path:        entry.RelPath,
			GitStatus:   report.statuses[entry.RelPath],
			ModeTag:     report.modeTags[entry.RelPath],
			TargetRoot:  entry.TargetRoot,
			Bypassed:    entry.Bypassed,
			BlockRule:   entry.BlockRule,
			BlockSource: entry.BlockSource,
		}
		if size, ok := report.sizes[entry.RelPath]; ok {
			sizeCopy := size
			treeEntry.Size = &sizeCopy
		}
		doc.Entries = append(doc.Entries, treeEntry)
	}

	doc.Entries = treepkg.SortedEntries(doc.Entries)
	doc.Summary = buildTreeSummaryFromReport(report)
	return doc
}

func buildTreeDocumentTarget(cfg runConfig) *treeDocumentTarget {
	return treepkg.BuildTarget(cfg.TreeTarget, cfg.TreeKind, cfg.TreeState)
}

func buildTreeDocumentTargetForEntries(cfg runConfig, entries []fileEntry) *treeDocumentTarget {
	if target := buildTreeDocumentTarget(cfg); target != nil {
		return target
	}
	if len(cfg.Scopes) != 1 || len(cfg.Scopes[0].Targets) != 1 || len(entries) == 0 {
		return nil
	}

	targetPath := normalizeRelPath(cfg.Scopes[0].Targets[0])
	if targetPath == "" {
		return nil
	}
	if len(entries) == 1 && normalizeRelPath(entries[0].RelPath) == targetPath {
		return treepkg.BuildTarget(targetPath, treeTargetKindFile, treeTargetStateText)
	}
	return treepkg.BuildTarget(targetPath, treeTargetKindDir, treeTargetStateOK)
}

func buildEmptyTreeDocument(cfg runConfig) (treeDocument, bool) {
	target := buildTreeDocumentTarget(cfg)
	if target == nil {
		return treeDocument{}, false
	}

	return treeDocument{
		Mode:   treeDocumentModeTree,
		Target: target,
	}, true
}

func buildTreeFilePreviewDocument(relPath, content, matchPattern string, truncated bool) treeDocument {
	return treeDocument{
		Mode: treeDocumentModeFile,
		File: &treeFilePreview{
			Path:         normalizeRelPath(relPath),
			Content:      content,
			MatchPattern: matchPattern,
			Truncated:    truncated,
		},
	}
}

func buildTreeSummaryFromReport(report outputReport) *treeDocumentSummary {
	return treepkg.BuildSummary(report.sizes, report.humanSize, report.tokens, report.fileWord)
}

func normalizeTreeTargetKind(kind string) string {
	return treepkg.NormalizeTargetKind(kind)
}

func normalizeTreeTargetState(state string) string {
	return treepkg.NormalizeTargetState(state)
}

func encodeTreePayload(w io.Writer, doc treeDocument) error {
	return treepkg.EncodePayload(w, doc)
}

func decodeTreePayload(r io.Reader) (treeDocument, error) {
	return treepkg.DecodePayload(r)
}

func renderTreeDocument(w io.Writer, doc treeDocument, opts treeRenderOptions, colors colorPalette) error {
	return treepkg.RenderDocument(w, doc, opts, treePalette(colors))
}

func renderTreeSummarySection(w io.Writer, summary *treeDocumentSummary, opts treeRenderOptions, colors colorPalette) error {
	return treepkg.RenderSummarySection(w, summary, opts, treePalette(colors))
}

func bypassesTreeDirectoryLabel(entry treeDocumentEntry, relDir string) bool {
	return treepkg.BypassesDirectoryLabel(entry, relDir)
}

// TreeMain is the catclip-tree process entrypoint used by cmd/catclip-tree.
func TreeMain() {
	if err := RunTreeCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		exitWithError(err, os.Stderr)
	}
}

// RunTreeCLI adapts root catclip helpers to the internal tree CLI surface.
func RunTreeCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	err := treepkg.RunCLI(args, stdin, stdout, stderr, treepkg.CLIConfig{
		Version:        loadVersion(),
		ResolvePalette: treePaletteForColorMode,
	})
	var usageErr treepkg.UsageError
	if errors.As(err, &usageErr) {
		return usageError{message: usageErr.Error()}
	}
	return err
}

func treePaletteForColorMode(mode string, w io.Writer) (treepkg.Palette, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		return treePalette(activeColorPaletteForWriter(w)), nil
	case "always":
		return treePalette(ansiColorPalette()), nil
	case "never":
		return treepkg.Palette{}, nil
	default:
		return treepkg.Palette{}, newUsageError("Error: invalid color mode %s\n  Use one of: auto, always, never", singleQuoted(mode))
	}
}

func treePalette(colors colorPalette) treepkg.Palette {
	return treepkg.Palette{
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
