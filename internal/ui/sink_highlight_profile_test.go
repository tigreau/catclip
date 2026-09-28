package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/output"
)

// Opt-in profiling of the actual capped payload, excluding discovery/stat,
// output-plan construction and file reads from the CPU profile.
func TestSinkHighlightCorpusProfile(t *testing.T) {
	dir := os.Getenv("CATCLIP_BENCH_ARTIFACT_DIR")
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" || dir == "" {
		t.Skip("requires opt-in corpus and artifact directory")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(home, "Desktop", "catclip-test-data"))
	defer scopeViewMemoReset()
	ctx, err := buildStartupSinkPickerContext([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	w := output.NewPreviewCapWriter(&raw, context.Background(), output.PreviewByteLimit)
	if err := output.WriteOutputPlanPayloadWithoutPrefetch(w, ctx.Emit, ctx.Plan); err != nil && !errors.Is(err, output.ErrPreviewLimitReached) {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload-raw.txt"), raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	first := highlightFileBlocksForSinkPreview(raw.Bytes())
	t.Logf("raw_bytes=%d closed_blocks=%d highlighted_bytes=%d first_call=%s", raw.Len(), bytes.Count(raw.Bytes(), []byte("</file>")), len(first), time.Since(start))
	f, err := os.Create(filepath.Join(dir, "highlight.cpu"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pprof.StartCPUProfile(f); err != nil {
		t.Fatal(err)
	}
	defer pprof.StopCPUProfile()
	for i := 0; i < 15; i++ {
		start := time.Now()
		got := highlightFileBlocksForSinkPreview(raw.Bytes())
		if !bytes.Equal(first, got) {
			t.Fatal("highlighting changed bytes")
		}
		t.Logf("warm_round=%d duration=%s", i, time.Since(start))
	}
}
