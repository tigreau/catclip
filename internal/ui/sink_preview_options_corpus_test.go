package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

// Diagnostic prototypes only: no production cache or scheduling is changed.
// Setup/discovery/stat is outside the measured region. Variants rotate order,
// with GC before each sample; allocations are process-wide Go TotalAlloc, not RSS.
func TestSinkPreviewOptionsCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full corpus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(home, "Desktop", "catclip-test-data"))
	defer scopeViewMemoReset()
	view, err := resolvedCurrentScopeViewForArgs([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := retainedScopeViewEntriesWithMetadata(view); !ok {
		t.Fatal("metadata not ready")
	}
	ctx, err := buildStartupSinkPickerContext([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("entries=%d", len(view.Entries))
	// Keep the original uncached baseline even when production sink contexts
	// now carry prepared presentation state.
	ctx.Render = ctx.Render.WithPreparedPresentation(nil)
	buildModel := func() treeDocument {
		return treeDocument{Mode: treeDocumentModeTree,
			Entries: renderpkg.SortedEntries(treeEntriesFromPlan(ctx.Plan, ctx.Report)), EntriesSorted: true}
	}
	doc := buildModel()
	renderCached := func(w io.Writer, colors platform.Palette) error {
		if !ctx.Render.Quiet {
			if err := writeFilterSummary(w, ctx.Git, colors); err != nil {
				return err
			}
			if err := writeReportNotices(w, ctx.Report, colors); err != nil {
				return err
			}
		}
		if !ctx.Render.NoTree {
			if err := renderTreeDocument(w, doc, treeRenderOptions{
				ShowModeTags: true, ShowSizes: true, ShowGitStatus: true,
			}, colors); err != nil {
				return err
			}
		}
		return writeSummary(w, ctx.Report, colors)
	}
	// Check complete output as well as truncated previews, in both palettes.
	for _, colors := range []platform.Palette{platform.ANSIPalette(), {}} {
		for _, limit := range []int64{output.PreviewByteLimit, math.MaxInt64} {
			var baseline, cached bytes.Buffer
			w1 := output.NewPreviewCapWriter(&baseline, context.Background(), limit)
			w2 := output.NewPreviewCapWriter(&cached, context.Background(), limit)
			e1 := RenderPreview(ctx.Render, ctx.Git, ctx.Plan, ctx.Report, w1, w1, colors)
			e2 := renderCached(w2, colors)
			if !errors.Is(e1, e2) || w1.Truncated() != w2.Truncated() || !bytes.Equal(baseline.Bytes(), cached.Bytes()) {
				t.Fatal("cached model changed rendered bytes or truncation")
			}
			t.Logf("parity ansi=%t limit=%d bytes=%d truncated=%t", colors.Reset != "", limit, baseline.Len(), w1.Truncated())
			if dir := os.Getenv("CATCLIP_BENCH_ARTIFACT_DIR"); dir != "" && colors.Reset != "" {
				name := "tree-full.txt"
				if limit == output.PreviewByteLimit {
					name = "tree-capped.txt"
				}
				if err := os.WriteFile(filepath.Join(dir, name), baseline.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	check := func(err error) {
		if err != nil && !errors.Is(err, output.ErrPreviewLimitReached) {
			t.Fatal(err)
		}
	}
	type variant struct {
		name   string
		run    func() int64
		times  []float64
		allocs []float64
	}
	variants := []variant{
		{name: "report_rebuild", run: func() int64 {
			report, err := output.BuildReportForPlan(ctx.Git, ctx.Plan, output.ReportOptions{IncludeTreeMetadata: true, Notices: ctx.Report.Notices})
			check(err)
			return int64(len(report.Sizes))
		}},
		{name: "sorted_model_build", run: func() int64 { return int64(len(buildModel().Entries)) }},
	}
	for _, limit := range []int64{output.PreviewByteLimit, math.MaxInt64} {
		label := "capped"
		if limit == math.MaxInt64 {
			label = "full"
		}
		variants = append(variants,
			variant{name: "tree_current_" + label, run: func() int64 {
				p, err := renderSinkTreeReportPreview(ctx, limit)
				check(err)
				return int64(len(p.Body))
			}},
			variant{name: "tree_cached_model_" + label, run: func() int64 {
				var buf bytes.Buffer
				w := output.NewPreviewCapWriter(&buf, context.Background(), limit)
				check(renderCached(w, platform.ANSIPalette()))
				body := append([]byte(nil), buf.Bytes()...)
				return int64(len(body))
			}},
		)
	}
	variants = append(variants, variant{name: "tree_cached_model_full_discard", run: func() int64 {
		check(renderCached(io.Discard, platform.ANSIPalette()))
		return 0
	}})
	for _, limit := range []int64{output.BundleThreshold, output.PreviewByteLimit} {
		label := "4k"
		if limit == output.PreviewByteLimit {
			label = "128k"
		}
		variants = append(variants, variant{name: "payload_raw_probe_" + label, run: func() int64 {
			w := output.NewPreviewCapWriter(io.Discard, context.Background(), limit)
			check(output.WriteOutputPlanPayloadWithoutPrefetch(w, ctx.Emit, ctx.Plan))
			return w.BytesWritten()
		}})
	}
	variants = append(variants, variant{name: "payload_raw_buffer_128k", run: func() int64 {
		var buf bytes.Buffer
		w := output.NewPreviewCapWriter(&buf, context.Background(), output.PreviewByteLimit)
		check(output.WriteOutputPlanPayloadWithoutPrefetch(w, ctx.Emit, ctx.Plan))
		return int64(buf.Len())
	}})
	variants = append(variants, variant{name: "payload_highlight_128k", run: func() int64 {
		p, err := renderSinkOutputTextPreview(ctx.Plan, ctx.Emit, output.PreviewByteLimit)
		check(err)
		return int64(len(p.Body))
	}})
	for round := 0; round < 5; round++ {
		for offset := range variants {
			v := &variants[(offset+round)%len(variants)]
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			n := v.run()
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			v.times = append(v.times, float64(elapsed)/float64(time.Millisecond))
			v.allocs = append(v.allocs, float64(after.TotalAlloc-before.TotalAlloc)/(1<<20))
			t.Logf("round=%d variant=%s ms=%.3f alloc_MiB=%.3f units=%d", round, v.name, v.times[len(v.times)-1], v.allocs[len(v.allocs)-1], n)
		}
	}
	for _, v := range variants {
		slices.Sort(v.times)
		slices.Sort(v.allocs)
		t.Logf("SUMMARY %s median_ms=%.3f range_ms=%.3f..%.3f median_alloc_MiB=%.3f", v.name, v.times[2], v.times[0], v.times[4], v.allocs[2])
	}
}

func TestSinkThresholdMeasurementDiagnostic(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in diagnostic; records current behavior, not desired contract")
	}
	root := setupTestProject(t, map[string]string{"small.go": "package main\n" + strings.Repeat("var value = 123 // a comment\n", 65)})
	t.Chdir(root)
	defer scopeViewMemoReset()
	ctx, err := buildStartupSinkPickerContext([]string{"small.go"})
	if err != nil {
		t.Fatal(err)
	}
	m := measureStartupSinkPayload(ctx)
	if m.Err != nil {
		t.Fatal(m.Err)
	}
	var raw bytes.Buffer
	if err := output.WriteOutputPlanPayloadWithoutPrefetch(&raw, ctx.Emit, ctx.Plan); err != nil {
		t.Fatal(err)
	}
	t.Logf("raw_bytes=%d measured_bytes=%d threshold=%d would_bundle=%t raw_requires_bundle=%t", raw.Len(), m.Bytes, output.BundleThreshold, m.WouldBundle, raw.Len() >= output.BundleThreshold)
}
