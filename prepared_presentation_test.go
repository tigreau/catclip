package catclip

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
	"github.com/tigreau/catclip/internal/ui"
)

func TestExecutePreparedPresentationMatchesFreshDiagnostics(t *testing.T) {
	plan := output.BuildPlan([]output.PreparedFileUnit{{Entry: discovery.Entry{RelPath: "src/a.go", Mode: command.EntryModeFull}, BodyBytes: 21}})
	report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{IncludeTreeMetadata: true, Notices: []string{"notice"}})
	if err != nil {
		t.Fatal(err)
	}
	p := ui.PreparePresentation(git.Context{}, plan, report)
	log := filepath.Join(t.TempDir(), "bench.log")
	t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", log)
	for _, cfg := range []ui.RenderConfig{{}, {NoTree: true}, {Quiet: true}} {
		var wantOut, wantErr, gotOut, gotErr bytes.Buffer
		ctx := outputExecutionContext{Invocation: command.Invocation{EmissionPolicy: command.EmissionNever}, Render: cfg, Stdout: &wantOut, Stderr: &wantErr}
		state := outputExecutionState{Plan: plan, Notices: report.Notices}
		if err := executePlanOutput(ctx, state); err != nil {
			t.Fatal(err)
		}
		ctx.Stdout, ctx.Stderr = &gotOut, &gotErr
		state.Presentation = p
		if err := executePlanOutput(ctx, state); err != nil {
			t.Fatal(err)
		}
		if wantOut.String() != gotOut.String() || wantErr.String() != gotErr.String() {
			t.Fatalf("cached final diagnostics differ cfg=%+v", cfg)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `reused="true"`) || !strings.Contains(string(data), `reused="false"`) {
		t.Fatal("expected both retained and fresh report paths")
	}
}
