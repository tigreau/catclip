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
	NoTree     bool
	Preview    bool
	Quiet      bool
	Yes        bool
	TreeTarget string
	TreeKind   string
	TreeState  string
	Scopes     []command.ExecutionScope
}

func RenderConfigFromParsedCommand(cfg command.Parsed) RenderConfig {
	return RenderConfig{
		NoTree:     cfg.NoTree,
		Preview:    cfg.Preview,
		Quiet:      cfg.Quiet,
		Yes:        cfg.Yes,
		TreeTarget: cfg.TreeTarget,
		TreeKind:   cfg.TreeKind,
		TreeState:  cfg.TreeState,
		Scopes:     command.ExecutionScopesFromSpec(cfg.Command),
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func NeedsTreeRender(cfg RenderConfig) bool {
	if cfg.NoTree {
		return false
	}
	if cfg.Preview {
		return true
	}
	return !cfg.Quiet
}

func TreeDocumentRenderConfig(cfg RenderConfig) RenderConfig {
	cfg.Preview = true
	return cfg
}

// RenderPreview writes the user-facing preview path: filter summary, notices,
// optional tree, and the final size/token summary.
func RenderPreview(cfg RenderConfig, gitCtx git.Context, plan output.Plan, report output.Report, stdout, stderr io.Writer, colors platform.Palette) error {
	if !cfg.Quiet {
		if err := writeFilterSummary(stderr, gitCtx, colors); err != nil {
			return err
		}
		if err := writeReportNotices(stderr, report, colors); err != nil {
			return err
		}
	}

	if !cfg.NoTree {
		if err := printPreviewTree(stdout, plan, report, colors); err != nil {
			return err
		}
	}

	return writeSummary(stdout, report, colors)
}

func WriteNormalDiagnostics(cfg RenderConfig, gitCtx git.Context, plan output.Plan, report output.Report, stderr io.Writer, colors platform.Palette) (bool, error) {
	if !cfg.Quiet {
		if err := writeFilterSummary(stderr, gitCtx, colors); err != nil {
			return false, err
		}
		if err := writeReportNotices(stderr, report, colors); err != nil {
			return false, err
		}
		if !cfg.NoTree {
			if err := printPreviewTree(stderr, plan, report, colors); err != nil {
				return false, err
			}
		}
		if err := writeSummary(stderr, report, colors); err != nil {
			return false, err
		}
		if report.Tokens > tokenWarnThreshold {
			if _, err := fmt.Fprintf(stderr, "  %s~%d tokens may exceed some LLM context windows.%s\n", colors.Warn, report.Tokens, colors.Reset); err != nil {
				return false, err
			}
		}
	}

	if report.Tokens <= tokenWarnThreshold || cfg.Yes || cfg.Quiet {
		return true, nil
	}

	proceedPrompt, err := PromptYesNo(colors.Prompt+"Proceed? [y/N]"+colors.Reset, false, stderr)
	if err != nil {
		return false, err
	}
	if proceedPrompt {
		return true, nil
	}
	if _, err := fmt.Fprintf(stderr, "%sAborted.%s\n", colors.Warn, colors.Reset); err != nil {
		return false, err
	}
	return false, nil
}

func writeFilterSummary(w io.Writer, gitCtx git.Context, colors platform.Palette) error {
	shortConfig := shortPath(discovery.GlobalHissPath())
	if gitCtx.Enabled && repoHasGitIgnore(gitCtx) {
		_, err := fmt.Fprintf(w, "  %sFiltered by .gitignore + %s%s\n", colors.Git, shortConfig, colors.Reset)
		return err
	}
	if gitCtx.Enabled {
		_, err := fmt.Fprintf(w, "  %sGit repo, but no .gitignore; only %s applies.%s\n", colors.Warn, shortConfig, colors.Reset)
		return err
	}
	_, err := fmt.Fprintf(w, "  %sNot a git repo; only %s applies.%s\n", colors.Warn, shortConfig, colors.Reset)
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
		return writeBundleSuccess(w, count, word, stats, colors)
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

func writeBundleSuccess(w io.Writer, count int, word string, stats output.EmitStats, colors platform.Palette) error {
	size := humanByteSize(stats.PayloadBytes)
	path := displayPath(stats.BundlePath)
	if _, err := fmt.Fprintf(w,
		"\n%sBundled%s %s%d %s%s %s→%s %s%s%s %s(%s)%s\n%sPaste attaches a file — works in web UIs and file managers, not terminals.%s\n%sUse --no-bundle to copy text instead.%s\n",
		colors.OK, colors.Reset,
		colors.Bold, count, word, colors.Reset,
		colors.OK, colors.Reset,
		colors.Bold, path, colors.Reset,
		colors.Dim, size, colors.Reset,
		colors.Dim, colors.Reset,
		colors.Dim, colors.Reset,
	); err != nil {
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

func shortPath(value string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return value
	}
	home = filepath.Clean(home)
	value = filepath.Clean(value)
	if value == home {
		return "~"
	}
	if strings.HasPrefix(value, home+string(os.PathSeparator)) {
		return "~" + string(os.PathSeparator) + strings.TrimPrefix(value, home+string(os.PathSeparator))
	}
	return value
}

// printPreviewTree renders the compact directory tree shown before clipboard or
// stdout emission, including include-allowed coloring and target path hints.
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

func allowedByIncludeDirectoryLabel(entry discovery.Entry, relDir string) bool {
	return allowedByIncludeTreeDirectoryLabel(treeDocumentEntry{
		Path:             entry.RelPath,
		TargetRoot:       entry.TargetRoot,
		AllowedByInclude: entry.AllowedByInclude,
		BlockSource:      entry.BlockSource,
	}, relDir)
}
