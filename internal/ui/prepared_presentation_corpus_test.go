package ui

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func TestPreparedPresentationCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full corpus")
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
	base := ctx.Render.WithPreparedPresentation(nil)
	p := ctx.Presentation
	check := func(err error) {
		if err != nil && !errors.Is(err, output.ErrPreviewLimitReached) {
			t.Fatal(err)
		}
	}
	render := func(cfg RenderConfig, report output.Report, limit int64, colors platform.Palette) []byte {
		var buf bytes.Buffer
		w := output.NewPreviewCapWriter(&buf, context.Background(), limit)
		check(RenderPreview(cfg, ctx.Git, ctx.Plan, report, w, w, colors))
		return buf.Bytes()
	}
	// Prime exactly as the capped worker does; the retained model must still
	// contain every row for the later complete final tree.
	runtime.GC()
	var liveBefore, liveAfter runtime.MemStats
	runtime.ReadMemStats(&liveBefore)
	_ = render(ctx.Render, ctx.Report, output.PreviewByteLimit, platform.ANSIPalette())
	runtime.GC()
	runtime.ReadMemStats(&liveAfter)
	t.Logf("prepared tree live heap delta_MiB=%.3f (post-GC process delta, not peak RSS)",
		float64(int64(liveAfter.HeapAlloc)-int64(liveBefore.HeapAlloc))/(1<<20))
	for _, colors := range []platform.Palette{platform.ANSIPalette(), {}} {
		want := render(base, ctx.Report, math.MaxInt64, colors)
		got := render(ctx.Render, ctx.Report, math.MaxInt64, colors)
		if !bytes.Equal(want, got) {
			t.Fatal("prepared final tree lost or changed bytes")
		}
		t.Logf("full parity ansi=%t bytes=%d entries=%d", colors.Reset != "", len(got), ctx.Plan.Len())
	}
	type variant struct {
		name          string
		run           func() int
		times, allocs []float64
	}
	variants := []variant{
		{name: "report_rebuild", run: func() int {
			r, e := output.BuildReportForPlan(ctx.Git, ctx.Plan, output.ReportOptions{IncludeTreeMetadata: true, Notices: ctx.Report.Notices})
			check(e)
			return len(r.Sizes)
		}},
		{name: "report_reuse_detached", run: func() int {
			r, ok := p.ReportForPlan(ctx.Git, ctx.Plan, ctx.Report.Notices)
			if !ok {
				t.Fatal("reuse miss")
			}
			return len(r.Sizes)
		}},
		{name: "first_capped_tree_baseline", run: func() int { return len(render(base, ctx.Report, output.PreviewByteLimit, platform.ANSIPalette())) }},
		{name: "first_capped_tree_with_snapshot", run: func() int {
			fresh := PreparePresentation(ctx.Git, ctx.Plan, ctx.Report)
			return len(render(base.WithPreparedPresentation(fresh), ctx.Report, output.PreviewByteLimit, platform.ANSIPalette()))
		}},
		{name: "repeated_capped_tree", run: func() int {
			return len(render(ctx.Render, ctx.Report, output.PreviewByteLimit, platform.ANSIPalette()))
		}},
		{name: "final_report_and_tree_baseline", run: func() int {
			r, e := output.BuildReportForPlan(ctx.Git, ctx.Plan, output.ReportOptions{IncludeTreeMetadata: true, Notices: ctx.Report.Notices})
			check(e)
			return len(render(base, r, math.MaxInt64, platform.ANSIPalette()))
		}},
		{name: "final_report_and_tree_reused", run: func() int {
			r, ok := p.ReportForPlan(ctx.Git, ctx.Plan, ctx.Report.Notices)
			if !ok {
				t.Fatal("reuse miss")
			}
			return len(render(ctx.Render, r, math.MaxInt64, platform.ANSIPalette()))
		}},
	}
	for round := 0; round < 5; round++ {
		for offset := range variants {
			v := &variants[(round+offset)%len(variants)]
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
		t.Logf("SUMMARY %s median_ms=%.3f range_ms=%.3f..%.3f alloc_MiB=%.3f", v.name, v.times[2], v.times[0], v.times[4], v.allocs[2])
	}
}
