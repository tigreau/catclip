package ui

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/output"
)

const (
	tokenWarnThreshold = 100000
	snippetContextMax  = 200
)

// invocationConfigFromParsedCommand is a dup of root catclip's
// invocationConfigFromParsedCommand. UI can't import root, so the
// constructor that builds command.Invocation from command.Parsed is
// duplicated here. The two copies must stay in lockstep when new
// invocation fields are added.
func invocationConfigFromParsedCommand(cfg command.Parsed) command.Invocation {
	return command.Invocation{
		Version:      cfg.Version,
		Platform:     cfg.Platform,
		WorkingDir:   cfg.WorkingDir,
		Verbose:      cfg.Verbose,
		Quiet:        cfg.Quiet,
		Headless:     cfg.Headless,
		WithBinaries: cfg.WithBinaries,
		Internal:     cfg.IsInternalKind(),
	}
}

// normalizeRelPath is a dup of root catclip's normalizeRelPath and
// discovery's private normalizeRelPath. UI uses it for path comparison
// and display formatting.
func normalizeRelPath(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "./")
	if value == "." || value == "/" {
		return "."
	}
	return value
}

// resolvedInvocationFromParsedCommand duplicates the root constructor
// so UI runners (internal_prediscovered, startup_sink_picker) can build
// a Resolved invocation without importing root.
func resolvedInvocationFromParsedCommand(cfg command.Parsed) command.Resolved {
	return command.Resolved{
		Config: invocationConfigFromParsedCommand(cfg),
		Scopes: command.ExecutionScopesFromSpec(cfg.Command),
	}
}

// emitConfigFromParsedCommand duplicates the root constructor so UI
// runners can build the output.EmitConfig directly. Identical to
// root's copy.
func emitConfigFromParsedCommand(cfg command.Parsed) output.EmitConfig {
	return output.EmitConfig{
		OutputMode: cfg.OutputMode,
		Raw:        cfg.Raw,
		NoBundle:   cfg.NoBundle,
	}
}

// rawArgsHasHeadless reports whether --headless appears in args. Dup
// of root main.go's helper.
func rawArgsHasHeadless(args []string) bool {
	for _, arg := range args {
		if arg == "--headless" {
			return true
		}
	}
	return false
}

// rawArgsRequestQuiet reports whether -q / --quiet / --headless
// appears in args.
func rawArgsRequestQuiet(args []string) bool {
	for _, arg := range args {
		if arg == "-q" || arg == "--quiet" || arg == "--headless" {
			return true
		}
	}
	return false
}

// rawArgsUseStdinPathValues reports whether a modifier with stdin-only
// values (-) appears. Dup of root main.go's helper.
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

// formatByteCount renders an int64 as the closest human-readable size
// (B / KB / MB / GB). Dup of root cli.go's helper used by sink picker
// preview labels.
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

// displayPath collapses a home-relative path to a shell-paste-safe
// shorthand. Dup of root path_helpers.go's helper — sync changes.
//
// POSIX uses `~/...` (every POSIX shell expands `~` before passing args
// to external tools). Windows uses `$HOME\...` because PowerShell does
// NOT expand `~` for external programs (notepad, code, nano, explorer,
// start, ...) — a printed `~\Documents\foo.txt` fails on paste with
// "The system cannot find the path specified". PowerShell DOES expand
// `$HOME` because it's a PowerShell automatic variable. Legacy cmd.exe
// doesn't expand `$HOME` either (uses `%USERPROFILE%`), but PowerShell
// is the modern Windows default.
func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	homePrefix := "~"
	if runtime.GOOS == "windows" {
		homePrefix = "$HOME"
	}
	if p == home {
		return homePrefix
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return homePrefix + string(filepath.Separator) + strings.TrimPrefix(p, home+string(filepath.Separator))
	}
	return p
}

// exitError is a typed exit-code error returned by UI helpers that
// want to short-circuit with a specific exit code (e.g. picker
// cancellation returns exit code 1 without an error message). Root
// exitWithError classifies it via the existing exitError branch.
type exitError struct {
	message string
	code    int
}

func (e exitError) Error() string { return e.message }

// Code returns the requested exit code. Used by root exitWithError.
func (e exitError) Code() int { return e.code }

// newExitError constructs an exitError with the given exit code and
// message. Replaces root newExitError calls from UI files after the
// move.
func newExitError(code int, message string) error {
	return exitError{message: message, code: code}
}

// cloneStringSlice returns a fresh copy of the input. Dup of root
// clone_helpers.go's helper — must match its zero-length-returns-nil
// behavior so tests comparing nil vs []string{} stay deterministic.
func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}
