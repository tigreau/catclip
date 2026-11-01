package search

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestFileDiagnosticsCauses(t *testing.T) {
	for _, tc := range []struct {
		name, goos, diagnostic, heading string
	}{
		{"windows", "windows", "rg: ./WindowsApps: Access is denied. (os error 5)\r\n", " because of permission errors."},
		{"mac_privacy", "darwin", "rg: ./Library: Operation not permitted (os error 1)", " because of permission errors."},
		{"linux_permission", "linux", "rg: ./private: Permission denied (os error 13)", " because of permission errors."},
		{"localized", "windows", "rg: ./private: Accès refusé. (os error 5)", " because of permission errors."},
		{"unix_io", "linux", "rg: ./disk: Input/output error (os error 5)", "scan-root."},
		{"missing", "darwin", "rg: ./missing: No such file or directory (os error 2)", "scan-root."},
		{"invalid_option", "windows", "error: unrecognized flag --invalid", "scan-root."},
		{"mixed", "windows", "rg: ./private: Access is denied. (os error 5)\nrg: ./missing: not found (os error 2)", " because of permission errors and other errors."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := fileErrorDiagnostics{goos: tc.goos}
			// Force arbitrary UTF-8 and CRLF chunk boundaries.
			for _, b := range []byte(tc.diagnostic) {
				if n, err := d.Write([]byte{b}); n != 1 || err != nil {
					t.Fatalf("write: %d, %v", n, err)
				}
			}
			cause := &exec.ExitError{}
			err := d.wrap("scan-root", cause)
			if !strings.HasPrefix(err.Error(), "Error: Could not finish scanning scan-root") || !strings.Contains(err.Error(), tc.heading+"\n") {
				t.Fatalf("wrong heading: %s", err)
			}
			for _, line := range strings.Split(strings.TrimSpace(tc.diagnostic), "\n") {
				if !strings.Contains(err.Error(), strings.TrimPrefix(strings.TrimSpace(line), "rg: ")) {
					t.Fatalf("lost actual cause: %s", err)
				}
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr != cause {
				t.Fatal("lost underlying process error")
			}
			if strings.Contains(err.Error(), `cd "`) != (d.permissions > 0) {
				t.Fatalf("incorrect permission guidance: %s", err)
			}
			if d.permissions > 0 {
				path := "/path/to/my-project"
				if tc.goos == "windows" {
					path = `C:\path\to\my-project`
				}
				if !strings.Contains(err.Error(), `cd "`+path+`"`) {
					t.Fatalf("incorrect platform path: %s", err)
				}
			}
		})
	}
}

func TestFileDiagnosticsBoundedStreaming(t *testing.T) {
	d := fileErrorDiagnostics{goos: "windows"}
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(&d, "rg: ./private-%06d: Access is denied. (os error 5)\n", i)
	}
	err := d.wrap("root", &exec.ExitError{})
	if len(d.lines) != 10 || d.total != 100000 || d.permissions != 100000 || cap(d.pending) > 2*fileErrorLineLimit {
		t.Fatalf("collector is not bounded: lines=%d total=%d permissions=%d capacity=%d", len(d.lines), d.total, d.permissions, cap(d.pending))
	}
	if !strings.Contains(err.Error(), "99990 more diagnostic lines") || strings.Contains(err.Error(), "private-000010") {
		t.Fatalf("incorrect truncation: %s", err)
	}
}

func TestFileDiagnosticsLongLineAndEscapes(t *testing.T) {
	d := fileErrorDiagnostics{goos: "windows"}
	d.Write([]byte(strings.Repeat("x", fileErrorLineLimit*10) + " (os error 5)\n"))
	d.Write([]byte("\nrg: ./file: \x1b[31mactual error\r\n"))
	err := d.wrap("root", &exec.ExitError{})
	if d.total != 2 || d.permissions != 0 || len(d.lines[0]) > fileErrorLineLimit+40 || !strings.Contains(err.Error(), "[message truncated]") {
		t.Fatalf("clipped line mishandled: total=%d permissions=%d", d.total, d.permissions)
	}
	if strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), `\x1b[31mactual error`) {
		t.Fatal("terminal control escaped incorrectly")
	}
}

func TestFileDiagnosticsPreserveUnexplainedAndCancellation(t *testing.T) {
	for _, cause := range []error{nil, context.Canceled, context.DeadlineExceeded, errors.New("cannot start executable")} {
		d := fileErrorDiagnostics{goos: "windows"}
		d.Write([]byte("rg: ./file: Access is denied. (os error 5)\n"))
		if got := d.wrap("root", cause); got != cause {
			t.Fatalf("changed original error: %v", got)
		}
	}
	d := fileErrorDiagnostics{}
	d.Write([]byte(" \r\n\n"))
	cause := &exec.ExitError{}
	if d.wrap("root", cause) != cause {
		t.Fatal("invented an explanation without stderr")
	}
}

func TestRipgrepFileEnumerationReportsCause(t *testing.T) {
	root := t.TempDir()
	writeMembershipFixture(t, root, "valid.go", "package valid\n")
	paths, err := RunRipgrepFiles(root, RipgrepFileOptions{Paths: []string{"valid.go", "missing-target"}})
	if err == nil || paths != nil {
		t.Fatalf("incomplete enumeration accepted: %v, %v", paths, err)
	}
	if !strings.Contains(err.Error(), "ripgrep reported:") || !strings.Contains(err.Error(), "missing-target") || !strings.Contains(err.Error(), "os error 2") {
		t.Fatalf("actual rg diagnostic missing: %v", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("underlying exit code lost: %v", err)
	}
}
