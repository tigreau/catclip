package catclip

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type verboseOutputMetrics struct {
	PayloadBytes           int64
	CleanTrackedCount      int
	ModifiedUntrackedCount int
	GitStateKnown          bool
	GitStateNote           string
}

func run(cfg runConfig, stdout, stderr io.Writer) error {
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
		return runEditHiss(cfg, stderr)
	case actionResetHiss:
		return runResetHiss(cfg, stderr)
	case actionRun:
		if err := validateImplementedFeatureSet(cfg); err != nil {
			return err
		}
		if cfg.FilePreview {
			return runInternalFilePreview(cfg, stdout)
		}
		if cfg.ContentMatchList {
			return runInternalContentMatchList(cfg, stdout)
		}
		if cfg.RecentPreview {
			return runInternalRecentPreview(cfg, stdout)
		}

		colors := activeColorPalette()
		started := time.Now()
		gitCtx := detectGitContext(cfg.WorkingDir)
		baseRules, err := loadIgnoreRules()
		if err != nil {
			return err
		}
		if proceed, err := warnDirectoryPatternSemantics(cfg, baseRules, stderr, colors); err != nil {
			return err
		} else if !proceed {
			return nil
		}

		commandScopes := configCommandScopes(cfg)
		if cfg.Verbose {
			fmt.Fprintf(stderr, "[verbose] parsed %d scope(s)\n", len(commandScopes))
			for i, s := range commandScopes {
				fmt.Fprintf(stderr, "[verbose] scope %d: %s\n", i+1, formatScopeSummary(executionScopeFromCommandScopeSpec(s)))
			}
		}
		discoverySpinnerStop := func() {}
		if !cfg.Quiet {
			discoverySpinnerStop = startLoadingSpinner(spinnerOutputFile(stderr), "Scanning files...")
		}
		var allEntries []fileEntry
		diagnostics := make([]diagnostic, 0, len(cfg.Warnings))
		for _, warning := range cfg.Warnings {
			diagnostics = append(diagnostics, diagnostic{message: warning})
		}
		var notices []string
		hadSelectionCancel := false
		for i, scopeSpec := range commandScopes {
			scopeStarted := time.Now()
			s := executionScopeFromCommandScopeSpec(scopeSpec)
			entries, scopeDiagnostics, scopeNotices, scopeSelectionCancel, err := evaluateScope(cfg, gitCtx, i, s, baseRules, stderr, colors)
			if err != nil {
				return err
			}
			if cfg.Verbose {
				fmt.Fprintf(stderr, "[verbose] scope %d: discovered %d file(s) in %s\n", i+1, len(entries), formatDuration(time.Since(scopeStarted)))
			}
			allEntries = append(allEntries, entries...)
			diagnostics = append(diagnostics, scopeDiagnostics...)
			notices = append(notices, scopeNotices...)
			hadSelectionCancel = hadSelectionCancel || scopeSelectionCancel
		}
		discoverySpinnerStop()
		if commandScopesUseRecentStage(commandScopes) {
			allEntries = dedupeEntriesByPathPreserveOrder(allEntries)
		} else {
			allEntries = dedupeEntriesByPath(allEntries)
		}
		if cfg.TreePayload {
			hadErrorDiagnostic := false
			for _, diag := range diagnostics {
				if !diag.isError {
					continue
				}
				hadErrorDiagnostic = true
				fmt.Fprintln(stderr, diag.message)
			}
			if hadErrorDiagnostic {
				return newExitError(1, "")
			}
			if len(allEntries) == 0 {
				doc, ok := buildEmptyTreeDocument(cfg)
				if !ok {
					if err := writeNoFilesMatchedMessage(cfg, stderr, colors, hadSelectionCancel); err != nil {
						return err
					}
					return newExitError(1, "")
				}
				return encodeTreePayload(stdout, doc)
			}
			reportStarted := time.Now()
			outputPlan, err := prepareOutputPlan(gitCtx, allEntries)
			if err != nil {
				return err
			}
			if len(outputPlan.items) == 0 {
				doc, ok := buildEmptyTreeDocument(cfg)
				if !ok {
					if err := writeNoFilesMatchedMessage(cfg, stderr, colors, hadSelectionCancel); err != nil {
						return err
					}
					return newExitError(1, "")
				}
				return encodeTreePayload(stdout, doc)
			}
			report, err := buildOutputReportForPlan(cfg, gitCtx, outputPlan, dedupePreserveOrder(notices))
			if err != nil {
				return err
			}
			if cfg.Verbose {
				fmt.Fprintf(stderr, "[verbose] report: %s\n", formatDuration(time.Since(reportStarted)))
			}
			return encodeTreePayload(stdout, buildTreeDocumentFromPreview(cfg, outputPlan, report))
		}
		for _, diag := range diagnostics {
			if diag.isError || !cfg.Quiet {
				fmt.Fprintln(stderr, diag.message)
			}
		}
		if len(allEntries) == 0 {
			if !cfg.Quiet {
				for _, notice := range dedupePreserveOrder(notices) {
					fmt.Fprintln(stderr, notice)
				}
			}
			if err := writeNoFilesMatchedMessage(cfg, stderr, colors, hadSelectionCancel); err != nil {
				return err
			}
			return newExitError(1, "")
		}
		reportStarted := time.Now()
		outputPlan, err := prepareOutputPlan(gitCtx, allEntries)
		if err != nil {
			return err
		}
		if len(outputPlan.items) == 0 {
			if !cfg.Quiet {
				for _, notice := range dedupePreserveOrder(notices) {
					fmt.Fprintln(stderr, notice)
				}
			}
			if err := writeNoFilesMatchedMessage(cfg, stderr, colors, hadSelectionCancel); err != nil {
				return err
			}
			return newExitError(1, "")
		}
		report, err := buildOutputReportForPlan(cfg, gitCtx, outputPlan, dedupePreserveOrder(notices))
		if err != nil {
			return err
		}
		if cfg.Verbose {
			fmt.Fprintf(stderr, "[verbose] report: %s\n", formatDuration(time.Since(reportStarted)))
		}
		if cfg.Preview {
			renderStarted := time.Now()
			err := renderPreview(cfg, gitCtx, outputPlan, report, stdout, stderr, colors)
			if err != nil {
				return err
			}
			if cfg.Verbose {
				fmt.Fprintf(stderr, "[verbose] preview: %s\n", formatDuration(time.Since(renderStarted)))
				fmt.Fprintf(stderr, "[verbose] total: %s\n", formatDuration(time.Since(started)))
			}
			return nil
		}
		diagStarted := time.Now()
		proceed, err := writeNormalDiagnostics(cfg, gitCtx, outputPlan, report, stderr, colors)
		if err != nil {
			return err
		}
		if cfg.Verbose {
			fmt.Fprintf(stderr, "[verbose] diagnostics: %s\n", formatDuration(time.Since(diagStarted)))
		}
		if !proceed {
			return nil
		}
		outputMetrics, err := collectVerboseOutputMetrics(cfg.Verbose, gitCtx, outputPlan)
		if err != nil {
			return err
		}
		outputSpinnerStop := func() {}
		if !cfg.Quiet && cfg.OutputMode == outputModeClipboard {
			outputSpinnerStop = startLoadingSpinner(spinnerOutputFile(stderr), outputSpinnerMessage(cfg))
		}
		if shouldSeparateStdoutPayload(cfg, stdout, stderr) {
			fmt.Fprintln(stderr)
		}
		outputStarted := time.Now()
		emitStats, err := emitOutputPlan(cfg, outputPlan, stdout, colors)
		if err != nil {
			outputSpinnerStop()
			return err
		}
		outputSpinnerStop()
		outputMetrics.PayloadBytes = emitStats.PayloadBytes
		if cfg.Verbose {
			outputDuration := time.Since(outputStarted)
			fmt.Fprintf(stderr, "[verbose] output: %s\n", formatDuration(outputDuration))
			writeVerboseOutputMetrics(stderr, outputMetrics, emitStats, len(outputPlan.items), outputDuration)
		}
		if cfg.OutputMode == outputModeClipboard && !cfg.Quiet {
			if err := writeClipboardSuccess(stderr, outputPlan, colors); err != nil {
				return err
			}
		}
		if cfg.Verbose {
			fmt.Fprintf(stderr, "[verbose] total: %s\n", formatDuration(time.Since(started)))
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q", cfg.Action)
	}
}

