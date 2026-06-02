package catclip

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type verboseOutputMetrics struct {
	PayloadBytes           int64
	CleanTrackedCount      int
	ModifiedUntrackedCount int
	GitStateKnown          bool
	GitStateNote           string
}

var headlessPromptGuardCount atomic.Int32

type stdinPathCache struct {
	loaded   bool
	paths    []string
	rawPaths []string
}

func run(cfg parsedCommand, stdout, stderr io.Writer, preparedOpt ...*startupPreparedOutputState) error {
	var prepared *startupPreparedOutputState
	if len(preparedOpt) > 0 {
		prepared = preparedOpt[0]
	}

	// Surface the rg call/timing counters at end-of-run when explicitly
	// opted into. Useful for diagnosing per-platform perf (especially
	// Windows where process spawn cost dominates --contains / --snippet).
	// No-op when CATCLIP_BENCH_RG is unset.
	defer benchReport()

	switch cfg.Action {
	case actionHelp:
		_, err := io.WriteString(stdout, shortHelpText(cfg.Version, activeColorPaletteForWriter(stdout)))
		return err
	case actionHelpAll:
		_, err := io.WriteString(stdout, fullHelpText(cfg.Version, activeColorPaletteForWriter(stdout)))
		return err
	case actionVersion:
		_, err := fmt.Fprintf(stdout, "catclip %s\n", cfg.Version)
		return err
	case actionEditHiss:
		return runEditHiss(hissConfigFromParsedCommand(cfg), stderr)
	case actionResetHiss:
		return runResetHiss(hissConfigFromParsedCommand(cfg), stderr)
	case actionListIgnoreRules:
		return runListIgnoreRules(listIgnoreRulesConfigFromParsedCommand(cfg), stdout, stderr)
	case actionRun:
		internalCfg := internalCommandConfigFromParsedCommand(cfg)
		if err := validateImplementedFeatureSet(internalCfg); err != nil {
			return err
		}
		restorePromptGuard := pushHeadlessPromptGuard(cfg.Headless || internalCfg.isInternalKind())
		defer restorePromptGuard()
		if internalCfg.isInternalKind() {
			// Internal reload/preview commands are short-lived fzf-spawned
			// helpers. Arm signal-driven cancellation so a superseded reload's
			// rg child is killed when fzf terminates this process, instead of
			// orphaning a full-corpus scan.
			installReloadCancellation()
		}
		if cfg.PrediscoveredPath != "" {
			prediscoveredCfg := prediscoveredCommandConfigFromParsedCommand(cfg)
			if cfg.ContentMatchList {
				err := runInternalPrediscoveredContentMatchList(prediscoveredCfg, stdout)
				if err != nil && reloadWasCancelled() {
					// Superseded by a newer keystroke; fzf killed our rg. Exit
					// quietly instead of surfacing "signal: killed".
					return nil
				}
				return err
			}
			if cfg.LinesPreview {
				return runInternalLinesPreview(prediscoveredCfg, emitConfigFromParsedCommand(cfg), stdout)
			}
			if cfg.TreePreview {
				return runInternalPrediscoveredTreePreview(prediscoveredCfg, stdout)
			}
			// --internal-file-preview paired with --internal-prediscovered
			// routes through the file-preview handler so it can dispatch on
			// (empty pattern, empty focused path, checkpoint present) — see
			// runInternalFilePreview for the three preview states.
			if cfg.FilePreview {
				return runInternalFilePreview(filePreviewConfigFromParsedCommand(cfg), stdout)
			}
			return runInternalPrediscoveredTreePayload(prediscoveredCfg, stdout)
		}
		if cfg.FilePreview {
			return runInternalFilePreview(filePreviewConfigFromParsedCommand(cfg), stdout)
		}
		if cfg.ContentMatchList {
			err := runInternalContentMatchList(contentMatchListConfigFromParsedCommand(cfg), stdout)
			if err != nil && reloadWasCancelled() {
				return nil
			}
			return err
		}
		if cfg.SnippetBoundaryPreview {
			err := runInternalSnippetBoundaryPreview(cfg.BoundarySourcePath, cfg.BoundaryKey, stdout)
			if err != nil && reloadWasCancelled() {
				return nil
			}
			return err
		}
		if cfg.RecentPreview {
			return runInternalRecentPreview(recentPreviewConfigFromParsedCommand(cfg), stdout)
		}
		if cfg.SinkTogglePath != "" {
			return runInternalSinkToggle(cfg.SinkTogglePath, stdout)
		}
		if cfg.SinkPreviewModePath != "" {
			return runInternalSinkPreview(cfg.SinkPreviewModePath, cfg.SinkPreviewOutputPath, cfg.SinkPreviewTreePath, stdout)
		}

		colors := activeColorPalette()
		started := time.Now()
		resolvedInvocation := resolvedInvocationFromParsedCommand(cfg)
		invocationCfg := resolvedInvocation.Config
		emitCfg := emitConfigFromParsedCommand(cfg)
		renderCfg := renderConfigFromParsedCommand(cfg)
		gitCtx := detectGitContext(invocationCfg.WorkingDir)
		if proceed, err := warnDirectoryPatternSemantics(stderr, colors); err != nil {
			return err
		} else if !proceed {
			return nil
		}

		commandScopes := resolvedInvocation.Scopes
		if cfg.Verbose {
			fmt.Fprintf(stderr, "[verbose] parsed %d scope(s)\n", len(commandScopes))
			for i, s := range commandScopes {
				fmt.Fprintf(stderr, "[verbose] scope %d: %s\n", i+1, formatScopeSummary(s))
			}
		}
		var (
			discoveryResult DiscoveryResult
			outputPlan      outputPlan
		)
		if prepared != nil {
			gitCtx = prepared.Git
			discoveryResult = prepared.Discovery
			outputPlan = prepared.Plan
		} else {
			discoverySpinnerStop := func() {}
			if !cfg.Quiet {
				discoverySpinnerStop = startLoadingSpinner(spinnerOutputFile(stderr), "Scanning files...")
			}
			var err error
			discoveryResult, err = discoverInvocation(resolvedInvocation, gitCtx, stderr, colors)
			if err != nil {
				discoverySpinnerStop()
				return err
			}
			discoverySpinnerStop()
			outputPlan, err = buildOutputPlanForDiscoveredInvocation(gitCtx, discoveryResult.Invocation)
			if err != nil {
				return err
			}
		}
		if cfg.Verbose {
			for _, stat := range discoveryResult.ScopeStats {
				fmt.Fprintf(stderr, "[verbose] scope %d: discovered %d file(s) in %s\n", stat.Index+1, stat.Count, formatDuration(stat.Duration))
			}
		}
		diagnostics := make([]diagnostic, 0, len(cfg.Warnings)+len(discoveryResult.Diagnostics))
		for _, warning := range cfg.Warnings {
			diagnostics = append(diagnostics, diagnostic{message: warning})
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
		if cfg.TreePayload {
			return executeTreePayload(outputCtx, outputState)
		}
		return executePlanOutput(outputCtx, outputState)
	default:
		return fmt.Errorf("unknown action %q", cfg.Action)
	}
}

func shouldSeparateStdoutPayload(emitCfg emitConfig, invocationCfg invocationConfig, stdout, stderr io.Writer) bool {
	if emitCfg.OutputMode != outputModeStdout || invocationCfg.Quiet {
		return false
	}
	stdoutFile := spinnerOutputFile(stdout)
	if stdoutFile == nil || !isTerminalFile(stdoutFile) {
		return false
	}
	if stderrFile := spinnerOutputFile(stderr); stderrFile != nil && !isTerminalFile(stderrFile) {
		return false
	}
	return true
}

func collectVerboseOutputMetrics(verbose bool, gitCtx gitContext, plan outputPlan) (verboseOutputMetrics, error) {
	if !verbose {
		return verboseOutputMetrics{}, nil
	}

	metrics := verboseOutputMetrics{}
	if !gitCtx.Enabled || len(plan.items) == 0 {
		return metrics, nil
	}

	trackedLines, err := runGitLines(gitCtx.Root, nil, "ls-files")
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

	changedLines, err := collectChangedRepoPaths(gitCtx, executionScope{})
	if err != nil {
		return verboseOutputMetrics{}, err
	}
	changed := make(map[string]struct{}, len(changedLines))
	for _, repoPath := range changedLines {
		changed[normalizeRelPath(repoPath)] = struct{}{}
	}

	for _, item := range plan.items {
		entry := item.entry
		repoPath := normalizeRelPath(gitCtx.toRepoPath(entry.RelPath))
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

func writeVerboseOutputMetrics(w io.Writer, metrics verboseOutputMetrics, emitStats emitStats, fileCount int, outputDuration time.Duration) {
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
	var validation validationFailure
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

func activeColorPalette() colorPalette {
	return activeColorPaletteForWriter(os.Stderr)
}

func activeColorPaletteForWriter(w io.Writer) colorPalette {
	file, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" || !isTerminalFile(file) {
		return colorPalette{}
	}
	return ansiColorPalette()
}

func ansiColorPalette() colorPalette {
	return colorPalette{
		Reset:  "\033[0m",
		Bold:   "\033[1m",
		Dim:    "\033[2m",
		OK:     "\033[32m",
		Err:    "\033[31m",
		Warn:   "\033[33m",
		Dir:    "\033[1;34m",
		Label:  "\033[90m",
		Tree:   "\033[90m",
		Prompt: "\033[36m",
		Git:    "\033[35m",
	}
}

type hissConfig struct {
	Quiet bool
	Yes   bool
}

func hissConfigFromParsedCommand(cfg parsedCommand) hissConfig {
	return hissConfig{
		Quiet: cfg.Quiet,
		Yes:   cfg.Yes,
	}
}

func runEditHiss(cfg hissConfig, stderr io.Writer) error {
	path, err := ensureGlobalHiss()
	if err != nil {
		return err
	}

	editor, err := resolveEditorCommand()
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
	path, err := ensureGlobalHiss()
	if err != nil {
		return err
	}

	shouldReset := cfg.Yes
	if !shouldReset {
		if !cfg.Quiet {
			fmt.Fprintf(stderr, "This will overwrite %s with defaults.\n", path)
		}
		shouldReset, err = promptYesNo("Are you sure? [y/N]", false, stderr)
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

	if err := os.WriteFile(path, []byte(defaultHissContents), 0o644); err != nil {
		return err
	}
	if !cfg.Quiet {
		fmt.Fprintln(stderr, "Configuration restored.")
	}
	return nil
}

type internalCommandConfig struct {
	TreePayload            bool
	TreePreview            bool
	PrediscoveredPath      string
	FilePreview            bool
	ContentMatchList       bool
	SnippetBoundaryPreview bool
	RecentPreview          bool
	LinesPreview           bool
	SinkTogglePath         string
	SinkPreviewModePath    string
}

func internalCommandConfigFromParsedCommand(cfg parsedCommand) internalCommandConfig {
	return internalCommandConfig{
		TreePayload:            cfg.TreePayload,
		TreePreview:            cfg.TreePreview,
		PrediscoveredPath:      cfg.PrediscoveredPath,
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
	return cfg.TreePayload || cfg.TreePreview || cfg.FilePreview ||
		cfg.ContentMatchList || cfg.SnippetBoundaryPreview || cfg.RecentPreview ||
		cfg.LinesPreview ||
		cfg.PrediscoveredPath != "" ||
		cfg.SinkTogglePath != "" || cfg.SinkPreviewModePath != ""
}

type editorCommand struct {
	Path string
	Args []string
}

func resolveEditorCommand() (editorCommand, error) {
	return resolveEditorCommandForGOOS(runtime.GOOS, os.Getenv("VISUAL"), os.Getenv("EDITOR"), exec.LookPath)
}

func promptYesNo(prompt string, defaultYes bool, stderr io.Writer) (bool, error) {
	defaultResponse := "n"
	if defaultYes {
		defaultResponse = "y"
	}

	response, ok, err := readPromptResponse(prompt, stderr)
	if err != nil {
		return false, err
	}
	if ok {
		response = strings.TrimSpace(strings.ToLower(response))
		if response == "" {
			response = defaultResponse
		}
		return response == "y" || response == "yes", nil
	}
	return defaultYes, nil
}

func canPromptInteractively() bool {
	if isTerminalFile(os.Stdin) {
		return true
	}
	ttyIn, ttyOut, err := openPromptTTY()
	if err != nil {
		return false
	}
	closePromptTTY(ttyIn, ttyOut)
	return true
}

func pushHeadlessPromptGuard(enabled bool) func() {
	if !enabled {
		return func() {}
	}
	headlessPromptGuardCount.Add(1)
	return func() {
		headlessPromptGuardCount.Add(-1)
	}
}

func headlessPromptGuardActive() bool {
	return headlessPromptGuardCount.Load() > 0
}

func headlessPromptBugError() error {
	return fmt.Errorf("BUG: reached interactive prompt in headless mode (--headless or --internal-*).\n  This is a catclip bug; please report it.")
}

func readPromptResponse(prompt string, stderr io.Writer) (string, bool, error) {
	if headlessPromptGuardActive() {
		return "", false, headlessPromptBugError()
	}
	if isTerminalFile(os.Stdin) {
		response, ok := readPromptKey(os.Stdin, stderr, prompt)
		return response, ok, nil
	}

	ttyIn, ttyOut, err := openPromptTTY()
	if err != nil {
		return "", false, nil
	}
	defer closePromptTTY(ttyIn, ttyOut)

	response, ok := readPromptKey(ttyIn, ttyOut, prompt)
	return response, ok, nil
}

func readLineResponse(prompt string, stderr io.Writer) (string, bool, error) {
	if headlessPromptGuardActive() {
		return "", false, headlessPromptBugError()
	}
	if isTerminalFile(os.Stdin) {
		response, ok := readPromptLine(os.Stdin, stderr, prompt)
		return response, ok, nil
	}

	ttyIn, ttyOut, err := openPromptTTY()
	if err != nil {
		return "", false, nil
	}
	defer closePromptTTY(ttyIn, ttyOut)

	response, ok := readPromptLine(ttyIn, ttyOut, prompt)
	return response, ok, nil
}

func readPromptKey(input *os.File, output io.Writer, prompt string) (string, bool) {
	if response, ok := readPromptByte(input, output, prompt); ok {
		return response, true
	}
	return readPromptLine(input, output, prompt)
}

func readPromptLine(input *os.File, output io.Writer, prompt string) (string, bool) {
	if _, err := fmt.Fprintf(output, "%s ", prompt); err != nil {
		return "", false
	}
	var response string
	_, scanErr := fmt.Fscanln(input, &response)
	if scanErr != nil && !errors.Is(scanErr, io.EOF) {
		return "", false
	}
	if errors.Is(scanErr, io.EOF) {
		return "", true
	}
	return response, true
}

func validateImplementedFeatureSet(cfg internalCommandConfig) error {
	if cfg.PrediscoveredPath != "" && !cfg.TreePayload && !cfg.TreePreview && !cfg.ContentMatchList && !cfg.LinesPreview && !cfg.FilePreview {
		return newUsageError("Error: --internal-prediscovered requires --internal-tree-payload, --internal-tree-preview, --internal-content-match-list, --internal-lines-preview, or --internal-file-preview.")
	}
	if cfg.TreePreview && cfg.PrediscoveredPath == "" {
		return newUsageError("Error: --internal-tree-preview requires --internal-prediscovered.")
	}
	if cfg.LinesPreview && cfg.PrediscoveredPath == "" {
		return newUsageError("Error: --internal-lines-preview requires --internal-prediscovered.")
	}
	return nil
}

func validateRawOutputPlan(cfg emitConfig, plan outputPlan) error {
	if !cfg.Raw {
		return nil
	}
	for _, item := range plan.items {
		if item.kind != outputSectionKindFiles {
			return newUsageError("Error: --raw cannot be combined with --paths.")
		}
		switch item.mode {
		case entryModeSnippet:
			return newUsageError("Error: --raw cannot be combined with snippet output.\n  Snippets can emit multiple regex-derived ranges per file; <file ... lines=\"...\"> wrappers keep those ranges attributable.\n  Omit --raw, or use --lines with --raw when you want one explicit raw line slice.")
		case entryModeDiff:
			return newUsageError("Error: --raw cannot be combined with diff output.")
		case entryModeFull, entryModeLines:
			// OK
		default:
			return newUsageError("Error: --raw cannot be combined with this output kind.")
		}
	}
	return nil
}

func invocationConfigFromParsedCommand(cfg parsedCommand) invocationConfig {
	return invocationConfig{
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

func resolvedInvocationFromParsedCommand(cfg parsedCommand) resolvedInvocation {
	return resolvedInvocation{
		Config: invocationConfigFromParsedCommand(cfg),
		Scopes: executionScopesFromCommandSpec(cfg.Command),
	}
}

func emitConfigFromParsedCommand(cfg parsedCommand) emitConfig {
	return emitConfig{
		OutputMode: cfg.OutputMode,
		Raw:        cfg.Raw,
		NoBundle:   cfg.NoBundle,
	}
}

func emitEnvironmentFromInvocationConfig(cfg invocationConfig) emitEnvironment {
	return emitEnvironment{
		Platform:   cfg.Platform,
		WorkingDir: cfg.WorkingDir,
	}
}

func parseArgs(args []string) (parsedCommand, error) {
	return parseArgsWithMode(args, false)
}

// parseArgsAllowImplicitDot is for internal inspection (startup picker, etc.)
// where it's OK to default the scope target to "." when none is provided.
// The CLI entry point uses parseArgs (strict) to require explicit targets.
func parseArgsAllowImplicitDot(args []string) (parsedCommand, error) {
	return parseArgsWithMode(args, true)
}

func parseArgsWithMode(args []string, allowImplicitDotScope bool) (parsedCommand, error) {
	wd, err := os.Getwd()
	if err != nil {
		return parsedCommand{}, err
	}

	cfg := parsedCommand{
		Action:     actionRun,
		Version:    loadVersion(),
		Platform:   detectPlatform(),
		WorkingDir: wd,
		OutputMode: outputModeClipboard,
	}

	executionScopes := make([]executionScope, 0, 2)
	var current executionScopeBuilder
	inModifierMode := false
	lastNoValueModifier := ""
	var stdinCache stdinPathCache

	finalize := func() error {
		if !current.hasContent() {
			current = executionScopeBuilder{}
			return nil
		}

		s := current.executionScope
		mode := executionScopeOutputMode(s)
		if s.Staged || s.Unstaged || s.Untracked {
			s.Changed = true
		}
		hasChangeSelector := executionScopeHasGitSelection(s)

		if mode == entryModeDiff && s.Untracked {
			return untrackedDiffError()
		}
		if mode == entryModeSnippet && s.SnippetPattern == "" && !cfg.FilePreview && !cfg.ContentMatchList {
			return requiredStageValueError("--snippet")
		}
		if mode == entryModeDiff && !hasChangeSelector {
			return missingDiffSelectorError()
		}
		if err := validateScopeStageOrder(current.Stages); err != nil {
			return err
		}
		if cfg.WithBinaries {
			if err := validateWithBinariesCompatibility(s); err != nil {
				return err
			}
		}
		if len(s.Targets) == 0 {
			if cfg.TreePayload && cfg.PrediscoveredPath == "" {
				return newUsageError("Error: --internal-tree-payload requires an explicit target.\n  Use: catclip TARGET --internal-tree-payload")
			}
			if cfg.Headless {
				return newUsageError("Error: --headless requires explicit targets.\n  Pass a path, --include, or another scope-defining flag.\n  Example: catclip . --headless")
			}
			if internalCommandConfigFromParsedCommand(cfg).isInternalKind() || cfg.Action != actionRun || allowImplicitDotScope {
				s.Targets = []string{"."}
			} else {
				return newUsageError("Error: no target specified.\n  Pass an explicit target — use '.' for the current directory.\n  Example: catclip . --paths")
			}
		}

		executionScopes = append(executionScopes, s)
		current = executionScopeBuilder{}
		inModifierMode = false
		lastNoValueModifier = ""
		return nil
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch arg {
		case "-h", "--help":
			cfg.Action = actionHelp
			return cfg, nil
		case "--help-all":
			cfg.Action = actionHelpAll
			return cfg, nil
		case "--version", "-V":
			cfg.Action = actionVersion
			return cfg, nil
		case "--hiss":
			cfg.Action = actionEditHiss
		case "--hiss-reset":
			cfg.Action = actionResetHiss
		case "--all-ignore-rules":
			cfg.Action = actionListIgnoreRules
		case "-v", "--verbose":
			cfg.Verbose = true
		case "-q", "--quiet":
			cfg.Quiet = true
		case "-y", "--yes":
			cfg.Yes = true
		case "-p", "--print":
			cfg.OutputMode = outputModeStdout
		case "--headless":
			cfg.Headless = true
		case "-r", "--raw":
			cfg.Raw = true
		case "--with-binaries":
			cfg.WithBinaries = true
		case "-t", "--no-tree":
			cfg.NoTree = true
		case "--no-bundle":
			cfg.NoBundle = true
		case "--preview":
			cfg.Preview = true
		case "--internal-tree-payload":
			cfg.TreePayload = true
		case "--internal-tree-preview":
			cfg.TreePreview = true
		case "--internal-prediscovered":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-prediscovered requires a checkpoint path.")
			}
			i++
			cfg.PrediscoveredPath = args[i]
		case "--internal-tree-target":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-tree-target requires a path.")
			}
			i++
			cfg.TreeTarget = args[i]
		case "--internal-tree-kind":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-tree-kind requires a value.")
			}
			i++
			cfg.TreeKind = args[i]
		case "--internal-tree-state":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-tree-state requires a value.")
			}
			i++
			cfg.TreeState = args[i]
		case "--internal-file-preview":
			cfg.FilePreview = true
		case "--internal-file-path":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-file-path requires a path.")
			}
			i++
			cfg.FilePath = args[i]
		case "--internal-content-match-list":
			cfg.ContentMatchList = true
		case "--internal-snippet-boundary-preview":
			cfg.SnippetBoundaryPreview = true
		case "--internal-boundary-source":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-boundary-source requires a path.")
			}
			i++
			cfg.BoundarySourcePath = args[i]
		case "--internal-boundary-key":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-boundary-key requires a value.")
			}
			i++
			cfg.BoundaryKey = args[i]
		case "--internal-lines-preview":
			cfg.LinesPreview = true
		case "--internal-recent-preview":
			cfg.RecentPreview = true
		case "--internal-recent-data":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-recent-data requires a path.")
			}
			i++
			cfg.RecentData = args[i]
		case "--internal-sink-toggle":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-sink-toggle requires a path.")
			}
			i++
			cfg.SinkTogglePath = args[i]
		case "--internal-sink-preview":
			if i+3 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-sink-preview requires mode, output, and tree paths.")
			}
			cfg.SinkPreviewModePath = args[i+1]
			cfg.SinkPreviewOutputPath = args[i+2]
			cfg.SinkPreviewTreePath = args[i+3]
			i += 3
		case "--internal-recent-selection":
			if i+1 >= len(args) {
				return parsedCommand{}, newUsageError("Error: --internal-recent-selection requires a value.")
			}
			i++
			cfg.RecentSelect = args[i]
		case "--changed":
			inModifierMode = true
			current.Changed = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageChanged})
			lastNoValueModifier = arg
		case "--staged":
			inModifierMode = true
			current.Staged = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageStaged})
			lastNoValueModifier = arg
		case "--unstaged":
			inModifierMode = true
			current.Unstaged = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageUnstaged})
			lastNoValueModifier = arg
		case "--untracked":
			inModifierMode = true
			current.Untracked = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageUntracked})
			lastNoValueModifier = arg
		case "--changed-diff":
			inModifierMode = true
			current.Changed = true
			current.Diff = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageChangedDiff})
			lastNoValueModifier = arg
		case "--staged-diff":
			inModifierMode = true
			current.Staged = true
			current.Diff = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageStagedDiff})
			lastNoValueModifier = arg
		case "--unstaged-diff":
			inModifierMode = true
			current.Unstaged = true
			current.Diff = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageUnstagedDiff})
			lastNoValueModifier = arg
		case "--lines":
			inModifierMode = true
			current.Lines = true
			var start, end int
			if i+1 < len(args) {
				next := args[i+1]
				if n, err := strconv.Atoi(next); err == nil {
					if n < 1 {
						return parsedCommand{}, newUsageError("Error: --lines start must be >= 1 (got %d).\n  Line numbers are 1-based, matching editors and compiler output.", n)
					}
					start = n
					i++
					if i+1 < len(args) {
						next2 := args[i+1]
						if n2, err := strconv.Atoi(next2); err == nil {
							if n2 < start {
								return parsedCommand{}, newUsageError("Error: --lines end (%d) must be >= start (%d).\n  Use: --lines START END where END >= START.", n2, start)
							}
							end = n2
							i++
						} else if next2 == "EOF" {
							// Sentinel from the interactive end-line picker preview command.
							// Behaves identically to an open-ended --lines START.
							i++
						} else if !strings.HasPrefix(next2, "-") {
							return parsedCommand{}, newUsageError("Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got %q).", next2)
						}
					}
				} else if !strings.HasPrefix(next, "-") {
					return parsedCommand{}, newUsageError("Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got %q).", next)
				}
			}
			current.LinesStart = start
			current.LinesEnd = end
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageLines})
			lastNoValueModifier = arg
		case "--snippet":
			inModifierMode = true
			if i+1 >= len(args) {
				return parsedCommand{}, requiredStageValueError("--snippet")
			}
			i++
			regex := args[i]
			current.SnippetPattern = regex
			current.Snippet = true
			// Optional fixed-context number after the regex (rg/grep -C N style).
			// Only an integer is consumed here; a non-integer token falls through
			// to the loop and errors as a stray positional after a modifier.
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					if n < 0 || n > snippetContextMax {
						return parsedCommand{}, newUsageError("Error: --snippet context must be between 0 and %d (got %d).\n  Use: --snippet 'REGEX' N for N lines around each match (0 = matching line only).", snippetContextMax, n)
					}
					i++
					current.SnippetContextSet = true
					current.SnippetContextLines = n
				}
			}
			// A bare token after the regex (and optional N) is almost always an
			// unquoted regex with spaces the shell split; give a quote hint
			// instead of the generic positional-after-modifier error.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return parsedCommand{}, regexModifierExtraValueError("--snippet", regex, args[i+1])
			}
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageSnippet, Values: []string{regex}})
			lastNoValueModifier = ""
		case "--then":
			if err := finalize(); err != nil {
				return parsedCommand{}, err
			}
		case "--":
			if i != len(args)-1 {
				return parsedCommand{}, bareModifierPlaceholderOrderError()
			}
			if cfg.Headless {
				return parsedCommand{}, bareModifierPlaceholderHeadlessModeError()
			}
			return parsedCommand{}, bareModifierPlaceholderInteractiveOnlyError()
		case "--only":
			inModifierMode = true
			values, next, exactValues, err := consumePathModifierValues(args, i+1, "--only", &stdinCache)
			if err != nil {
				return parsedCommand{}, err
			}
			if len(values) == 0 {
				return parsedCommand{}, requiredStageValueError("--only")
			}
			current.Only = append(current.Only, values...)
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageOnly, Values: append([]string(nil), values...), ExactValues: exactValues})
			i = next - 1
			lastNoValueModifier = ""
		case "--include":
			inModifierMode = true
			values, next, exactValues, err := consumePathModifierValues(args, i+1, "--include", &stdinCache)
			if err != nil {
				return parsedCommand{}, err
			}
			if len(values) == 0 {
				return parsedCommand{}, requiredStageValueError("--include")
			}
			if err := validateIncludeValues(values); err != nil {
				return parsedCommand{}, err
			}
			current.IncludedTargets = append(current.IncludedTargets, values...)
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageInclude, Values: append([]string(nil), values...), ExactValues: exactValues})
			i = next - 1
			lastNoValueModifier = ""
		case "--exclude":
			inModifierMode = true
			values, next, exactValues, err := consumePathModifierValues(args, i+1, "--exclude", &stdinCache)
			if err != nil {
				return parsedCommand{}, err
			}
			if len(values) == 0 {
				return parsedCommand{}, requiredStageValueError("--exclude")
			}
			current.Exclude = append(current.Exclude, values...)
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageExclude, Values: append([]string(nil), values...), ExactValues: exactValues})
			i = next - 1
			lastNoValueModifier = ""
		case "--recent":
			inModifierMode = true
			limit, next, err := consumeOptionalRecentLimit(args, i+1)
			if err != nil {
				return parsedCommand{}, err
			}
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageRecent, Limit: limit})
			i = next - 1
			lastNoValueModifier = ""
		case "--depth":
			inModifierMode = true
			if i+1 >= len(args) {
				return parsedCommand{}, requiredStageValueError("--depth")
			}
			i++
			depth, err := parseDepthToken(args[i])
			if err != nil {
				return parsedCommand{}, err
			}
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageDepth, Limit: intPtr(depth)})
			lastNoValueModifier = ""
		case "--contains":
			inModifierMode = true
			if i+1 >= len(args) {
				return parsedCommand{}, containsMissingPatternError(args, i)
			}
			i++
			current.Contains = args[i]
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return parsedCommand{}, regexModifierExtraValueError("--contains", current.Contains, args[i+1])
			}
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageContains, Values: []string{args[i]}})
			if looksLikeGlobConfusion(current.Contains) {
				cfg.Warnings = append(cfg.Warnings, "Warning: --contains uses regex, not globs. Did you mean '.*' instead of '*'?\n  Example: --contains 'use.*Context' (not 'use*Context')")
			}
			lastNoValueModifier = ""
		case "--paths":
			inModifierMode = true
			current.Paths = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStagePaths})
			lastNoValueModifier = arg
		default:
			switch {
			case strings.HasPrefix(arg, "--contains="):
				return parsedCommand{}, newUsageError("Error: --contains requires a space before the pattern.\n  Use: catclip src --contains 'pattern'\n  Not: catclip src --contains='pattern'")
			case strings.HasPrefix(arg, "--snippet="):
				return parsedCommand{}, newUsageError("Error: --snippet requires a space before the pattern.\n  Use: catclip src --snippet 'pattern'\n  Not: catclip src --snippet='pattern'")
			case strings.HasPrefix(arg, "--recent="):
				return parsedCommand{}, newUsageError("Error: --recent requires a space before the value.\n  Use: catclip src --recent 5\n  Or:  catclip src --recent")
			case strings.HasPrefix(arg, "--depth="):
				return parsedCommand{}, newUsageError("Error: --depth requires a space before the value.\n  Use: catclip src --depth 2")
			case arg == "--diff":
				return parsedCommand{}, diffStandaloneError()
			case strings.HasPrefix(arg, "--"):
				return parsedCommand{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			case strings.HasPrefix(arg, "-") && len(arg) > 1:
				return parsedCommand{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			case arg == "-":
				return parsedCommand{}, newUsageError("Error: '-' is not a valid target path.\n  To read file paths from stdin, use it with a modifier:\n    catclip src --exclude -\n    catclip src --only -")
			default:
				if inModifierMode {
					if lastNoValueModifier != "" {
						return parsedCommand{}, noValueModifierError(lastNoValueModifier)
					}
					return parsedCommand{}, positionalAfterModifierError()
				}
				current.Targets = append(current.Targets, arg)
				current.explicitTargets++
				lastNoValueModifier = ""
			}
		}
	}

	if err := finalize(); err != nil {
		return parsedCommand{}, err
	}

	if cfg.Headless || internalCommandConfigFromParsedCommand(cfg).isInternalKind() {
		cfg.OutputMode = outputModeStdout
		cfg.Quiet = true
	}

	if len(executionScopes) == 0 {
		if cfg.TreePayload && cfg.PrediscoveredPath == "" {
			return parsedCommand{}, newUsageError("Error: --internal-tree-payload requires an explicit target.\n  Use: catclip TARGET --internal-tree-payload")
		}
		if cfg.Headless {
			return parsedCommand{}, newUsageError("Error: --headless requires explicit targets.\n  Pass a path, --include, or another scope-defining flag.\n  Example: catclip . --headless")
		}
		if internalCommandConfigFromParsedCommand(cfg).isInternalKind() || cfg.Action != actionRun || allowImplicitDotScope {
			executionScopes = append(executionScopes, executionScope{Targets: []string{"."}})
		} else {
			return parsedCommand{}, newUsageError("Error: no target specified.\n  Run catclip from an interactive terminal to pick targets, or pass an explicit target — use '.' for the current directory.\n  Example: catclip .")
		}
	}

	cfg.Command = finalizedCommandSpecFromExecutionScopes(executionScopes)

	return cfg, nil
}

