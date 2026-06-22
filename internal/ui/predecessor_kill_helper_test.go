package ui

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func startLongRunningHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("timeout", "/t", "30", "/nobreak")
	} else {
		cmd = exec.Command("sleep", "30")
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("no usable long-running helper on this platform: %v", err)
	}
	// Give the OS a beat to actually set up the process.
	time.Sleep(20 * time.Millisecond)
	return cmd
}

// waitedAlive returns true if the process is still running after a short
// grace period; false if it exited. Used to assert kill behavior with a
// generous slack for the OS to reap the child.
func waitedAlive(cmd *exec.Cmd) bool {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return false
	case <-time.After(2 * time.Second):
		return true
	}
}
