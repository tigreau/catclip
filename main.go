package catclip

// See RULES.md for product rules, layout, execution flow, and performance notes.

import (
	"fmt"
	"io"
	"os"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/ui"
)

// =============================================================================
// Constants and types
// =============================================================================

// canPromptForChoice was a method on command.Invocation at root. After the
// command extraction it stays root-side as a free function so internal/command
// can remain stdlib-only — the platform.CanPromptInteractively dependency is
// runtime concern, not part of the command model.
func canPromptForChoice(cfg command.Invocation) bool {
	return !cfg.Headless && !cfg.Internal && platform.CanPromptInteractively()
}

type usageError struct {
	message string
}

type exitError struct {
	message string
	code    int
}

const tokenWarnThreshold = 100000

// snippetContextMax bounds --snippet REGEX N. Above this the context is close
// to "read most of the file" and should be an explicit --lines/full request.
const snippetContextMax = 200

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
	args := os.Args[1:]
	commandKind := internalBenchCommandKind(args)
	// Opt-in diagnostic timeline for interactive Windows slowness. fzf spawns
	// catclip --internal-* helpers repeatedly for --snippet/--lines; normal
	// process profiles miss those child processes. This span is inert unless
	// CATCLIP_INTERNAL_BENCH_LOG is set.
	finishMainBench := platform.InternalBenchSpan("main.total",
		"kind", commandKind,
		"argc", platform.InternalBenchInt(len(args)),
	)
	defer finishMainBench()

	// Wire the version resolver into the cli parser. cli/ can't import root,
	// so the version-file lookup logic (which uses platform.ExecutableCandidateDirs
	// + project-root VERSION lookup) is provided here at process start.
	cli.SetVersionLoader(loadVersion)
	// Wire the args -> view callback into the discovery resolver. Used
	// by the content-match picker's checkpoint preview path (see
	// resolver.fzfCheckpointContentMatchListCommand). discovery can't
	// import internal/cli or root, so this stitching happens here.
	discovery.SetScopeViewResolver(ui.ScopeViewForDiscoveryArgs)
	// Refuse to run in detected X11 desktop sessions before any other
	// startup work (tool check, argv normalization, picker, parser).
	// The plan's "no X11 testing surface" rationale requires that even
	// --help and --version fail here, so this must come before
	// ensureRequiredTools (a missing rg/fzf must not mask the policy
	// message) and before cli.ParseArgs. See
	// docs/versions/v0.6.0/reports/ACTIVE_PLAN_x11_full_removal.md.
	finishGateBench := platform.InternalBenchSpan("main.phase", "kind", commandKind, "phase", "linux_session_gate")
	if err := linuxSessionGateError(); err != nil {
		finishGateBench("err", platform.InternalBenchError(err))
		exitWithError(err, os.Stderr)
		return
	}
	finishGateBench("err", "false")

	finishToolsBench := platform.InternalBenchSpan("main.phase", "kind", commandKind, "phase", "ensure_required_tools")
	if err := ensureRequiredTools(os.Stderr); err != nil {
		finishToolsBench("err", platform.InternalBenchError(err))
		os.Exit(1)
		return
	}
	finishToolsBench("err", "false")

	finishNormalizeBench := platform.InternalBenchSpan("main.phase", "kind", commandKind, "phase", "normalize_positional_globs")
	normResult, err := normalizePositionalGlobArgs(args, positionalGlobArgsQuiet(args))
	finishNormalizeBench("err", platform.InternalBenchError(err))
	if err != nil {
		exitWithError(err, os.Stderr)
		return
	}
	args = normResult.Args
	startupResult := ui.StartupPickerResult{Args: args}
	handled := false
	finishStartupBench := platform.InternalBenchSpan("main.phase", "kind", commandKind, "phase", "startup_picker")
	startupResult, handled, err = ui.MaybeResolveStartupPickerAndSinkArgs(args)
	finishStartupBench(
		"err", platform.InternalBenchError(err),
		"handled", fmt.Sprint(handled),
	)
	if err != nil {
		exitWithError(err, os.Stderr)
		return
	} else if handled {
		if startupResult.Args == nil {
			return
		}
		args = startupResult.Args
	}

	finishParseBench := platform.InternalBenchSpan("main.phase", "kind", commandKind, "phase", "parse")
	cfg, err := cli.ParseArgs(args)
	finishParseBench("err", platform.InternalBenchError(err))
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

	finishRunBench := platform.InternalBenchSpan("main.phase", "kind", commandKind, "phase", "run")
	if err := run(cfg, os.Stdout, os.Stderr, startupResult.PreparedOutput); err != nil {
		finishRunBench("err", platform.InternalBenchError(err))
		exitWithError(err, os.Stderr)
		return
	}
	finishRunBench("err", "false")
}

func internalBenchCommandKind(args []string) string {
	for _, arg := range args {
		switch arg {
		case "--internal-content-match-list":
			return "internal-content-match-list"
		case "--internal-lines-preview":
			return "internal-lines-preview"
		case "--internal-file-preview":
			return "internal-file-preview"
		case "--internal-snippet-boundary-preview":
			return "internal-snippet-boundary-preview"
		case "--internal-recent-preview":
			return "internal-recent-preview"
		case "--internal-tree-preview":
			return "internal-tree-preview"
		case "--internal-sink-preview":
			return "internal-sink-preview"
		case "--internal-sink-toggle":
			return "internal-sink-toggle"
		case "--help", "-h":
			return "help"
		case "--help-all":
			return "help-all"
		case "--version", "-V":
			return "version"
		}
	}
	return "run"
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

func shouldWriteResolvedStartupCommand(result ui.StartupPickerResult, quiet bool) bool {
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
			values, next := cli.ConsumeModifierValues(args, i+1)
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
	_, err := fmt.Fprintf(stderr, "Resolved command:\n  %s\n\n", cli.FormatResolvedStartupCommand(args))
	return err
}

// linuxSessionGateError returns the reliability-focused usage error when
// catclip is invoked inside a detected Linux X11 desktop session. nil for
// every other classification (Wayland, WSL, unknown/displayless, non-Linux),
// so SSH/Docker/TTY/CI runs continue to stdout sinks while desktop X11 is
// blocked at startup. See
// docs/versions/v0.6.0/reports/ACTIVE_PLAN_x11_full_removal.md.
func linuxSessionGateError() error {
	return linuxSessionGateErrorFor(platform.DetectLinuxSession())
}

// linuxSessionGateErrorFor is the testable core of linuxSessionGateError.
// Takes the session kind as input so unit tests don't have to inject env
// state or re-exec a child process.
func linuxSessionGateErrorFor(kind platform.LinuxSessionKind) error {
	if kind != platform.LinuxSessionX11 {
		return nil
	}
	return newUsageError(
		"Error: catclip requires a reliable Linux desktop session.\n\n" +
			"X11 clipboard delivery is not reliable enough for catclip's paste workflow, so\n" +
			"Linux desktop sessions must use Wayland.\n\n" +
			"Log into a Wayland session and rerun catclip.",
	)
}
