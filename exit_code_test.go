package catclip

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

// TestExitCodeForRegexModifierExtraValueError pins the exit code returned
// when --contains REGEX EXTRA (an unquoted regex split into multiple
// shell args) is rejected by the parser. Caught in review of 3e4b562:
// after the validation_error.go move, cli.RegexModifierExtraValueError
// returns cli.UsageError, which exitWithError classified as exit code 1
// instead of 2. The fix added cli.UsageError to the usage-error
// classification; this test prevents that regression from returning
// silently if anyone moves the constructor again.
//
// Uses the CATCLIP_TEST_RUN_MAIN re-exec pattern (see main_test.go's
// TestMain) so the parser path runs end-to-end through exitWithError.
func TestExitCodeForRegexModifierExtraValueError(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	cases := []struct {
		name string
		args []string
	}{
		{name: "contains regex extra", args: []string{".", "--contains", "foo", "bar", "--paths"}},
		{name: "snippet regex extra", args: []string{".", "--snippet", "foo", "bar", "extra", "--paths"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(exe, tc.args...)
			cmd.Env = append(os.Environ(), "CATCLIP_TEST_RUN_MAIN=1")
			out, runErr := cmd.CombinedOutput()
			exitErr, ok := runErr.(*exec.ExitError)
			if !ok {
				t.Fatalf("expected exec.ExitError, got %T: %v\noutput: %s", runErr, runErr, string(out))
			}
			if got := exitErr.ExitCode(); got != 2 {
				t.Fatalf("exit code = %d, want 2 (usage error)\nargs: %v\noutput: %s", got, tc.args, string(out))
			}
			if !strings.Contains(string(out), "Error:") {
				t.Fatalf("output missing Error: header\nargs: %v\noutput: %s", tc.args, string(out))
			}
		})
	}
}

// TestExitCodeForDiscoveryUsageError pins the exit code returned when
// discovery-side input validation rejects user input — e.g. an absolute
// target path. Added with the v0.6.0 discovery extraction prep: the
// new typed discovery.UsageError must be classified by exitWithError as
// exit code 2. When the type moves into internal/discovery and becomes
// discovery.UsageError, this test still pins the contract from the
// outside.
func TestExitCodeForDiscoveryUsageError(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	// Absolute path is platform-specific. On POSIX a leading slash is
	// absolute; on Windows the parser uses filepath.IsAbs which requires
	// a drive letter (or UNC). The path doesn't need to exist — the
	// resolver rejects absolute paths before touching the filesystem.
	absPath := "/etc/hosts"
	if runtime.GOOS == "windows" {
		absPath = `C:\Windows\System32\drivers\etc\hosts`
	}
	cmd := exec.Command(exe, absPath)
	cmd.Env = append(os.Environ(), "CATCLIP_TEST_RUN_MAIN=1")
	out, runErr := cmd.CombinedOutput()
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exec.ExitError, got %T: %v\noutput: %s", runErr, runErr, string(out))
	}
	if got := exitErr.ExitCode(); got != 2 {
		t.Fatalf("exit code = %d, want 2 (discovery usage error)\noutput: %s", got, string(out))
	}
	if !strings.Contains(string(out), "Absolute paths not allowed") {
		t.Fatalf("output missing 'Absolute paths not allowed' message\noutput: %s", string(out))
	}
}

// TestExitCodeForOutputUsageError pins the exit code returned when
// output-side input validation rejects user input — e.g. --raw paired
// with snippet output. Added with the v0.6.0 output extraction prep:
// the new typed output.UsageError must be classified by exitWithError
// as exit code 2. When the type moves into internal/output and becomes
// output.UsageError, this test still pins the contract from the
// outside.
func TestExitCodeForOutputUsageError(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, ".", "--snippet", "foo", "--raw")
	cmd.Env = append(os.Environ(), "CATCLIP_TEST_RUN_MAIN=1")
	out, runErr := cmd.CombinedOutput()
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exec.ExitError, got %T: %v\noutput: %s", runErr, runErr, string(out))
	}
	if got := exitErr.ExitCode(); got != 2 {
		t.Fatalf("exit code = %d, want 2 (output usage error)\noutput: %s", got, string(out))
	}
	if !strings.Contains(string(out), "--raw cannot be combined with snippet output") {
		t.Fatalf("output missing '--raw cannot be combined with snippet output' message\noutput: %s", string(out))
	}
}

// TestExitCodeForUIUsageError pins the exit code returned when
// UI-side input validation rejects user input — the
// --internal-prediscovered runner enforces "one preview scope" and
// returns ui.UsageError when the caller supplies more. exitWithError
// classifies it as exit code 2, matching the cli.UsageError /
// discovery.UsageError / output.UsageError precedent.
//
// Test writes a real checkpoint before invoking the runner because
// the prediscovered runners read the checkpoint file before they
// validate scope count — passing a nonexistent path would fail with
// file-not-found (exit 1) before reaching the UsageError path.
func TestExitCodeForUIUsageError(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	checkpointDir := t.TempDir()
	checkpointPath := filepath.Join(checkpointDir, "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, checkpointDir, discovery.CheckpointData{
		GitStatus: map[string]string{},
		Entries:   nil,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint: %v", err)
	}

	cmd := exec.Command(exe, "--internal-prediscovered", checkpointPath, "--internal-tree-preview", ".", "--then", ".")
	cmd.Env = append(os.Environ(), "CATCLIP_TEST_RUN_MAIN=1")
	out, runErr := cmd.CombinedOutput()
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exec.ExitError, got %T: %v\noutput: %s", runErr, runErr, string(out))
	}
	if got := exitErr.ExitCode(); got != 2 {
		t.Fatalf("exit code = %d, want 2 (UI usage error)\noutput: %s", got, string(out))
	}
	if !strings.Contains(string(out), "one preview scope") {
		t.Fatalf("output missing 'one preview scope' message\noutput: %s", string(out))
	}
}
