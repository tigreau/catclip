package picker

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/tigreau/catclip/internal/platform"
)

// ErrSelectionCancelled reports an fzf exit that should be treated as a
// cancelled interactive choice rather than a hard failure.
var ErrSelectionCancelled = errors.New("selection cancelled")

// Request describes a generic picker session for the fzf backend.
type Request struct {
	Query          string
	Prompt         string
	WithNth        string
	Nth            string
	Header         string
	Footer         string
	FooterBorder   string
	PreviewCommand string
	PreviewWindow  string
	ColorSpecs     []string
	Disabled       bool
	Multi          bool
	NoSort         bool
	Exact          bool
	PrintQuery     bool
	ExpectKeys     []string
	Bindings       []string
	Lines          []string
	Env            []string
}

// Result contains the query and selected item keys returned by fzf.
type Result struct {
	Query   string
	Key     string
	Matches []string
}

const (
	defaultPreviewWindow = "right:55%:wrap:border-rounded"
	defaultPreviewLabel  = "Scrollable Preview"
)

// RevealHeaderAfterQueryChangeBinding replaces the picker header after the
// first query edit, then removes itself. The action is handled entirely by
// fzf; it does not start a subprocess on each keystroke.
func RevealHeaderAfterQueryChangeBinding(header string) string {
	return "change:change-header{" + header + "}+unbind(change)"
}

// DefaultPreviewWindow is the standard preview-window layout used by every
// catclip picker. Callers can pass this verbatim as Request.PreviewWindow when
// they want the standard layout but want to drive the preview via a binding
// (e.g. start:preview(...)) instead of the focus-triggered --preview command.
const DefaultPreviewWindow = defaultPreviewWindow

// Filter runs fzf in --filter mode and returns the matched keys from the
// provided tab-delimited lines without opening an interactive picker.
func Filter(bin, query string, lines []string) ([]string, error) {
	return FilterByNth(bin, query, lines, "1,2")
}

// FilterByNth runs fzf in filter mode while restricting matching to the
// requested tab-delimited fields. The complete input row is still returned so
// callers can recover its stable key.
func FilterByNth(bin, query string, lines []string, nth string) ([]string, error) {
	cmd := exec.Command(bin, filterArgs(query, nth)...)
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, nil
	}
	return parseMatches(text), nil
}

func filterArgs(query, nth string) []string {
	if strings.TrimSpace(nth) == "" {
		nth = "1,2"
	}
	return []string{"--delimiter", "\t", "--nth", nth, "--filter", query}
}

// Run executes an interactive fzf picker and returns the parsed selection.
func Run(bin string, req Request) (Result, error) {
	// Opt-in interactive diagnostic. This is the parent-side fzf duration:
	// picker input has already been prepared, and preview child processes log
	// separately via CATCLIP_INTERNAL_BENCH_LOG. Do not log query text,
	// preview commands, or row contents here.
	benchEnabled := platform.InternalBenchEnabled()
	benchFields := []string(nil)
	finishBench := func(...string) {}
	finishPrepareBench := func(...string) {}
	if benchEnabled {
		benchFields = pickerBenchFields(req)
		finishBench = platform.InternalBenchSpan("picker.fzf.run", benchFields...)
		finishPrepareBench = platform.InternalBenchSpan("picker.fzf.prepare", benchFields...)
	}
	args := buildArgs(req)
	input := strings.Join(req.Lines, "\n") + "\n"
	if benchEnabled {
		finishPrepareBench(
			"args", platform.InternalBenchInt(len(args)),
			"input_bytes", platform.InternalBenchInt(len(input)),
		)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr
	if req.Env != nil {
		cmd.Env = req.Env
	}
	if benchEnabled {
		platform.InternalBenchLog("picker.fzf.ready", append(benchFields,
			"args", platform.InternalBenchInt(len(args)),
			"input_bytes", platform.InternalBenchInt(len(input)),
		)...)
	}

	var out []byte
	var err error
	if benchEnabled {
		// Split process creation from the complete interactive lifetime only in
		// diagnostic mode. The normal path keeps exec.Cmd.Output unchanged.
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		finishStartBench := platform.InternalBenchSpan("picker.fzf.start", benchFields...)
		err = cmd.Start()
		finishStartBench("err", platform.InternalBenchError(err))
		if err == nil {
			err = cmd.Wait()
		}
		out = stdout.Bytes()
	} else {
		out, err = cmd.Output()
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 130) {
			finishBench("err", "false", "cancelled", "true")
			return Result{}, ErrSelectionCancelled
		}
		finishBench("err", "true")
		return Result{}, err
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		finishBench("err", "false", "cancelled", "true")
		return Result{}, ErrSelectionCancelled
	}

	if req.PrintQuery {
		result := parseChooseResult(string(out), req.ExpectKeys)
		if result.Key == "" && len(result.Matches) == 0 {
			finishBench("err", "false", "cancelled", "true")
			return Result{}, ErrSelectionCancelled
		}
		finishBench(
			"err", "false",
			"cancelled", "false",
			"matches", platform.InternalBenchInt(len(result.Matches)),
			"key", platform.InternalBenchBool(result.Key != ""),
		)
		return result, nil
	}

	result := Result{Matches: parseMatches(text)}
	if len(result.Matches) == 0 {
		finishBench("err", "false", "cancelled", "true")
		return Result{}, ErrSelectionCancelled
	}
	finishBench(
		"err", "false",
		"cancelled", "false",
		"matches", platform.InternalBenchInt(len(result.Matches)),
	)
	return result, nil
}

