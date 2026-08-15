package output

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

func TestOutputPlanPreviewModeTagsMatchPreparedOutputModes(t *testing.T) {
	units := []PreparedFileUnit{
		{Entry: discovery.Entry{RelPath: "full.txt", Mode: command.EntryModeFull}},
		{Entry: discovery.Entry{RelPath: "snippet.txt", Mode: command.EntryModeSnippet}},
		{Entry: discovery.Entry{RelPath: "tracked-diff.txt", Mode: command.EntryModeDiff}},
		{Entry: discovery.Entry{RelPath: "untracked.txt", Mode: command.EntryModeDiff}},
	}
	statuses := map[string]string{
		"tracked-diff.txt": "M",
		"untracked.txt":    "?",
	}

	plan := BuildPlan(units)
	got := plan.PreviewModeTags(statuses)
	want := map[string]string{
		"snippet.txt":      "snippet",
		"tracked-diff.txt": "diff only",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestOutputPlanPreviewModeTagsIncludePathsModes(t *testing.T) {
	plan := Plan{
		items: []PlanItem{
			newPathOutputPlanItem(discovery.Entry{RelPath: "path-only.txt"}),
			newPathOutputPlanItem(discovery.Entry{RelPath: "path-and-file.txt"}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "path-and-file.txt", Mode: command.EntryModeFull}, BodyBytes: 10}),
			newPathOutputPlanItem(discovery.Entry{RelPath: "path-and-snippet.txt"}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "path-and-snippet.txt", Mode: command.EntryModeSnippet}, BodyBytes: 5}),
			newPathOutputPlanItem(discovery.Entry{RelPath: "path-and-diff.txt"}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "path-and-diff.txt", Mode: command.EntryModeDiff}, BodyBytes: 7}),
			newPathOutputPlanItem(discovery.Entry{RelPath: "path-and-untracked.txt"}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "path-and-untracked.txt", Mode: command.EntryModeDiff}, BodyBytes: 9}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "snippet-only.txt", Mode: command.EntryModeSnippet}, BodyBytes: 4}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "diff-only.txt", Mode: command.EntryModeDiff}, BodyBytes: 6}),
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
		"snippet-only.txt":       "snippet",
		"diff-only.txt":          "diff only",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestBuildOutputReportForPlanUsesPrecomputedGitStatusMap(t *testing.T) {
	plan := Plan{
		items: []PlanItem{
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "changed.txt", Mode: command.EntryModeDiff}, BodyBytes: 10}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "new.txt", Mode: command.EntryModeDiff}, BodyBytes: 5}),
		},
	}
	precomputed := map[string]string{
		"changed.txt": "M",
		"new.txt":     "?",
	}

	report, err := BuildReportForPlan(git.Context{Enabled: true, Root: "/definitely/missing"}, plan, ReportOptions{IncludeTreeMetadata: true, PrecomputedGitStatuses: precomputed})
	if err != nil {
		t.Fatalf("BuildReportForPlan returned error with precomputed Statuses:%v", err)
	}
	if !reflect.DeepEqual(report.Statuses, precomputed) {
		t.Fatalf("report.Statuses = %#v, want %#v", report.Statuses, precomputed)
	}
	wantTags := map[string]string{
		"changed.txt": "diff only",
	}
	if !reflect.DeepEqual(report.ModeTags, wantTags) {
		t.Fatalf("report.ModeTags = %#v, want %#v", report.ModeTags, wantTags)
	}

	precomputed["changed.txt"] = "S"
	if report.Statuses["changed.txt"] != "M" {
		t.Fatalf("report.Statuses was aliased to caller map: %#v", report.Statuses)
	}
}

func TestBuildOutputReportForPlanNilPrecomputedGitStatusMapRecomputes(t *testing.T) {
	_, err := BuildReportForPlan(git.Context{Enabled: true, Root: "/definitely/missing"}, Plan{}, ReportOptions{IncludeTreeMetadata: true})
	if err == nil {
		t.Fatal("expected nil precomputed status map to fall back to git status collection")
	}
}

