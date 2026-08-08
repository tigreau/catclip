package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

// TestWriteClipboardSuccessUsesDistinctSummarySubjects pins the
// success-message phrasing branches: paths-only, mixed paths+files, and
// single-distinct-relpath. Restored after the v0.6.0 output extraction
// using the exported test helpers (output.NewFilePlanItem /
// output.NewPathPlanItem / output.PlanFromItemsForTesting).
func TestWriteClipboardSuccessUsesDistinctSummarySubjects(t *testing.T) {
	tests := []struct {
		name     string
		plan     output.Plan
		wantText string
	}{
		{
			name: "paths only",
			plan: output.PlanFromItemsForTesting([]output.PlanItem{
				output.NewPathPlanItem(discovery.Entry{RelPath: "src/a.ts"}),
				output.NewPathPlanItem(discovery.Entry{RelPath: "src/b.ts"}),
			}),
			wantText: "Copied 2 paths to clipboard (src/a.ts ... src/b.ts)",
		},
		{
			name: "mixed uses items and distinct relpaths",
			plan: output.PlanFromItemsForTesting([]output.PlanItem{
				output.NewPathPlanItem(discovery.Entry{RelPath: "src/a.ts"}),
				output.NewFilePlanItem(output.PreparedFileUnit{Entry: discovery.Entry{RelPath: "src/a.ts", Mode: command.EntryModeFull}, BodyBytes: 10}),
				output.NewFilePlanItem(output.PreparedFileUnit{Entry: discovery.Entry{RelPath: "src/b.ts", Mode: command.EntryModeFull}, BodyBytes: 12}),
			}),
			wantText: "Copied 2 items to clipboard (src/a.ts ... src/b.ts)",
		},
		{
			name: "single distinct relpath stays direct",
			plan: output.PlanFromItemsForTesting([]output.PlanItem{
				output.NewPathPlanItem(discovery.Entry{RelPath: "src/a.ts"}),
				output.NewFilePlanItem(output.PreparedFileUnit{Entry: discovery.Entry{RelPath: "src/a.ts", Mode: command.EntryModeFull}, BodyBytes: 10}),
			}),
			wantText: "Copied src/a.ts to clipboard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := WriteClipboardSuccess(&out, tt.plan, output.EmitStats{}, platform.Palette{}); err != nil {
				t.Fatalf("WriteClipboardSuccess returned error: %v", err)
			}
			if got := strings.TrimSpace(out.String()); got != tt.wantText {
				t.Fatalf("WriteClipboardSuccess output = %q, want %q", got, tt.wantText)
			}
		})
	}
}

// TestWriteBundleSuccessIncludesWarnings pins the bundle-success
// formatter: warnings emitted by the EmitStats producer must survive into
// user-facing output when the bundle sink path runs.
func TestWriteBundleSuccessIncludesWarnings(t *testing.T) {
	plan := output.PlanFromItemsForTesting([]output.PlanItem{
		output.NewFilePlanItem(output.PreparedFileUnit{Entry: discovery.Entry{RelPath: "src/a.ts", Mode: command.EntryModeFull}, BodyBytes: 10}),
	})
	stats := output.EmitStats{
		SinkName:     "bundle",
		BundlePath:   "/tmp/catclip/catclip-123.txt",
		PayloadBytes: 5000,
		Warnings:     []string{"producer warning"},
	}

	var out bytes.Buffer
	if err := WriteClipboardSuccess(&out, plan, stats, platform.Palette{}); err != nil {
		t.Fatalf("WriteClipboardSuccess returned error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Warning:", "producer warning"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bundle success output missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Use --no-bundle to copy text instead.") {
		t.Fatalf("bundle success output missing success-level --no-bundle guidance:\n%s", got)
	}
}
