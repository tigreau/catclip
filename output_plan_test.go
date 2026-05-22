package catclip

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestOutputPlanPreviewModeTagsMatchPreparedOutputModes(t *testing.T) {
	units := []preparedFileUnit{
		{Entry: fileEntry{RelPath: "full.txt", Mode: entryModeFull}},
		{Entry: fileEntry{RelPath: "snippet.txt", Mode: entryModeSnippet}},
		{Entry: fileEntry{RelPath: "tracked-diff.txt", Mode: entryModeDiff}},
		{Entry: fileEntry{RelPath: "untracked.txt", Mode: entryModeDiff}},
	}
	statuses := map[string]string{
		"tracked-diff.txt": "M",
		"untracked.txt":    "?",
	}

	plan := buildOutputPlan(units)
	got := plan.PreviewModeTags(statuses)
	want := map[string]string{
		"snippet.txt":      "snippet only",
		"tracked-diff.txt": "diff only",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestOutputPlanPreviewModeTagsIncludePathsModes(t *testing.T) {
	plan := outputPlan{
		items: []outputPlanItem{
			newPathOutputPlanItem(fileEntry{RelPath: "path-only.txt"}),
			newPathOutputPlanItem(fileEntry{RelPath: "path-and-file.txt"}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "path-and-file.txt", Mode: entryModeFull}, BodyBytes: 10}),
			newPathOutputPlanItem(fileEntry{RelPath: "path-and-snippet.txt"}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "path-and-snippet.txt", Mode: entryModeSnippet}, BodyBytes: 5}),
			newPathOutputPlanItem(fileEntry{RelPath: "path-and-diff.txt"}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "path-and-diff.txt", Mode: entryModeDiff}, BodyBytes: 7}),
			newPathOutputPlanItem(fileEntry{RelPath: "path-and-untracked.txt"}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "path-and-untracked.txt", Mode: entryModeDiff}, BodyBytes: 9}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "snippet-only.txt", Mode: entryModeSnippet}, BodyBytes: 4}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "diff-only.txt", Mode: entryModeDiff}, BodyBytes: 6}),
		},
	}
	statuses := map[string]string{
		"path-and-diff.txt":      "M",
		"path-and-untracked.txt": "?",
		"diff-only.txt":          "M",
	}

	got := plan.PreviewModeTags(statuses)
	want := map[string]string{
		"path-only.txt":          "path only",
		"path-and-file.txt":      "path + file",
		"path-and-snippet.txt":   "path + snippet",
		"path-and-diff.txt":      "path + diff",
		"path-and-untracked.txt": "path + file",
		"snippet-only.txt":       "snippet only",
		"diff-only.txt":          "diff only",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestBuildOutputReportForPlanUsesPrecomputedGitStatusMap(t *testing.T) {
	plan := outputPlan{
		items: []outputPlanItem{
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "changed.txt", Mode: entryModeDiff}, BodyBytes: 10}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "new.txt", Mode: entryModeDiff}, BodyBytes: 5}),
		},
	}
	precomputed := map[string]string{
		"changed.txt": "M",
		"new.txt":     "?",
	}

	report, err := buildOutputReportForPlan(renderConfig{}, gitContext{Enabled: true, Root: "/definitely/missing"}, plan, nil, precomputed)
	if err != nil {
		t.Fatalf("buildOutputReportForPlan returned error with precomputed statuses: %v", err)
	}
	if !reflect.DeepEqual(report.statuses, precomputed) {
		t.Fatalf("report.statuses = %#v, want %#v", report.statuses, precomputed)
	}
	wantTags := map[string]string{
		"changed.txt": "diff only",
	}
	if !reflect.DeepEqual(report.modeTags, wantTags) {
		t.Fatalf("report.modeTags = %#v, want %#v", report.modeTags, wantTags)
	}

	precomputed["changed.txt"] = "S"
	if report.statuses["changed.txt"] != "M" {
		t.Fatalf("report.statuses was aliased to caller map: %#v", report.statuses)
	}
}

