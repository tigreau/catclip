package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

type RenderConfig struct {
	prepared          *PreparedPresentation
	NoTree            bool
	Quiet             bool
	ForceTreeMetadata bool
	TreeTarget        string
	TreeKind          string
	TreeState         string
	Scopes            []command.ExecutionScope
}

func RenderConfigFromParsedCommand(cfg command.Parsed) RenderConfig {
	return RenderConfig{
		NoTree:     cfg.NoTree,
		Quiet:      cfg.Quiet,
		TreeTarget: cfg.TreeTarget,
		TreeKind:   cfg.TreeKind,
		TreeState:  cfg.TreeState,
		Scopes:     command.ExecutionScopesFromSpec(cfg.Command),
	}
}

func NeedsTreeRender(cfg RenderConfig) bool {
	if cfg.ForceTreeMetadata {
		return true
	}
	if cfg.NoTree {
		return false
	}
	return !cfg.Quiet
}

func TreeDocumentRenderConfig(cfg RenderConfig) RenderConfig {
	cfg.ForceTreeMetadata = true
	return cfg
}

// RenderPreview writes the user-facing preview path: filter summary, notices,
// optional tree, and the final size/token summary.
func RenderPreview(cfg RenderConfig, gitCtx git.Context, plan output.Plan, report output.Report, stdout, stderr io.Writer, colors platform.Palette) error {
	if !cfg.Quiet {
		if err := writeFilterSummary(stderr, gitCtx, colors, cfg.Scopes...); err != nil {
			return err
		}
		if err := writeReportNotices(stderr, report, colors); err != nil {
			return err
		}
	}

	if !cfg.NoTree {
		if err := printConfiguredPreviewTree(cfg, stdout, plan, report, colors); err != nil {
			return err
		}
	}

	if err := plan.WriteRefinementNotices(stderr, colors); err != nil {
		return err
	}
	return writeSummary(stdout, report, colors)
}

func WriteNormalDiagnostics(
	cfg RenderConfig,
	gitCtx git.Context,
	plan output.Plan,
	report output.Report,
	emissionPolicy command.EmissionPolicy,
	presentationWriter, diagnosticWriter io.Writer,
	presentationColors, diagnosticColors platform.Palette,
) (bool, error) {
	if cfg.Quiet {
		if err := plan.WriteRefinementNotices(diagnosticWriter, diagnosticColors); err != nil {
			return false, err
		}
	}
	if !cfg.Quiet {
		if err := writeFilterSummary(diagnosticWriter, gitCtx, diagnosticColors, cfg.Scopes...); err != nil {
			return false, err
		}
		if err := writeReportNotices(diagnosticWriter, report, diagnosticColors); err != nil {
			return false, err
		}
		if !cfg.NoTree {
			if err := printConfiguredPreviewTree(cfg, presentationWriter, plan, report, presentationColors); err != nil {
				return false, err
			}
		}
		if err := plan.WriteRefinementNotices(diagnosticWriter, diagnosticColors); err != nil {
			return false, err
		}
		if err := writeSummary(presentationWriter, report, presentationColors); err != nil {
			return false, err
		}
		if report.Tokens > tokenWarnThreshold {
			if _, err := fmt.Fprintf(diagnosticWriter, "  %s~%d tokens may exceed some LLM context windows.%s\n", diagnosticColors.Warn, report.Tokens, diagnosticColors.Reset); err != nil {
				return false, err
			}
		}
	}

	if emissionPolicy == command.EmissionNever {
		return false, nil
	}
	if report.Tokens <= tokenWarnThreshold || emissionPolicy == command.EmissionAlways || cfg.Quiet {
		return true, nil
	}

	proceedPrompt, err := PromptYesNo(diagnosticColors.Prompt+"Proceed? [y/N]"+diagnosticColors.Reset, false, diagnosticWriter)
	if err != nil {
		return false, err
	}
	if proceedPrompt {
		return true, nil
	}
	if _, err := fmt.Fprintf(diagnosticWriter, "%sAborted.%s\n", diagnosticColors.Warn, diagnosticColors.Reset); err != nil {
		return false, err
	}
	return false, nil
}