func (b executionScopeBuilder) hasContent() bool {
	return len(b.Targets) > 0 || len(b.IncludedTargets) > 0 || len(b.Only) > 0 || len(b.Exclude) > 0 ||
		len(b.Stages) > 0 ||
		b.Contains != "" || b.Paths || b.SnippetPattern != "" || b.Snippet || b.Changed || b.Staged || b.Unstaged ||
		b.Untracked || b.Diff
}

func splitCommaPatterns(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func consumeModifierValues(args []string, start int) ([]string, int) {
	values := make([]string, 0, len(args)-start)
	i := start
	for i < len(args) {
		if isModifierBoundaryToken(args[i]) {
			break
		}
		values = append(values, args[i])
		i++
	}
	return values, i
}

func consumePathModifierValues(args []string, start int, flag string, cache *stdinPathCache) ([]string, int, bool, error) {
	values, next := consumeModifierValues(args, start)
	if len(values) == 1 && values[0] == "-" {
		paths, err := readModifierPathsFromStdin(flag, cache)
		if err != nil {
			return nil, 0, false, err
		}
		return paths, next, true, nil
	}
	return values, next, false, nil
}

func validateIncludeValues(values []string) error {
	for _, value := range values {
		if value == "*" {
			continue
		}
		if isAbsolutePathForValidation(value) {
			return newUsageError("Error: --include does not accept absolute paths: %s\n  Use a relative path inside the current target scope.", singleQuoted(value))
		}
		if containsParentTraversal(value) {
			return newUsageError("Error: --include cannot traverse above the current target scope: %s\n  Use a relative path inside the current target scope.", singleQuoted(value))
		}
		if hasGlobChars(value) {
			return newUsageError("Error: --include does not accept glob patterns. Use literal paths or '*' to include all.")
		}
	}
	return nil
}

func isAbsolutePathForValidation(value string) bool {
	if path.IsAbs(value) || filepath.IsAbs(value) {
		return true
	}
	if strings.HasPrefix(value, `\\`) {
		return true
	}
	if len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\') {
		drive := value[0]
		return (drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')
	}
	return false
}

func readModifierPathsFromStdin(flag string, cache *stdinPathCache) ([]string, error) {
	if cache.loaded {
		if flag == "--include" {
			if err := validateIncludeValues(cache.rawPaths); err != nil {
				return nil, err
			}
		}
		return append([]string(nil), cache.paths...), nil
	}
	if isTerminalFile(os.Stdin) {
		return nil, newUsageError("Error: %s - reads paths from stdin, but no input is being piped.\n  Pipe paths from another command:\n    catclip src --contains \"pattern\" --paths | catclip src %s -", flag, flag)
	}
	rawPaths, paths, err := readStdinPathValues(os.Stdin)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, newUsageError("Error: %s - received no paths from stdin.\n  The piped command produced no output.", flag)
	}
	if flag == "--include" {
		if err := validateIncludeValues(rawPaths); err != nil {
			return nil, err
		}
	}
	cache.loaded = true
	cache.paths = append([]string(nil), paths...)
	cache.rawPaths = append([]string(nil), rawPaths...)
	return append([]string(nil), cache.paths...), nil
}