func shouldSeparateStdoutPayload(cfg runConfig, stdout, stderr io.Writer) bool {
	if cfg.OutputMode != outputModeStdout || cfg.Quiet {
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
		entry := item.unit.Entry
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

	if emitStats.GenerateDuration > 0 {
		fmt.Fprintf(w, "[verbose] emit generate: %s\n", formatDuration(emitStats.GenerateDuration))
	}
	if emitStats.SinkFinalizeDuration > 0 {
		if emitStats.ClipboardWaitDuration > 0 {
			fmt.Fprintf(w, "[verbose] clipboard flush/close (%s): %s\n", emitStats.SinkName, formatDuration(emitStats.SinkFinalizeDuration))
		} else {
			fmt.Fprintf(w, "[verbose] emit flush (%s): %s\n", emitStats.SinkName, formatDuration(emitStats.SinkFinalizeDuration))
		}
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

func (cfg runConfig) HeadlessStdoutMode() bool {
	return cfg.Print && cfg.Quiet
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
		Value:  "\033[1m",
		Tree:   "\033[90m",
		Prompt: "\033[36m",
		Git:    "\033[35m",
	}
}

func runEditHiss(cfg runConfig, stderr io.Writer) error {
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

func runResetHiss(cfg runConfig, stderr io.Writer) error {
	path, err := ensureGlobalHiss()
	if err != nil {
		return err
	}

	shouldReset := cfg.Yes
	if !shouldReset {
		if !cfg.Quiet {
			fmt.Fprintf(stderr, "This will overwrite %s with defaults.\n", path)
		}
		shouldReset = promptYesNo("Are you sure? [y/N]", false, stderr)
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

type editorCommand struct {
	Path string
	Args []string
}

func resolveEditorCommand() (editorCommand, error) {
	return resolveEditorCommandForGOOS(runtime.GOOS, os.Getenv("VISUAL"), os.Getenv("EDITOR"), exec.LookPath)
}

func promptYesNo(prompt string, defaultYes bool, stderr io.Writer) bool {
	defaultResponse := "n"
	if defaultYes {
		defaultResponse = "y"
	}

	if response, ok := readPromptResponse(prompt, stderr); ok {
		response = strings.TrimSpace(strings.ToLower(response))
		if response == "" {
			response = defaultResponse
		}
		return response == "y" || response == "yes"
	}
	return defaultYes
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

func readPromptResponse(prompt string, stderr io.Writer) (string, bool) {
	if isTerminalFile(os.Stdin) {
		return readPromptKey(os.Stdin, stderr, prompt)
	}

	ttyIn, ttyOut, err := openPromptTTY()
	if err != nil {
		return "", false
	}
	defer closePromptTTY(ttyIn, ttyOut)

	return readPromptKey(ttyIn, ttyOut, prompt)
}

func readLineResponse(prompt string, stderr io.Writer) (string, bool) {
	if isTerminalFile(os.Stdin) {
		return readPromptLine(os.Stdin, stderr, prompt)
	}

	ttyIn, ttyOut, err := openPromptTTY()
	if err != nil {
		return "", false
	}
	defer closePromptTTY(ttyIn, ttyOut)

	return readPromptLine(ttyIn, ttyOut, prompt)
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

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}

func validateImplementedFeatureSet(cfg runConfig) error {
	return nil
}

func parseArgs(args []string) (runConfig, error) {
	wd, err := os.Getwd()
	if err != nil {
		return runConfig{}, err
	}

	cfg := runConfig{
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
		if len(s.Targets) == 0 {
			if cfg.TreePayload {
				return newUsageError("Error: --internal-tree-payload requires an explicit target.\n  Use: catclip TARGET --internal-tree-payload")
			}
			s.Targets = []string{"."}
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
		case "-v", "--verbose":
			cfg.Verbose = true
		case "-q", "--quiet":
			cfg.Quiet = true
		case "-y", "--yes":
			cfg.Yes = true
		case "-p", "--print":
			cfg.Print = true
			cfg.OutputMode = outputModeStdout
		case "-t", "--no-tree":
			cfg.NoTree = true
		case "--preview":
			cfg.Preview = true
		case "--internal-tree-payload":
			cfg.TreePayload = true
			cfg.Print = true
			cfg.Quiet = true
			cfg.OutputMode = outputModeStdout
		case "--internal-tree-target":
			if i+1 >= len(args) {
				return runConfig{}, newUsageError("Error: --internal-tree-target requires a path.")
			}
			i++
			cfg.TreeTarget = args[i]
		case "--internal-tree-kind":
			if i+1 >= len(args) {
				return runConfig{}, newUsageError("Error: --internal-tree-kind requires a value.")
			}
			i++
			cfg.TreeKind = args[i]
		case "--internal-tree-state":
			if i+1 >= len(args) {
				return runConfig{}, newUsageError("Error: --internal-tree-state requires a value.")
			}
			i++
			cfg.TreeState = args[i]
		case "--internal-file-preview":
			cfg.FilePreview = true
			cfg.Print = true
			cfg.Quiet = true
			cfg.OutputMode = outputModeStdout
		case "--internal-file-path":
			if i+1 >= len(args) {
				return runConfig{}, newUsageError("Error: --internal-file-path requires a path.")
			}
			i++
			cfg.FilePath = args[i]
		case "--internal-content-match-list":
			cfg.ContentMatchList = true
			cfg.Print = true
			cfg.Quiet = true
			cfg.OutputMode = outputModeStdout
		case "--internal-recent-preview":
			cfg.RecentPreview = true
			cfg.Print = true
			cfg.Quiet = true
			cfg.OutputMode = outputModeStdout
		case "--internal-recent-data":
			if i+1 >= len(args) {
				return runConfig{}, newUsageError("Error: --internal-recent-data requires a path.")
			}
			i++
			cfg.RecentData = args[i]
		case "--internal-recent-selection":
			if i+1 >= len(args) {
				return runConfig{}, newUsageError("Error: --internal-recent-selection requires a value.")
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
		case "--snippet":
			inModifierMode = true
			if i+1 >= len(args) {
				return runConfig{}, requiredStageValueError("--snippet")
			}
			i++
			current.SnippetPattern = args[i]
			current.Snippet = true
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageSnippet, Values: []string{args[i]}})
			lastNoValueModifier = ""
		case "--then":
			if err := finalize(); err != nil {
				return runConfig{}, err
			}
		case "--":
			if i != len(args)-1 {
				return runConfig{}, bareModifierPlaceholderOrderError()
			}
			return runConfig{}, bareModifierPlaceholderInteractiveOnlyError()
		case "--only":
			inModifierMode = true
			values, next := consumeModifierValues(args, i+1)
			if len(values) == 0 {
				return runConfig{}, requiredStageValueError("--only")
			}
			current.Only = append(current.Only, values...)
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageOnly, Values: append([]string(nil), values...)})
			i = next - 1
			lastNoValueModifier = ""
		case "--include":
			inModifierMode = true
			values, next := consumeModifierValues(args, i+1)
			if len(values) == 0 {
				return runConfig{}, requiredStageValueError("--include")
			}
			current.IncludedTargets = append(current.IncludedTargets, values...)
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageInclude, Values: append([]string(nil), values...)})
			i = next - 1
			lastNoValueModifier = ""
		case "--exclude":
			inModifierMode = true
			values, next := consumeModifierValues(args, i+1)
			if len(values) == 0 {
				return runConfig{}, requiredStageValueError("--exclude")
			}
			current.Exclude = append(current.Exclude, values...)
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageExclude, Values: append([]string(nil), values...)})
			i = next - 1
			lastNoValueModifier = ""
		case "--recent":
			inModifierMode = true
			limit, next, err := consumeOptionalRecentLimit(args, i+1)
			if err != nil {
				return runConfig{}, err
			}
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageRecent, Limit: limit})
			i = next - 1
			lastNoValueModifier = ""
		case "--contains":
			inModifierMode = true
			if i+1 >= len(args) {
				return runConfig{}, containsMissingPatternError(args, i)
			}
			i++
			current.Contains = args[i]
			current.Stages = append(current.Stages, scopeStage{Kind: scopeStageContains, Values: []string{args[i]}})
			if looksLikeGlobConfusion(current.Contains) {
				cfg.Warnings = append(cfg.Warnings, "Warning: --contains uses regex, not globs. Did you mean '.*' instead of '*'?\n  Example: --contains 'use.*Context' (not 'use*Context')")
			}
			lastNoValueModifier = ""
		default:
			switch {
			case strings.HasPrefix(arg, "--contains="):
				return runConfig{}, newUsageError("Error: --contains requires a space before the pattern.\n  Use: catclip src --contains 'pattern'\n  Not: catclip src --contains='pattern'")
			case strings.HasPrefix(arg, "--snippet="):
				return runConfig{}, newUsageError("Error: --snippet requires a space before the pattern.\n  Use: catclip src --snippet 'pattern'\n  Not: catclip src --snippet='pattern'")
			case strings.HasPrefix(arg, "--recent="):
				return runConfig{}, newUsageError("Error: --recent requires a space before the value.\n  Use: catclip src --recent 5\n  Or:  catclip src --recent")
			case arg == "--diff":
				return runConfig{}, diffStandaloneError()
			case strings.HasPrefix(arg, "--"):
				return runConfig{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			case strings.HasPrefix(arg, "-") && len(arg) > 1:
				return runConfig{}, newUsageError("Error: Unknown option %s\n  Run 'catclip --help' for available options.", singleQuoted(arg))
			default:
				if inModifierMode {
					if lastNoValueModifier != "" {
						return runConfig{}, noValueModifierError(lastNoValueModifier)
					}
					return runConfig{}, positionalAfterModifierError()
				}
				current.Targets = append(current.Targets, arg)
				current.explicitTargets++
				lastNoValueModifier = ""
			}
		}
	}

	if err := finalize(); err != nil {
		return runConfig{}, err
	}

	if len(executionScopes) == 0 {
		if cfg.TreePayload {
			return runConfig{}, newUsageError("Error: --internal-tree-payload requires an explicit target.\n  Use: catclip TARGET --internal-tree-payload")
		}
		executionScopes = append(executionScopes, executionScope{Targets: []string{"."}})
	}

	cfg.Command = finalizedCommandSpecFromExecutionScopes(executionScopes)

	return cfg, nil
}

func (b executionScopeBuilder) hasContent() bool {
	return len(b.Targets) > 0 || len(b.IncludedTargets) > 0 || len(b.Only) > 0 || len(b.Exclude) > 0 ||
		len(b.Stages) > 0 ||
		b.Contains != "" || b.SnippetPattern != "" || b.Snippet || b.Changed || b.Staged || b.Unstaged ||
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

func warnDirectoryPatternSemantics(cfg runConfig, baseRules []ignoreRule, stderr io.Writer, colors colorPalette) (bool, error) {
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
		if stage.Kind != scopeStageRecent {
			continue
		}
		if stage.Limit == nil {
			parts = append(parts, "recent=true")
			continue
		}
		parts = append(parts, fmt.Sprintf("recent=%d", *stage.Limit))
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