// WriteMetadataDiagnostics keeps the ordinary selected-file tree and summary,
// but bases the context-window warning and confirmation on the metadata bytes
// that will actually be emitted rather than on the selected files' contents.
func WriteMetadataDiagnostics(
	cfg RenderConfig,
	gitCtx git.Context,
	plan output.Plan,
	report output.Report,
	metadata *MetadataReport,
	emissionPolicy command.EmissionPolicy,
	presentationWriter, diagnosticWriter io.Writer,
	presentationColors, diagnosticColors platform.Palette,
) (bool, error) {
	if !cfg.Quiet {
		if err := writeFilterSummary(diagnosticWriter, gitCtx, diagnosticColors, cfg.Scopes...); err != nil {
			return false, err
		}
		if err := writeReportNotices(diagnosticWriter, report, diagnosticColors); err != nil {
			return false, err
		}
		if !cfg.NoTree {
			if err := printConfiguredPreviewTree(cfg, presentationWriter, plan, report, presentationColors); err != nil {
				return false, err
			}
		}
		if err := writeSummary(presentationWriter, report, presentationColors); err != nil {
			return false, err
		}
	}

	if emissionPolicy == command.EmissionNever {
		return false, nil
	}
	payloadBytes, err := metadata.EncodedSize()
	if err != nil {
		return false, err
	}
	payloadTokens := payloadBytes / 4
	if !cfg.Quiet && payloadTokens > tokenWarnThreshold {
		if _, err := fmt.Fprintf(diagnosticWriter,
			"  %sMetadata output is ~%d tokens and may exceed some LLM context windows.%s\n",
			diagnosticColors.Warn, payloadTokens, diagnosticColors.Reset); err != nil {
			return false, err
		}
	}
	if payloadTokens <= tokenWarnThreshold || emissionPolicy == command.EmissionAlways || cfg.Quiet {
		return true, nil
	}

	proceedPrompt, err := PromptYesNo(diagnosticColors.Prompt+"Proceed? [y/N]"+diagnosticColors.Reset, false, diagnosticWriter)
	if err != nil {
		return false, err
	}
	if proceedPrompt {
		return true, nil
	}
	if _, err := fmt.Fprintf(diagnosticWriter, "%sAborted.%s\n", diagnosticColors.Warn, diagnosticColors.Reset); err != nil {
		return false, err
	}
	return false, nil
}

func writeFilterSummary(w io.Writer, gitCtx git.Context, colors platform.Palette, scopes ...command.ExecutionScope) error {
	noIgnore := 0
	for _, scope := range scopes {
		if scope.NoIgnore {
			noIgnore++
		}
	}
	if noIgnore > 0 {
		if noIgnore == len(scopes) {
			_, err := fmt.Fprintf(w, "  %sIgnore rules disabled (--no-ignore).%s\n", colors.Git, colors.Reset)
			return err
		}
		_, err := fmt.Fprintf(w, "  %sIgnore rules apply per scope; %d scope(s) use --no-ignore.%s\n", colors.Git, noIgnore, colors.Reset)
		return err
	}
	shortConfig := platform.DisplayPath(discovery.GlobalHissPath())
	if gitCtx.Enabled && repoHasGitIgnore(gitCtx) {
		_, err := fmt.Fprintf(w, "  %sFiltered by .gitignore + %s%s\n", colors.Git, shortConfig, colors.Reset)
		return err
	}
	if gitCtx.Enabled {
		_, err := fmt.Fprintf(w, "  %sGit repo; %s and applicable .gitignore rules apply.%s\n", colors.Warn, shortConfig, colors.Reset)
		return err
	}
	_, err := fmt.Fprintf(w, "  %sNot a git repo; %s and applicable .gitignore rules apply.%s\n", colors.Warn, shortConfig, colors.Reset)
	return err
}

func writeReportNotices(w io.Writer, report output.Report, colors platform.Palette) error {
	for _, notice := range report.Notices {
		if _, err := fmt.Fprintf(w, "  %s%s%s\n", colors.Warn, notice, colors.Reset); err != nil {
			return err
		}
	}
	return nil
}

func writeSummary(w io.Writer, report output.Report, colors platform.Palette) error {
	return renderTreeSummarySection(w, buildTreeSummaryFromReport(report), treeRenderOptions{
		ShowSummary: true,
		ShowTokens:  true,
	}, colors)
}

func WriteClipboardSuccess(w io.Writer, plan output.Plan, stats output.EmitStats, colors platform.Palette) error {
	relPaths := plan.DistinctRelPaths()
	if len(relPaths) == 0 {
		return nil
	}
	count, word := plan.SummaryCountWord()
	first := relPaths[0]
	last := relPaths[len(relPaths)-1]

	if stats.SinkName == "bundle" {
		return writeBundleSuccess(w, bundleDescription(plan, count, word, false), bundleOverviewHint(plan, false), stats, colors)
	}

	switch {
	case count == 1:
		_, err := fmt.Fprintf(w, "\n%sCopied%s %s%s%s %sto clipboard%s\n", colors.OK, colors.Reset, colors.Bold, first, colors.Reset, colors.OK, colors.Reset)
		return err
	case first == last:
		_, err := fmt.Fprintf(w, "\n%sCopied%s %s%d %s%s %sto clipboard%s\n", colors.OK, colors.Reset, colors.Bold, count, word, colors.Reset, colors.OK, colors.Reset)
		return err
	default:
		_, err := fmt.Fprintf(w, "\n%sCopied%s %s%d %s%s %sto clipboard%s %s(%s ... %s)%s\n", colors.OK, colors.Reset, colors.Bold, count, word, colors.Reset, colors.OK, colors.Reset, colors.Dim, first, last, colors.Reset)
		return err
	}
}