func readNormalizedStdinPaths(stdin io.Reader) ([]string, error) {
	_, paths, err := readStdinPathValues(stdin)
	return paths, err
}

func readStdinPathValues(stdin io.Reader) ([]string, []string, error) {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	rawPaths := make([]string, 0, 64)
	paths := make([]string, 0, 64)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		normalized := normalizeRelPath(line)
		if normalized == "" {
			continue
		}
		rawPaths = append(rawPaths, line)
		paths = append(paths, normalized)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return rawPaths, paths, nil
}

func warnDirectoryPatternSemantics(stderr io.Writer, colors colorPalette) (bool, error) {
	return true, nil
}

func hasGlobChars(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func looksLikeDirectoryPattern(workingDir, pattern string, ignoredDirs map[string]struct{}) bool {
	if pattern == "" || strings.HasSuffix(pattern, "/") || hasGlobChars(pattern) {
		return false
	}

	if info, err := os.Stat(filepath.Join(workingDir, filepath.FromSlash(pattern))); err == nil && info.IsDir() {
		return true
	}
	if _, ok := ignoredDirs[pattern]; ok {
		return true
	}

	if strings.Contains(pattern, "/") || strings.Contains(pattern, ".") {
		return false
	}
	for i, r := range pattern {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		if i == 0 && r >= 'A' && r <= 'Z' {
			continue
		}
		return false
	}
	return pattern != ""
}

func validateWithBinariesCompatibility(s executionScope) error {
	if s.Contains != "" {
		return newUsageError("Error: --with-binaries cannot be combined with --contains.\n  --contains searches file content, which is not meaningful for binary files.")
	}
	if s.Snippet {
		return newUsageError("Error: --with-binaries cannot be combined with --snippet.\n  --snippet extracts content blocks, which is not meaningful for binary files.")
	}
	if s.Diff {
		return newUsageError("Error: --with-binaries cannot be combined with diff output.\n  Diff output operates on file content, which is not meaningful for binary files.")
	}
	return nil
}

func looksLikeGlobConfusion(pattern string) bool {
	return strings.Contains(pattern, "*") &&
		!strings.Contains(pattern, ".") &&
		!strings.Contains(pattern, "[") &&
		!strings.Contains(pattern, "|") &&
		!strings.Contains(pattern, "\\")
}

func formatScopeSummary(s executionScope) string {
	parts := []string{
		fmt.Sprintf("targets=%q", s.Targets),
		fmt.Sprintf("included=%q", s.IncludedTargets),
		fmt.Sprintf("only=%q", s.Only),
		fmt.Sprintf("exclude=%q", s.Exclude),
	}
	if s.Contains != "" {
		parts = append(parts, fmt.Sprintf("contains=%q", s.Contains))
	}
	if s.Snippet {
		if s.SnippetPattern != "" {
			parts = append(parts, fmt.Sprintf("snippet=%q", s.SnippetPattern))
		} else {
			parts = append(parts, "snippet=true")
		}
	}
	if s.Paths {
		parts = append(parts, "paths=true")
	}
	if s.Changed {
		parts = append(parts, "changed=true")
	}
	if s.Staged {
		parts = append(parts, "staged=true")
	}
	if s.Unstaged {
		parts = append(parts, "unstaged=true")
	}
	if s.Untracked {
		parts = append(parts, "untracked=true")
	}
	if s.Diff {
		parts = append(parts, "diff=true")
	}
	for _, stage := range s.Stages {
		switch stage.Kind {
		case scopeStageRecent:
			if stage.Limit == nil {
				parts = append(parts, "recent=true")
				continue
			}
			parts = append(parts, fmt.Sprintf("recent=%d", *stage.Limit))
		case scopeStageDepth:
			if stage.Limit == nil {
				continue
			}
			parts = append(parts, fmt.Sprintf("depth=%d", *stage.Limit))
		case scopeStagePaths:
			parts = append(parts, "paths_stage=true")
		}
	}
	return strings.Join(parts, " ")
}

func commandScopesUseRecentStage(scopes []commandScopeSpec) bool {
	for _, s := range scopes {
		for _, stage := range s.Stages() {
			if stage.Kind == scopeStageRecent {
				return true
			}
		}
	}
	return false
}

func commandScopesUsePathsStage(scopes []commandScopeSpec) bool {
	for _, s := range scopes {
		if s.HasPathsOutput() {
			return true
		}
	}
	return false
}
