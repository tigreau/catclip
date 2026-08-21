package picker

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestPreviewProcessSessionClaimsAndCleansCurrentProcess(t *testing.T) {
	dir := t.TempDir()
	ClaimPreviewProcess(dir, TargetPreviewPIDFile)
	pidPath := previewPIDPath(dir, TargetPreviewPIDFile)
	if got := readPreviewPID(pidPath); got != os.Getpid() {
		t.Fatalf("recorded pid = %d, want %d", got, os.Getpid())
	}

	// The self-PID guard makes this safe in the process that owns the test.
	StopPreviewProcess(dir, TargetPreviewPIDFile)
	if got := readPreviewPID(pidPath); got != 0 {
		t.Fatalf("pid after cleanup = %d, want 0", got)
	}
}

func TestPreviewProcessPIDCanReplaceExistingClaim(t *testing.T) {
	dir := t.TempDir()
	pidPath := previewPIDPath(dir, TargetPreviewPIDFile)
	writePreviewPID(pidPath, 101)
	writePreviewPID(pidPath, 202)
	if got := readPreviewPID(pidPath); got != 202 {
		t.Fatalf("replacement pid = %d, want 202", got)
	}
}

func TestPreviewProcessSessionRejectsEmptyCoordinates(t *testing.T) {
	if got := previewPIDPath("", TargetPreviewPIDFile); got != "" {
		t.Fatalf("empty session path = %q", got)
	}
	if got := previewPIDPath(t.TempDir(), ""); got != "" {
		t.Fatalf("empty bucket path = %q", got)
	}
}

func TestPreviewProcessClaimTerminatesPriorHelper(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real child process")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPreviewProcessBlockingHelper$")
	cmd.Env = append(os.Environ(), "CATCLIP_TEST_PREVIEW_PROCESS_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		default:
		}
	})

	dir := t.TempDir()
	writePreviewPID(previewPIDPath(dir, TargetPreviewPIDFile), cmd.Process.Pid)
	ClaimPreviewProcess(dir, TargetPreviewPIDFile)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("prior preview helper PID %d survived replacement", cmd.Process.Pid)
	}
}

func TestPreviewProcessBlockingHelper(t *testing.T) {
	if os.Getenv("CATCLIP_TEST_PREVIEW_PROCESS_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}