func TestBuildOutputReportForPlanNilPrecomputedGitStatusMapRecomputes(t *testing.T) {
	_, err := buildOutputReportForPlan(renderConfig{}, gitContext{Enabled: true, Root: "/definitely/missing"}, outputPlan{}, nil, nil)
	if err == nil {
		t.Fatal("expected nil precomputed status map to fall back to git status collection")
	}
}

func TestOutputPlanPreviewModeTagsLines(t *testing.T) {
	plan := outputPlan{
		items: []outputPlanItem{
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "bare.go", Mode: entryModeLines, Lines: true}, BodyBytes: 100}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "ranged.go", Mode: entryModeLines, Lines: true, LinesStart: 1, LinesEnd: 5}, BodyBytes: 50}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "open.go", Mode: entryModeLines, Lines: true, LinesStart: 400}, BodyBytes: 80}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "full.go", Mode: entryModeFull}, BodyBytes: 200}),
		},
	}
	got := plan.PreviewModeTags(nil)
	want := map[string]string{
		"bare.go":   "numbered",
		"ranged.go": "lines 1-5",
		"open.go":   "lines 400-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestOutputPlanPreviewModeTagsLinesWithPaths(t *testing.T) {
	plan := outputPlan{
		items: []outputPlanItem{
			newPathOutputPlanItem(fileEntry{RelPath: "numbered.go"}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "numbered.go", Mode: entryModeLines, Lines: true}, BodyBytes: 100}),
			newPathOutputPlanItem(fileEntry{RelPath: "ranged.go"}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "ranged.go", Mode: entryModeLines, Lines: true, LinesStart: 1, LinesEnd: 5}, BodyBytes: 50}),
		},
	}
	got := plan.PreviewModeTags(nil)
	want := map[string]string{
		"numbered.go": "path + numbered",
		"ranged.go":   "path + lines 1-5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestOutputPlanPreviewModeTagsLinesMultiEntry(t *testing.T) {
	plan := outputPlan{
		items: []outputPlanItem{
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "multi.go", Mode: entryModeLines, Lines: true, LinesStart: 1, LinesEnd: 5}, BodyBytes: 50}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "multi.go", Mode: entryModeLines, Lines: true, LinesStart: 400, LinesEnd: 450}, BodyBytes: 50}),
		},
	}
	got := plan.PreviewModeTags(nil)
	want := map[string]string{
		"multi.go": "lines 1-5, lines 400-450",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestOutputPlanPreviewModeTagsLinesDedup(t *testing.T) {
	// Snippet wins over lines (higher priority).
	plan := outputPlan{
		items: []outputPlanItem{
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "snippet-wins.go", Mode: entryModeLines, Lines: true, LinesStart: 1, LinesEnd: 5}, BodyBytes: 50}),
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "snippet-wins.go", Mode: entryModeSnippet}, BodyBytes: 30}),
		},
	}
	got := plan.PreviewModeTags(nil)
	want := map[string]string{
		"snippet-wins.go": "snippet only",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestOutputPlanSummaryCountWordUsesDistinctPathsAndComposition(t *testing.T) {
	tests := []struct {
		name      string
		plan      outputPlan
		wantCount int
		wantWord  string
	}{
		{
			name: "paths only",
			plan: outputPlan{
				items: []outputPlanItem{
					newPathOutputPlanItem(fileEntry{RelPath: "src/a.ts"}),
					newPathOutputPlanItem(fileEntry{RelPath: "src/b.ts"}),
				},
			},
			wantCount: 2,
			wantWord:  "paths",
		},
		{
			name: "files only",
			plan: outputPlan{
				items: []outputPlanItem{
					newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "src/a.ts", Mode: entryModeFull}, BodyBytes: 10}),
				},
			},
			wantCount: 1,
			wantWord:  "file",
		},
		{
			name: "mixed output uses items",
			plan: outputPlan{
				items: []outputPlanItem{
					newPathOutputPlanItem(fileEntry{RelPath: "src/a.ts"}),
					newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "src/a.ts", Mode: entryModeFull}, BodyBytes: 10}),
					newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "src/b.ts", Mode: entryModeFull}, BodyBytes: 12}),
				},
			},
			wantCount: 2,
			wantWord:  "items",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCount, gotWord := tt.plan.SummaryCountWord()
			if gotCount != tt.wantCount || gotWord != tt.wantWord {
				t.Fatalf("SummaryCountWord() = (%d, %q), want (%d, %q)", gotCount, gotWord, tt.wantCount, tt.wantWord)
			}
		})
	}
}

