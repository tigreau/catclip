package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func TestThenPreviewKeepsMultipleViewsOfOneExactFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "sample.go")
	if err := os.WriteFile(p, []byte("alpha\nbravo\ncharlie\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entries := []discovery.Entry{
		{RelPath: "sample.go", AbsPath: p, Mode: command.EntryModeSnippet, SnippetPattern: "alpha", SnippetContextSet: true, SnippetMatchLines: []int{1}},
		{RelPath: "sample.go", AbsPath: p, Mode: command.EntryModeLines, Lines: true, LinesStart: 3, LinesEnd: 3},
	}
	scopes := []command.ExecutionScope{{Targets: []string{"sample.go"}, Snippet: true}, {Targets: []string{"sample.go"}, Lines: true, LinesStart: 3, LinesEnd: 3}}
	plan, err := output.BuildPlanForResolvedScopes(git.Context{}, scopes, []output.EvaluatedScope{{Entries: entries[:1]}, {Entries: entries[1:]}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := RenderConfig{Scopes: scopes}
	target := buildTreeDocumentTargetForPlan(cfg, plan)
	if target == nil || target.Kind != TreeTargetKindFile {
		t.Fatalf("multiple units misidentified as directory: %+v", target)
	}
	report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{IncludeTreeMetadata: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := StartupSinkPickerContext{Render: cfg, Git: git.Context{}, Plan: plan, Report: report}
	preview, err := renderSinkTreeReportPreview(ctx, output.PreviewByteLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(preview.Body), "snippet 1-1 + lines 3-3") {
		t.Fatalf("lost mode tag: %s", preview.Body)
	}
	var final bytes.Buffer
	if err := RenderPreview(cfg, git.Context{}, plan, report, &final, &final, platform.ANSIPalette()); err != nil {
		t.Fatal(err)
	}
	if string(preview.Body) != final.String() {
		t.Fatalf("sink tree/final report differ\n%s\n%s", preview.Body, final.String())
	}
}
