//go:build windows

package platform

import (
	"os"
	"syscall"
)

var setANSIConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

// Native CMD/conhost needs VT output enabled before SGR is emitted. Only called
// for terminal handles; redirected output stays plain. Preserve other flags.
func enableANSIOutput(file *os.File) bool {
	var mode uint32
	if syscall.GetConsoleMode(syscall.Handle(file.Fd()), &mode) != nil {
		return false
	}
	const required = uint32(0x0001 | 0x0004) // processed output + virtual terminal output
	if mode&required == required {
		return true
	}
	ok, _, _ := setANSIConsoleMode.Call(file.Fd(), uintptr(mode|required))
	return ok != 0
}