func TestBuildOutputReportForPlanUsesOutputPlanModeTags(t *testing.T) {
	units := []preparedFileUnit{
		{
			Entry: fileEntry{
				RelPath: "snippet.txt",
				Mode:    entryModeSnippet,
			},
			BodyBytes: 10,
			Payload:   []byte("snippet"),
		},
		{
			Entry: fileEntry{
				RelPath: "diff.txt",
				Mode:    entryModeDiff,
			},
			BodyBytes: 20,
			Payload:   []byte("diff"),
		},
	}

	report, err := buildOutputReportForPlan(renderConfig{}, gitContext{}, buildOutputPlan(units), nil)
	if err != nil {
		t.Fatalf("buildOutputReportForPlan returned error: %v", err)
	}
	want := map[string]string{
		"snippet.txt": "snippet only",
		"diff.txt":    "diff only",
	}
	if !reflect.DeepEqual(report.modeTags, want) {
		t.Fatalf("report.modeTags = %#v, want %#v", report.modeTags, want)
	}
}

func TestBuildOutputReportForPlanUsesPathsWordForPathOnlyOutput(t *testing.T) {
	plan := outputPlan{
		items: []outputPlanItem{
			newPathOutputPlanItem(fileEntry{RelPath: "src/a.ts"}),
			newPathOutputPlanItem(fileEntry{RelPath: "src/b.ts"}),
		},
	}

	report, err := buildOutputReportForPlan(renderConfig{}, gitContext{}, plan, nil)
	if err != nil {
		t.Fatalf("buildOutputReportForPlan returned error: %v", err)
	}
	if got, want := report.countWord, "paths"; got != want {
		t.Fatalf("report.countWord = %q, want %q", got, want)
	}
}

func TestBuildOutputReportForPlanUsesPlanAccounting(t *testing.T) {
	units := []preparedFileUnit{
		{
			Entry:     fileEntry{RelPath: "full.txt", Mode: entryModeFull},
			BodyBytes: 12,
		},
		{
			Entry:     fileEntry{RelPath: "snippet.txt", Mode: entryModeSnippet},
			BodyBytes: 5,
			Payload:   []byte("TODO\n"),
		},
	}

	cfg := renderConfig{}
	gitCtx := gitContext{}
	report, err := buildOutputReportForPlan(cfg, gitCtx, buildOutputPlan(units), []string{"notice"})
	if err != nil {
		t.Fatalf("buildOutputReportForPlan returned error: %v", err)
	}

	wantSizes := map[string]int64{
		"full.txt":    12,
		"snippet.txt": 5,
	}
	if !reflect.DeepEqual(report.sizes, wantSizes) {
		t.Fatalf("report.sizes = %#v, want %#v", report.sizes, wantSizes)
	}
	if !reflect.DeepEqual(report.modeTags, map[string]string{"snippet.txt": "snippet only"}) {
		t.Fatalf("report.modeTags = %#v", report.modeTags)
	}
	if !reflect.DeepEqual(report.notices, []string{"notice"}) {
		t.Fatalf("report.notices = %#v", report.notices)
	}
	if report.humanSize == "" || report.tokens == 0 {
		t.Fatalf("expected summary fields to be populated, got size=%q tokens=%d", report.humanSize, report.tokens)
	}
}

