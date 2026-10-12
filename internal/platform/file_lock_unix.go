//go:build !windows

package platform

import (
	"os"
	"syscall"
)

// TryLockFile takes a nonblocking exclusive lock released on file close or
// process exit. A contended diagnostic cache must never delay a preview.
func TryLockFile(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}
