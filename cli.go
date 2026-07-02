package catclip

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
	"github.com/tigreau/catclip/internal/ui"
)

type verboseOutputMetrics struct {
	PayloadBytes           int64
	CleanTrackedCount      int
	ModifiedUntrackedCount int
	GitStateKnown          bool
	GitStateNote           string
}

func run(cfg command.Parsed, stdout, stderr io.Writer, preparedOpt ...*ui.StartupPreparedOutputState) error {
	var prepared *ui.StartupPreparedOutputState
	if len(preparedOpt) > 0 {
		prepared = preparedOpt[0]
	}

	// Surface the rg call/timing counters at end-of-run when explicitly
	// opted into. Useful for diagnosing per-platform perf (especially
	// Windows where process spawn cost dominates --contains / --snippet).
	// No-op when CATCLIP_BENCH_RG is unset.
	defer search.BenchReport()

	switch cfg.Action {
	case command.ActionHelp, command.ActionHelpAll:
		helpText := cli.ShortHelpText
		if cfg.Action == command.ActionHelpAll {
			helpText = cli.FullHelpText
		}
		hiss := platform.DisplayPath(discovery.GlobalHissPath())
		_, err := io.WriteString(stdout, helpText(cfg.Version, hiss, platform.ActivePaletteForWriter(stdout)))
		return err
	case command.ActionVersion:
		return ui.RenderVersionOutput(cfg.Version, stdout)
	case command.ActionEditHiss:
		return runEditHiss(hissConfigFromParsedCommand(cfg), stderr)
	case command.ActionResetHiss:
		return runResetHiss(hissConfigFromParsedCommand(cfg), stderr)
	case command.ActionListIgnoreRules:
		return runListIgnoreRules(listIgnoreRulesConfigFromParsedCommand(cfg), stdout, stderr)
	case command.ActionRun:
		internalCfg := internalCommandConfigFromParsedCommand(cfg)
		if err := validateImplementedFeatureSet(internalCfg); err != nil {
			return err
		}
		restorePromptGuard := ui.PushHeadlessPromptGuard(cfg.Headless || internalCfg.isInternalKind())
		defer restorePromptGuard()
		if internalCfg.isInternalKind() {
			// Internal reload/preview commands are short-lived fzf-spawned
			// helpers. Arm signal-driven cancellation so a superseded reload's
			// rg child is killed when fzf terminates this process, instead of
			// orphaning a full-corpus scan.
			search.InstallReloadCancellation()
		}
		if cfg.TreePreview && cfg.TreeInputDir != "" {
			return ui.RunInternalTreePayloadFilePreview(cfg.TreeInputDir, cfg.TreeInputStem, stdout)
		}
		if cfg.PrediscoveredPath != "" {
			prediscoveredCfg := ui.PrediscoveredCommandConfigFromParsedCommand(cfg)
			if cfg.ContentMatchList {
				err := ui.RunInternalPrediscoveredContentMatchList(prediscoveredCfg, stdout)
				if err != nil && search.ReloadWasCancelled() {
					// Superseded by a newer keystroke; fzf killed our rg. Exit
					// quietly instead of surfacing "signal: killed".
					return nil
				}
				return err
			}
			if cfg.LinesPreview {
				return ui.RunInternalLinesPreview(prediscoveredCfg, emitConfigFromParsedCommand(cfg), stdout)
			}
			if cfg.TreePreview {
				return ui.RunInternalPrediscoveredTreePreview(prediscoveredCfg, stdout)
			}
			// --internal-file-preview paired with --internal-prediscovered
			// routes through the file-preview handler so it can dispatch on
			// (empty pattern, empty focused path, checkpoint present) — see
			// ui.RunInternalFilePreview for the three preview states.
			if cfg.FilePreview {
				return ui.RunInternalFilePreview(ui.FilePreviewConfigFromParsedCommand(cfg), stdout)
			}
		}
		if cfg.FilePreview {
			return ui.RunInternalFilePreview(ui.FilePreviewConfigFromParsedCommand(cfg), stdout)
		}
		if cfg.ContentMatchList {
			err := ui.RunInternalContentMatchList(ui.ContentMatchListConfigFromParsedCommand(cfg), stdout)
			if err != nil && search.ReloadWasCancelled() {
				return nil
			}
			return err
		}
		if cfg.SnippetBoundaryPreview {
			err := ui.RunInternalSnippetBoundaryPreview(cfg.BoundarySourcePath, cfg.BoundaryKey, stdout)
			if err != nil && search.ReloadWasCancelled() {
				return nil
			}
			return err
		}
		if cfg.RecentPreview {
			return ui.RunInternalRecentPreview(ui.RecentPreviewConfigFromParsedCommand(cfg), stdout)
		}
		if cfg.SinkTogglePath != "" {
			return ui.RunInternalSinkToggle(cfg.SinkTogglePath, stdout)
		}
		if cfg.SinkPreviewModePath != "" {
			return ui.RunInternalSinkPreview(cfg.SinkPreviewModePath, cfg.SinkPreviewOutputPath, cfg.SinkPreviewTreePath, stdout)
		}

		colors := platform.ActivePalette()
		started := time.Now()
		resolved := resolvedInvocationFromParsedCommand(cfg)
		invocationCfg := resolved.Config
		emitCfg := emitConfigFromParsedCommand(cfg)
		renderCfg := ui.RenderConfigFromParsedCommand(cfg)
		gitCtx := git.Detect(invocationCfg.WorkingDir)
		if proceed, err := warnDirectoryPatternSemantics(stderr, colors); err != nil {
			return err
		} else if !proceed {
			return nil
		}

		commandScopes := resolved.Scopes
		if cfg.Verbose {
			fmt.Fprintf(stderr, "[verbose] parsed %d scope(s)\n", len(commandScopes))
			for i, s := range commandScopes {
				fmt.Fprintf(stderr, "[verbose] scope %d: %s\n", i+1, cli.FormatScopeSummary(s))
			}
		}
		var (
			discoveryResult discovery.Result
			outputPlan      output.Plan
		)
		if prepared != nil {
			gitCtx = prepared.Git
			discoveryResult = prepared.Discovery
			outputPlan = prepared.Plan
		} else {
			discoverySpinnerStop := func() {}
			if !cfg.Quiet {
				// Same 5 s delayed reassurance as the target-picker
				// spinner; this discovery phase is the one that
				// dominates cold runs on big trees.
				discoverySpinnerStop = platform.StartLoadingSpinnerWithDelayedHint(
					platform.SpinnerOutputFile(stderr),
					"Scanning files...",
					"(first run is supposed to be slow)",
					5*time.Second,
				)
			}
			var err error
			discoveryResult, err = discovery.DiscoverInvocation(resolved, gitCtx, stderr, colors)
			if err != nil {
				discoverySpinnerStop()
				return err
			}
			discoverySpinnerStop()
			outputPlan, err = output.BuildPlanForDiscoveredInvocation(gitCtx, discoveryResult.Invocation)
			if err != nil {
				return err
			}
		}
		if cfg.Verbose {
			for _, stat := range discoveryResult.ScopeStats {
				fmt.Fprintf(stderr, "[verbose] scope %d: discovered %d file(s) in %s\n", stat.Index+1, stat.Count, formatDuration(stat.Duration))
			}
		}
		diagnostics := make([]discovery.Diagnostic, 0, len(cfg.Warnings)+len(discoveryResult.Diagnostics))
		for _, warning := range cfg.Warnings {
			diagnostics = append(diagnostics, discovery.Diagnostic{Message: warning})
		}
		diagnostics = append(diagnostics, discoveryResult.Diagnostics...)
		notices := discoveryResult.Notices
		diagnosticSummary := summarizeDiagnostics(diagnostics, discoveryResult.HadSelectionCancel)
		outputCtx := outputExecutionContext{
			Invocation: invocationCfg,
			Render:     renderCfg,
			Emit:       emitCfg,
			Git:        gitCtx,
			Stdout:     stdout,
			Stderr:     stderr,
			Colors:     colors,
			Started:    started,
		}
		outputState := outputExecutionState{
			Scopes:      commandScopes,
			Plan:        outputPlan,
			Diagnostics: diagnostics,
			Summary:     diagnosticSummary,
			Notices:     notices,
		}
		if cfg.TreePreview {
			return executeTreePreview(outputCtx, outputState)
		}
		return executePlanOutput(outputCtx, outputState)
	default:
		return fmt.Errorf("unknown action %q", cfg.Action)
	}
}

