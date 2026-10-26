package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

func TestEmptyRefinementRetainedPreviewAndFinalParity(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/a.txt": "DO_NOT_COPY\n", "src/b.txt": "KEEP\n"})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	// Simulate selecting the directory, then a file, then applying a snippet.
	for _, args := range [][]string{{"src"}, {"src/a.txt"}, {"src/a.txt", "--snippet", "ABSENT"}} {
		if _, err := resolvedCurrentScopeViewForArgs(args); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"src/a.txt", "--snippet", "ABSENT"}
	first, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Discovered.EmptyRefinement.Paths) != 1 || first.Discovered.EmptyRefinement.Paths[0] != "src/a.txt" {
		t.Fatalf("lost refinement: %+v", first.Discovered.EmptyRefinement)
	}
	first.Discovered.EmptyRefinement.Paths[0] = "corrupted"
	// Revisiting the previous picker must not mutate the derived view.
	if _, err := resolvedCurrentScopeViewForArgs([]string{"src/a.txt"}); err != nil {
		t.Fatal(err)
	}
	again, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil || again.Discovered.EmptyRefinement.Paths[0] != "src/a.txt" {
		t.Fatalf("undo corrupted evidence: %+v %v", again.Discovered.EmptyRefinement, err)
	}
	ctx, err := buildStartupSinkPickerContext([]string{"src", "--then", "src/a.txt", "--snippet", "ABSENT"})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Plan.Len() != 1 {
		t.Fatalf("unexpected plan length %d", ctx.Plan.Len())
	}
	preview, err := renderSinkTreeReportPreview(ctx, output.PreviewByteLimit)
	if err != nil {
		t.Fatal(err)
	}
	colors := platform.ANSIPalette()
	notice := "Scope 2 produced no output.\nFull files omitted:\n  src/a.txt"
	if strings.Count(string(preview.Body), notice) != 1 || !bytes.Contains(preview.Body, []byte(colors.Warn+notice+colors.Reset)) {
		t.Fatalf("missing/duplicate/colorless notice: %q", preview.Body)
	}
	if bytes.Index(preview.Body, []byte("b.txt")) > bytes.Index(preview.Body, []byte(notice)) || bytes.Index(preview.Body, []byte(notice)) > bytes.Index(preview.Body, []byte("Count:")) {
		t.Fatalf("wrong order: %q", preview.Body)
	}
	var final bytes.Buffer
	if _, err := WriteNormalDiagnostics(ctx.Render.WithPreparedPresentation(ctx.Presentation), ctx.Git, ctx.Plan, ctx.Report, command.EmissionNever, &final, &final, colors, colors); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(final.Bytes(), preview.Body) {
		t.Fatalf("preview=%q final=%q", preview.Body, final.Bytes())
	}
	content, err := renderSinkPreviewWithModeContext(context.Background(), ctx, sinkPreviewModeOutputText, output.PreviewByteLimit)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content.Body, []byte("DO_NOT_COPY")) || bytes.Contains(content.Body, []byte("omitted")) {
		t.Fatalf("content leaked: %q", content.Body)
	}
	// The separate serialized tree helper must carry the same notice.
	var encoded, rendered bytes.Buffer
	if err := EncodeTreePayloadFromPlan(&encoded, ctx.Render, ctx.Git, ctx.Plan, nil); err != nil {
		t.Fatal(err)
	}
	doc, err := renderpkg.DecodePayload(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := renderpkg.RenderDocument(&rendered, doc, renderpkg.RenderOptions{ShowSummary: true}, renderpkg.Palette{}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(rendered.String(), notice) != 1 {
		t.Fatalf("serialized preview lost notice: %s", &rendered)
	}
}

func TestEmptyRefinementSkipsEmptySink(t *testing.T) {
	project := setupTestProject(t, map[string]string{"a.txt": "body\n"})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	// A missing fzf executable turns an accidental picker launch into an error.
	t.Setenv("CATCLIP_FZF", project+"/does-not-exist.exe")
	args := []string{"a.txt", "--then", "a.txt", "--snippet", "ABSENT"}
	result, err := maybeResolveStartupSinkPickerArgs(nil, StartupPickerResult{Args: args, UsedFzf: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreparedOutput == nil || !result.PreparedOutput.Plan.IsEmpty() || len(result.PreparedOutput.Plan.RefinementNoticeText()) != 1 {
		t.Fatalf("lost empty prepared plan: %+v", result)
	}
}
