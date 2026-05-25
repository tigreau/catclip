package catclip

// See RULES.md for product rules, layout, execution flow, and performance notes.

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
)

// =============================================================================
// Constants and types
// =============================================================================

type action string

const (
	actionRun       action = "run"
	actionHelp      action = "help"
	actionHelpAll   action = "help-all"
	actionVersion   action = "version"
	actionEditHiss  action = "hiss"
	actionResetHiss action = "hiss-reset"
)

type outputMode string

const (
	outputModeClipboard outputMode = "clipboard"
	outputModeStdout    outputMode = "stdout"
)

type parsedCommand struct {
	Action     action
	Version    string
	Platform   string
	WorkingDir string
	OutputMode outputMode

	Verbose               bool
	Quiet                 bool
	Headless              bool
	WithBinaries          bool
	Yes                   bool
	Raw                   bool
	Preview               bool
	NoTree                bool
	NoBundle              bool
	TreePayload           bool
	TreePreview           bool
	PrediscoveredPath     string
	TreeTarget            string
	TreeKind              string
	TreeState             string
	FilePreview           bool
	FilePath              string
	ContentMatchList      bool
	RecentPreview         bool
	RecentData            string
	RecentSelect          string
	LinesPreview          bool
	SinkTogglePath        string
	SinkPreviewModePath   string
	SinkPreviewOutputPath string
	SinkPreviewTreePath   string

	Command commandSpec

	Warnings []string
}

type invocationConfig struct {
	Version      string
	Platform     string
	WorkingDir   string
	Verbose      bool
	Quiet        bool
	Headless     bool
	WithBinaries bool
	Internal     bool
}

func (cfg invocationConfig) canPromptForChoice() bool {
	return !cfg.Headless && !cfg.Internal && canPromptInteractively()
}

type resolvedInvocation struct {
	Config invocationConfig
	Scopes []executionScope
}

type discoveredInvocation struct {
	Config invocationConfig
	Scopes []discoveredScope
}

type executionScope struct {
	Targets         []string
	IncludedTargets []string
	Only            []string
	Exclude         []string
	Contains        string
	SnippetPattern  string
	Lines           bool
	LinesStart      int
	LinesEnd        int
	Stages          []scopeStage
	Paths           bool
	Snippet         bool
	Changed         bool
	Staged          bool
	Unstaged        bool
	Untracked       bool
	Diff            bool
}

func executionScopeOutputMode(s executionScope) entryMode {
	if s.Diff {
		return entryModeDiff
	}
	if s.Snippet {
		return entryModeSnippet
	}
	if s.Lines {
		return entryModeLines
	}
	return entryModeFull
}

func executionScopeHasGitSelection(s executionScope) bool {
	return s.Changed || s.Staged || s.Unstaged || s.Untracked
}

func gitSelectionRequiresGitRepoMessage() string {
	return "Error: --changed/--staged/--unstaged/--untracked (and -diff variants) require a git repository.\n  cwd is not a git repo or git is not installed."
}

type executionScopeBuilder struct {
	executionScope
	explicitTargets int
}

type compiledGlob struct {
	raw string
	re  *regexp.Regexp
}

type scopeStageKind string

const (
	scopeStageInclude      scopeStageKind = "include"
	scopeStageOnly         scopeStageKind = "only"
	scopeStageExclude      scopeStageKind = "exclude"
	scopeStageRecent       scopeStageKind = "recent"
	scopeStageDepth        scopeStageKind = "depth"
	scopeStageContains     scopeStageKind = "contains"
	scopeStageChanged      scopeStageKind = "changed"
	scopeStageStaged       scopeStageKind = "staged"
	scopeStageUnstaged     scopeStageKind = "unstaged"
	scopeStageUntracked    scopeStageKind = "untracked"
	scopeStagePaths        scopeStageKind = "paths"
	scopeStageDiff         scopeStageKind = "diff"
	scopeStageSnippet      scopeStageKind = "snippet"
	scopeStageChangedDiff  scopeStageKind = "changed-diff"
	scopeStageStagedDiff   scopeStageKind = "staged-diff"
	scopeStageUnstagedDiff scopeStageKind = "unstaged-diff"
	scopeStageLines        scopeStageKind = "lines"
)

type scopeStage struct {
	Kind        scopeStageKind
	Values      []string
	Limit       *int
	ExactValues bool
}

type fileEntry struct {
	AbsPath          string
	RelPath          string
	ModTime          time.Time
	SizeBytes        int64
	SizeKnown        bool
	TargetRoot       string
	GitVisible       bool
	Mode             entryMode
	SnippetPattern   string
	Lines            bool
	LinesStart       int
	LinesEnd         int
	DiffWantStaged   bool
	DiffWantUnstaged bool
	AllowedByInclude bool
	BlockSource      string
}

