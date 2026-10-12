//go:build windows

package fileclip

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the actual helper startup in both transport modes without touching
// the clipboard. Without -NonInteractive, Read-Host can accept closed stdin and
// exit successfully, hiding an unattended prompt. Clipboard helpers must fail.
func TestClipboardPowerShellRejectsInteractivePrompt(t *testing.T) {
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("Windows PowerShell unavailable")
	}
	for _, stdin := range []bool{false, true} {
		name := "command"
		if stdin {
			name = "stdin"
		}
		t.Run(name, func(t *testing.T) {
			script := "Read-Host"
			if stdin {
				script = "-"
			}
			cmd := powershellClipboardCommand("powershell", script)
			if stdin {
				cmd.Stdin = strings.NewReader("Read-Host")
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			var timedOut atomic.Bool
			timer := time.AfterFunc(10*time.Second, func() {
				timedOut.Store(true)
				_ = cmd.Process.Kill()
			})
			defer timer.Stop()
			err := cmd.Wait()
			if timedOut.Load() {
				t.Fatal("clipboard helper hung waiting for interactive input")
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || stderr.Len() == 0 {
				t.Fatalf("prompt did not fail cleanly: err=%v stderr=%s", err, stderr.String())
			}
		})
	}
}
