package catclip

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	case command.ActionCheckUpdate:
		return runCheckUpdate(checkUpdateConfigFromParsedCommand(cfg), stdout)
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
		restorePromptGuard := ui.PushHeadlessPromptGuard(cfg.Headless || cfg.IsInternalKind())
		defer restorePromptGuard()
		if cfg.IsInternalKind() {
			// Internal reload/preview commands are short-lived fzf-spawned
			// helpers. Arm signal-driven cancellation so a superseded reload's
			// rg child is killed when fzf terminates this process, instead of
			// orphaning a full-corpus scan.
			search.InstallReloadCancellation()
		}
		if cfg.TreePreview && cfg.TreeInputDir != "" {
			return ui.RunInternalTreePayloadFilePreview(cfg.TreeInputDir, cfg.TreeInputStem, stdout)
		}
		if cfg.TreePreview && cfg.TargetPreviewInventory != "" {
			ui.KillSupersededTargetTreePreview()
			resolved := command.ResolvedFromParsed(cfg)
			if !freshTargetTreePreviewScopesEligible(resolved.Scopes) || len(resolved.Scopes) != 1 {
				return fmt.Errorf("target inventory preview does not accept filter or output stages")
			}
			finishRead := platform.InternalBenchSpan("ui.target_tree_preview.inventory_read")
			inventory, err := discovery.ReadTargetPreviewInventory(cfg.TargetPreviewInventory, resolved.Config.WorkingDir)
			finishRead(
				"err", platform.InternalBenchError(err),
				"entries", platform.InternalBenchInt(len(inventory.Entries)),
			)
			if err != nil {
				return err
			}
			// Prefer the completed session snapshot when it already exists. The
			// base inventory remains the immediate path that lets fzf open while
			// the parent-owned size capture is still finishing.
			if inventory.SizesPending {
				if sized, sizedErr := discovery.ReadTargetPreviewInventory(
					discovery.TargetPreviewSizedInventoryPath(cfg.TargetPreviewInventory),
					resolved.Config.WorkingDir,
				); sizedErr == nil {
					inventory = sized
				} else if !os.IsNotExist(sizedErr) {
					return sizedErr
				}
			}
			finishSelect := platform.InternalBenchSpan("ui.target_tree_preview.inventory_select",
				"entries", platform.InternalBenchInt(len(inventory.Entries)),
			)
			entries := discovery.SelectTargetPreviewEntries(inventory.Entries, resolved.Scopes[0].Targets)
			finishSelect("selected", platform.InternalBenchInt(len(entries)))
			if inventory.SizesPending && targetPreviewUnknownSizeCount(entries) >= targetPreviewSharedSizeWaitThreshold {
				finishWait := platform.InternalBenchSpan("ui.target_tree_preview.inventory_size_wait",
					"selected", platform.InternalBenchInt(len(entries)),
				)
				sized, ok, waitErr := discovery.WaitForTargetPreviewSizedInventory(
					search.ReloadCancelContext(),
					cfg.TargetPreviewInventory,
					resolved.Config.WorkingDir,
				)
				finishWait("err", platform.InternalBenchError(waitErr), "ready", platform.InternalBenchBool(ok))
				if waitErr != nil {
					if search.ReloadWasCancelled() {
						return nil
					}
					return waitErr
				}
				if ok {
					inventory = sized
					entries = discovery.SelectTargetPreviewEntries(inventory.Entries, resolved.Scopes[0].Targets)
				}
			}
			return executeFreshTargetTreePreview(
				stdout,
				ui.RenderConfigFromParsedCommand(cfg),
				inventory.GitContext,
				entries,
				DiagnosticSummary{},
			)
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
		if cfg.TreePreview {
			ui.KillSupersededTargetTreePreview()
		}

		colors := platform.ActivePalette()
		started := time.Now()
		resolved := command.ResolvedFromParsed(cfg)
		invocationCfg := resolved.Config
		emitCfg := emitConfigFromParsedCommand(cfg)
		renderCfg := ui.RenderConfigFromParsedCommand(cfg)
		finishGitBench := platform.InternalBenchSpan("cli.run.git_detect")
		gitCtx := git.Detect(invocationCfg.WorkingDir)
		finishGitBench(
			"enabled", platform.InternalBenchBool(gitCtx.Enabled),
			"has_head", platform.InternalBenchBool(gitCtx.HasHead),
		)
		commandScopes := resolved.Scopes
		freshTargetScopesEligible := freshTargetTreePreviewScopesEligible(commandScopes)
		if cfg.TreePreview && prepared == nil && ui.TargetTreePreviewSessionActive() && !freshTargetScopesEligible {
			return fmt.Errorf("fresh target preview does not accept filter or output stages")
		}
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
		freshTargetTreePreview := cfg.TreePreview && prepared == nil && freshTargetScopesEligible
		if prepared != nil {
			gitCtx = prepared.Git
			discoveryResult = prepared.Discovery
			outputPlan = prepared.Plan
		} else {
			discoverySpinnerStop := func() {}
			if !cfg.Quiet {
				// Same 5 s delayed reassurance as the target-picker
				// spinner; this discovery phase is the one that
				// dominates cold runs on big trees. On Windows the
				// dominant cold cost is Defender scanning every file a
				// content search reads (once per boot), so the hint
				// says why, matching the content picker's searching
				// preview document.
				discoverySpinnerStop = platform.StartLoadingSpinnerWithDelayedHint(
					platform.SpinnerOutputFile(stderr),
					"Scanning files...",
					platform.SlowFileScanHint(),
					5*time.Second,
				)
			}
			var err error
			finishDiscoveryBench := platform.InternalBenchSpan("cli.run.discovery",
				"scopes", platform.InternalBenchInt(len(resolved.Scopes)),
			)
			discoveryResult, err = discovery.DiscoverInvocation(resolved, gitCtx, stderr, colors)
			discoveredEntries := 0
			for _, scope := range discoveryResult.Invocation.Scopes {
				discoveredEntries += len(scope.Entries)
			}
			finishDiscoveryBench(
				"err", platform.InternalBenchError(err),
				"entries", platform.InternalBenchInt(discoveredEntries),
			)
			if err != nil {
				discoverySpinnerStop()
				return err
			}
			discoverySpinnerStop()
			if !freshTargetTreePreview {
				finishPlanBench := platform.InternalBenchSpan("cli.run.build_plan",
					"scopes", platform.InternalBenchInt(len(discoveryResult.Invocation.Scopes)),
				)
				outputPlan, err = output.BuildPlanForDiscoveredInvocation(gitCtx, discoveryResult.Invocation)
				finishPlanBench(
					"err", platform.InternalBenchError(err),
					"items", platform.InternalBenchInt(outputPlan.Len()),
				)
				if err != nil {
					return err
				}
			}
		}
		if cfg.Verbose {
			for _, stat := range discoveryResult.ScopeStats {
				fmt.Fprintf(stderr, "[verbose] scope %d: discovered %d file(s) in %s\n", stat.Index+1, stat.Count, formatDuration(stat.Duration))
			}
			printTextClassificationResidue(stderr)
		}
		diagnostics := make([]discovery.Diagnostic, 0, len(cfg.Warnings)+len(discoveryResult.Diagnostics))
		for _, warning := range cfg.Warnings {
			diagnostics = append(diagnostics, discovery.Diagnostic{Message: warning, ScopeIndex: -1})
		}
		diagnostics = append(diagnostics, discoveryResult.Diagnostics...)
		notices := discoveryResult.Notices
		diagnosticSummary := summarizeDiagnostics(diagnostics, discoveryResult.HadSelectionCancel)
		if freshTargetTreePreview {
			entries := make([]discovery.Entry, 0)
			for _, scope := range discoveryResult.Invocation.Scopes {
				entries = append(entries, scope.Entries...)
			}
			err := executeFreshTargetTreePreview(stdout, renderCfg, gitCtx, entries, diagnosticSummary)
			if err != nil && search.ReloadWasCancelled() {
				return nil
			}
			return err
		}
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

const targetPreviewSharedSizeWaitThreshold = 4096

func targetPreviewUnknownSizeCount(entries []discovery.Entry) int {
	unknown := 0
	for index := range entries {
		if !entries[index].SizeKnown {
			unknown++
		}
	}
	return unknown
}

func freshTargetTreePreviewScopesEligible(scopes []command.ExecutionScope) bool {
	if len(scopes) != 1 {
		return false
	}
	scope := scopes[0]
	if len(scope.Stages) != 0 || scope.Paths || scope.OutputMode() != command.EntryModeFull {
		return false
	}
	return true
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
	if msg := strings.TrimSpace(err.Error()); msg != "" {
		fmt.Fprintln(stderr, msg)
	}
	os.Exit(exitCodeForError(err))
}

type catclipExitCoder interface {
	error
	CatclipExitCode() int
}

func exitCodeForError(err error) int {
	if err == nil {
		return 0
	}
	var coded catclipExitCoder
	if !errors.As(err, &coded) {
		return 1
	}
	code := coded.CatclipExitCode()
	if code != 1 && code != 2 {
		return 1
	}
	return code
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
	TargetPreviewInventory string
	TreeInputDir           string
	TreeInputStem          string
	FileSetSelectionPath   string
	FileSetSelectionStage  string
	FilePreview            bool
	FileSearchingPreview   bool
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
		TargetPreviewInventory: cfg.TargetPreviewInventory,
		TreeInputDir:           cfg.TreeInputDir,
		TreeInputStem:          cfg.TreeInputStem,
		FileSetSelectionPath:   cfg.FileSetSelectionPath,
		FileSetSelectionStage:  cfg.FileSetSelectionStage,
		FilePreview:            cfg.FilePreview,
		FileSearchingPreview:   cfg.FileSearchingPreview,
		ContentMatchList:       cfg.ContentMatchList,
		SnippetBoundaryPreview: cfg.SnippetBoundaryPreview,
		RecentPreview:          cfg.RecentPreview,
		LinesPreview:           cfg.LinesPreview,
		SinkTogglePath:         cfg.SinkTogglePath,
		SinkPreviewModePath:    cfg.SinkPreviewModePath,
	}
}

