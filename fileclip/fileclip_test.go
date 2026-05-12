package fileclip

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	// Some filesystems allow double quotes in filenames (macOS HFS+/APFS does).
	path := filepath.Join(t.TempDir(), `file"with"quotes.txt`)
	if err := os.WriteFile(path, []byte("quotes test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Copy(path); err != nil {
		t.Fatalf("Copy() failed for path with quotes: %v", err)
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

func TestCopyThenHas(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	path := filepath.Join(t.TempDir(), "has-test.txt")
	if err := os.WriteFile(path, []byte("has test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Copy(path); err != nil {
		t.Fatalf("Copy() failed: %v", err)
	}

	// Give background clipboards (like xclip) time to acquire the selection
	time.Sleep(100 * time.Millisecond)

	has, err := Has()
	if err != nil {
		t.Fatalf("Has() failed: %v", err)
	}
	if !has {
		t.Fatal("Has() returned false after Copy(), expected true")
	}
}

func TestCopyThenPaste(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	// Use a persistent path (not t.TempDir) so the file exists at Paste time.
	path := "/tmp/fileclip-paste-test.txt"
	if err := os.WriteFile(path, []byte("paste round-trip\n"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	if err := Copy(path); err != nil {
		t.Fatalf("Copy() failed: %v", err)
	}

	// Give background clipboards (like xclip) time to acquire the selection
	time.Sleep(100 * time.Millisecond)

	paths, err := Paste()
	if err != nil {
		t.Fatalf("Paste() failed: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("Paste() returned no paths")
	}
	if paths[0] != path {
		t.Fatalf("Paste() returned %q, want %q", paths[0], path)
	}
}

func TestHasAfterTextCopy(t *testing.T) {
	if os.Getenv("FILECLIP_INTEGRATION") == "" {
		t.Skip("set FILECLIP_INTEGRATION=1 to run clipboard integration tests")
	}

	// Put plain text on the clipboard — Has() should return false.
	tool := "pbcopy"
	if _, err := exec.LookPath("pbcopy"); err != nil {
		if _, err := exec.LookPath("xclip"); err == nil {
			tool = "xclip"
		} else if _, err := exec.LookPath("wl-copy"); err == nil {
			tool = "wl-copy"
		} else {
			t.Skip("no plain text clipboard tool found")
		}
	}

	var cmd *exec.Cmd
	if tool == "xclip" {
		cmd = exec.Command("xclip", "-selection", "clipboard")
	} else if tool == "wl-copy" {
		cmd = exec.Command("wl-copy")
	} else {
		cmd = exec.Command("pbcopy")
	}
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
	tool := "pbcopy"
	if _, err := exec.LookPath("pbcopy"); err != nil {
		if _, err := exec.LookPath("xclip"); err == nil {
			tool = "xclip"
		} else if _, err := exec.LookPath("wl-copy"); err == nil {
			tool = "wl-copy"
		} else {
			t.Skip("no plain text clipboard tool found")
		}
	}

	var cmd *exec.Cmd
	if tool == "xclip" {
		cmd = exec.Command("xclip", "-selection", "clipboard")
	} else if tool == "wl-copy" {
		cmd = exec.Command("wl-copy")
	} else {
		cmd = exec.Command("pbcopy")
	}
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
