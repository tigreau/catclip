package catclip

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

// previewMetaSep splits an aligned metadata line into fields on runs of 2+ spaces
// (the date's single spaces are safe; no field value contains a double space).
var previewMetaSep = regexp.MustCompile(`\s{2,}`)

// previewTableFixture builds a small plan + report with mtimes pre-set (so
// ensureEntryModTimes does not stat real files) for the table renderer tests.
func previewTableFixture() (outputPlan, outputReport, time.Time) {
	now := time.Date(2026, 5, 27, 15, 45, 0, 0, time.UTC)
	items := []outputPlanItem{
		{relPath: "src/a.go", entry: fileEntry{RelPath: "src/a.go", AbsPath: "/x/src/a.go", ModTime: time.Date(2026, 5, 27, 9, 30, 0, 0, time.UTC)}, mode: entryModeFull},
		{relPath: "docs/very/long/path/notes.md", entry: fileEntry{RelPath: "docs/very/long/path/notes.md", AbsPath: "/x/docs/very/long/path/notes.md", ModTime: time.Date(2026, 3, 11, 2, 17, 0, 0, time.UTC)}, mode: entryModeFull},
	}
	plan := outputPlan{items: items}
	report := outputReport{
		sizes:     map[string]int64{"src/a.go": 7800, "docs/very/long/path/notes.md": 41000},
		statuses:  map[string]string{"src/a.go": "M"}, // notes.md has no status -> [-]
		modeTags:  map[string]string{},                // both full -> "full"
		humanSize: "47.7KB",
		tokens:    11950,
		countWord: "files",
	}
	return plan, report, now
}

// parsePreviewRecords parses the plain (no-color) table into path->5-field
// records, asserting the structure: # header lines skipped, a blank line ends the
// records (footer follows), each path is at column 0, and its metadata line is
// indented and splits into exactly 5 fields on runs of 2+ spaces.
func parsePreviewRecords(t *testing.T, out string) map[string][]string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	records := map[string][]string{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "#") {
			continue // header
		}
		if strings.TrimSpace(line) == "" {
			break // a blank line ends the records; the footer follows
		}
		if strings.HasPrefix(line, " ") {
			t.Fatalf("expected a path line at column 0, got: %q", line)
		}
		// path line; next line must be its indented metadata
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "  ") {
			t.Fatalf("path %q not followed by an indented metadata line", line)
		}
		fields := previewMetaSep.Split(strings.TrimSpace(lines[i+1]), -1)
		if len(fields) != 5 {
			t.Fatalf("path %q metadata split into %d fields, want 5: %q", line, len(fields), lines[i+1])
		}
		records[line] = fields
		i++ // consumed the metadata line
	}
	return records
}

func TestPreviewTablePlainParseable(t *testing.T) {
	plan, report, now := previewTableFixture()
	var buf bytes.Buffer
	// Empty palette = the plain (piped / headless) form.
	if err := renderPreviewTable(renderConfig{Preview: true, Quiet: true}, gitContext{}, plan, report, &buf, io.Discard, "", now, colorPalette{}); err != nil {
		t.Fatalf("renderPreviewTable: %v", err)
	}
	out := buf.String()

	if !strings.HasPrefix(out, previewTableHeaderLines[0]+"\n") {
		t.Fatalf("output must start with the # header, got:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("plain output must contain no ANSI:\n%s", out)
	}

	recs := parsePreviewRecords(t, out)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}

	// src/a.go: full path preserved (never truncated), git [M], today's date, full.
	a, ok := recs["src/a.go"]
	if !ok {
		t.Fatalf("missing record for src/a.go in:\n%s", out)
	}
	if a[2] != "[M]" {
		t.Errorf("src/a.go git = %q, want [M]", a[2])
	}
	if a[3] != "Today at 9:30 AM" {
		t.Errorf("src/a.go modified = %q, want Today at 9:30 AM", a[3])
	}
	if a[4] != "full" {
		t.Errorf("src/a.go shape = %q, want full", a[4])
	}

	// The long path is present in full (no ellipsis), with [-] for no git status.
	n, ok := recs["docs/very/long/path/notes.md"]
	if !ok {
		t.Fatalf("long path not preserved in full:\n%s", out)
	}
	if n[2] != "[-]" {
		t.Errorf("notes.md git = %q, want [-]", n[2])
	}
	if n[3] != "Mar 11, 2026 at 2:17 AM" {
		t.Errorf("notes.md modified = %q, want Mar 11, 2026 at 2:17 AM", n[3])
	}

	// Footer reuses the shared summary language.
	if !strings.Contains(out, "Count:") || !strings.Contains(out, "Tokens:") {
		t.Errorf("footer missing Count/Tokens:\n%s", out)
	}
}

