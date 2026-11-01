package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const (
	fileErrorLimit     = 10
	fileErrorLineLimit = 4096
)

// Drain stderr completely but retain only ten bounded messages. Count the
// remainder while streaming so large permission-error lists cannot consume
// unbounded memory or be silently truncated by exec.Cmd.Output's stderr saver.
// exec owns the single writer; inspect this collector only after Output returns.
type fileErrorDiagnostics struct {
	goos        string
	pending     []byte
	truncated   bool
	lines       []string
	total       int
	permissions int
}

func (d *fileErrorDiagnostics) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		part := p
		if end >= 0 {
			part = p[:end]
		}
		keep := min(len(part), fileErrorLineLimit-len(d.pending))
		d.pending = append(d.pending, part[:keep]...)
		d.truncated = d.truncated || keep < len(part)
		if end < 0 {
			break
		}
		d.finishLine()
		p = p[end+1:]
	}
	return n, nil
}

func (d *fileErrorDiagnostics) finishLine() {
	line := strings.TrimSpace(string(d.pending))
	if line != "" || d.truncated {
		d.total++
		// A clipped diagnostic is not enough evidence to classify its cause.
		if !d.truncated && fileDiagnosticIsPermission(line, d.goos) {
			d.permissions++
		}
		if len(d.lines) < fileErrorLimit {
			if d.truncated {
				line = strings.ToValidUTF8(line, "�") + " … [message truncated]"
			}
			// Keep subprocess text from injecting terminal escape sequences.
			line = strings.ReplaceAll(line, "\x1b", `\x1b`)
			d.lines = append(d.lines, line)
		}
	}
	d.pending = d.pending[:0]
	d.truncated = false
}

func fileDiagnosticIsPermission(line, goos string) bool {
	// OS error numbers are platform-specific: Unix 5 is an I/O error, while
	// Windows 5 is access denied. macOS privacy restrictions can report EPERM.
	if goos == "windows" {
		return strings.HasSuffix(line, "(os error 5)")
	}
	if goos == "darwin" || goos == "linux" {
		return strings.HasSuffix(line, "(os error 13)") || strings.HasSuffix(line, "(os error 1)")
	}
	return false
}

type fileEnumerationError struct {
	message string
	cause   error
}

func (e *fileEnumerationError) Error() string { return e.message }
func (e *fileEnumerationError) Unwrap() error { return e.cause }

func (d *fileErrorDiagnostics) wrap(workingDir string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err // Startup errors already describe the failed operation.
	}
	d.finishLine() // Include an unterminated final diagnostic.
	if d.total == 0 {
		return err // Never invent a cause from an exit code alone.
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Error: Could not finish scanning %s", workingDir)
	switch {
	case d.permissions == d.total:
		out.WriteString(" because of permission errors")
	case d.permissions > 0:
		out.WriteString(" because of permission errors and other errors")
	}
	out.WriteString(".\n\n  ripgrep reported:\n")
	for _, line := range d.lines {
		fmt.Fprintf(&out, "    %s\n", strings.TrimPrefix(line, "rg: "))
	}
	if omitted := d.total - len(d.lines); omitted > 0 {
		fmt.Fprintf(&out, "    …and %d more diagnostic lines\n", omitted)
	}
	if d.permissions > 0 {
		projectPath := "/path/to/my-project"
		if d.goos == "windows" {
			projectPath = `C:\path\to\my-project`
		}
		fmt.Fprintf(&out, "\n  Run Catclip inside your project folder, for example:\n    cd \"%s\"\n    catclip --\n", projectPath)
	}
	return &fileEnumerationError{message: strings.TrimRight(out.String(), "\n"), cause: err}
}