func validateImplementedFeatureSet(cfg internalCommandConfig) error {
	if cfg.PrediscoveredPath != "" && !cfg.TreePreview && !cfg.ContentMatchList && !cfg.LinesPreview && !cfg.FilePreview {
		return newUsageError("Error: --internal-prediscovered requires --internal-tree-preview, --internal-content-match-list, --internal-lines-preview, or --internal-file-preview.")
	}
	if cfg.TargetPreviewInventory != "" && !cfg.TreePreview {
		return newUsageError("Error: --internal-target-inventory requires --internal-tree-preview.")
	}
	if cfg.FileSearchingPreview && !cfg.FilePreview {
		return newUsageError("Error: --internal-searching-preview requires --internal-file-preview.")
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
	if cfg.TargetPreviewInventory != "" && (cfg.PrediscoveredPath != "" || cfg.TreeInputDir != "") {
		return newUsageError("Error: --internal-target-inventory cannot be combined with another tree preview input.")
	}
	if (cfg.FileSetSelectionPath != "" || cfg.FileSetSelectionStage != "") && (cfg.PrediscoveredPath == "" || !cfg.TreePreview) {
		return newUsageError("Error: --internal-file-set-selection requires --internal-prediscovered and --internal-tree-preview.")
	}
	if (cfg.FileSetSelectionPath == "") != (cfg.FileSetSelectionStage == "") {
		return newUsageError("Error: --internal-file-set-selection and --internal-file-set-stage must be used together.")
	}
	if cfg.LinesPreview && cfg.PrediscoveredPath == "" {
		return newUsageError("Error: --internal-lines-preview requires --internal-prediscovered.")
	}
	return nil
}

func emitConfigFromParsedCommand(cfg command.Parsed) output.EmitConfig {
	return output.EmitConfig{
		OutputMode: cfg.OutputMode,
		Raw:        cfg.Raw,
		NoBundle:   cfg.NoBundle,
	}
}

func outputEnvironmentFromInvocation(cfg command.Invocation) output.RuntimeEnvironment {
	return output.RuntimeEnvironment{
		Platform:   cfg.Platform,
		WorkingDir: cfg.WorkingDir,
	}
}

// printTextClassificationResidue reports, under --verbose, which files
// the hybrid Stage 2 classifier had to content-scan because their names
// were undecidable (see internal/search/known_files.go). This is the
// list-growth feedback loop, so the extension histogram leads: an
// extension recurring here is a list candidate with evidence. Individual
// paths print only when the residue is small enough to read.
func printTextClassificationResidue(stderr io.Writer) {
	residue, textCount := search.TextClassificationResidue()
	if len(residue) == 0 {
		return
	}
	fmt.Fprintf(stderr, "[verbose] text classification: %d file(s) content-scanned (name undecidable), %d classified text\n", len(residue), textCount)

	counts := map[string]int{}
	for _, rel := range residue {
		counts[residueReportExtension(rel)]++
	}
	type extRow struct {
		ext string
		n   int
	}
	rows := make([]extRow, 0, len(counts))
	for ext, n := range counts {
		rows = append(rows, extRow{ext: ext, n: n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].ext < rows[j].ext
	})
	const histogramCap = 20
	for i, r := range rows {
		if i == histogramCap {
			fmt.Fprintf(stderr, "[verbose]   ... and %d more extension(s)\n", len(rows)-histogramCap)
			break
		}
		fmt.Fprintf(stderr, "[verbose]   %4d  %s\n", r.n, r.ext)
	}

	const pathPrintCap = 20
	if len(residue) <= pathPrintCap {
		for _, rel := range residue {
			fmt.Fprintf(stderr, "[verbose]   %s\n", rel)
		}
	}
}

// residueReportExtension mirrors the classifier's last-dot-segment
// extension semantics for reporting (".tar.gz" groups under ".gz";
// dotfiles and extensionless names group under "(no extension)").
func residueReportExtension(rel string) string {
	base := strings.ToLower(filepath.Base(rel))
	lastDot := strings.LastIndexByte(base, '.')
	if lastDot <= 0 || lastDot == len(base)-1 {
		return "(no extension)"
	}
	return "." + base[lastDot+1:]
}
