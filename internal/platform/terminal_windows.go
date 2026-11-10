//go:build windows

package platform

import (
	"io"
	"os"
	"syscall"
)

var setInputConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

func OpenPromptTTY() (*os.File, *os.File, error) {
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		_ = input.Close()
		return nil, nil, err
	}
	return input, output, nil
}

func ClosePromptTTY(input, output *os.File) {
	if output != nil {
		_ = output.Close()
	}
	if input != nil {
		_ = input.Close()
	}
}

func ReadPromptByte(input *os.File, output io.Writer, prompt string) (string, bool) {
	return "", false
}

func IsTerminalFile(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// BeginPickerInputSession preserves raw input across theme-picker relaunches.
// Pseudoterminals without a native console keep their existing input handling.
func BeginPickerInputSession(input *os.File) (func() error, error) {
	handle := syscall.Handle(input.Fd())
	var original uint32
	if err := syscall.GetConsoleMode(handle, &original); err != nil {
		return func() error { return nil }, nil
	}
	setMode := func(mode uint32) error {
		ok, _, err := setInputConsoleMode.Call(uintptr(handle), uintptr(mode))
		if ok == 0 {
			return err
		}
		return nil
	}
	const processedLineEchoInput = 0x0001 | 0x0002 | 0x0004
	if err := setMode(original &^ processedLineEchoInput); err != nil {
		return nil, err
	}
	return func() error { return setMode(original) }, nil
}
