//go:build !windows

package platform

import (
	"bytes"
	"os/exec"
)

// PickerOutput retains the normal Unix launch path. fzf owns the preview and
// reload process groups; descendants must not be moved out of those groups.
func PickerOutput(cmd *exec.Cmd, started func(error)) ([]byte, error) {
	if started == nil {
		return cmd.Output()
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Start()
	started(err)
	if err != nil {
		return nil, err
	}
	err = cmd.Wait()
	return stdout.Bytes(), err
}

// BeginInternalWork is intentionally inert on Unix.
func BeginInternalWork() error { return nil }

// These are only selected by Windows call sites; keep Unix coordination as-is.
func ClaimPickerWork(string) error { return nil }
func StopPickerWork(string) error  { return nil }

func ClaimInternalWork(string) error { return nil }
func InternalWorkClaimPath() string  { return "" }
func InternalWorkCanPublish() bool   { return true }
