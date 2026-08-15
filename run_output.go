package catclip

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/ui"
)

type outputExecutionContext struct {
	Invocation command.Invocation
	Render     ui.RenderConfig
	Emit       output.EmitConfig
	Git        git.Context
	Stdout     io.Writer
	Stderr     io.Writer
	Colors     platform.Palette
	Started    time.Time
}

type outputExecutionState struct {
	Scopes      []command.ExecutionScope
	Plan        output.Plan
	Diagnostics []discovery.Diagnostic
	Summary     DiagnosticSummary
	Notices     []string
}

func executeTreePreview(ctx outputExecutionContext, state outputExecutionState) error {
	finishBench := platform.InternalBenchSpan("output.tree_preview.render",
		"items", platform.InternalBenchInt(state.Plan.Len()),
	)
	if state.Summary.HasError {
		err := newExitError(1, "")
		finishBench("err", "true", "summary_error", "true")
		return err
	}
	err := ui.RenderTreePreviewFromPlan(ctx.Stdout, ctx.Render, ctx.Git, state.Plan, state.Notices, ui.FzfFilterTreeRenderOptions())
	finishBench(
		"err", platform.InternalBenchError(err),
		"empty", platform.InternalBenchBool(errors.Is(err, ui.ErrTreePayloadEmptyNoTarget)),
	)
	if err != nil {
		if errors.Is(err, ui.ErrTreePayloadEmptyNoTarget) {
			return nil
		}
		return err
	}
	return nil
}

func executePlanOutput(ctx outputExecutionContext, state outputExecutionState) error {
	if err := writeDiscoveryDiagnostics(state.Diagnostics, ctx.Stderr); err != nil {
		return err
	}
	if state.Plan.IsEmpty() {
		return executeEmptyOutput(ctx, state)
	}
	reportStarted := time.Now()
	report, err := output.BuildReportForPlan(ctx.Git, state.Plan, output.ReportOptions{
		IncludeTreeMetadata: ui.NeedsTreeRender(ctx.Render),
		Notices:             discovery.DedupePreserveOrder(state.Notices),
	})
	if err != nil {
		return err
	}
	if ctx.Invocation.Verbose {
		fmt.Fprintf(ctx.Stderr, "[verbose] report: %s\n", formatDuration(time.Since(reportStarted)))
	}
	if ctx.Render.Preview {
		return executePreview(ctx, state, report)
	}
	return executeNormalOutput(ctx, state, report)
}

func executeEmptyOutput(ctx outputExecutionContext, state outputExecutionState) error {
	if !ctx.Invocation.Quiet {
		for _, notice := range discovery.DedupePreserveOrder(state.Notices) {
			fmt.Fprintln(ctx.Stderr, notice)
		}
	}
	if !state.Summary.AllEmptyScopesExplained(len(state.Scopes)) {
		if err := discovery.WriteNoFilesMatchedMessage(state.Scopes, ctx.Stderr, ctx.Colors, state.Summary.HadSelectionCancel); err != nil {
			return err
		}
	}
	if state.Summary.HasScopeUnsatisfiable {
		return newExitError(2, "")
	}
	return newExitError(1, "")
}

func executePreview(ctx outputExecutionContext, state outputExecutionState, report output.Report) error {
	renderStarted := time.Now()
	// --preview renders the file table (not the tree). The tree's ui.RenderPreview
	// stays for the sink picker; the confirmation flow keeps ui.WriteNormalDiagnostics.
	err := ui.RenderPreviewTable(ctx.Render, ctx.Git, state.Plan, report, ctx.Stdout, ctx.Stderr,
		ctx.Invocation.WorkingDir, time.Now(), ctx.Colors)
	if err != nil {
		return err
	}
	if ctx.Invocation.Verbose {
		fmt.Fprintf(ctx.Stderr, "[verbose] preview: %s\n", formatDuration(time.Since(renderStarted)))
		fmt.Fprintf(ctx.Stderr, "[verbose] total: %s\n", formatDuration(time.Since(ctx.Started)))
	}
	if state.Summary.HasScopeUnsatisfiable {
		return newExitError(1, "")
	}
	if state.Summary.HasTargetNotFound {
		return newExitError(1, "")
	}
	return nil
}

func executeNormalOutput(ctx outputExecutionContext, state outputExecutionState, report output.Report) error {
	if err := output.ValidateRawPlan(ctx.Emit, state.Plan); err != nil {
		return err
	}
	diagStarted := time.Now()
	proceed, err := ui.WriteNormalDiagnostics(ctx.Render, ctx.Git, state.Plan, report, ctx.Stderr, ctx.Colors)
	if err != nil {
		return err
	}
	if ctx.Invocation.Verbose {
		fmt.Fprintf(ctx.Stderr, "[verbose] diagnostics: %s\n", formatDuration(time.Since(diagStarted)))
	}
	if !proceed {
		return nil
	}
	outputMetrics, err := collectVerboseOutputMetrics(ctx.Invocation.Verbose, ctx.Git, state.Plan)
	if err != nil {
		return err
	}
	outputSpinnerStop := func() {}
	if !ctx.Invocation.Quiet && ctx.Emit.OutputMode == command.OutputModeClipboard {
		outputSpinnerStop = platform.StartLoadingSpinner(platform.SpinnerOutputFile(ctx.Stderr), "Copying files...")
	}
	if shouldSeparateStdoutPayload(ctx.Emit, ctx.Invocation, ctx.Stdout, ctx.Stderr) {
		fmt.Fprintln(ctx.Stderr)
	}
	outputStarted := time.Now()
	emitStats, err := output.EmitOutputPlan(ctx.Emit, outputEnvironmentFromInvocation(ctx.Invocation), state.Plan, ctx.Stdout, ctx.Colors)
	if err != nil {
		outputSpinnerStop()
		return err
	}
	outputSpinnerStop()
	outputMetrics.PayloadBytes = emitStats.PayloadBytes
	if ctx.Invocation.Verbose {
		outputDuration := time.Since(outputStarted)
		fmt.Fprintf(ctx.Stderr, "[verbose] output: %s\n", formatDuration(outputDuration))
		writeVerboseOutputMetrics(ctx.Stderr, outputMetrics, emitStats, state.Plan.Len(), outputDuration)
	}
	if ctx.Emit.OutputMode == command.OutputModeClipboard && !ctx.Invocation.Quiet {
		if err := ui.WriteClipboardSuccess(ctx.Stderr, state.Plan, emitStats, ctx.Colors); err != nil {
			return err
		}
	}
	if ctx.Invocation.Verbose {
		fmt.Fprintf(ctx.Stderr, "[verbose] total: %s\n", formatDuration(time.Since(ctx.Started)))
	}
	if state.Summary.HasScopeUnsatisfiable {
		return newExitError(1, "")
	}
	if state.Summary.HasTargetNotFound {
		return newExitError(1, "")
	}
	return nil
}