func shouldSeparateStdoutPayload(emitCfg output.EmitConfig, invocationCfg command.Invocation, stdout, stderr io.Writer) bool {
	if emitCfg.OutputMode != command.OutputModeStdout || invocationCfg.Quiet {
		return false
	}
	stdoutFile := platform.SpinnerOutputFile(stdout)
	if stdoutFile == nil || !platform.IsTerminalFile(stdoutFile) {
		return false
	}
	if stderrFile := platform.SpinnerOutputFile(stderr); stderrFile != nil && !platform.IsTerminalFile(stderrFile) {
		return false
	}
	return true
}

func collectVerboseOutputMetrics(verbose bool, gitCtx git.Context, plan output.Plan) (verboseOutputMetrics, error) {
	if !verbose {
		return verboseOutputMetrics{}, nil
	}

	metrics := verboseOutputMetrics{}
	if !gitCtx.Enabled || plan.IsEmpty() {
		return metrics, nil
	}

	trackedLines, err := git.Lines(gitCtx.Root, nil, "ls-files")
	if err != nil {
		return verboseOutputMetrics{}, err
	}
	if len(trackedLines) == 0 {
		metrics.GitStateNote = "unavailable (git ls-files returned no tracked files)"
		return metrics, nil
	}
	tracked := make(map[string]struct{}, len(trackedLines))
	for _, repoPath := range trackedLines {
		tracked[normalizeRelPath(repoPath)] = struct{}{}
	}

	changedLines, err := discovery.CollectChangedRepoPaths(gitCtx, command.ExecutionScope{})
	if err != nil {
		return verboseOutputMetrics{}, err
	}
	changed := make(map[string]struct{}, len(changedLines))
	for _, repoPath := range changedLines {
		changed[normalizeRelPath(repoPath)] = struct{}{}
	}

	for _, entry := range plan.EntriesInEmissionOrder() {
		repoPath := normalizeRelPath(gitCtx.ToRepoPath(entry.RelPath))
		if _, ok := tracked[repoPath]; !ok {
			metrics.ModifiedUntrackedCount++
			continue
		}
		if _, ok := changed[repoPath]; ok {
			metrics.ModifiedUntrackedCount++
			continue
		}
		metrics.CleanTrackedCount++
	}
	metrics.GitStateKnown = true
	return metrics, nil
}

