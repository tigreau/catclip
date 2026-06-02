package catclip

import (
	"errors"
	"fmt"
	"io"
	"time"
)

type outputExecutionContext struct {
	Invocation invocationConfig
	Render     renderConfig
	Emit       emitConfig
	Git        gitContext
	Stdout     io.Writer
	Stderr     io.Writer
	Colors     colorPalette
	Started    time.Time
}

type outputExecutionState struct {
	Scopes      []executionScope
	Plan        outputPlan
	Diagnostics []diagnostic
	Summary     DiagnosticSummary
	Notices     []string
}

func executeTreePayload(ctx outputExecutionContext, state outputExecutionState) error {
	writeTreePayloadDiagnostics(state.Diagnostics, ctx.Stderr)
	if state.Summary.HasError {
		return newExitError(1, "")
	}
	reportStarted := time.Now()
	err := encodeTreePayloadFromPlan(ctx.Stdout, ctx.Render, ctx.Git, state.Plan, state.Notices)
	if err != nil {
		if errors.Is(err, errTreePayloadEmptyNoTarget) {
			if err := writeNoFilesMatchedMessage(state.Scopes, ctx.Stderr, ctx.Colors, state.Summary.HadSelectionCancel); err != nil {
				return err
			}
			if state.Summary.HasScopeUnsatisfiable {
				return newExitError(2, "")
			}
			return newExitError(1, "")
		}
		return err
	}
	if ctx.Invocation.Verbose {
		fmt.Fprintf(ctx.Stderr, "[verbose] report: %s\n", formatDuration(time.Since(reportStarted)))
	}
	if state.Summary.HasScopeUnsatisfiable {
		if len(state.Plan.items) == 0 {
			return newExitError(2, "")
		}
		return newExitError(1, "")
	}
	if state.Summary.HasTargetNotFound {
		return newExitError(1, "")
	}
	return nil
}

func executePlanOutput(ctx outputExecutionContext, state outputExecutionState) error {
	writeDiscoveryDiagnostics(state.Diagnostics, ctx.Invocation.Quiet, ctx.Stderr)
	if len(state.Plan.items) == 0 {
		return executeEmptyOutput(ctx, state)
	}
	reportStarted := time.Now()
	report, err := buildOutputReportForPlan(ctx.Render, ctx.Git, state.Plan, dedupePreserveOrder(state.Notices))
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
		for _, notice := range dedupePreserveOrder(state.Notices) {
			fmt.Fprintln(ctx.Stderr, notice)
		}
	}
	if err := writeNoFilesMatchedMessage(state.Scopes, ctx.Stderr, ctx.Colors, state.Summary.HadSelectionCancel); err != nil {
		return err
	}
	if state.Summary.HasScopeUnsatisfiable {
		return newExitError(2, "")
	}
	return newExitError(1, "")
}

func executePreview(ctx outputExecutionContext, state outputExecutionState, report outputReport) error {
	renderStarted := time.Now()
	// --preview renders the file table (not the tree). The tree's renderPreview
	// stays for the sink picker; the confirmation flow keeps writeNormalDiagnostics.
	err := renderPreviewTable(ctx.Render, ctx.Git, state.Plan, report, ctx.Stdout, ctx.Stderr,
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

func executeNormalOutput(ctx outputExecutionContext, state outputExecutionState, report outputReport) error {
	if err := validateRawOutputPlan(ctx.Emit, state.Plan); err != nil {
		return err
	}
	diagStarted := time.Now()
	proceed, err := writeNormalDiagnostics(ctx.Render, ctx.Git, state.Plan, report, ctx.Stderr, ctx.Colors)
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
	if !ctx.Invocation.Quiet && ctx.Emit.OutputMode == outputModeClipboard {
		outputSpinnerStop = startLoadingSpinner(spinnerOutputFile(ctx.Stderr), outputSpinnerMessage(ctx.Emit))
	}
	if shouldSeparateStdoutPayload(ctx.Emit, ctx.Invocation, ctx.Stdout, ctx.Stderr) {
		fmt.Fprintln(ctx.Stderr)
	}
	outputStarted := time.Now()
	emitStats, err := emitOutputPlan(ctx.Emit, emitEnvironmentFromInvocationConfig(ctx.Invocation), state.Plan, ctx.Stdout, ctx.Colors)
	if err != nil {
		outputSpinnerStop()
		return err
	}
	outputSpinnerStop()
	outputMetrics.PayloadBytes = emitStats.PayloadBytes
	if ctx.Invocation.Verbose {
		outputDuration := time.Since(outputStarted)
		fmt.Fprintf(ctx.Stderr, "[verbose] output: %s\n", formatDuration(outputDuration))
		writeVerboseOutputMetrics(ctx.Stderr, outputMetrics, emitStats, len(state.Plan.items), outputDuration)
	}
	if ctx.Emit.OutputMode == outputModeClipboard && !ctx.Invocation.Quiet {
		if err := writeClipboardSuccess(ctx.Stderr, state.Plan, emitStats, ctx.Colors); err != nil {
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
