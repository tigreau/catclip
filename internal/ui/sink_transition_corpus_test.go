package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This is the pre-picker sink path, not a checkpoint microbenchmark. All
// target metadata is complete before the clock starts. No sink is opened.
func TestSinkTransitionCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full corpus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	corpus := filepath.Join(home, "Desktop", "catclip-test-data")
	t.Chdir(corpus)
	defer scopeViewMemoReset()
	for _, extra := range [][]string{nil, {"--paths"}, {"--only", "*.h"}} {
		scopeViewMemoReset()
		args := append([]string{"."}, extra...)
		view, err := resolvedCurrentScopeViewForArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := retainedScopeViewEntriesWithMetadata(view); !ok {
			t.Fatal("metadata not ready")
		}
		for i := 0; i < 3; i++ {
			start := time.Now()
			ctx, err := buildStartupSinkPickerContext(args)
			if err != nil {
				t.Fatal(err)
			}
			contextTime := time.Since(start)
			startMeasure := time.Now()
			measurement := measureStartupSinkPayload(ctx)
			if measurement.Err != nil {
				t.Fatal(measurement.Err)
			}
			measureTime := time.Since(startMeasure)
			startFiles := time.Now()
			ctx.rawOutputPreview = measurement.RawPreview
			files, err := prepareStartupSinkPreviewFiles(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			fileTime := time.Since(startFiles)
			t.Logf("args=%v entries=%d context=%s measurement=%s preview_files=%s pre_picker_total=%s", args, len(view.Entries), contextTime, measureTime, fileTime, time.Since(start))
			files.Cleanup()
		}
		scopeViewMemoReset()
	}
}
