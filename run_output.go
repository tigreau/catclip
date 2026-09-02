package catclip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
	"github.com/tigreau/catclip/internal/ui"
)

func executeFreshTargetTreePreview(
	stdout io.Writer,
	renderCfg ui.RenderConfig,
	gitCtx git.Context,
	entries []discovery.Entry,
	summary DiagnosticSummary,
) error {
	if summary.HasError {
		return newExitError(1, "")
	}
	err := ui.RenderFreshTargetTreePreview(search.ReloadCancelContext(), stdout, renderCfg, gitCtx, entries)
	if errors.Is(err, ui.ErrTreePayloadEmptyNoTarget) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

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
	Scopes           []command.ExecutionScope
	DiscoveredScopes []discovery.Scope
	Plan             output.Plan
	Metadata         *ui.MetadataReport
	Diagnostics      []discovery.Diagnostic
	Summary          DiagnosticSummary
	Notices          []string
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
	finishReportBench := platform.InternalBenchSpan("output.report", "items", platform.InternalBenchInt(state.Plan.Len()))
	report, err := output.BuildReportForPlan(ctx.Git, state.Plan, output.ReportOptions{
		IncludeTreeMetadata: ui.NeedsTreeRender(ctx.Render) || ctx.Invocation.PayloadKind == command.PayloadMetadata,
		Notices:             discovery.DedupePreserveOrder(state.Notices),
	})
	finishReportBench("err", platform.InternalBenchError(err))
	if err != nil {
		return err
	}
	if ctx.Invocation.Verbose {
		fmt.Fprintf(ctx.Stderr, "[verbose] report: %s\n", formatDuration(time.Since(reportStarted)))
	}
	if ctx.Invocation.PayloadKind == command.PayloadMetadata {
		return executeMetadata(ctx, state, report)
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

func executeMetadata(ctx outputExecutionContext, state outputExecutionState, report output.Report) error {
	finishBench := platform.InternalBenchSpan("output.metadata", "items", platform.InternalBenchInt(state.Plan.Len()))
	defer finishBench()
	diagStarted := time.Now()
	presentationWriter := ctx.Stderr
	presentationColors := ctx.Colors
	if ctx.Invocation.EmissionPolicy == command.EmissionNever {
		presentationWriter = ctx.Stdout
		presentationColors = platform.ActivePaletteForWriter(ctx.Stdout)
		proceed, err := ui.WriteNormalDiagnostics(ctx.Render, ctx.Git, state.Plan, report,
			ctx.Invocation.EmissionPolicy, presentationWriter, ctx.Stderr, presentationColors, ctx.Colors)
		if err != nil {
			return err
		}
		if proceed {
			return fmt.Errorf("internal error: never-emit metadata diagnostics reached payload emission")
		}
		if ctx.Invocation.Verbose {
			fmt.Fprintf(ctx.Stderr, "[verbose] diagnostics: %s\n", formatDuration(time.Since(diagStarted)))
			fmt.Fprintf(ctx.Stderr, "[verbose] total: %s\n", formatDuration(time.Since(ctx.Started)))
		}
		if state.Summary.HasScopeUnsatisfiable || state.Summary.HasTargetNotFound {
			return newExitError(1, "")
		}
		return nil
	}

	metadata := state.Metadata
	var err error
	if metadata == nil {
		metadata, err = ui.BuildMetadataReport(ctx.Invocation.WorkingDir, ctx.Git, state.DiscoveredScopes, state.Plan, report, ctx.Invocation.WithBinaries, time.Now())
		if err != nil {
			return err
		}
	}
	proceed, err := ui.WriteMetadataDiagnostics(ctx.Render, ctx.Git, state.Plan, report, metadata,
		ctx.Invocation.EmissionPolicy, presentationWriter, ctx.Stderr, presentationColors, ctx.Colors)
	if err != nil {
		return err
	}
	if ctx.Invocation.Verbose {
		fmt.Fprintf(ctx.Stderr, "[verbose] diagnostics: %s\n", formatDuration(time.Since(diagStarted)))
	}
	if !proceed {
		if ctx.Invocation.Verbose {
			fmt.Fprintf(ctx.Stderr, "[verbose] total: %s\n", formatDuration(time.Since(ctx.Started)))
		}
		return nil
	}
	if shouldSeparateStdoutPayload(ctx.Emit, ctx.Invocation, ctx.Stdout, ctx.Stderr) {
		fmt.Fprintln(ctx.Stderr)
	}
	outputStarted := time.Now()
	finishEmitBench := platform.InternalBenchSpan("output.metadata.emit")
	emitStats, err := output.WithPayloadWriter(ctx.Emit, outputEnvironmentFromInvocation(ctx.Invocation), ctx.Stdout, ctx.Colors, func(w io.Writer) error {
		return ui.WriteMetadataReport(w, metadata)
	})
	finishEmitBench("err", platform.InternalBenchError(err), "bytes", fmt.Sprintf("%d", emitStats.PayloadBytes))
	if err != nil {
		return err
	}
	if ctx.Invocation.Verbose {
		fmt.Fprintf(ctx.Stderr, "[verbose] metadata output: %s\n", formatDuration(time.Since(outputStarted)))
	}
	if ctx.Emit.OutputMode == command.OutputModeClipboard && !ctx.Invocation.Quiet {
		if err := ui.WriteMetadataClipboardSuccess(ctx.Stderr, state.Plan, emitStats, ctx.Colors); err != nil {
			return err
		}
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
	presentationWriter := ctx.Stderr
	presentationColors := ctx.Colors
	if ctx.Invocation.EmissionPolicy == command.EmissionNever {
		presentationWriter = ctx.Stdout
		presentationColors = platform.ActivePaletteForWriter(ctx.Stdout)
	}
	proceed, err := ui.WriteNormalDiagnostics(
		ctx.Render,
		ctx.Git,
		state.Plan,
		report,
		ctx.Invocation.EmissionPolicy,
		presentationWriter,
		ctx.Stderr,
		presentationColors,
		ctx.Colors,
	)
	if err != nil {
		return err
	}
	if ctx.Invocation.Verbose {
		fmt.Fprintf(ctx.Stderr, "[verbose] diagnostics: %s\n", formatDuration(time.Since(diagStarted)))
	}
	if !proceed {
		if ctx.Invocation.Verbose {
			fmt.Fprintf(ctx.Stderr, "[verbose] total: %s\n", formatDuration(time.Since(ctx.Started)))
		}
		if ctx.Invocation.EmissionPolicy == command.EmissionNever &&
			(state.Summary.HasScopeUnsatisfiable || state.Summary.HasTargetNotFound) {
			return newExitError(1, "")
		}
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
