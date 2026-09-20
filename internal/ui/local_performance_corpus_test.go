package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/search"
)

func TestLocalPerformanceCorpusTiming(t *testing.T) {
	testLocalPerformanceCorpusTiming(t, []string{"."})
}

func TestLocalPerformanceNoIgnoreCorpusTiming(t *testing.T) {
	testLocalPerformanceCorpusTiming(t, []string{".", "--no-ignore", "--with-binaries"})
}

func testLocalPerformanceCorpusTiming(t *testing.T, args []string) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full corpus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "Desktop", "catclip-test-data")
	t.Chdir(root)
	defer scopeViewMemoReset()
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := retainedScopeViewEntriesWithMetadata(view); !ok {
		t.Fatal("metadata not ready")
	}
	ctx, err := buildStartupSinkPickerContext(args)
	if err != nil {
		t.Fatal(err)
	}
	paths := ctx.Plan.DistinctRelPaths()
	seed := search.StartTextSizeCapture(root, paths[:len(paths)/2])
	<-seed.Done()
	defer seed.Stop()
	for round := 0; round < 5; round++ {
		for offset := 0; offset < 2; offset++ {
			variant := (round + offset) % 2
			start := time.Now()
			if variant == 0 {
				p, err := renderSinkOutputTextPreview(ctx.Plan, ctx.Emit, output.PreviewByteLimit)
				if err != nil {
					t.Fatal(err)
				}
				if p.FullBytes < output.BundleThreshold {
					t.Fatal("expected large fixture")
				}
			} else {
				m := measureStartupSinkPayload(ctx)
				if m.Err != nil || !m.WouldBundle {
					t.Fatalf("measurement: %+v", m)
				}
			}
			t.Logf("phase=measurement round=%d variant=%d ms=%.3f", round, variant, float64(time.Since(start))/float64(time.Millisecond))
			start = time.Now()
			var capture *search.TextSizeCapture
			if variant == 0 {
				capture = search.StartTextSizeCapture(root, paths)
			} else {
				capture = search.ResumeTextSizeCapture(root, paths, seed)
			}
			<-capture.Done()
			elapsed := time.Since(start)
			metadata := capture.MetadataSnapshot()
			capture.Stop()
			if len(metadata) != len(paths) {
				t.Fatalf("metadata count %d != %d", len(metadata), len(paths))
			}
			t.Logf("phase=undo_capture round=%d variant=%d files=%d seeded=%d ms=%.3f", round, variant, len(paths), len(paths)/2, float64(elapsed)/float64(time.Millisecond))
		}
	}
}