func writeVerboseOutputMetrics(w io.Writer, metrics verboseOutputMetrics, emitStats output.EmitStats, fileCount int, outputDuration time.Duration) {
	payloadHuman := formatByteCount(metrics.PayloadBytes)
	avgBytes := int64(0)
	if fileCount > 0 {
		avgBytes = metrics.PayloadBytes / int64(fileCount)
	}
	throughputMiB := 0.0
	if outputDuration > 0 {
		throughputMiB = float64(metrics.PayloadBytes) / (1024 * 1024) / outputDuration.Seconds()
	}

	// Always emit; Windows monotonic clock can round sub-millisecond operations
	// to 0 and we still want the row in the verbose log for parity with
	// macOS/Linux output.
	fmt.Fprintf(w, "[verbose] emit generate: %s\n", formatDuration(emitStats.GenerateDuration))
	if emitStats.ClipboardWaitDuration > 0 {
		fmt.Fprintf(w, "[verbose] clipboard flush/close (%s): %s\n", emitStats.SinkName, formatDuration(emitStats.SinkFinalizeDuration))
	} else {
		fmt.Fprintf(w, "[verbose] emit flush (%s): %s\n", emitStats.SinkName, formatDuration(emitStats.SinkFinalizeDuration))
	}
	if emitStats.ClipboardWaitDuration > 0 {
		fmt.Fprintf(w, "[verbose] clipboard wait (%s): %s\n", emitStats.SinkName, formatDuration(emitStats.ClipboardWaitDuration))
	}
	fmt.Fprintf(w, "[verbose] payload: %d bytes (%s), avg/file: %d bytes (%s)\n",
		metrics.PayloadBytes, payloadHuman, avgBytes, formatByteCount(avgBytes))
	if metrics.GitStateKnown {
		fmt.Fprintf(w, "[verbose] git file states: clean-tracked=%d modified/untracked=%d\n",
			metrics.CleanTrackedCount, metrics.ModifiedUntrackedCount)
	} else if metrics.GitStateNote != "" {
		fmt.Fprintf(w, "[verbose] git file states: %s\n", metrics.GitStateNote)
	}
	fmt.Fprintf(w, "[verbose] output throughput: %.2f MiB/s\n", throughputMiB)
}

