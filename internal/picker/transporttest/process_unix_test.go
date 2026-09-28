//go:build !windows

package transporttest

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
)

func configureTransportCancellation(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if runtime.GOOS == "linux" {
		// CI provides a private controlling terminal. Our separate process
		// group must own its foreground too: Linux otherwise stops terminal
		// operations with job-control signals before fzf can run load actions.
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			t.Fatalf("Linux fzf transport requires a controlling terminal: %v", err)
		}
		t.Cleanup(func() { _ = tty.Close() })
		cmd.SysProcAttr.Foreground = true
		cmd.SysProcAttr.Ctty = int(tty.Fd())
	}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
