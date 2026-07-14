package platform

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// ProbeToolVersionTimeout caps how long ProbeToolVersion waits for
// a bundled tool to print its --version output. A tool worth running
// prints in a few milliseconds warm; the 3 s budget covers cold
// starts on Windows CI (process spawn floor + antivirus scan), macOS
// Gatekeeper first-exec assessment, and concurrent test runs where
// /bin/sh startup can queue. Longer than this is a broken binary and
// the caller (renderVersionOutput) already has a fallback line for
// the "probe failed / timed out" case.
const ProbeToolVersionTimeout = 3 * time.Second

// ErrProbeTimeout is returned when a `<binary> --version` invocation
// doesn't complete within ProbeToolVersionTimeout. Distinguished from
// generic exec errors so `--version` output can show a
// timeout-specific message.
var ErrProbeTimeout = errors.New("version probe timed out")

// ProbeToolVersion runs `<path> --version` with a short timeout and
// returns the parsed version string. Handles the two tool formats
// catclip actually cares about (fzf, rg) and falls back to the
// trimmed first line for unknown formats so future upstream changes
// still show something informative.
//
// fzf format: `0.44.1 (26f37b8)` — first token before the space.
// rg format:  `ripgrep 14.1.0`  — last token on the first line.
func ProbeToolVersion(path string) (string, error) {
	return probeToolVersion(path, ProbeToolVersionTimeout)
}

// probeToolVersion is ProbeToolVersion with an explicit timeout. Tests use it to
// stay independent of the production budget: fast-path probes pass a generous
// timeout so full-suite parallel load (process-spawn queueing, macOS first-exec
// assessment) cannot flake them, and the timeout case passes a short one so it
// is fast and deterministic instead of waiting the full production budget.
func probeToolVersion(path string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty tool path")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "--version")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &bytes.Buffer{} // discard; some tools chatter on stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", ErrProbeTimeout
	}
	if err != nil {
		return "", err
	}

	return parseToolVersionOutput(stdout.String()), nil
}

// parseToolVersionOutput is the pure formatter behind ProbeToolVersion.
// Kept separate so any (goos-independent) output shape can be
// exercised without spawning a subprocess.
func parseToolVersionOutput(raw string) string {
	// Normalize Windows CRLF so first-line splitting works uniformly.
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	firstLine := strings.TrimSpace(strings.SplitN(raw, "\n", 2)[0])
	if firstLine == "" {
		return ""
	}

	// rg: "ripgrep 14.1.0" — extract the token after "ripgrep".
	// A future rg version like "ripgrep 15.0.0 (rev abcdef)" is still
	// safe because we take index 1, not the last token.
	if strings.HasPrefix(firstLine, "ripgrep ") {
		tokens := strings.Fields(firstLine)
		if len(tokens) >= 2 {
			return tokens[1]
		}
	}

	// fzf: "0.44.1 (26f37b8)" — extract the first space-separated token.
	// Any first line starting with a digit falls into this branch.
	if firstLine != "" && firstLine[0] >= '0' && firstLine[0] <= '9' {
		tokens := strings.Fields(firstLine)
		if len(tokens) >= 1 {
			return tokens[0]
		}
	}

	// Unknown format — return the trimmed first line so users at
	// least see what the binary emitted.
	return firstLine
}
