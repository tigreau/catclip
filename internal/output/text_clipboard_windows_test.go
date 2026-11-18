//go:build windows

package output

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

var (
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procLstrlenW         = kernel32.NewProc("lstrlenW")
)

func readWindowsClipboardText(t *testing.T) string {
	t.Helper()
	if err := openClipboardWithRetry(); err != nil {
		t.Fatal(err)
	}
	defer procCloseClipboard.Call()
	h, _, err := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		t.Fatalf("GetClipboardData: %v", err)
	}
	ptr, _, err := procGlobalLock.Call(h)
	if ptr == 0 {
		t.Fatalf("GlobalLock: %v", err)
	}
	defer procGlobalUnlock.Call(h)
	n, _, _ := procLstrlenW.Call(ptr)
	buf := make([]uint16, n)
	if n > 0 {
		procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), ptr, n*2)
	}
	return syscall.UTF16ToString(buf)
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