func TestOutputPlanPreviewModeTagsLines(t *testing.T) {
	plan := Plan{
		items: []PlanItem{
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "bare.go", Mode: command.EntryModeLines, Lines: true}, BodyBytes: 100}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "ranged.go", Mode: command.EntryModeLines, Lines: true, LinesStart: 1, LinesEnd: 5}, BodyBytes: 50}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "open.go", Mode: command.EntryModeLines, Lines: true, LinesStart: 400}, BodyBytes: 80}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "full.go", Mode: command.EntryModeFull}, BodyBytes: 200}),
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
	plan := Plan{
		items: []PlanItem{
			newPathOutputPlanItem(discovery.Entry{RelPath: "numbered.go"}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "numbered.go", Mode: command.EntryModeLines, Lines: true}, BodyBytes: 100}),
			newPathOutputPlanItem(discovery.Entry{RelPath: "ranged.go"}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "ranged.go", Mode: command.EntryModeLines, Lines: true, LinesStart: 1, LinesEnd: 5}, BodyBytes: 50}),
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
	plan := Plan{
		items: []PlanItem{
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "multi.go", Mode: command.EntryModeLines, Lines: true, LinesStart: 1, LinesEnd: 5}, BodyBytes: 50}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "multi.go", Mode: command.EntryModeLines, Lines: true, LinesStart: 400, LinesEnd: 450}, BodyBytes: 50}),
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
	plan := Plan{
		items: []PlanItem{
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "snippet-wins.go", Mode: command.EntryModeLines, Lines: true, LinesStart: 1, LinesEnd: 5}, BodyBytes: 50}),
			newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "snippet-wins.go", Mode: command.EntryModeSnippet}, BodyBytes: 30}),
		},
	}
	got := plan.PreviewModeTags(nil)
	want := map[string]string{
		"snippet-wins.go": "snippet",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestOutputPlanPreviewModeTagsSnippetRanges(t *testing.T) {
	plan := Plan{
		items: []PlanItem{
			newFileOutputPlanItem(PreparedFileUnit{
				Entry:         discovery.Entry{RelPath: "one.go", Mode: command.EntryModeSnippet},
				SnippetRanges: []SnippetRange{{Start: 12, End: 16}},
				BodyBytes:     10,
			}),
			newFileOutputPlanItem(PreparedFileUnit{
				Entry:         discovery.Entry{RelPath: "two.go", Mode: command.EntryModeSnippet},
				SnippetRanges: []SnippetRange{{Start: 12, End: 16}, {Start: 80, End: 84}},
				BodyBytes:     20,
			}),
			newFileOutputPlanItem(PreparedFileUnit{
				Entry:         discovery.Entry{RelPath: "three.go", Mode: command.EntryModeSnippet},
				SnippetRanges: []SnippetRange{{Start: 12, End: 16}, {Start: 80, End: 84}, {Start: 120, End: 122}},
				BodyBytes:     30,
			}),
		},
	}
	got := plan.PreviewModeTags(nil)
	want := map[string]string{
		"one.go":   "snippet 12-16",
		"two.go":   "2 snippets 12-16,80-84",
		"three.go": "3 snippets 12-16,80-84,...",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PreviewModeTags() = %#v, want %#v", got, want)
	}
}

func TestOutputPlanSummaryCountWordUsesDistinctPathsAndComposition(t *testing.T) {
	tests := []struct {
		name      string
		plan      Plan
		wantCount int
		wantWord  string
	}{
		{
			name: "paths only",
			plan: Plan{
				items: []PlanItem{
					newPathOutputPlanItem(discovery.Entry{RelPath: "src/a.ts"}),
					newPathOutputPlanItem(discovery.Entry{RelPath: "src/b.ts"}),
				},
			},
			wantCount: 2,
			wantWord:  "paths",
		},
		{
			name: "files only",
			plan: Plan{
				items: []PlanItem{
					newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "src/a.ts", Mode: command.EntryModeFull}, BodyBytes: 10}),
				},
			},
			wantCount: 1,
			wantWord:  "file",
		},
		{
			name: "mixed output uses items",
			plan: Plan{
				items: []PlanItem{
					newPathOutputPlanItem(discovery.Entry{RelPath: "src/a.ts"}),
					newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "src/a.ts", Mode: command.EntryModeFull}, BodyBytes: 10}),
					newFileOutputPlanItem(PreparedFileUnit{Entry: discovery.Entry{RelPath: "src/b.ts", Mode: command.EntryModeFull}, BodyBytes: 12}),
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
	units := []PreparedFileUnit{
		{
			Entry: discovery.Entry{
				RelPath: "snippet.txt",
				Mode:    command.EntryModeSnippet,
			},
			BodyBytes: 10,
			Payload:   []byte("snippet"),
		},
		{
			Entry: discovery.Entry{
				RelPath: "diff.txt",
				Mode:    command.EntryModeDiff,
			},
			BodyBytes: 20,
			Payload:   []byte("diff"),
		},
	}

	report, err := BuildReportForPlan(git.Context{}, BuildPlan(units), ReportOptions{IncludeTreeMetadata: true})
	if err != nil {
		t.Fatalf("BuildReportForPlan returned error: %v", err)
	}
	want := map[string]string{
		"snippet.txt": "snippet",
		"diff.txt":    "diff only",
	}
	if !reflect.DeepEqual(report.ModeTags, want) {
		t.Fatalf("report.ModeTags = %#v, want %#v", report.ModeTags, want)
	}
}