func pickerBenchFields(req Request) []string {
	previewMode := "none"
	if req.PreviewCommand != "" {
		previewMode = "focus"
	} else if req.PreviewWindow != "" {
		previewMode = "binding"
	}
	return []string{
		"prompt", strings.TrimSpace(req.Prompt),
		"lines", platform.InternalBenchInt(len(req.Lines)),
		"preview_mode", previewMode,
		"bindings", platform.InternalBenchInt(len(req.Bindings)),
		"disabled", platform.InternalBenchBool(req.Disabled),
		"multi", platform.InternalBenchBool(req.Multi),
		"no_sort", platform.InternalBenchBool(req.NoSort),
		"exact", platform.InternalBenchBool(req.Exact),
		"print_query", platform.InternalBenchBool(req.PrintQuery),
	}
}

func buildArgs(req Request) []string {
	args := []string{"--ansi", "--layout=default", "--info=inline-right", "--delimiter", "\t", "--with-nth", req.WithNth, "--query", req.Query, "--prompt", req.Prompt}
	if req.Disabled {
		args = append(args, "--disabled")
	}
	if req.Multi {
		args = append(args, "--multi")
	}
	if req.NoSort {
		args = append(args, "--no-sort")
	}
	if req.Exact {
		args = append(args, "--exact")
	}
	if req.PrintQuery {
		args = append(args, "--print-query")
	}
	if len(req.Bindings) > 0 {
		for _, binding := range req.Bindings {
			args = append(args, "--bind", binding)
		}
	}
	if req.Nth != "" {
		args = append(args, "--nth", req.Nth)
	}
	if req.Header != "" {
		args = append(args, "--header", req.Header, "--header-border=rounded")
	}
	if req.Footer != "" {
		args = append(args, "--footer", req.Footer)
		if req.FooterBorder != "" {
			args = append(args, "--footer-border", req.FooterBorder)
		}
	}
	window := req.PreviewWindow
	if window == "" {
		window = defaultPreviewWindow
	}
	if req.PreviewCommand != "" {
		args = append(args, "--preview", req.PreviewCommand, "--preview-window", window, "--preview-label", defaultPreviewLabel)
	} else if req.PreviewWindow != "" {
		args = append(args, "--preview-window", window, "--preview-label", defaultPreviewLabel)
	}
	if len(req.ColorSpecs) > 0 {
		for _, spec := range req.ColorSpecs {
			args = append(args, "--color", spec)
		}
	}
	if len(req.ExpectKeys) > 0 {
		args = append(args, "--expect", strings.Join(req.ExpectKeys, ","))
	}
	return args
}

func parseChooseResult(text string, expectedKeys []string) Result {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return Result{}
	}

	lines := strings.Split(text, "\n")
	result := Result{Query: lines[0]}
	lines = lines[1:]
	if len(lines) == 0 {
		return result
	}

	keySet := make(map[string]struct{}, len(expectedKeys))
	for _, key := range expectedKeys {
		keySet[key] = struct{}{}
	}
	if _, ok := keySet[lines[0]]; ok {
		result.Key = lines[0]
		lines = lines[1:]
	}
	if len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return result
	}
	result.Matches = parseMatches(strings.Join(lines, "\n"))
	return result
}

func parseMatches(text string) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 && parts[1] != "" {
			out = append(out, parts[1])
			continue
		}
		if len(parts) >= 1 && parts[0] != "" {
			out = append(out, parts[0])
			continue
		}
		out = append(out, line)
	}
	return out
}
