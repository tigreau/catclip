//go:build darwin || linux

package platform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func OpenPromptTTY() (*os.File, *os.File, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	return tty, tty, nil
}

func ClosePromptTTY(input, output *os.File) {
	if input != nil {
		_ = input.Close()
	}
}

func ReadPromptByte(input *os.File, output io.Writer, prompt string) (string, bool) {
	state, err := getTerminalState(input)
	if err != nil {
		return "", false
	}
	raw := *state
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if _, err := fmt.Fprintf(output, "%s ", prompt); err != nil {
		return "", false
	}
	if err := setTerminalState(input, &raw); err != nil {
		return "", false
	}
	defer func() {
		_ = setTerminalState(input, state)
	}()

	var buf [1]byte
	n, readErr := input.Read(buf[:])
	if _, err := fmt.Fprint(output, "\n"); err != nil {
		return "", false
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", false
	}
	if n == 0 {
		return "", true
	}
	return string(buf[:n]), true
}

func IsTerminalFile(f *os.File) bool {
	if f == nil {
		return false
	}
	_, err := getTerminalState(f)
	return err == nil
}

// BeginPickerInputSession holds raw input across child picker relaunches. Each
// child restores this inherited state when it exits; the owner restores the
// original state after its last child. No input is read, translated or flushed.
func BeginPickerInputSession(input *os.File) (func() error, error) {
	state, err := getTerminalState(input)
	if err != nil {
		return nil, err
	}
	quiet := *state
	quiet.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	quiet.Oflag &^= syscall.OPOST
	quiet.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	quiet.Cflag &^= syscall.CSIZE | syscall.PARENB
	quiet.Cflag |= syscall.CS8
	quiet.Cc[syscall.VMIN], quiet.Cc[syscall.VTIME] = 1, 0
	if err := setTerminalState(input, &quiet); err != nil {
		return nil, err
	}
	return func() error { return setTerminalState(input, state) }, nil
}

func getTerminalState(f *os.File) (*syscall.Termios, error) {
	reqGet, _, ok := terminalIOCTLRequests()
	if !ok {
		return nil, syscall.ENOTTY
	}
	state := &syscall.Termios{}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), reqGet, uintptr(unsafe.Pointer(state)))
	if errno != 0 {
		return nil, errno
	}
	return state, nil
}

func setTerminalState(f *os.File, state *syscall.Termios) error {
	_, reqSet, ok := terminalIOCTLRequests()
	if !ok {
		return syscall.ENOTTY
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), reqSet, uintptr(unsafe.Pointer(state)))
	if errno != 0 {
		return errno
	}
	return nil
}

func terminalIOCTLRequests() (uintptr, uintptr, bool) {
	switch runtime.GOOS {
	case "darwin":
		return 0x40487413, 0x80487414, true
	case "linux":
		return 0x5401, 0x5402, true
	default:
		return 0, 0, false
	}
}
