package picker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const (
	sgrMouseEnable        = "\x1b[?1006h"
	sgrMouseReset         = "\x1b[?1006l"
	processedOutput       = 0x1
	virtualTerminalOutput = 0x4
)

type mouseConsole interface {
	io.WriteCloser
	mode() (uint32, error)
	setMode(uint32) error
}

type windowsMouseConsole struct{ *os.File }

var setMouseConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

func (c windowsMouseConsole) mode() (uint32, error) {
	var mode uint32
	err := syscall.GetConsoleMode(syscall.Handle(c.Fd()), &mode)
	return mode, err
}

func (c windowsMouseConsole) setMode(mode uint32) error {
	ok, _, err := setMouseConsoleMode.Call(c.Fd(), uintptr(mode))
	if ok == 0 {
		return err
	}
	return nil
}

func preparePickerTerminal() (func() error, error) {
	// Check the parent's terminal, independently of the selected shell or the
	// preview subprocess environment. Git Bash in JetBrains needs this too.
	return prepareJetBrainsMouse(os.Getenv("TERMINAL_EMULATOR"), func() (mouseConsole, error) {
		file, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
		if err != nil {
			return nil, err
		}
		return windowsMouseConsole{file}, nil
	})
}

func prepareJetBrainsMouse(terminal string, openConsole func() (mouseConsole, error)) (func() error, error) {
	noop := func() error { return nil }
	if terminal != "JetBrains-JediTerm" {
		return noop, nil
	}
	console, err := openConsole()
	if err != nil {
		// There is no attached Windows console. Leave fzf's own terminal
		// detection/error handling in charge; never write escapes to stdout.
		return noop, nil
	}
	originalMode, err := console.mode()
	if err != nil {
		_ = console.Close()
		return noop, nil
	}
	mode := originalMode | processedOutput | virtualTerminalOutput
	if mode != originalMode {
		if err := console.setMode(mode); err != nil {
			_ = console.Close()
			return nil, fmt.Errorf("prepare JetBrains mouse output: %w", err)
		}
	}
	restore := func() error {
		// JediTerm does not support querying or saving/restoring this private
		// mode. Own it for this picker and reset it afterward, like fzf's
		// lightweight renderer. This restores the normal shell baseline, not
		// an unknown enclosing application's mouse encoding. Do not change
		// mouse tracking modes or read/drain console input here.
		resetErr := writeMouseSequence(console, sgrMouseReset)
		var modeErr error
		if mode != originalMode {
			modeErr = console.setMode(originalMode)
		}
		if err := errors.Join(resetErr, modeErr, console.Close()); err != nil {
			return fmt.Errorf("restore JetBrains mouse output: %w", err)
		}
		return nil
	}
	// Native fullscreen fzf expects MOUSE_EVENT_RECORDs. On the affected
	// JetBrains host, legacy ESC[M reports instead arrive as KEY_EVENT_RECORDs:
	// Escape aborts fzf and the remaining bytes become query text. Requesting
	// SGR encoding lets the console translate reports into native mouse events.
	if err := writeMouseSequence(console, sgrMouseEnable); err != nil {
		return nil, errors.Join(fmt.Errorf("enable JetBrains SGR mouse encoding: %w", err), restore())
	}
	return restore, nil
}

func writeMouseSequence(w io.Writer, sequence string) error {
	n, err := io.WriteString(w, sequence)
	if err == nil && n != len(sequence) {
		err = io.ErrShortWrite
	}
	return err
}
