package ui

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

// preview_table.go renders the `--preview` file table — the body-free, one-row-
// per-file listing that replaces the tree on the `--preview` dry-run (and only
// there; the confirmation flow, sink picker, and fzf pickers keep their trees).
// See docs/versions/v0.5.7/reports/RESOLVED_PLAN_preview_table.md.
//
// Layout is two lines per file: the full relative path on its own line (column 0,
// never truncated, so an agent reads a usable path first), then its metadata
// space-padded into aligned columns beneath. There is one layout; color is the
// only thing that varies — a TTY gets ANSI, a piped/headless agent run gets plain
// text (split metadata on runs of 2+ spaces). Rows are in emission order, 1:1 with
// what `--preview`-less output would copy (rule 1, truthfulness).

// previewTableHeaderLines document the listing's structure for a context-free
// agent: tool-agnostic on purpose (never catclip, flags, or commands), skippable
// (# comments), and explicit about the ambiguous columns — git enumerates its
// status codes and shape enumerates its forms (including path-only = no content).
var previewTableHeaderLines = []string{
	"# line 1: relative path",
	"# line 2: size, tokens, git, modified, shape",
	"#   git:   M=modified S=staged SM=staged+modified ?=untracked -=none",
	"#   shape: full | snippet | lines | diff | path-only (= just the path, no content)",
}

type previewTableRow struct {
	Path     string
	Size     string
	Tokens   string
	Git      string
	Modified string
	Shape    string

	// raw values, used only to color cells in normal mode.
	sizeBytes int64
	recency   recencyBucket
	gitStatus string
}

// RenderPreviewTable is the `--preview` render path (called from executePreview in
// place of the tree's RenderPreview, which stays for the sink picker). Filter
// summary and notices go to stderr as before; stdout carries only the banner,
// records, and the shared Count/Size/Tokens footer.
func RenderPreviewTable(cfg RenderConfig, gitCtx git.Context, plan output.Plan, report output.Report, stdout, stderr io.Writer, workingDir string, now time.Time, colors platform.Palette) error {
	if !cfg.Quiet {
		if err := writeFilterSummary(stderr, gitCtx, colors); err != nil {
			return err
		}
		if err := writeReportNotices(stderr, report, colors); err != nil {
			return err
		}
	}

	// --preview always renders the table. --no-tree governs only the
	// confirmation-flow tree, not --preview. The single layout never changes; color
	// is the only variable (an empty palette — piped or headless agent runs — yields
	// plain text), so an agent sees the same table, just without ANSI.
	rows, err := buildPreviewTableRows(plan, report, workingDir, now)
	if err != nil {
		return err
	}
	if err := writePreviewTable(stdout, rows, colors); err != nil {
		return err
	}
	// Reuse the tree summary's footer verbatim so Count/Size/Tokens language is
	// identical to every other preview surface.
	return writeSummary(stdout, report, colors)
}

// buildPreviewTableRows produces one row per selected file, in emission order
// (first appearance), summing per-file size across the file's plan items. Modified
// times are guaranteed via discovery.EnsureEntryModTimes (it stats only entries that lack
// one). Size/git/mode come from the already-built report so the table agrees with
// the tree and the copied payload.
func buildPreviewTableRows(plan output.Plan, report output.Report, workingDir string, now time.Time) ([]previewTableRow, error) {
	order := plan.EntriesInEmissionOrder()
	withTimes, err := discovery.EnsureEntryModTimes(order, workingDir)
	if err != nil {
		return nil, err
	}

	rows := make([]previewTableRow, 0, len(withTimes))
	for _, e := range withTimes {
		size := report.Sizes[e.RelPath]
		humanSize, tokens := renderpkg.FormatSizeAndTokens(size, 1)
		status := report.Statuses[e.RelPath]
		badge := "[-]"
		if status != "" {
			badge = "[" + status + "]"
		}
		shape := report.ModeTags[e.RelPath]
		if shape == "" {
			shape = string(command.EntryModeFull)
		}
		rows = append(rows, previewTableRow{
			Path:      e.RelPath,
			Size:      humanSize,
			Tokens:    fmt.Sprintf("~%d", tokens),
			Git:       badge,
			Modified:  formatFinderModifiedSpec(now, e.ModTime),
			Shape:     shape,
			sizeBytes: size,
			recency:   bucketRecency(now, e.ModTime),
			gitStatus: status,
		})
	}
	return rows, nil
}