func TestBuildOutputReportForPlanUsesPathsWordForPathOnlyOutput(t *testing.T) {
	plan := Plan{
		items: []PlanItem{
			newPathOutputPlanItem(discovery.Entry{RelPath: "src/a.ts"}),
			newPathOutputPlanItem(discovery.Entry{RelPath: "src/b.ts"}),
		},
	}

	report, err := BuildReportForPlan(git.Context{}, plan, ReportOptions{})
	if err != nil {
		t.Fatalf("BuildReportForPlan returned error: %v", err)
	}
	if got, want := report.CountWord, "paths"; got != want {
		t.Fatalf("report.CountWord = %q, want %q", got, want)
	}
}

func TestBuildOutputReportForPlanUsesPlanAccounting(t *testing.T) {
	units := []PreparedFileUnit{
		{
			Entry:     discovery.Entry{RelPath: "full.txt", Mode: command.EntryModeFull},
			BodyBytes: 12,
		},
		{
			Entry:     discovery.Entry{RelPath: "snippet.txt", Mode: command.EntryModeSnippet},
			BodyBytes: 5,
			Payload:   []byte("TODO\n"),
		},
	}

	gitCtx := git.Context{}
	report, err := BuildReportForPlan(gitCtx, BuildPlan(units), ReportOptions{IncludeTreeMetadata: true, Notices: []string{"notice"}})
	if err != nil {
		t.Fatalf("BuildReportForPlan returned error: %v", err)
	}

	wantSizes := map[string]int64{
		"full.txt":    12,
		"snippet.txt": 5,
	}
	if !reflect.DeepEqual(report.Sizes, wantSizes) {
		t.Fatalf("report.Sizes = %#v, want %#v", report.Sizes, wantSizes)
	}
	if !reflect.DeepEqual(report.ModeTags, map[string]string{"snippet.txt": "snippet"}) {
		t.Fatalf("report.ModeTags = %#v", report.ModeTags)
	}
	if !reflect.DeepEqual(report.Notices, []string{"notice"}) {
		t.Fatalf("report.Notices = %#v", report.Notices)
	}
	if report.HumanSize == "" || report.Tokens == 0 {
		t.Fatalf("expected summary fields to be populated, got size=%q tokens=%d", report.HumanSize, report.Tokens)
	}
}

func TestEmitOutputPlanUsesPreparedPayload(t *testing.T) {
	unit := PreparedFileUnit{
		Entry: discovery.Entry{
			AbsPath: "/does/not/matter",
			RelPath: "snippet.txt",
			Mode:    command.EntryModeSnippet,
		},
		Payload:   []byte("<file path=\"snippet.txt\" lines=\"1-1\">\nTODO\n</file>\n\n"),
		BodyBytes: int64(len("TODO\n")),
	}

	var stdout bytes.Buffer
	_, err := EmitOutputPlan(EmitConfig{OutputMode: command.OutputModeStdout}, RuntimeEnvironment{}, BuildPlan([]PreparedFileUnit{unit}), &stdout, platform.Palette{})
	if err != nil {
		t.Fatalf("EmitOutputPlan returned error: %v", err)
	}
	if got, want := stdout.String(), string(unit.Payload); got != want {
		t.Fatalf("EmitOutputPlan output mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}