func TestWriteClipboardSuccessUsesDistinctSummarySubjects(t *testing.T) {
	tests := []struct {
		name     string
		plan     outputPlan
		wantText string
	}{
		{
			name: "paths only",
			plan: outputPlan{
				items: []outputPlanItem{
					newPathOutputPlanItem(fileEntry{RelPath: "src/a.ts"}),
					newPathOutputPlanItem(fileEntry{RelPath: "src/b.ts"}),
				},
			},
			wantText: "Copied 2 paths to clipboard (src/a.ts ... src/b.ts)",
		},
		{
			name: "mixed uses items and distinct relpaths",
			plan: outputPlan{
				items: []outputPlanItem{
					newPathOutputPlanItem(fileEntry{RelPath: "src/a.ts"}),
					newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "src/a.ts", Mode: entryModeFull}, BodyBytes: 10}),
					newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "src/b.ts", Mode: entryModeFull}, BodyBytes: 12}),
				},
			},
			wantText: "Copied 2 items to clipboard (src/a.ts ... src/b.ts)",
		},
		{
			name: "single distinct relpath stays direct",
			plan: outputPlan{
				items: []outputPlanItem{
					newPathOutputPlanItem(fileEntry{RelPath: "src/a.ts"}),
					newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "src/a.ts", Mode: entryModeFull}, BodyBytes: 10}),
				},
			},
			wantText: "Copied src/a.ts to clipboard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := writeClipboardSuccess(&out, tt.plan, emitStats{}, colorPalette{}); err != nil {
				t.Fatalf("writeClipboardSuccess returned error: %v", err)
			}
			if got := strings.TrimSpace(out.String()); got != tt.wantText {
				t.Fatalf("writeClipboardSuccess output = %q, want %q", got, tt.wantText)
			}
		})
	}
}

func TestWriteBundleSuccessIncludesWarnings(t *testing.T) {
	plan := outputPlan{
		items: []outputPlanItem{
			newFileOutputPlanItem(preparedFileUnit{Entry: fileEntry{RelPath: "src/a.ts", Mode: entryModeFull}, BodyBytes: 10}),
		},
	}
	stats := emitStats{
		SinkName:     "bundle",
		BundlePath:   "/tmp/catclip/catclip-123.txt",
		PayloadBytes: 5000,
		Warnings:     []string{"xdg-desktop-portal 1.18 is older than the recommended 1.21 baseline. Sandboxed apps such as Firefox Snap may not attach this bundle from the clipboard; drag and drop the file if paste fails."},
	}

	var out bytes.Buffer
	if err := writeClipboardSuccess(&out, plan, stats, colorPalette{}); err != nil {
		t.Fatalf("writeClipboardSuccess returned error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Warning:", "xdg-desktop-portal 1.18", "Firefox Snap", "drag and drop"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bundle success output missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Use --no-bundle to copy text instead.") {
		t.Fatalf("bundle success output missing success-level --no-bundle guidance:\n%s", got)
	}
}

func TestEmitOutputPlanUsesPreparedPayload(t *testing.T) {
	unit := preparedFileUnit{
		Entry: fileEntry{
			AbsPath: "/does/not/matter",
			RelPath: "snippet.txt",
			Mode:    entryModeSnippet,
		},
		Payload:   []byte("<file path=\"snippet.txt\" lines=\"1-1\">\nTODO\n</file>\n\n"),
		BodyBytes: int64(len("TODO\n")),
	}

	var stdout bytes.Buffer
	_, err := emitOutputPlan(emitConfig{OutputMode: outputModeStdout}, emitEnvironment{}, buildOutputPlan([]preparedFileUnit{unit}), &stdout, colorPalette{})
	if err != nil {
		t.Fatalf("emitOutputPlan returned error: %v", err)
	}
	if got, want := stdout.String(), string(unit.Payload); got != want {
		t.Fatalf("emitOutputPlan output mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}
