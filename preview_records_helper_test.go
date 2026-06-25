package catclip

import (
	"regexp"
	"strings"
	"testing"
)

// previewMetaSep splits an aligned metadata line into fields on runs of 2+ spaces.
var previewMetaSep = regexp.MustCompile(`\s{2,}`)

// parsePreviewRecordsRoot is a root dup of internal/ui's
// preview_table_test.ParsePreviewRecords. Test helpers in _test.go
// files are package-private, so root tests that run catclip via Main()
// and assert on its preview output (TestRunPreviewQuiet*) keep their
// own copy here. The dup is small (~20 LOC) and the two should stay
// in lockstep.
func parsePreviewRecordsRoot(t *testing.T, out string) map[string][]string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	records := map[string][]string{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		if strings.HasPrefix(line, " ") {
			t.Fatalf("expected a path line at column 0, got: %q", line)
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "  ") {
			t.Fatalf("path %q not followed by an indented metadata line", line)
		}
		fields := previewMetaSep.Split(strings.TrimSpace(lines[i+1]), -1)
		if len(fields) != 5 {
			t.Fatalf("path %q metadata split into %d fields, want 5: %q", line, len(fields), lines[i+1])
		}
		records[line] = fields
		i++
	}
	return records
}

// rootAnsiEscape matches SGR sequences chroma's TTY formatter emits.
// Dup of internal/ui/snippet_boundary_preview_test.go's AnsiEscape so
// root tests strip ANSI from stdout-captured output without depending
// on a UI test-only helper.
var rootAnsiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)
