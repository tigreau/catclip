package ui

import (
	"bytes"
	"context"
	"io"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func TestPreparedPresentationDetachesReportsAndChecksIdentity(t *testing.T) {
	project := setupTestProject(t, map[string]string{"a.go": "package a\n"})
	plan := testSinkOutputPlan(t, project, "a.go")
	report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{IncludeTreeMetadata: true, Notices: []string{"notice"}})
	if err != nil {
		t.Fatal(err)
	}
	report.Statuses = map[string]string{"a.go": "M"}
	want := report.Clone()
	p := PreparePresentation(git.Context{}, plan, report)
	report.Sizes["a.go"] = 999
	report.Statuses["a.go"] = "D"
	report.ModeTags["a.go"] = "corrupt"
	report.Notices[0] = "corrupt"
	got, ok := p.ReportForPlan(git.Context{}, plan, []string{"notice"})
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatal("constructor retained mutable report inputs")
	}
	got.Sizes["a.go"] = 888
	got.Statuses["a.go"] = "D"
	got.ModeTags["a.go"] = "corrupt"
	got.Notices[0] = "corrupt"
	again, _ := p.ReportForPlan(git.Context{}, plan, []string{"notice"})
	if !reflect.DeepEqual(again, want) {
		t.Fatal("returned report corrupted prepared state")
	}
	other := testSinkOutputPlan(t, project, "a.go")
	if _, ok := p.ReportForPlan(git.Context{}, other, want.Notices); ok {
		t.Fatal("independent plan reused report")
	}
	if _, ok := p.ReportForPlan(git.Context{Enabled: true}, plan, want.Notices); ok {
		t.Fatal("different Git context reused report")
	}
	if _, ok := p.ReportForPlan(git.Context{}, plan, []string{"new notice"}); ok {
		t.Fatal("new notices reused stale report")
	}
	if p.matches(plan, got) {
		t.Fatal("modified report reused stale tree")
	}
}

func TestPreparedPresentationCappedAndFinalParity(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/a.go": "package a\n", "other/src/b.go": "package b\n"})
	t.Chdir(project)
	defer scopeViewMemoReset()
	ctx, err := buildStartupSinkPickerContext([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	plan := ctx.Plan
	report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{IncludeTreeMetadata: true})
	if err != nil {
		t.Fatal(err)
	}
	p := PreparePresentation(git.Context{}, plan, report)
	for _, colors := range []platform.Palette{platform.ANSIPalette(), {}} {
		for _, limit := range []int64{45, math.MaxInt64} {
			for _, cfg := range []RenderConfig{{}, {NoTree: true}, {Quiet: true}} {
				var want, got bytes.Buffer
				wc := output.NewPreviewCapWriter(&want, context.Background(), limit)
				gc := output.NewPreviewCapWriter(&got, context.Background(), limit)
				e1 := RenderPreview(cfg, git.Context{}, plan, report, wc, wc, colors)
				e2 := RenderPreview(cfg.WithPreparedPresentation(p), git.Context{}, plan, report, gc, gc, colors)
				if e1 != e2 || wc.Truncated() != gc.Truncated() || !bytes.Equal(want.Bytes(), got.Bytes()) {
					t.Fatalf("render mismatch cfg=%+v limit=%d", cfg, limit)
				}
			}
		}
	}
	if p.tree == nil {
		t.Fatal("preview never prepared tree")
	}
	tree := p.tree
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = RenderPreview(RenderConfig{}.WithPreparedPresentation(p), git.Context{}, plan, report, io.Discard, io.Discard, platform.Palette{})
		}()
	}
	group.Wait()
	if p.tree != tree {
		t.Fatal("tree was rebuilt")
	}
	changed := report.Clone()
	changed.Sizes["src/a.go"] = 555
	var got bytes.Buffer
	if err := RenderPreview(RenderConfig{}.WithPreparedPresentation(p), git.Context{}, plan, changed, &got, &got, platform.Palette{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.String(), "555B") {
		t.Fatal("modified report reused stale tree sizes")
	}
}

func TestPreparedPresentationSuppressedTreeStaysLazy(t *testing.T) {
	project := setupTestProject(t, map[string]string{"a.go": "package a\n"})
	plan := testSinkOutputPlan(t, project, "a.go")
	report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{IncludeTreeMetadata: true})
	if err != nil {
		t.Fatal(err)
	}
	p := PreparePresentation(git.Context{}, plan, report)
	for _, cfg := range []RenderConfig{{NoTree: true}, {Quiet: true}} {
		if _, err := WriteNormalDiagnostics(cfg.WithPreparedPresentation(p), git.Context{}, plan, report,
			command.EmissionNever, io.Discard, io.Discard, platform.Palette{}, platform.Palette{}); err != nil {
			t.Fatal(err)
		}
	}
	if p.tree != nil {
		t.Fatal("suppressed final diagnostics prepared unused tree")
	}
	// Simultaneous first readers must safely publish one complete model.
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := RenderPreview(RenderConfig{}.WithPreparedPresentation(p), git.Context{}, plan, report,
				io.Discard, io.Discard, platform.Palette{}); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if p.tree == nil {
		t.Fatal("first readers failed to prepare tree")
	}
}