func WriteMetadataClipboardSuccess(w io.Writer, plan output.Plan, stats output.EmitStats, colors platform.Palette) error {
	count := len(plan.DistinctRelPaths())
	if count == 0 {
		return nil
	}
	word := "files"
	if count == 1 {
		word = "file"
	}
	if stats.SinkName == "bundle" {
		return writeBundleSuccess(w, bundleDescription(plan, count, word, true), bundleOverviewHint(plan, true), stats, colors)
	}
	_, err := fmt.Fprintf(w, "\n%sCopied%s metadata for %s%d %s%s %sto clipboard%s\n",
		colors.OK, colors.Reset, colors.Bold, count, word, colors.Reset, colors.OK, colors.Reset)
	return err
}

// Describe surviving output kinds, not requested flags: an empty source scope
// must not make an overview-only bundle claim to contain code. The dedicated
// metadata-only transport still uses an ordinary file-facts plan.
func bundleDescription(plan output.Plan, count int, word string, metadataOnly bool) string {
	metadata := metadataOnly || plan.HasMetadata()
	paths := !metadata && plan.HasPaths()
	if !metadata && !paths {
		return fmt.Sprintf("%d %s", count, word)
	}
	var parts []string
	if metadata {
		parts = append(parts, "file metadata")
	}
	if paths {
		parts = append(parts, "file list")
	}
	if !metadataOnly && plan.HasFiles() {
		parts = append(parts, "code")
	}
	label := strings.Join(parts, " + ")
	if len(parts) == 1 {
		word = "files"
		if paths {
			word = "paths"
		}
		if count == 1 {
			word = strings.TrimSuffix(word, "s")
		}
		label += fmt.Sprintf(" (%d %s)", count, word)
	}
	return label
}

func bundleOverviewHint(plan output.Plan, metadataOnly bool) string {
	if !metadataOnly && plan.HasFiles() {
		return ""
	}
	if metadataOnly || plan.HasMetadata() {
		return "File details only — no file contents.\nAn AI can use this overview to suggest which files to share next."
	}
	if plan.HasPaths() {
		return "File paths only — no file contents.\nAn AI can use this list to suggest which files to share next."
	}
	return ""
}

func writeBundleSuccess(w io.Writer, description, overviewHint string, stats output.EmitStats, colors platform.Palette) error {
	size := humanByteSize(stats.PayloadBytes)
	path := platform.DisplayPath(stats.BundlePath)
	if _, err := fmt.Fprintf(w,
		"\n%sCopied%s %s%s%s %sto clipboard as a bundle%s\n%s%s%s %s(%s)%s\n\n",
		colors.OK, colors.Reset,
		colors.Bold, description, colors.Reset,
		colors.OK, colors.Reset,
		colors.Bold, path, colors.Reset,
		colors.Dim, size, colors.Reset,
	); err != nil {
		return err
	}
	if overviewHint != "" {
		if _, err := fmt.Fprintf(w, "%s%s%s\n\n", colors.Dim, overviewHint, colors.Reset); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "%sPaste attaches a file%s%s — works in web UIs and file managers, not terminals.%s\n%sUse --no-bundle to copy text instead.%s\n", colors.OK, colors.Reset, colors.Dim, colors.Reset, colors.Dim, colors.Reset); err != nil {
		return err
	}
	for _, warning := range stats.Warnings {
		if _, err := fmt.Fprintf(w, "%sWarning: %s%s\n", colors.Warn, warning, colors.Reset); err != nil {
			return err
		}
	}
	return nil
}

func humanByteSize(n int64) string {
	const (
		kb = 1024
		mb = 1024 * 1024
	)
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1fMB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%dKB", n/kb)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func repoHasGitIgnore(gitCtx git.Context) bool {
	if !gitCtx.Enabled {
		return false
	}
	info, err := os.Stat(filepath.Join(gitCtx.Root, ".gitignore"))
	return err == nil && !info.IsDir()
}

// printPreviewTree renders the compact directory tree shown before clipboard or
// stdout emission, including ignore-bypass coloring and target path hints.
func printPreviewTree(w io.Writer, plan output.Plan, report output.Report, colors platform.Palette) error {
	return renderTreeDocument(w, treeDocument{
		Mode:    treeDocumentModeTree,
		Entries: treeEntriesFromPlan(plan, report),
	}, treeRenderOptions{
		ShowModeTags:  true,
		ShowSizes:     true,
		ShowGitStatus: true,
		ShowSummary:   false,
		ShowTokens:    false,
	}, colors)
}

func ignoreBypassedDirectoryLabel(entry discovery.Entry, relDir string) bool {
	return ignoreBypassedTreeDirectoryLabel(treeDocumentEntry{
		Path:           entry.RelPath,
		TargetRoot:     entry.TargetRoot,
		IgnoreBypassed: entry.IgnoreBypassed,
		BlockSource:    entry.BlockSource,
	}, relDir)
}