func TestPreviewTableNormalAligned(t *testing.T) {
	plan, report, now := previewTableFixture()
	colors := colorPalette{Dir: "<dir>", OK: "<ok>", Warn: "<w>", Label: "<lbl>", Dim: "<dim>", Git: "<git>", Reset: "<r>"}
	var buf bytes.Buffer
	if err := renderPreviewTable(renderConfig{Preview: true, Quiet: true}, gitContext{}, plan, report, &buf, io.Discard, "", now, colors); err != nil {
		t.Fatalf("renderPreviewTable: %v", err)
	}
	out := buf.String()

	// No TABs in the aligned form.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "\t") {
			t.Errorf("aligned (normal) form must not contain TABs, got: %q", line)
		}
	}
	// Path: directory colored blue, basename at the terminal default (no bold).
	if !strings.Contains(out, "<dir>src/<r>a.go") {
		t.Errorf("directory not colored / basename should be plain:\n%s", out)
	}
	if strings.Contains(out, "a.go<r>") {
		t.Errorf("basename should not be wrapped in color:\n%s", out)
	}
	// Git badge colored by status (tree styleStatus): [M] -> warn.
	if !strings.Contains(out, "<w>[M]<r>") {
		t.Errorf("modified-status badge not warn-colored:\n%s", out)
	}
	// Date colored by recency: today -> ok (fresh); ~77 days ago -> label (aged).
	if !strings.Contains(out, "<ok>Today at 9:30 AM<r>") {
		t.Errorf("today's date not freshness-colored:\n%s", out)
	}
	if !strings.Contains(out, "<lbl>Mar 11, 2026 at 2:17 AM<r>") {
		t.Errorf("older date not aged-colored:\n%s", out)
	}
}

// TestPreviewSizeColorMatchesTree pins size coloring to the tree's styleSize
// thresholds (40 KB / 200 KB), so a large file reads red in both surfaces.
func TestPreviewSizeColorMatchesTree(t *testing.T) {
	colors := colorPalette{Dim: "<dim>", Warn: "<w>", Err: "<e>"}
	cases := []struct {
		bytes int64
		want  string
	}{
		{1000, "<dim>"},     // tiny
		{39999, "<dim>"},    // just under 40 KB
		{40000, "<w>"},      // 40 KB -> warn
		{199999, "<w>"},     // just under 200 KB
		{200000, "<e>"},     // 200 KB -> err
		{337 * 1024, "<e>"}, // main_test.go scale -> red
	}
	for _, tc := range cases {
		if got := previewSizeColor(colors, tc.bytes); got != tc.want {
			t.Errorf("previewSizeColor(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

// TestPreviewTableRowsAreEmissionOrderUnique proves one row per file in emission
// order, even when a file appears in multiple plan items.
func TestPreviewTableRowsAreEmissionOrderUnique(t *testing.T) {
	now := time.Date(2026, 5, 27, 15, 45, 0, 0, time.UTC)
	mt := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	plan := outputPlan{items: []outputPlanItem{
		{relPath: "z.go", entry: fileEntry{RelPath: "z.go", AbsPath: "/x/z.go", ModTime: mt}, mode: entryModeSnippet},
		{relPath: "z.go", entry: fileEntry{RelPath: "z.go", AbsPath: "/x/z.go", ModTime: mt}, mode: entryModeSnippet}, // second range, same file
		{relPath: "a.go", entry: fileEntry{RelPath: "a.go", AbsPath: "/x/a.go", ModTime: mt}, mode: entryModeFull},
	}}
	report := outputReport{sizes: map[string]int64{"z.go": 200, "a.go": 100}}
	rows, err := buildPreviewTableRows(plan, report, "", now)
	if err != nil {
		t.Fatalf("buildPreviewTableRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (one per file)", len(rows))
	}
	if rows[0].Path != "z.go" || rows[1].Path != "a.go" {
		t.Errorf("rows not in emission order: %q then %q", rows[0].Path, rows[1].Path)
	}
}
