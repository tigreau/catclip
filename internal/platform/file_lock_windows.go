//go:build windows

package platform

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

// TryLockFile uses a kernel-owned lock so terminating a superseded helper
// releases it. Never wait behind another preview's cache publication.
func TryLockFile(f *os.File) bool {
	var overlapped syscall.Overlapped
	ok, _, _ := lockFileEx.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	runtime.KeepAlive(&overlapped)
	return ok != 0
}
