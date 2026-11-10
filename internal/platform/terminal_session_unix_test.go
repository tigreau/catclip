//go:build darwin || linux

package platform

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestPickerInputSessionSurvivesChildRestore(t *testing.T) {
	master, input := openPickerTestPTY(t)
	original, err := getTerminalState(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, echo := range []bool{true, false} {
		baseline := *original
		baseline.Lflag |= syscall.ICANON | syscall.ECHONL
		if echo {
			baseline.Lflag |= syscall.ECHO
		} else {
			baseline.Lflag &^= syscall.ECHO
		}
		if err := setTerminalState(input, &baseline); err != nil {
			t.Fatal(err)
		}
		restore, err := BeginPickerInputSession(input)
		if err != nil {
			t.Fatal(err)
		}
		quiet, err := getTerminalState(input)
		if err != nil {
			t.Fatal(err)
		}
		if quiet.Lflag&(syscall.ECHO|syscall.ECHONL|syscall.ICANON|syscall.ISIG|syscall.IEXTEN) != 0 || quiet.Iflag&(syscall.ICRNL|syscall.INLCR|syscall.IGNCR) != 0 {
			t.Fatal("picker input can echo, buffer, translate Enter, or turn Ctrl-C into a signal")
		}
		// fzf snapshots its inherited mode, uses raw input, then restores the
		// snapshot. Repeated children must never restore shell echo in the gap.
		for range 3 {
			raw := *quiet
			raw.Lflag &^= syscall.ICANON | syscall.ISIG
			if err := setTerminalState(input, &raw); err != nil {
				t.Fatal(err)
			}
			if err := setTerminalState(input, quiet); err != nil {
				t.Fatal(err)
			}
			between, err := getTerminalState(input)
			if err != nil || *between != *quiet {
				t.Fatalf("child restore lost quiet input: %v", err)
			}
		}
		// Typeahead in the relaunch gap must reach the next picker byte for
		// byte, including Enter and Ctrl-C, without requiring a newline.
		keys := []byte("\x1b[A\x1b[B\r\x03")
		if _, err := master.Write(keys); err != nil {
			t.Fatal(err)
		}
		fd := int(input.Fd())
		if err := syscall.SetNonblock(fd, true); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(keys))
		n := 0
		deadline := time.Now().Add(time.Second)
		for n < len(got) && time.Now().Before(deadline) {
			count, err := syscall.Read(fd, got[n:])
			if err == syscall.EAGAIN {
				time.Sleep(time.Millisecond)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			n += count
		}
		if n != len(keys) || !bytes.Equal(got, keys) {
			t.Fatalf("queued picker keys changed: got %q, want %q", got[:n], keys)
		}
		if err := restore(); err != nil {
			t.Fatal(err)
		}
		after, err := getTerminalState(input)
		if err != nil || *after != baseline {
			t.Fatalf("original terminal mode not restored: %v", err)
		}
	}
}

func openPickerTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("private PTY unavailable: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	ioctl := func(request uintptr, value unsafe.Pointer) {
		t.Helper()
		if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), request, uintptr(value)); err != 0 {
			t.Fatalf("prepare private PTY: %v", err)
		}
	}
	var name string
	if runtime.GOOS == "darwin" {
		ioctl(0x20007454, nil) // TIOCPTYGRANT
		ioctl(0x20007452, nil) // TIOCPTYUNLK
		var path [128]byte
		ioctl(0x40807453, unsafe.Pointer(&path[0])) // TIOCPTYGNAME
		for i, b := range path {
			if b == 0 {
				name = string(path[:i])
				break
			}
		}
	} else {
		var unlock int32
		ioctl(0x40045431, unsafe.Pointer(&unlock)) // TIOCSPTLCK
		var number uint32
		ioctl(0x80045430, unsafe.Pointer(&number)) // TIOCGPTN
		name = fmt.Sprintf("/dev/pts/%d", number)
	}
	slave, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}
