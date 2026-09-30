package picker

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
)

type fakeMouseConsole struct {
	bytes.Buffer
	currentMode                        uint32
	modes                              []uint32
	closed                             bool
	getErr, setErr, resetErr, closeErr error
	shortWrite                         bool
}

func (c *fakeMouseConsole) mode() (uint32, error) { return c.currentMode, c.getErr }
func (c *fakeMouseConsole) setMode(mode uint32) error {
	c.modes = append(c.modes, mode)
	if c.setErr != nil {
		return c.setErr
	}
	c.currentMode = mode
	return nil
}
func (c *fakeMouseConsole) Close() error { c.closed = true; return c.closeErr }
func (c *fakeMouseConsole) Write(p []byte) (int, error) {
	if string(p) == sgrMouseReset && c.resetErr != nil {
		return 0, c.resetErr
	}
	if c.shortWrite {
		return len(p) - 1, nil
	}
	return c.Buffer.Write(p)
}

// Prevent bytes.Buffer's promoted WriteString from bypassing fault injection.
func (c *fakeMouseConsole) WriteString(s string) (int, error) { return c.Write([]byte(s)) }

func TestJetBrainsMouseScope(t *testing.T) {
	for _, host := range []string{"", "Windows Terminal", "mintty", "xterm", "JetBrains-JediTerm-other"} {
		t.Run(host, func(t *testing.T) {
			restore, err := prepareJetBrainsMouse(host, func() (mouseConsole, error) {
				t.Fatal("unaffected host opened console")
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := restore(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, noConsole := range []bool{false, true} {
		c := &fakeMouseConsole{getErr: errors.New("not a console")}
		restore, err := prepareJetBrainsMouse("JetBrains-JediTerm", func() (mouseConsole, error) {
			if noConsole {
				return nil, os.ErrNotExist
			}
			return c, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := restore(); err != nil {
			t.Fatal(err)
		}
		if c.Len() != 0 || len(c.modes) != 0 || (!noConsole && !c.closed) {
			t.Fatal("non-console handle was modified or leaked")
		}
	}
}

func TestJetBrainsMouseModeLifecycle(t *testing.T) {
	for _, initialMode := range []uint32{0, 2, 7, 0x1f} {
		c := &fakeMouseConsole{currentMode: initialMode}
		for range 2 { // Fresh mode ownership for successive pickers.
			c.closed = false
			c.Reset()
			c.modes = nil
			restore, err := prepareJetBrainsMouse("JetBrains-JediTerm", func() (mouseConsole, error) { return c, nil })
			if err != nil {
				t.Fatal(err)
			}
			if c.String() != sgrMouseEnable || c.currentMode != initialMode|5 || c.closed {
				t.Fatalf("incorrect setup: bytes=%q mode=%x closed=%v", c.String(), c.currentMode, c.closed)
			}
			if err := restore(); err != nil {
				t.Fatal(err)
			}
			if c.String() != sgrMouseEnable+sgrMouseReset || c.currentMode != initialMode || !c.closed {
				t.Fatalf("incorrect cleanup: bytes=%q mode=%x closed=%v", c.String(), c.currentMode, c.closed)
			}
			if initialMode|5 == initialMode && len(c.modes) != 0 {
				t.Fatalf("unnecessary mode writes: %v", c.modes)
			}
			if initialMode|5 != initialMode && !slices.Equal(c.modes, []uint32{initialMode | 5, initialMode}) {
				t.Fatalf("mode writes: %v", c.modes)
			}
		}
	}
}

func TestJetBrainsMouseFailuresReleaseConsole(t *testing.T) {
	failure := errors.New("console I/O failed")
	for _, outcome := range []string{"set", "enable", "reset", "close"} {
		t.Run(outcome, func(t *testing.T) {
			c := &fakeMouseConsole{}
			switch outcome {
			case "set":
				c.setErr = failure
			case "enable":
				c.shortWrite = true
			case "reset":
				c.resetErr = failure
			case "close":
				c.closeErr = failure
			}
			restore, err := prepareJetBrainsMouse("JetBrains-JediTerm", func() (mouseConsole, error) { return c, nil })
			if restore != nil {
				err = restore()
			}
			want := failure
			if outcome == "enable" {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) || !c.closed || c.currentMode != 0 {
				t.Fatalf("err=%v closed=%v mode=%x", err, c.closed, c.currentMode)
			}
		})
	}
}

func TestMouseConsoleNativeModeRestore(t *testing.T) {
	const helper = "CATCLIP_TEST_NATIVE_MOUSE_CONSOLE"
	if os.Getenv(helper) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMouseConsoleNativeModeRestore$")
		cmd.Env = append(os.Environ(), helper+"=1")
		// An isolated invisible console; never modify the developer's terminal.
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("native console helper: %v\n%s", err, out)
		} else if bytes.Contains(out, []byte("\x1b")) {
			t.Fatalf("console escapes leaked into captured output: %q", out)
		}
		return
	}
	file, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	console := windowsMouseConsole{file}
	initialMode, err := console.mode()
	if err != nil {
		t.Fatal(err)
	}
	defer console.setMode(initialMode)
	baseline := initialMode &^ uint32(5)
	if err := console.setMode(baseline); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERMINAL_EMULATOR", "JetBrains-JediTerm")
	restore, err := preparePickerTerminal()
	if err != nil {
		t.Fatal(err)
	}
	active, err := console.mode()
	if err != nil || active != baseline|5 {
		t.Fatalf("active mode=%x err=%v", active, err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	restored, err := console.mode()
	if err != nil || restored != baseline {
		t.Fatalf("restored mode=%x err=%v", restored, err)
	}
}