type gitContext struct {
	Enabled    bool
	Root       string
	WorkPrefix string
	HasHead    bool
}

type colorPalette struct {
	Reset  string
	Bold   string
	Dim    string
	OK     string
	Err    string
	Warn   string
	Dir    string
	Label  string
	Value  string
	Tree   string
	Prompt string
	Git    string
}

type outputReport struct {
	sizes     map[string]int64
	statuses  map[string]string
	modeTags  map[string]string
	humanSize string
	tokens    int64
	countWord string
	notices   []string
}

type visibleDirIndex struct {
	dirs        []string
	set         map[string]struct{}
	symlinkDirs []string
}

type visibleFileIndex struct {
	byBase        map[string][]fileEntry
	skippedByBase map[string][]skippedMatch
}

type blockInfo struct {
	Rule   string
	Source string
}

type skippedMatch struct {
	RelPath     string
	BlockSource string
}

type targetMatch struct {
	Path         string
	Kind         string
	State        string
	Ignored      bool
	IgnoreSource string
}

type usageError struct {
	message string
}

type exitError struct {
	message string
	code    int
}

// Diagnostics are collected in encounter order so stderr matches the shell's
// target-by-target flow. Some of them are real "Error:" blocks that should
// still print under --quiet even when soft warnings are suppressed.
type diagnostic struct {
	message              string
	isError              bool
	isTargetNotFound     bool
	isScopeUnsatisfiable bool
}

type entryMode string

const (
	entryModeFull    entryMode = "full"
	entryModeLines   entryMode = "lines"
	entryModeSnippet entryMode = "snippet"
	entryModeDiff    entryMode = "diff"
)

const tokenWarnThreshold = 100000

func (e usageError) Error() string {
	return e.message
}

func (e exitError) Error() string {
	return e.message
}

func newUsageError(format string, args ...any) error {
	return usageError{message: fmt.Sprintf(format, args...)}
}

func newExitError(code int, message string) error {
	return exitError{message: message, code: code}
}

// =============================================================================
// Main entrypoint
// =============================================================================

// Main parses the CLI and runs the selected action.
func Main() {
	if err := ensureRequiredTools(os.Stderr); err != nil {
		os.Exit(1)
		return
	}
	args := os.Args[1:]
	normResult, err := normalizePositionalGlobArgs(args, positionalGlobArgsQuiet(args))
	if err != nil {
		exitWithError(err, os.Stderr)
		return
	}
	args = normResult.Args
	startupResult := startupPickerResult{Args: args}
	handled := false
	startupResult, handled, err = maybeResolveStartupPickerAndSinkArgs(args)
	if err != nil {
		exitWithError(err, os.Stderr)
		return
	} else if handled {
		if startupResult.Args == nil {
			return
		}
		args = startupResult.Args
	}

	cfg, err := parseArgs(args)
	if err != nil {
		exitWithError(err, os.Stderr)
		return
	}
	if !cfg.Quiet {
		for _, hint := range normResult.Hints {
			if _, err := fmt.Fprintln(os.Stderr, hint); err != nil {
				exitWithError(err, os.Stderr)
				return
			}
		}
	}
	if shouldWriteResolvedStartupCommand(startupResult, cfg.Quiet) {
		if err := writeResolvedStartupCommand(os.Stderr, args); err != nil {
			exitWithError(err, os.Stderr)
			return
		}
	}

	if err := run(cfg, os.Stdout, os.Stderr, startupResult.PreparedOutput); err != nil {
		exitWithError(err, os.Stderr)
	}
}

func rawArgsHasHeadless(args []string) bool {
	for _, arg := range args {
		if arg == "--headless" {
			return true
		}
	}
	return false
}

func rawArgsRequestQuiet(args []string) bool {
	for _, arg := range args {
		if arg == "-q" || arg == "--quiet" || arg == "--headless" {
			return true
		}
	}
	return false
}

func shouldWriteResolvedStartupCommand(result startupPickerResult, quiet bool) bool {
	if !result.UsedFzf {
		return false
	}
	if !quiet {
		return true
	}
	return result.ForceResolvedCommand
}

func rawArgsUseStdinPathValues(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--include", "--only", "--exclude":
			values, next := consumeModifierValues(args, i+1)
			if len(values) == 1 && values[0] == "-" {
				return true
			}
			i = next - 1
		}
	}
	return false
}

func writeResolvedStartupCommand(stderr io.Writer, args []string) error {
	if len(args) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(stderr, "Resolved command:\n  %s\n\n", formatResolvedStartupCommand(args))
	return err
}
