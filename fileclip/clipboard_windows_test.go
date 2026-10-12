//go:build windows

package fileclip

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
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
			const ready = "catclip-prompt-probe-ready"
			probe := "Write-Output '" + ready + "'; Read-Host"
			script := probe
			if stdin {
				script = "-"
			}
			cmd := powershellClipboardCommand("powershell", script)
			if stdin {
				cmd.Stdin = strings.NewReader(probe)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			// Bound pipe cleanup too, even if a child retains an output handle.
			cmd.WaitDelay = 5 * time.Second
			started := time.Now()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// Cold PowerShell startup competes with other packages on CI. This
			// is a liveness guard, not a ten-second startup performance contract.
			timer := time.NewTimer(time.Minute)
			defer timer.Stop()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			var err error
			select {
			case err = <-done:
			case <-timer.C:
				_ = cmd.Process.Kill()
				err = <-done
				phase := "before the prompt probe started"
				if strings.Contains(stdout.String(), ready) {
					phase = "after the prompt probe started"
				}
				t.Fatalf("clipboard helper timed out %s after %s: err=%v stdout=%q stderr=%q",
					phase, time.Since(started), err, stdout.String(), stderr.String())
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 ||
				!strings.Contains(stdout.String(), ready) ||
				!strings.Contains(stderr.String(), "Microsoft.PowerShell.Commands.ReadHostCommand") {
				t.Fatalf("prompt did not fail cleanly after %s: err=%v stdout=%q stderr=%q",
					time.Since(started), err, stdout.String(), stderr.String())
			}
		})
	}
}
