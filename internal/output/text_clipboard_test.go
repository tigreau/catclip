package output

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/tigreau/catclip/internal/platform"
)

// encodeClipboard runs the encoder with a small chunk size, so CRLF pairs and
// surrogate pairs land on chunk boundaries, and returns the decoded text.
func encodeClipboard(payload []byte, chunk int) string {
	e := clipboardEncoder{src: payload}
	var units []uint16
	buf := make([]uint16, chunk)
	for {
		n := e.fill(buf)
		if n == 0 {
			break
		}
		units = append(units, buf[:n]...)
	}
	return string(utf16.Decode(units))
}

func TestClipboardEncoderLineBreaksInvalidBytesAndChunks(t *testing.T) {
	for _, tt := range []struct{ name, in, want string }{
		{"empty", "", ""},
		{"no newline", "a—b", "a—b"},
		{"LF becomes CRLF", "a\nb\n", "a\r\nb\r\n"},
		{"CRLF kept", "a\r\nb", "a\r\nb"},
		{"mixed", "a\r\nb\nc", "a\r\nb\r\nc"},
		{"leading LF", "\nx", "\r\nx"},
		{"lone CR kept", "a\rb", "a\rb"},
		{"invalid UTF-8", "a\xffb", "a�b"},
		{"non-ASCII kept", "é 😀 中 Bush hid the facts", "é 😀 中 Bush hid the facts"},
		{"emoji runs", "😀😀😀\n😀", "😀😀😀\r\n😀"},
	} {
		for _, chunk := range []int{2, 3, 4, 4096} {
			if got := encodeClipboard([]byte(tt.in), chunk); got != tt.want {
				t.Errorf("%s (chunk %d): got %q, want %q", tt.name, chunk, got, tt.want)
			}
		}
		if got, want := clipboardUTF16Len([]byte(tt.in)), len(utf16.Encode([]rune(tt.want))); got != want {
			t.Errorf("%s: clipboardUTF16Len = %d, want %d", tt.name, got, want)
		}
	}
}

// Windows copies must go through the clipboard API, never a child process:
// with clip.exe the result depended on the terminal's code page.
func TestWindowsTextCopiesUseNativeClipboard(t *testing.T) {
	var got []string
	saved := nativeTextClipboard
	nativeTextClipboard = func(payload []byte) error { got = append(got, encodeClipboard(payload, 4096)); return nil }
	t.Cleanup(func() { nativeTextClipboard = saved })
	t.Setenv("PATH", "") // no clip.exe to fall back on

	env := RuntimeEnvironment{Platform: "windows"}
	stats, err := emitBufferedToTextClipboard(env, []byte("x — y\n"), 0, platform.Palette{})
	if err != nil {
		t.Fatalf("buffered copy: %v", err)
	}
	if stats.SinkName != NativeClipboardSinkName || stats.PayloadBytes != int64(len("x — y\n")) {
		t.Fatalf("stats = %+v", stats)
	}
	if _, err := streamToTextClipboard(env, func(w io.Writer) error {
		_, err := w.Write([]byte("streamed é\n"))
		return err
	}, platform.Palette{}, 64); err != nil {
		t.Fatalf("streamed copy: %v", err)
	}
	want := []string{"x — y\r\n", "streamed é\r\n"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("clipboard received %q, want %q", got, want)
	}

	// Other platforms keep their clipboard command.
	if useNativeTextClipboard("wsl") || useNativeTextClipboard("macos") || useNativeTextClipboard("linux") {
		t.Fatal("only native Windows may use the clipboard API")
	}
}

func TestWSLClipboardCommandSwitchesToUTF8(t *testing.T) {
	dir := t.TempDir()
	name := "cmd.exe"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".EXE")
	}
	cmd, err := ClipboardCommand("wsl", platform.Palette{})
	if err != nil {
		t.Fatalf("ClipboardCommand(wsl): %v", err)
	}
	if !strings.EqualFold(filepath.Base(cmd.Path), "cmd.exe") {
		t.Fatalf("command = %q, want cmd.exe", cmd.Path)
	}
	if args := strings.Join(cmd.Args[1:], " "); args != "/d /c chcp 65001 >nul & clip.exe" {
		t.Fatalf("args = %q", args)
	}
	// Started from cmd.exe's own folder, never the user's Linux folder.
	if !strings.EqualFold(filepath.Clean(cmd.Dir), filepath.Clean(dir)) {
		t.Fatalf("dir = %q, want %q", cmd.Dir, dir)
	}
}
