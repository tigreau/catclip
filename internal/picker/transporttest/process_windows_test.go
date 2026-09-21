package transporttest

import (
	"context"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func configureTransportCancellation(_ *testing.T, cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Kill only this test-owned fzf tree, not other fzf/shell processes.
		if err := exec.CommandContext(ctx, "taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
