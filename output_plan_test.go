package catclip

import (
	"bytes"
	"reflect"
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

	report, err := buildOutputReportForPlan(runConfig{}, gitContext{}, buildOutputPlan(units), nil)
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

	cfg := runConfig{}
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

func TestEmitOutputPlanUsesPreparedPayload(t *testing.T) {
	unit := preparedFileUnit{
		Entry: fileEntry{
			AbsPath: "/does/not/matter",
			RelPath: "snippet.txt",
			Mode:    entryModeSnippet,
		},
		Payload:   []byte("<file path=\"snippet.txt\" snippet=\"1-1\">\nTODO\n</file>\n\n"),
		BodyBytes: int64(len("TODO\n")),
	}

	var stdout bytes.Buffer
	_, err := emitOutputPlan(runConfig{OutputMode: outputModeStdout}, buildOutputPlan([]preparedFileUnit{unit}), &stdout, colorPalette{})
	if err != nil {
		t.Fatalf("emitOutputPlan returned error: %v", err)
	}
	if got, want := stdout.String(), string(unit.Payload); got != want {
		t.Fatalf("emitOutputPlan output mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}
