//go:build windows

package transporttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
)

const ownedFzfMode = "CATCLIP_TEST_OWNED_FZF_MODE"

func init() {
	runOwnedFzfHelper = func() {
		mode := os.Getenv(ownedFzfMode)
		if mode == "" {
			return
		}
		if _, err := picker.NormalizeCommandArgs(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		dir := os.Getenv("CATCLIP_TEST_OWNED_FZF_DIR")
		if mode == "worker" {
			if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0600); err != nil {
				os.Exit(1)
			}
			time.Sleep(time.Minute)
			os.Exit(0)
		}
		// Deliberately unregistered: picker ownership must cover descendants
		// even when no helper has published a claim or entered a nested job.
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), ownedFzfMode+"=worker")
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(dir, "worker.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
			os.Exit(1)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
				os.Exit(0)
			}
			time.Sleep(time.Millisecond)
		}
		os.Exit(1)
	}
}

func TestOwnedRealFzfStopsUnrecordedWorker(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_FZF_TRANSPORT_TESTS") != "1" {
		t.Skip("real-fzf shell matrix is enabled explicitly by CI")
	}
	bin := os.Getenv("CATCLIP_FZF")
	if !filepath.IsAbs(bin) {
		t.Fatal("CATCLIP_FZF must be absolute")
	}
	shell, err := exec.LookPath(os.Getenv("CATCLIP_TEST_FZF_SHELL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	t.Setenv("FZF_DEFAULT_OPTS", "")
	t.Setenv("FZF_DEFAULT_OPTS_FILE", "")
	t.Setenv("CATCLIP_FZF_ARGV_PROBE", "")
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := picker.CommandExecutable(self)
	cmd := exec.Command(bin, "--sync", "--disabled", "--bind", "load:execute-silent("+command+")+accept")
	cmd.Env = append(os.Environ(), ownedFzfMode+"=helper", "CATCLIP_TEST_OWNED_FZF_DIR="+dir)
	cmd.Stdin = strings.NewReader("selected/path.ts\n")
	cmd.Stderr = os.Stderr
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	out, err := platform.PickerOutput(cmd, func(err error) {
		if err == nil {
			timer = time.AfterFunc(30*time.Second, func() { _ = cmd.Process.Kill() })
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "selected/path.ts\n" {
		t.Fatalf("selection changed: %q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "worker.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err == syscall.Errno(87) { // Already reaped; no live worker remains.
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)
	if status, err := syscall.WaitForSingleObject(handle, 1000); err != nil || status != syscall.WAIT_OBJECT_0 {
		t.Fatalf("unrecorded worker survived fzf exit: %d %v", status, err)
	}
}