func writePreviewTable(w io.Writer, rows []previewTableRow, colors platform.Palette) error {
	for _, line := range previewTableHeaderLines {
		if _, err := fmt.Fprintf(w, "%s%s%s\n", colors.Dim, line, colors.Reset); err != nil {
			return err
		}
	}
	return writePreviewTableAligned(w, rows, colors)
}

// writePreviewTableAligned writes the single table layout: a path line, then its
// metadata space-padded into aligned columns. On a TTY it colors size/tokens by
// magnitude (the tree's styleSize thresholds), the date by recency, the git badge
// by status (the tree's styleStatus), and the directory blue (basename plain, no
// bold). With an empty palette (piped / headless agent run) the same layout prints
// as plain text, machine-parseable by splitting each metadata line on runs of 2+
// spaces (no field value contains a double space, so the date's single spaces are
// safe). Cells are padded by their *visible* width so ANSI codes don't break
// alignment.
func writePreviewTableAligned(w io.Writer, rows []previewTableRow, colors platform.Palette) error {
	var wSize, wTok, wGit, wMod int
	for _, r := range rows {
		wSize = max(wSize, len(r.Size))
		wTok = max(wTok, len(r.Tokens))
		wGit = max(wGit, len(r.Git))
		wMod = max(wMod, len(r.Modified))
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%s\n", colorPreviewPath(r.Path, colors)); err != nil {
			return err
		}
		sizeColor := previewSizeColor(colors, r.sizeBytes)
		if _, err := fmt.Fprintf(w, "  %s  %s  %s  %s  %s\n",
			padColoredCell(r.Size, sizeColor, wSize, colors),
			padColoredCell(r.Tokens, sizeColor, wTok, colors),
			padColoredCell(r.Git, previewGitColor(colors, r.gitStatus), wGit, colors),
			padColoredCell(r.Modified, previewRecencyColor(colors, r.recency), wMod, colors),
			previewShapeColor(colors, r.Shape)+r.Shape+colors.Reset); err != nil {
			return err
		}
	}
	return nil
}

// padColoredCell wraps plain text in color and pads to a column width using the
// text's visible (uncolored) length so ANSI codes don't throw off alignment.
func padColoredCell(plain, color string, width int, colors platform.Palette) string {
	pad := max(width-len(plain), 0)
	return color + plain + colors.Reset + strings.Repeat(" ", pad)
}

// colorPreviewPath colors only the directory portion (blue, like the tree's
// folders); the basename stays the terminal default — no bold.
func colorPreviewPath(path string, colors platform.Palette) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return colors.Dir + path[:i+1] + colors.Reset + path[i+1:]
	}
	return path
}

// previewSizeColor colors size/tokens by the file's byte size, matching the tree's
// styleSize thresholds (so a 337 KB file reads red here just as it does in the
// tree): < 40 KB dim, < 200 KB warn, otherwise err.
func previewSizeColor(colors platform.Palette, sizeBytes int64) string {
	switch {
	case sizeBytes < 40000:
		return colors.Dim
	case sizeBytes < 200000:
		return colors.Warn
	default:
		return colors.Err
	}
}

// previewRecencyColor fades the modified date with age — no bold: fresh is green,
// then default, then grey, then dim.
func previewRecencyColor(colors platform.Palette, b recencyBucket) string {
	switch b {
	case recencyToday, recencyYesterday:
		return colors.OK
	case recencyMonth:
		return "" // terminal default
	case recencyYear:
		return colors.Label
	default:
		return colors.Dim
	}
}

// previewGitColor matches the tree's styleStatus: M/SM = warn, S = ok, ? = git
// (magenta), clean / unavailable ("[-]") = uncolored.
func previewGitColor(colors platform.Palette, status string) string {
	switch status {
	case "M", "SM":
		return colors.Warn
	case "S":
		return colors.OK
	case "?":
		return colors.Git
	default:
		return ""
	}
}

// previewShapeColor keeps the common "full" dim and tags the rest in magenta, the
// color the tree uses for mode badges. No bold.
func previewShapeColor(colors platform.Palette, shape string) string {
	if shape == string(command.EntryModeFull) {
		return colors.Dim
	}
	return colors.Git
}
