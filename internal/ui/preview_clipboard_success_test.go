package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
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

func TestBundleDescriptionsFollowSurvivingOutput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kinds     []string
		want      string
		dedicated bool
	}{
		{"metadata only", []string{"file"}, "file metadata (1 file)", true},
		{"metadata and code", []string{"metadata", "file"}, "file metadata + code", false},
		{"paths only", []string{"paths"}, "file list (1 path)", false},
		{"paths and code", []string{"paths", "file"}, "file list + code", false},
		{"code then paths", []string{"file", "paths"}, "file list + code", false},
		{"metadata and paths", []string{"metadata", "paths"}, "file metadata (1 file)", false},
		{"all three", []string{"metadata", "paths", "file"}, "file metadata + code", false},
		{"empty source", []string{"metadata", "empty"}, "file metadata (1 file)", false},
		{"ordinary files", []string{"file"}, "1 file", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var scopes []command.ExecutionScope
			var evaluated []output.EvaluatedScope
			for _, kind := range tc.kinds {
				scopes = append(scopes, command.ExecutionScope{Metadata: kind == "metadata", Paths: kind == "paths"})
				entries := []discovery.Entry{{RelPath: "a.go", Mode: command.EntryModeFull, SizeKnown: true, SizeBytes: 12}}
				if kind == "empty" {
					entries = nil
				}
				evaluated = append(evaluated, output.EvaluatedScope{Paths: kind == "paths", Entries: entries})
			}
			plan, err := output.BuildPlanForResolvedScopes(git.Context{}, scopes, evaluated, nil)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			stats := output.EmitStats{SinkName: "bundle", BundlePath: "bundle.txt", PayloadBytes: 61440, Warnings: []string{"producer warning"}}
			if tc.dedicated {
				err = WriteMetadataClipboardSuccess(&out, plan, stats, platform.Palette{})
			} else {
				err = WriteClipboardSuccess(&out, plan, stats, platform.Palette{})
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Copied " + tc.want + " to clipboard as a bundle\nbundle.txt", "(60KB)\n\n", "Paste attaches a file — works in web UIs and file managers, not terminals.", "Use --no-bundle to copy text instead.", "Warning: producer warning"} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q: %s", want, out.String())
				}
			}
			wantOverview := strings.Contains(tc.want, "file metadata") && !strings.Contains(tc.want, "code")
			for _, hint := range []string{"File details only — no file contents.", "An AI can use this overview to suggest which files to share next."} {
				if strings.Contains(out.String(), hint) != wantOverview {
					t.Fatalf("overview hint %q presence should be %v: %s", hint, wantOverview, out.String())
				}
			}
			if got, want := strings.Contains(out.String(), "File paths only — no file contents."), tc.name == "paths only"; got != want {
				t.Fatalf("paths-only explanation present = %v, want %v: %s", got, want, out.String())
			}
		})
	}
}

func TestWriteMetadataClipboardSuccessNamesThePayload(t *testing.T) {
	plan := output.BuildPlan([]output.PreparedFileUnit{
		{Entry: discovery.Entry{RelPath: "a.go"}},
		{Entry: discovery.Entry{RelPath: "b.go"}},
	})
	var out bytes.Buffer
	if err := WriteMetadataClipboardSuccess(&out, plan, output.EmitStats{SinkName: "clipboard"}, platform.Palette{}); err != nil {
		t.Fatalf("WriteMetadataClipboardSuccess returned error: %v", err)
	}
	if got, want := strings.TrimSpace(out.String()), "Copied metadata for 2 files to clipboard"; got != want {
		t.Fatalf("success = %q, want %q", got, want)
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
