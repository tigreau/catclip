//go:build linux

package output

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/fileclip"
	"github.com/tigreau/catclip/internal/platform"
)

// Writes the Windows clipboard from WSL, so it runs only when asked:
// CATCLIP_TEST_REAL_CLIPBOARD=1 inside WSL.
func TestRealWSLClipboardRoundTripsUnicode(t *testing.T) {
	if os.Getenv("CATCLIP_TEST_REAL_CLIPBOARD") != "1" || platform.Detect() != "wsl" {
		t.Skip("set CATCLIP_TEST_REAL_CLIPBOARD=1 inside WSL to overwrite the Windows clipboard")
	}
	t.Chdir(t.TempDir()) // a Linux folder: cmd.exe must not warn about UNC paths
	readBack := func() string {
		out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			`[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false); [Console]::Out.Write((Get-Clipboard -Raw))`).Output()
		if err != nil {
			t.Fatalf("read clipboard: %v", err)
		}
		return string(out)
	}
	for _, tt := range []struct{ in, want string }{
		{"a—b é 😀 中\nz", "a—b é 😀 中\r\nz"},
		{"中文字符", "中文字符"},
		{"—", "—"},
		{"<file path=\"cmd/catclip/main.go\" lines=\"13-36\">\n// mostly *off* CPU — waiting\n</file>\n", "<file path=\"cmd/catclip/main.go\" lines=\"13-36\">\r\n// mostly *off* CPU — waiting\r\n</file>\r\n"},
	} {
		cmd, err := ClipboardCommand("wsl", platform.Palette{})
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stdin = strings.NewReader(tt.in)
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("copy %q: %v (%s)", tt.in, err, stderr.String())
		}
		if stderr.Len() > 0 {
			t.Errorf("copy %q wrote to stderr: %q", tt.in, stderr.String())
		}
		if got := readBack(); got != tt.want {
			t.Errorf("round trip: got %q, want %q", got, tt.want)
		}
	}
}

// Reads the Windows clipboard from WSL in one PowerShell call: text arrives
// with LF line breaks; a copied file arrives as its Linux path.
func TestRealWSLClipboardRead(t *testing.T) {
	if os.Getenv("CATCLIP_TEST_REAL_CLIPBOARD") != "1" || platform.Detect() != "wsl" {
		t.Skip("set CATCLIP_TEST_REAL_CLIPBOARD=1 inside WSL to overwrite the Windows clipboard")
	}
	t.Chdir(t.TempDir())
	text := "<file path=\"src/a.go\">\nfunc A() {} // — é 😀 中\n</file>\n"
	cmd, err := ClipboardCommand("wsl", platform.Palette{})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadClipboard("wsl", platform.Palette{})
	if err != nil || got.Text != text || got.Files != nil {
		t.Fatalf("text read: %+v %v", got, err)
	}
	file := filepath.Join("/mnt/c/Users/Public", "catclip-wsl-read-test.txt")
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Skipf("no writable Windows folder: %v", err)
	}
	t.Cleanup(func() { os.Remove(file) })
	if err := fileclip.Copy(file); err != nil {
		t.Fatal(err)
	}
	got, err = ReadClipboard("wsl", platform.Palette{})
	if err != nil || len(got.Files) != 1 || got.Files[0] != file {
		t.Fatalf("file read: %+v %v (want %s)", got, err, file)
	}
}
