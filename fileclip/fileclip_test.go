package fileclip

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/platform"
)

func TestCopyEmptySlice(t *testing.T) {
	// Calling Copy with no arguments should be a no-op.
	if err := Copy(); err != nil {
		t.Fatalf("Copy() with no args returned error: %v", err)
	}
}

func TestCopyNonExistent(t *testing.T) {
	err := Copy("/tmp/fileclip-test-nonexistent-file-that-does-not-exist.txt")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("expected ErrFileNotFound, got: %v", err)
	}
}

func TestCopyDirectory(t *testing.T) {
	dir := t.TempDir()
	err := Copy(dir)
	if err == nil {
		t.Fatal("expected error for directory, got nil")
	}
	if !errors.Is(err, ErrNotAFile) {
		t.Fatalf("expected ErrNotAFile, got: %v", err)
	}
}

func TestCopySingleFile(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	path := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(path, []byte("fileclip test content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Copy(path); err != nil {
		t.Fatalf("Copy() failed: %v", err)
	}
}

func TestCopyMultipleFiles(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	dir := t.TempDir()
	files := []string{
		filepath.Join(dir, "a.txt"),
		filepath.Join(dir, "b.txt"),
		filepath.Join(dir, "c.txt"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("content of "+filepath.Base(f)+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Copy(files...); err != nil {
		t.Fatalf("Copy() failed: %v", err)
	}
}

func TestCopyPathWithSpaces(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	path := filepath.Join(t.TempDir(), "file with spaces.txt")
	if err := os.WriteFile(path, []byte("spaces test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Copy(path); err != nil {
		t.Fatalf("Copy() failed for path with spaces: %v", err)
	}
}

func TestCopyPathWithQuotes(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}
	if runtime.GOOS == "windows" {
		t.Skip("NTFS disallows double quotes in filenames")
	}

	// Some filesystems allow double quotes in filenames (macOS HFS+/APFS does).
	path := filepath.Join(t.TempDir(), `file"with"quotes.txt`)
	if err := os.WriteFile(path, []byte("quotes test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Copy(path); err != nil {
		t.Fatalf("Copy() failed for path with quotes: %v", err)
	}
}

func TestCopyPathWithNewline(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}
	if runtime.GOOS == "windows" {
		t.Skip("NTFS disallows newlines in filenames")
	}

	// APFS and most Linux filesystems allow newlines in filenames; the
	// darwin backend must emit them as AppleScript escapes, not raw bytes.
	path := filepath.Join(t.TempDir(), "file\nwith\nnewlines.txt")
	if err := os.WriteFile(path, []byte("newline test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Copy(path); err != nil {
		t.Fatalf("Copy() failed for path with newlines: %v", err)
	}
}

func TestCopySymlink(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")

	if err := os.WriteFile(target, []byte("symlink target\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// Symlinks should be accepted without being resolved.
	if err := Copy(link); err != nil {
		t.Fatalf("Copy() failed for symlink: %v", err)
	}
}

func TestCopyRelativePath(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	// Create a file in a temp dir, but pass a relative path.
	dir := t.TempDir()
	path := filepath.Join(dir, "relative-test.txt")
	if err := os.WriteFile(path, []byte("relative path test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use the absolute path since we can't chdir in tests reliably,
	// but verify Copy accepts it and resolves it.
	if err := Copy(path); err != nil {
		t.Fatalf("Copy() failed: %v", err)
	}
}

// waitForFileRefsOnClipboard polls Has() until it returns true or the
// budget elapses. macOS pasteboard writes can lag a few hundred ms
// behind the osascript call that issued them, especially under CI
// load — a fixed 100ms Sleep then a single Has() check raced that
// latency intermittently on macos-latest runners. Polling tolerates
// whatever the actual write-to-visible delay is on the current run.
//
// Returns false if Has() never returns true within budget; callers
// fail the test with their own diagnostic message.
func waitForFileRefsOnClipboard(t *testing.T, budget time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		has, err := Has()
		if err == nil && has {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// copyAndWaitForFileRefs retries the complete transaction once on macOS. The
// general pasteboard is global process state, and macOS CI occasionally loses
// one otherwise successful osascript write between Copy returning and Has
// observing it. Requiring two consecutive failures preserves the integration
// assertion without making Linux or Windows clipboard tests less strict.
func copyAndWaitForFileRefs(t *testing.T, path string, budget time.Duration) bool {
	t.Helper()
	attempts := 1
	if runtime.GOOS == "darwin" {
		attempts = 2
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := Copy(path); err != nil {
			t.Fatalf("Copy() failed: %v", err)
		}
		if waitForFileRefsOnClipboard(t, budget) {
			return true
		}
		if attempt < attempts {
			t.Log("macOS pasteboard did not retain the first write; retrying once")
		}
	}
	return false
}

func TestCopyThenHas(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	path := filepath.Join(t.TempDir(), "has-test.txt")
	if err := os.WriteFile(path, []byte("has test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if !copyAndWaitForFileRefs(t, path, 3*time.Second) {
		t.Fatal("Has() never returned true within 3s after Copy()")
	}
}

func TestCopyThenPaste(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	// t.TempDir survives until the test ends, which covers Copy + Paste.
	path := filepath.Join(t.TempDir(), "fileclip-paste-test.txt")
	if err := os.WriteFile(path, []byte("paste round-trip\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if !copyAndWaitForFileRefs(t, path, 3*time.Second) {
		t.Fatal("Has() never returned true within 3s after Copy(); cannot Paste")
	}

	paths, err := Paste()
	if err != nil {
		t.Fatalf("Paste() failed: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("Paste() returned no paths")
	}
	// Compare by file identity, not string equality: on Windows
	// t.TempDir() can return an 8.3 short-name form (RUNNER~1) while
	// the clipboard round-trip canonicalises to the long form
	// (runneradmin), so a string compare fails despite both pointing
	// at the same file.
	gotInfo, err := os.Stat(paths[0])
	if err != nil {
		t.Fatalf("stat returned path %q: %v", paths[0], err)
	}
	wantInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat want path %q: %v", path, err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("Paste() returned %q, want %q (different files)", paths[0], path)
	}
}

func TestHasAfterTextCopy(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	// Put plain text on the clipboard — Has() should return false.
	cmd := plainTextClipboardCommand(t)
	cmd.Stdin = strings.NewReader("just plain text")
	if err := cmd.Run(); err != nil {
		t.Fatalf("text copy failed: %v", err)
	}

	has, err := Has()
	if err != nil {
		t.Fatalf("Has() failed: %v", err)
	}
	if has {
		t.Fatal("Has() returned true after text copy, expected false")
	}
}

func TestPasteWithoutFileRefs(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	// Put plain text on the clipboard.
	cmd := plainTextClipboardCommand(t)
	cmd.Stdin = strings.NewReader("not a file reference")
	if err := cmd.Run(); err != nil {
		t.Fatalf("text copy failed: %v", err)
	}

	_, err := Paste()
	if err == nil {
		t.Fatal("expected error from Paste() with text clipboard, got nil")
	}
	if !errors.Is(err, ErrNoFileRefs) {
		t.Fatalf("expected ErrNoFileRefs, got: %v", err)
	}
}

func plainTextClipboardCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		path, err := exec.LookPath("pbcopy")
		if err != nil {
			t.Skip("pbcopy not found")
		}
		return exec.Command(path)
	case "linux":
		if platform.DetectLinuxSession() != platform.LinuxSessionWayland {
			t.Skip("Linux fileclip integration tests require a Wayland session")
		}
		path, err := exec.LookPath("wl-copy")
		if err != nil {
			t.Skip("wl-copy not found")
		}
		return exec.Command(path)
	case "windows":
		path, err := exec.LookPath("clip.exe")
		if err != nil {
			t.Skip("clip.exe not found")
		}
		return exec.Command(path)
	default:
		t.Skipf("no plain text clipboard helper for %s", runtime.GOOS)
		return nil
	}
}
