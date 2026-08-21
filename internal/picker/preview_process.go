package picker

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	TargetPreviewSessionEnv = "CATCLIP_TARGET_PREVIEW_SESSION"
	TargetPreviewPIDFile    = "target-tree-pid.txt"
)

// ClaimPreviewProcess records this helper as the active process for a picker
// preview bucket and terminates the previously recorded helper, if any.
func ClaimPreviewProcess(sessionDir, bucket string) {
	pidPath := previewPIDPath(sessionDir, bucket)
	if pidPath == "" {
		return
	}
	self := os.Getpid()
	prior := readPreviewPID(pidPath)
	writePreviewPID(pidPath, self)
	if prior != 0 && prior != self {
		killPID(prior)
	}
}

// StopPreviewProcess terminates the last helper recorded for a picker session.
// The picker parent calls this before deleting the session directory so the
// final Windows preview child cannot survive picker exit.
func StopPreviewProcess(sessionDir, bucket string) {
	pidPath := previewPIDPath(sessionDir, bucket)
	if pidPath == "" {
		return
	}
	pid := readPreviewPID(pidPath)
	if pid != 0 && pid != os.Getpid() {
		killPID(pid)
	}
	_ = os.Remove(pidPath)
}

func previewPIDPath(sessionDir, bucket string) string {
	if strings.TrimSpace(sessionDir) == "" || strings.TrimSpace(bucket) == "" {
		return ""
	}
	return filepath.Join(sessionDir, bucket)
}

func readPreviewPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return 0
		}
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func writePreviewPID(path string, pid int) {
	tmp := path + "." + strconv.Itoa(pid) + ".tmp"
	defer os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func killPID(pid int) {
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Kill()
	}
}
