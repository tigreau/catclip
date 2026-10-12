package ui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

// Predecessor-kill identifiers. Each preview handler that wants the
// Windows orphan-kill behavior owns ONE bucket so different handlers
// don't kill each other (content-match-list and file-preview both fire
// off the same picker; they have to track predecessors separately).
const (
	predecessorBucketContentMatch = "content-match-pid.txt"
	predecessorBucketFilePreview  = "file-preview-pid.txt"
)

// KillSupersededTargetTreePreview stops the previous focus-preview helper in
// this target picker session. fzf terminates superseded helpers on Unix; this
// explicit coordination supplies the equivalent behavior on Windows.
func KillSupersededTargetTreePreview() error {
	sessionDir := strings.TrimSpace(os.Getenv(picker.TargetPreviewSessionEnv))
	if sessionDir == "" {
		return nil
	}
	return picker.ClaimPreviewProcess(sessionDir, picker.TargetPreviewPIDFile)
}

// TargetTreePreviewSessionActive reports whether this helper was launched by
// a live target picker rather than invoked directly for a diagnostic/test
// preview command.
func TargetTreePreviewSessionActive() bool {
	return strings.TrimSpace(os.Getenv(picker.TargetPreviewSessionEnv)) != ""
}

// killSupersededPredecessor delegates managed Windows work to the per-picker
// role claimed at executable entry. Direct library/diagnostic calls use the
// artifact's separate reload and preview buckets. Windows claims are locked
// and validate process creation identity before termination; helper jobs stop
// descendants even when the helper is killed abruptly.
//
// Unix keeps its existing PID bookkeeping pending native route coverage.
// Normal fzf supersession there kills the entire inherited process group.
func killSupersededPredecessor(checkpointPath, bucket string) error {
	if strings.TrimSpace(checkpointPath) == "" || bucket == "" {
		return nil
	}
	pidPath := predecessorPidPath(checkpointPath, bucket)
	if pidPath == "" {
		return nil
	}
	if runtime.GOOS == "windows" {
		return platform.ClaimPickerWork(pidPath)
	}
	self := os.Getpid()
	prior := readPredecessorPID(pidPath)
	writePredecessorPID(pidPath, self)
	// Library callers may render a reference frame before launching a helper
	// with the same artifact. The helper must never terminate its launcher.
	if prior == 0 || prior == self || prior == os.Getppid() {
		return nil
	}
	proc, err := os.FindProcess(prior)
	if err != nil {
		// On Windows FindProcess fails for non-existent PIDs.
		return nil
	}
	defer proc.Release()
	// Kill returns an error on POSIX when the process is already gone
	// (ESRCH); swallow because the gone-case is exactly what we wanted.
	// On Windows, returns an error if the process handle can't be opened
	// (also gone-equivalent).
	_ = proc.Kill()
	return nil
}

// predecessorPidPath conventionally lives next to the checkpoint, with
// the bucket as the filename. The parent picker driver owns the tmpdir
// and removes it on picker close, so the PID file's lifetime is bounded.
func predecessorPidPath(checkpointPath, bucket string) string {
	if strings.TrimSpace(checkpointPath) == "" || bucket == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(checkpointPath), bucket)
}

func readPredecessorPID(path string) int {
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			// Unexpected error — treat as empty, do not crash the picker.
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

// writePredecessorPID writes atomically via rename so an in-flight
// reader cannot see a torn/partial PID value. Errors are silent — a
// failed write costs the next keystroke a no-kill (back to the orphan
// problem for that one transition, not a crash).
func writePredecessorPID(path string, pid int) {
	if path == "" {
		return
	}
	body := []byte(strconv.Itoa(pid))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