func formatByteCount(totalBytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)

	switch {
	case totalBytes < kb:
		return fmt.Sprintf("%dB", totalBytes)
	case totalBytes < mb:
		return fmt.Sprintf("%.2fKB", float64(totalBytes)/kb)
	case totalBytes < gb:
		return fmt.Sprintf("%.2fMB", float64(totalBytes)/mb)
	default:
		return fmt.Sprintf("%.2fGB", float64(totalBytes)/gb)
	}
}

func exitWithError(err error, stderr io.Writer) {
	if err == nil {
		return
	}

	code := 1
	if _, ok := err.(usageError); ok {
		code = 2
	}
	var discoveryUsage discovery.UsageError
	if errors.As(err, &discoveryUsage) {
		code = 2
	}
	var outputUsage output.UsageError
	if errors.As(err, &outputUsage) {
		code = 2
	}
	var uiUsage ui.UsageError
	if errors.As(err, &uiUsage) {
		code = 2
	}
	var cliUsage cli.UsageError
	if errors.As(err, &cliUsage) {
		code = 2
	}
	var validation cli.ValidationFailure
	if errors.As(err, &validation) {
		code = 2
	}
	if exitErr, ok := err.(exitError); ok {
		code = exitErr.code
	}
	if msg := strings.TrimSpace(err.Error()); msg != "" {
		fmt.Fprintln(stderr, msg)
	}
	os.Exit(code)
}

type hissConfig struct {
	Quiet bool
	Yes   bool
}

func hissConfigFromParsedCommand(cfg command.Parsed) hissConfig {
	return hissConfig{
		Quiet: cfg.Quiet,
		Yes:   cfg.Yes,
	}
}

