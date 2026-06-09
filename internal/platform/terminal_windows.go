//go:build windows

package platform

import (
	"io"
	"os"
)

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
