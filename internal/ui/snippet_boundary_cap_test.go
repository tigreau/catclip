package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/output"
)

// TestRunInternalSnippetBoundaryPreviewCapsLargePayload verifies that the
// per-focus snippet boundary preview wraps stdout in PreviewCapWriter:
// when many matched files would emit more than PreviewByteLimit bytes,
// the handler stops mid-stream and appends the truncation footer instead
// of dumping the full payload to fzf's preview pipe.
func TestRunInternalSnippetBoundaryPreviewCapsLargePayload(t *testing.T) {
	files := map[string]string{}
	// Big bodies × many files — at "block" boundary each match expands to
	// the whole containing block, so a single TODO inside a long block
	// yields a large per-file payload. 60 files × ~5 KiB block each ≈
	// 300 KiB total — comfortably over the 128 KiB cap.
	for i := 0; i < 60; i++ {
		filler := strings.Repeat("filler line "+strings.Repeat("x", 60)+"\n", 80)
		body := "func F() {\n" + filler + "\tTODO marker\n" + filler + "}\n"
		path := filepath.Join("src", "f"+padIndex(i)+".go")
		files[path] = body
	}
	project := setupTestProject(t, files)
	_ = parseInProject(t, project, []string{"."})

	view, err := resolvedCurrentScopeViewForArgs([]string{"src"})
	if err != nil {
		t.Fatalf("resolvedCurrentScopeViewForArgs: %v", err)
	}
	matched, err := snippetBoundaryPreviewMatchedEntries(view, "TODO", nil)
	if err != nil {
		t.Fatalf("snippetBoundaryPreviewMatchedEntries: %v", err)
	}
	cmd, tmpdir := buildSnippetBoundaryPreviewForScope(view, "TODO", matched, nil)
	if cmd == "" || tmpdir == "" {
		t.Fatal("expected snippet boundary preview command and tmpdir")
	}
	defer os.RemoveAll(tmpdir)

	sourcePath := filepath.Join(tmpdir, "source.json")
	var buf bytes.Buffer
	if err := RunInternalSnippetBoundaryPreview(sourcePath, "block", &buf); err != nil {
		t.Fatalf("RunInternalSnippetBoundaryPreview returned error: %v", err)
	}

	cap := int(output.PreviewByteLimit)
	footer := "[snippet preview truncated at 128 KiB]"
	if !strings.Contains(buf.String(), footer) {
		t.Fatalf("expected truncation footer %q in output (len=%d); tail:\n%s",
			footer, buf.Len(), tailOf(buf.Bytes(), 400))
	}
	// Some slack: cap + ANSI tail for the last block + footer. A loose
	// 4 KiB upper bound over the cap catches catastrophic regressions
	// (unbounded streaming) without flaking on ANSI-byte variations.
	if got, ceiling := buf.Len(), cap+4096; got > ceiling {
		t.Fatalf("output ≤ %d expected, got %d", ceiling, got)
	}
}

func padIndex(i int) string {
	s := []byte("00")
	if i >= 10 {
		s[0] = byte('0' + i/10)
	}
	s[1] = byte('0' + i%10)
	return string(s)
}
