//go:build windows

package output

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// Windows text copies go straight to the clipboard API as CF_UNICODETEXT.
// clip.exe decodes piped bytes by the console code page and guesses UTF-16
// with IsTextUnicode, so the same UTF-8 text arrived intact, as "-", or as
// "ΓÇö" depending on the terminal. Labelled UTF-16 leaves nothing to guess
// and needs no child process.

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory    = kernel32.NewProc("RtlMoveMemory")
)

func init() {
	nativeTextClipboard = setWindowsClipboardText
}

func setWindowsClipboardText(payload []byte) error {
	units := clipboardUTF16Len(payload) + 1 // CF_UNICODETEXT is NUL-terminated
	size := uintptr(units) * 2

	// The clipboard is opened per thread; keep this goroutine on one.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := openClipboardWithRetry(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()

	if r, _, err := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("Error: Could not clear the clipboard: %v", err)
	}
	mem, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if mem == 0 {
		return fmt.Errorf("Error: Could not allocate clipboard memory: %v", err)
	}
	ptr, _, err := procGlobalLock.Call(mem)
	if ptr == 0 {
		procGlobalFree.Call(mem)
		return fmt.Errorf("Error: Could not lock clipboard memory: %v", err)
	}
	// Encode straight into the clipboard memory in small chunks, so the
	// payload is never held as a second full-size string or rune slice.
	enc := clipboardEncoder{src: payload}
	var chunk [16384]uint16
	offset := uintptr(0)
	for {
		n := enc.fill(chunk[:len(chunk)-1])
		if n == 0 {
			break
		}
		procRtlMoveMemory.Call(ptr+offset, uintptr(unsafe.Pointer(&chunk[0])), uintptr(n)*2)
		offset += uintptr(n) * 2
	}
	chunk[0] = 0
	procRtlMoveMemory.Call(ptr+offset, uintptr(unsafe.Pointer(&chunk[0])), 2)
	procGlobalUnlock.Call(mem)
	if r, _, err := procSetClipboardData.Call(cfUnicodeText, mem); r == 0 {
		// Ownership passes to the system only on success.
		procGlobalFree.Call(mem)
		return fmt.Errorf("Error: Could not write to the clipboard: %v", err)
	}
	return nil
}

// Another program (a clipboard manager, a remote-desktop client) can hold the
// clipboard open for a moment; retry briefly before giving up.
func openClipboardWithRetry() error {
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("unknown error")
	}
	return fmt.Errorf("Error: The clipboard is in use by another program (%v). Try again, or use --print.", lastErr)
}
