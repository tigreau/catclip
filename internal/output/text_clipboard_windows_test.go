//go:build windows

package output

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tigreau/catclip/fileclip"
	"github.com/tigreau/catclip/internal/platform"
	"testing"
)

func readWindowsClipboardText(t *testing.T) string {
	t.Helper()
	content, err := readWindowsClipboard()
	if err != nil {
		t.Fatal(err)
	}
	return content.Text
}

// Writes the real clipboard, so it runs only when asked:
// CATCLIP_TEST_REAL_CLIPBOARD=1 go test ./internal/output -run RealClipboard
func TestRealClipboardRoundTripsUnicode(t *testing.T) {
	if os.Getenv("CATCLIP_TEST_REAL_CLIPBOARD") != "1" {
		t.Skip("set CATCLIP_TEST_REAL_CLIPBOARD=1 to overwrite the real clipboard")
	}
	for _, text := range []string{
		"a—b é 😀 中\r\nz",
		"中文字符",               // no ASCII: clip.exe's UTF-16 guess failed here
		"—",                  // a single non-ASCII character
		"Bush hid the facts", // ASCII that IsTextUnicode reads as UTF-16
		"x",
		"<file path=\"cmd/catclip/main.go\" lines=\"13-36\">\r\n// mostly *off* CPU — waiting\r\n</file>\r\n",
	} {
		if err := setWindowsClipboardText([]byte(text)); err != nil {
			t.Fatalf("set %q: %v", text, err)
		}
		if got := readWindowsClipboardText(t); got != text {
			t.Errorf("round trip: got %q, want %q", got, text)
		}
	}
}

// Reads what was just written: text arrives with LF line breaks; copied files
// arrive as paths, not text. Runs only with CATCLIP_TEST_REAL_CLIPBOARD=1.
func TestRealClipboardReadTextAndCopiedFiles(t *testing.T) {
	if os.Getenv("CATCLIP_TEST_REAL_CLIPBOARD") != "1" {
		t.Skip("set CATCLIP_TEST_REAL_CLIPBOARD=1 to overwrite the real clipboard")
	}
	text := "<file path=\"src/a.go\">\nfunc A() {} // — é 😀 中\n</file>\n"
	if err := setWindowsClipboardText([]byte(text)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadClipboard("windows", platform.Palette{})
	if err != nil || got.Text != text || got.Files != nil {
		t.Fatalf("text read: %+v %v", got, err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "reply [1].txt") // wildcard characters on purpose
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fileclip.Copy(file); err != nil {
		t.Fatal(err)
	}
	got, err = ReadClipboard("windows", platform.Palette{})
	if err != nil || len(got.Files) != 1 || !strings.EqualFold(got.Files[0], file) {
		t.Fatalf("file read: %+v %v (want %s)", got, err, file)
	}
}
