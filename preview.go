package catclip

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	treepkg "github.com/tigreau/catclip/internal/tree"
)

func buildOutputReportForPlan(cfg runConfig, gitCtx gitContext, plan outputPlan, notices []string) (outputReport, error) {
	sizes, totalBytes := plan.PayloadSizes()
	count, word := plan.SummaryCountWord()
	report := outputReport{
		sizes:   sizes,
		notices: append([]string(nil), notices...),
	}

	if needsTreeRender(cfg) {
		if gitCtx.Enabled {
			statuses, err := collectGitStatusMapForPlan(gitCtx, plan)
			if err != nil {
				return outputReport{}, err
			}
			report.statuses = statuses
		}
		report.modeTags = plan.PreviewModeTags(report.statuses)
	}

	report.humanSize, report.tokens = treepkg.FormatSizeAndTokens(totalBytes, count)
	report.countWord = word
	return report, nil
}

func needsTreeRender(cfg runConfig) bool {
	if cfg.NoTree {
		return false
	}
	if cfg.TreePayload {
		return true
	}
	if cfg.Preview {
		return true
	}
	return !cfg.Quiet
}

// renderPreview writes the user-facing preview path: filter summary, notices,
// optional tree, and the final size/token summary.
func renderPreview(cfg runConfig, gitCtx gitContext, plan outputPlan, report outputReport, stdout, stderr io.Writer, colors colorPalette) error {
	if !cfg.Quiet {
		if err := writeFilterSummary(stderr, cfg, gitCtx, colors); err != nil {
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

func writeNormalDiagnostics(cfg runConfig, gitCtx gitContext, plan outputPlan, report outputReport, stderr io.Writer, colors colorPalette) (bool, error) {
	if !cfg.Quiet {
		if err := writeFilterSummary(stderr, cfg, gitCtx, colors); err != nil {
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
		if report.tokens > tokenWarnThreshold {
			if _, err := fmt.Fprintf(stderr, "  %s~%d tokens may exceed some LLM context windows.%s\n", colors.Warn, report.tokens, colors.Reset); err != nil {
				return false, err
			}
		}
	}

	if report.tokens <= tokenWarnThreshold || cfg.Yes || cfg.Quiet {
		return true, nil
	}

	proceedPrompt, err := promptYesNo(colors.Prompt+"Proceed? [y/N]"+colors.Reset, false, stderr)
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

func writeFilterSummary(w io.Writer, cfg runConfig, gitCtx gitContext, colors colorPalette) error {
	shortConfig := shortPath(globalHissPath())
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

func writeReportNotices(w io.Writer, report outputReport, colors colorPalette) error {
	for _, notice := range report.notices {
		if _, err := fmt.Fprintf(w, "  %s%s%s\n", colors.Warn, notice, colors.Reset); err != nil {
			return err
		}
	}
	return nil
}

func writeSummary(w io.Writer, report outputReport, colors colorPalette) error {
	return renderTreeSummarySection(w, buildTreeSummaryFromReport(report), treeRenderOptions{
		ShowSummary: true,
		ShowTokens:  true,
	}, colors)
}

func writeClipboardSuccess(w io.Writer, plan outputPlan, colors colorPalette) error {
	relPaths := plan.DistinctRelPaths()
	if len(relPaths) == 0 {
		return nil
	}
	count, word := plan.SummaryCountWord()
	first := relPaths[0]
	last := relPaths[len(relPaths)-1]

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

func repoHasGitIgnore(gitCtx gitContext) bool {
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

func collectGitStatusMapForPlan(gitCtx gitContext, plan outputPlan) (map[string]string, error) {
	pathspecs := plan.GitStatusPathspecs(gitCtx)
	out, err := collectGitStatusOutput(gitCtx, pathspecs)
	if err != nil {
		if len(pathspecs) > 0 {
			out, err = collectGitStatusOutput(gitCtx, nil)
		}
		if err != nil {
			return nil, err
		}
	}
	return parseGitStatusMap(gitCtx, string(out)), nil
}

func collectGitStatusOutput(gitCtx gitContext, pathspecs []string) ([]byte, error) {
	args := []string{"status", "--porcelain"}
	if len(pathspecs) > 0 && canScopePreviewGitStatus(pathspecs) {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = gitCtx.Root
	return cmd.Output()
}

func canScopePreviewGitStatus(pathspecs []string) bool {
	if len(pathspecs) == 0 {
		return false
	}
	if len(pathspecs) > 256 {
		return false
	}
	total := 0
	for _, pathspec := range pathspecs {
		total += len(pathspec) + 1
	}
	return total <= 32768
}

func parseGitStatusMap(gitCtx gitContext, output string) map[string]string {
	statuses := make(map[string]string)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || len(line) < 4 {
			continue
		}
		xy := line[:2]
		pathPart := line[3:]
		if strings.Contains(pathPart, " -> ") {
			parts := strings.Split(pathPart, " -> ")
			pathPart = parts[len(parts)-1]
		}
		repoPath := normalizeRelPath(pathPart)
		workPath := gitCtx.toWorkPath(repoPath)
		if workPath == "" {
			continue
		}

		if xy == "??" {
			statuses[workPath] = "?"
			continue
		}

		staged := len(xy) >= 1 && xy[0] != ' ' && xy[0] != '?'
		unstaged := len(xy) >= 2 && xy[1] != ' ' && xy[1] != '?'
		switch {
		case staged && unstaged:
			statuses[workPath] = "SM"
		case staged:
			statuses[workPath] = "S"
		case unstaged:
			statuses[workPath] = "M"
		}
	}
	return statuses
}

// printPreviewTree renders the compact directory tree shown before clipboard or
// stdout emission, including include-allowed coloring and target path hints.
func printPreviewTree(w io.Writer, plan outputPlan, report outputReport, colors colorPalette) error {
	return renderTreeDocument(w, treeDocument{
		Mode:    treeDocumentModeTree,
		Entries: plan.TreeEntries(report),
	}, treeRenderOptions{
		ShowSizes:     true,
		ShowGitStatus: true,
		ShowSummary:   false,
		ShowTokens:    false,
	}, colors)
}

func allowedByIncludeDirectoryLabel(entry fileEntry, relDir string) bool {
	return allowedByIncludeTreeDirectoryLabel(treeDocumentEntry{
		Path:             entry.RelPath,
		TargetRoot:       entry.TargetRoot,
		AllowedByInclude: entry.AllowedByInclude,
		BlockSource:      entry.BlockSource,
	}, relDir)
}
