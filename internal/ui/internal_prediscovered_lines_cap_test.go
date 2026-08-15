package ui

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

// TestRunInternalLinesPreviewCapsLargePayload verifies the per-focus
// --lines preview is byte-capped via output.PreviewCapWriter: a file
// large enough to blow past PreviewByteLimit emits bounded output
// followed by the truncation footer, instead of streaming the full
// body.
func TestRunInternalLinesPreviewCapsLargePayload(t *testing.T) {
	// One big file is enough — the preview emits the full body when not
	// capped; PreviewByteLimit is 128 KiB, so make the body comfortably
	// larger (256 KiB).
	bigLine := strings.Repeat("x", 1023) + "\n"
	bigBody := strings.Repeat(bigLine, 260) // ~260 KiB
	project := setupTestProject(t, map[string]string{
		"src/huge.txt": bigBody,
	})

	parentCfg := parseInProject(t, project, []string{"src/huge.txt"})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(command.InvocationFromParsed(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("discovery.EvaluateScope returned error: %v", err)
	}

	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	previewArgs := []string{
		"--quiet",
		"--internal-lines-preview",
		"--internal-prediscovered", checkpointPath,
		"src/huge.txt",
		"--lines", "1", "999999",
	}
	previewCfg, err := cli.ParseArgs(previewArgs)
	if err != nil {
		t.Fatalf("cli.ParseArgs returned error: %v", err)
	}

	prediscoveredCfg := PrediscoveredCommandConfigFromParsedCommand(previewCfg)
	emitCfg := emitConfigFromParsedCommand(previewCfg)

	var out bytes.Buffer
	if err := RunInternalLinesPreview(prediscoveredCfg, emitCfg, &out); err != nil {
		t.Fatalf("RunInternalLinesPreview returned error: %v", err)
	}

	cap := int(output.PreviewByteLimit)
	footer := "[lines preview truncated at 128 KiB"
	if !strings.Contains(out.String(), footer) {
		t.Fatalf("expected truncation footer %q in output, got last 200 bytes: %q",
			footer, tailOf(out.Bytes(), 200))
	}
	// Total bytes = capped body (≤ PreviewByteLimit) + footer (small
	// constant). A loose budget of cap + 1 KiB catches catastrophic
	// regressions without flaking on minor footer wording changes.
	if got, ceiling := out.Len(), cap+1024; got > ceiling {
		t.Fatalf("expected total output ≤ %d (cap + footer slack), got %d", ceiling, got)
	}
	// Sanity: the body that landed before the footer should be close to
	// the cap — if it's tiny something else short-circuited.
	bodyEnd := strings.Index(out.String(), footer)
	if bodyEnd < cap/2 {
		t.Fatalf("body before footer should be near the cap (>%d), got %d bytes", cap/2, bodyEnd)
	}
}

// TestRunInternalLinesPreviewPassesThroughUnderCap verifies the cap
// wrapper is invisible when output fits: no footer is appended, all
// bytes flow through unchanged.
func TestRunInternalLinesPreviewPassesThroughUnderCap(t *testing.T) {
	small := "hello\nworld\n"
	project := setupTestProject(t, map[string]string{
		"src/small.txt": small,
	})

	parentCfg := parseInProject(t, project, []string{"src/small.txt"})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(command.InvocationFromParsed(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("discovery.EvaluateScope returned error: %v", err)
	}

	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	previewArgs := []string{
		"--quiet",
		"--internal-lines-preview",
		"--internal-prediscovered", checkpointPath,
		"src/small.txt",
		"--lines", "1", "999999",
	}
	previewCfg, err := cli.ParseArgs(previewArgs)
	if err != nil {
		t.Fatalf("cli.ParseArgs returned error: %v", err)
	}

	prediscoveredCfg := PrediscoveredCommandConfigFromParsedCommand(previewCfg)
	emitCfg := emitConfigFromParsedCommand(previewCfg)

	var out bytes.Buffer
	if err := RunInternalLinesPreview(prediscoveredCfg, emitCfg, &out); err != nil {
		t.Fatalf("RunInternalLinesPreview returned error: %v", err)
	}

	if strings.Contains(out.String(), "[lines preview truncated") {
		t.Fatalf("small payload should not trip the cap; got footer in:\n%s", out.String())
	}
	// Output should contain every line of the original body verbatim.
	for _, line := range []string{"hello", "world"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("expected output to contain %q, got:\n%s", line, out.String())
		}
	}
}

func tailOf(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[len(b)-n:])
}