func runEditHiss(cfg hissConfig, stderr io.Writer) error {
	path, err := discovery.EnsureGlobalHiss()
	if err != nil {
		return err
	}

	editor, err := platform.ResolveEditorCommand()
	if err != nil {
		return err
	}

	if !cfg.Quiet {
		fmt.Fprintf(stderr, "Opening %s in %s...\n", path, filepath.Base(editor.Path))
	}

	cmd := exec.Command(editor.Path, append(editor.Args, path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runResetHiss(cfg hissConfig, stderr io.Writer) error {
	path, err := discovery.EnsureGlobalHiss()
	if err != nil {
		return err
	}

	shouldReset := cfg.Yes
	if !shouldReset {
		if !cfg.Quiet {
			fmt.Fprintf(stderr, "This will overwrite %s with defaults.\n", path)
		}
		shouldReset, err = ui.PromptYesNo("Are you sure? [y/N]", false, stderr)
		if err != nil {
			return err
		}
	}

	if !shouldReset {
		if !cfg.Quiet {
			fmt.Fprintln(stderr, "Cancelled.")
		}
		return nil
	}

	if err := os.WriteFile(path, []byte(discovery.DefaultHissContents), 0o644); err != nil {
		return err
	}
	if !cfg.Quiet {
		fmt.Fprintln(stderr, "Configuration restored.")
	}
	return nil
}

type internalCommandConfig struct {
	TreePreview            bool
	PrediscoveredPath      string
	TreeInputDir           string
	TreeInputStem          string
	FilePreview            bool
	ContentMatchList       bool
	SnippetBoundaryPreview bool
	RecentPreview          bool
	LinesPreview           bool
	SinkTogglePath         string
	SinkPreviewModePath    string
}

func internalCommandConfigFromParsedCommand(cfg command.Parsed) internalCommandConfig {
	return internalCommandConfig{
		TreePreview:            cfg.TreePreview,
		PrediscoveredPath:      cfg.PrediscoveredPath,
		TreeInputDir:           cfg.TreeInputDir,
		TreeInputStem:          cfg.TreeInputStem,
		FilePreview:            cfg.FilePreview,
		ContentMatchList:       cfg.ContentMatchList,
		SnippetBoundaryPreview: cfg.SnippetBoundaryPreview,
		RecentPreview:          cfg.RecentPreview,
		LinesPreview:           cfg.LinesPreview,
		SinkTogglePath:         cfg.SinkTogglePath,
		SinkPreviewModePath:    cfg.SinkPreviewModePath,
	}
}

func (cfg internalCommandConfig) isInternalKind() bool {
	return cfg.TreePreview || cfg.FilePreview ||
		cfg.ContentMatchList || cfg.SnippetBoundaryPreview || cfg.RecentPreview ||
		cfg.LinesPreview ||
		cfg.PrediscoveredPath != "" || cfg.TreeInputDir != "" ||
		cfg.SinkTogglePath != "" || cfg.SinkPreviewModePath != ""
}

func validateImplementedFeatureSet(cfg internalCommandConfig) error {
	if cfg.PrediscoveredPath != "" && !cfg.TreePreview && !cfg.ContentMatchList && !cfg.LinesPreview && !cfg.FilePreview {
		return newUsageError("Error: --internal-prediscovered requires --internal-tree-preview, --internal-content-match-list, --internal-lines-preview, or --internal-file-preview.")
	}
	if (cfg.TreeInputDir != "" || cfg.TreeInputStem != "") && !cfg.TreePreview {
		return newUsageError("Error: --input-dir and --input-stem require --internal-tree-preview.")
	}
	if cfg.TreeInputDir != "" && cfg.TreeInputStem == "" {
		return newUsageError("Error: --input-dir requires --input-stem.")
	}
	if cfg.TreeInputStem != "" && cfg.TreeInputDir == "" {
		return newUsageError("Error: --input-stem requires --input-dir.")
	}
	if cfg.PrediscoveredPath != "" && cfg.TreeInputDir != "" {
		return newUsageError("Error: --internal-tree-preview accepts either --internal-prediscovered or --input-dir/--input-stem, not both.")
	}
	if cfg.LinesPreview && cfg.PrediscoveredPath == "" {
		return newUsageError("Error: --internal-lines-preview requires --internal-prediscovered.")
	}
	return nil
}

func invocationConfigFromParsedCommand(cfg command.Parsed) command.Invocation {
	return command.Invocation{
		Version:      cfg.Version,
		Platform:     cfg.Platform,
		WorkingDir:   cfg.WorkingDir,
		Verbose:      cfg.Verbose,
		Quiet:        cfg.Quiet,
		Headless:     cfg.Headless,
		WithBinaries: cfg.WithBinaries,
		Internal:     internalCommandConfigFromParsedCommand(cfg).isInternalKind(),
	}
}

func resolvedInvocationFromParsedCommand(cfg command.Parsed) command.Resolved {
	return command.Resolved{
		Config: invocationConfigFromParsedCommand(cfg),
		Scopes: command.ExecutionScopesFromSpec(cfg.Command),
	}
}

func emitConfigFromParsedCommand(cfg command.Parsed) output.EmitConfig {
	return output.EmitConfig{
		OutputMode: cfg.OutputMode,
		Raw:        cfg.Raw,
		NoBundle:   cfg.NoBundle,
	}
}

func emitEnvironmentFromInvocationConfig(cfg command.Invocation) output.EmitEnvironment {
	return output.EmitEnvironment{
		Platform:   cfg.Platform,
		WorkingDir: cfg.WorkingDir,
	}
}

// warnDirectoryPatternSemantics is a stub the runtime dispatcher
// retained from the legacy parser-warning surface. Always returns
// (true, nil) today; left here so adding a new warning is a one-file
// edit at run-time entry.
func warnDirectoryPatternSemantics(stderr io.Writer, colors platform.Palette) (bool, error) {
	return true, nil
}
