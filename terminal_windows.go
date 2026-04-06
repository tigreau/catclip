//go:build windows

package catclip

import (
	"io"
	"os"
)

func openPromptTTY() (*os.File, *os.File, error) {
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

func closePromptTTY(input, output *os.File) {
	if output != nil {
		_ = output.Close()
	}
	if input != nil {
		_ = input.Close()
	}
}

func readPromptByte(input *os.File, output io.Writer, prompt string) (string, bool) {
	return "", false
}

func isTerminalFile(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
