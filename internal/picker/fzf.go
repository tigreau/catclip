package picker

import (
	"errors"
	"os"
	"os/exec"
	"strings"
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

// DefaultPreviewWindow is the standard preview-window layout used by every
// catclip picker. Callers can pass this verbatim as Request.PreviewWindow when
// they want the standard layout but want to drive the preview via a binding
// (e.g. start:preview(...)) instead of the focus-triggered --preview command.
const DefaultPreviewWindow = defaultPreviewWindow

// Filter runs fzf in --filter mode and returns the matched keys from the
// provided tab-delimited lines without opening an interactive picker.
func Filter(bin, query string, lines []string) ([]string, error) {
	cmd := exec.Command(bin, "--delimiter", "\t", "--nth", "1,2", "--filter", query)
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

// Run executes an interactive fzf picker and returns the parsed selection.
func Run(bin string, req Request) (Result, error) {
	args := buildArgs(req)
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(strings.Join(req.Lines, "\n") + "\n")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 130) {
			return Result{}, ErrSelectionCancelled
		}
		return Result{}, err
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		return Result{}, ErrSelectionCancelled
	}

	if req.PrintQuery {
		result := parseChooseResult(string(out), req.ExpectKeys)
		if result.Key == "" && len(result.Matches) == 0 {
			return Result{}, ErrSelectionCancelled
		}
		return result, nil
	}

	result := Result{Matches: parseMatches(text)}
	if len(result.Matches) == 0 {
		return Result{}, ErrSelectionCancelled
	}
	return result, nil
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
