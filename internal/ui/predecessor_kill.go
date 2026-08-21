package ui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/picker"
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
func KillSupersededTargetTreePreview() {
	sessionDir := strings.TrimSpace(os.Getenv(picker.TargetPreviewSessionEnv))
	if sessionDir == "" {
		return
	}
	picker.ClaimPreviewProcess(sessionDir, picker.TargetPreviewPIDFile)
}

// TargetTreePreviewSessionActive reports whether this helper was launched by
// a live target picker rather than invoked directly for a diagnostic/test
// preview command.
func TargetTreePreviewSessionActive() bool {
	return strings.TrimSpace(os.Getenv(picker.TargetPreviewSessionEnv)) != ""
}

// killSupersededPredecessor is the Windows orphan-child workaround for
// preview handlers fzf re-spawns on every keystroke / focus change.
// fzf's `change:reload:` rebinding spawns a new child on every keystroke,
// and on macOS/Linux fzf SIGTERMs the previous child so it dies before
// the new one's rg starts. On Windows fzf does NOT send SIGTERM (Part
// Two Item 7 / v0.6.1 finding 2), so the previous child runs to
// completion — typically a multi-second ripgrep scan over the full
// corpus — in parallel with the new child. Four keystrokes = four
// concurrent scans, fighting for CPU and Defender. This also defeats
// Item 6a's prefix-extension memo because new children read the memo
// file before any prior child has finished writing it.
//
// The fix is decentralized PID coordination via a file in the same
// tmpdir as the picker's checkpoint. Each new child:
//
//  1. Reads the PID file. If it contains a non-zero PID different from
//     its own, it terminates that process (Kill = TerminateProcess on
//     Windows, SIGKILL on POSIX).
//  2. Writes its own PID into the file atomically (rename) so the next
//     child sees it.
//
// `bucket` separates handler classes. Content-match-list and
// file-preview both spawn from the same picker tmpdir; using a shared
// PID file would cause them to kill each other on every event. Each
// handler owns its own bucket name (see the predecessorBucket*
// constants).
//
// PID reuse risk: PID files live only for the picker session. The
// window for an OS to recycle a PID into an unrelated process during
// that lifetime is small enough that we accept it rather than building
// a process-fingerprint scheme. The kill is a no-op when the named
// process is already gone (os.Process.Kill on a dead PID returns an
// error that we silently swallow — the dead/missing case is the common
// one on POSIX).
//
// Race: two children that read concurrently may both try to kill the
// same predecessor; Kill is idempotent. They both then race to write
// their own PID via rename; one wins, the other's PID is lost (only
// matters for a third sibling, which is rare). Acceptable.
func killSupersededPredecessor(checkpointPath, bucket string) {
	if strings.TrimSpace(checkpointPath) == "" || bucket == "" {
		return
	}
	pidPath := predecessorPidPath(checkpointPath, bucket)
	if pidPath == "" {
		return
	}
	self := os.Getpid()
	prior := readPredecessorPID(pidPath)
	writePredecessorPID(pidPath, self)
	if prior == 0 || prior == self {
		return
	}
	proc, err := os.FindProcess(prior)
	if err != nil {
		// On Windows FindProcess fails for non-existent PIDs.
		return
	}
	// Kill returns an error on POSIX when the process is already gone
	// (ESRCH); swallow because the gone-case is exactly what we wanted.
	// On Windows, returns an error if the process handle can't be opened
	// (also gone-equivalent).
	_ = proc.Kill()
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
