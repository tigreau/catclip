package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPredecessorPidPathDerivesFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "scope.json")
	got := predecessorPidPath(cp, predecessorBucketContentMatch)
	if got != filepath.Join(dir, "content-match-pid.txt") {
		t.Fatalf("unexpected pid path: %s", got)
	}
	if predecessorPidPath("", predecessorBucketContentMatch) != "" {
		t.Fatal("empty checkpoint must yield empty pid path")
	}
	if predecessorPidPath("   ", predecessorBucketContentMatch) != "" {
		t.Fatal("whitespace-only checkpoint must yield empty pid path")
	}
	if predecessorPidPath(cp, "") != "" {
		t.Fatal("empty bucket must yield empty pid path")
	}
}

func TestPredecessorPidPathBucketsAreDistinct(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "scope.json")
	cm := predecessorPidPath(cp, predecessorBucketContentMatch)
	fp := predecessorPidPath(cp, predecessorBucketFilePreview)
	if cm == fp {
		t.Fatalf("content-match and file-preview buckets must yield different paths; both got %s", cm)
	}
}

func TestReadPredecessorPIDMissingFileReturnsZero(t *testing.T) {
	if readPredecessorPID(filepath.Join(t.TempDir(), "nope.txt")) != 0 {
		t.Fatal("missing file should return 0")
	}
	if readPredecessorPID("") != 0 {
		t.Fatal("empty path should return 0")
	}
}

func TestReadPredecessorPIDMalformedReturnsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pid.txt")
	cases := []string{"", "  ", "not-a-number", "-5", "0", "abc123"}
	for _, body := range cases {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := readPredecessorPID(path); got != 0 {
			t.Fatalf("body %q want 0, got %d", body, got)
		}
	}
}

func TestWriteThenReadPredecessorPIDRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pid.txt")
	writePredecessorPID(path, 12345)
	if got := readPredecessorPID(path); got != 12345 {
		t.Fatalf("want 12345, got %d", got)
	}
	// Overwrite must replace, not append.
	writePredecessorPID(path, 67890)
	if got := readPredecessorPID(path); got != 67890 {
		t.Fatalf("after overwrite want 67890, got %d", got)
	}
}

func TestWritePredecessorPIDIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pid.txt")
	writePredecessorPID(path, 999)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover tmp after atomic write: %s", e.Name())
		}
	}
}

func TestWritePredecessorPIDEmptyPathIsNoop(t *testing.T) {
	// Must not panic.
	writePredecessorPID("", 123)
}

func TestKillSupersededPredecessorEmptyArgIsNoop(t *testing.T) {
	// Must not panic and must not touch the disk.
	killSupersededPredecessor("", predecessorBucketContentMatch)
	killSupersededPredecessor("   ", predecessorBucketContentMatch)
	killSupersededPredecessor("/tmp/anything.json", "")
}

func TestKillSupersededPredecessorFirstChildHasNoPriorToKill(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "scope.json")
	// First child: no pid file yet. After the call, the pid file should
	// contain THIS process's PID so the next child can target it.
	killSupersededPredecessor(cp, predecessorBucketContentMatch)
	got := readPredecessorPID(predecessorPidPath(cp, predecessorBucketContentMatch))
	if got != os.Getpid() {
		t.Fatalf("want self pid %d, got %d", os.Getpid(), got)
	}
}

func TestKillSupersededPredecessorSwallowsDeadPID(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "scope.json")
	// Seed the pid file with a PID that's almost certainly dead. The
	// kill attempt must not panic, must not error-out the caller, and
	// must replace the file with our own pid.
	writePredecessorPID(predecessorPidPath(cp, predecessorBucketContentMatch), 999999)
	killSupersededPredecessor(cp, predecessorBucketContentMatch)
	got := readPredecessorPID(predecessorPidPath(cp, predecessorBucketContentMatch))
	if got != os.Getpid() {
		t.Fatalf("want self pid %d, got %d", os.Getpid(), got)
	}
}

func TestKillSupersededPredecessorIgnoresSelfPID(t *testing.T) {
	dir := t.TempDir()
	cp := filepath.Join(dir, "scope.json")
	// If the file already holds OUR pid (re-entrant call somehow), we
	// must not try to kill ourselves. Just rewrite and continue.
	writePredecessorPID(predecessorPidPath(cp, predecessorBucketContentMatch), os.Getpid())
	killSupersededPredecessor(cp, predecessorBucketContentMatch)
	// Test process is still alive — that's the assertion.
}

// TestKillSupersededPredecessorActuallyKillsLiveProcess spawns a long-
// sleeping child, records its PID, calls killSupersededPredecessor,
// and asserts the child is gone. This is the only test that exercises
// the real OS Kill path; the others guard the bookkeeping.
func TestKillSupersededPredecessorActuallyKillsLiveProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real child process")
	}
	dir := t.TempDir()
	cp := filepath.Join(dir, "scope.json")

	cmd := startLongRunningHelper(t)
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	writePredecessorPID(predecessorPidPath(cp, predecessorBucketContentMatch), cmd.Process.Pid)
	killSupersededPredecessor(cp, predecessorBucketContentMatch)

	if waitedAlive(cmd) {
		t.Fatalf("expected predecessor PID %d to be dead", cmd.Process.Pid)
	}
}

// TestKillSupersededPredecessorBucketsDoNotKillEachOther ensures the
// content-match-list and file-preview handlers can coexist in the same
// picker tmpdir without one killing the other's child on every event.
func TestKillSupersededPredecessorBucketsDoNotKillEachOther(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real child process")
	}
	dir := t.TempDir()
	cp := filepath.Join(dir, "scope.json")

	// Seed the file-preview bucket with a live helper PID.
	helper := startLongRunningHelper(t)
	t.Cleanup(func() { _ = helper.Process.Kill() })
	writePredecessorPID(predecessorPidPath(cp, predecessorBucketFilePreview), helper.Process.Pid)

	// Trigger a content-match-list kill cycle. The helper PID lives in a
	// DIFFERENT bucket, so this call must leave it alone.
	killSupersededPredecessor(cp, predecessorBucketContentMatch)

	if !waitedAlive(helper) {
		t.Fatalf("content-match kill should not touch file-preview bucket; helper PID %d died", helper.Process.Pid)
	}
}
