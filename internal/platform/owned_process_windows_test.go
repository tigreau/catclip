//go:build windows

package platform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const ownedTestMode = "CATCLIP_TEST_OWNED_MODE"

func ownedTestCommand(mode, dir, name string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnedProcessHelper$")
	cmd.Env = append(os.Environ(), ownedTestMode+"="+mode, "CATCLIP_TEST_OWNED_DIR="+dir, "CATCLIP_TEST_OWNED_NAME="+name)
	cmd.Stderr = os.Stderr
	return cmd
}

func awaitOwnedFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && (!strings.HasSuffix(path, ".pid") || len(data) > 0) {
			return data
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", filepath.Base(path))
	return nil
}

func ownedProcessHandle(t *testing.T, dir, name string) syscall.Handle {
	t.Helper()
	pid, err := strconv.Atoi(string(awaitOwnedFile(t, filepath.Join(dir, name+".pid"))))
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE|syscall.PROCESS_TERMINATE|0x1000, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.CloseHandle(h) })
	return h
}

func assertOwnedExited(t *testing.T, handles ...syscall.Handle) {
	t.Helper()
	for _, handle := range handles {
		status, err := syscall.WaitForSingleObject(handle, 1000)
		if err != nil || status != syscall.WAIT_OBJECT_0 {
			t.Fatalf("owned process survived: wait=%d err=%v", status, err)
		}
	}
}

func launchOwnedTestPicker(t *testing.T, mode string) (string, <-chan error) {
	t.Helper()
	dir := t.TempDir()
	cmd := ownedTestCommand(mode, dir, "picker")
	cmd.Stdin = strings.NewReader("input\n")
	done := make(chan error, 1)
	go func() { _, err := PickerOutput(cmd, nil); done <- err }()
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "exit"), nil, 0600)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	return dir, done
}

func finishOwnedTestPicker(t *testing.T, dir string, done <-chan error) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "exit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("picker cleanup did not finish")
	}
}

func TestOwnedPickerExitStopsUnrecordedDescendants(t *testing.T) {
	dir, done := launchOwnedTestPicker(t, "picker-unrecorded")
	helper := ownedProcessHandle(t, dir, "helper")
	worker := ownedProcessHandle(t, dir, "worker")
	finishOwnedTestPicker(t, dir, done)
	assertOwnedExited(t, helper, worker)
}

func TestOwnedHelperKillStopsDescendant(t *testing.T) {
	dir, done := launchOwnedTestPicker(t, "picker-unrecorded")
	helper := ownedProcessHandle(t, dir, "helper")
	worker := ownedProcessHandle(t, dir, "worker")
	if err := syscall.TerminateProcess(helper, 1); err != nil {
		t.Fatal(err)
	}
	assertOwnedExited(t, helper, worker)
	// The enclosing picker is still usable after its helper was killed.
	finishOwnedTestPicker(t, dir, done)
}

func TestOwnedClaimsRejectDelayedOlderLaunch(t *testing.T) {
	dir, done := launchOwnedTestPicker(t, "picker-delayed")
	current := ownedProcessHandle(t, dir, "new")
	awaitOwnedFile(t, filepath.Join(dir, "new.claimed"))
	if err := os.WriteFile(filepath.Join(dir, "old.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitOwnedFile(t, filepath.Join(dir, "old.rejected"))
	if status, err := syscall.WaitForSingleObject(current, 0); err != nil || status != syscall.WAIT_TIMEOUT {
		t.Fatalf("delayed old request killed current helper: %d %v", status, err)
	}
	finishOwnedTestPicker(t, dir, done)
	assertOwnedExited(t, current)
}

func TestOwnedConcurrentClaimsLeaveNewestHelper(t *testing.T) {
	dir, done := launchOwnedTestPicker(t, "picker-concurrent")
	var handles []syscall.Handle
	for i := 0; i < 8; i++ {
		handles = append(handles, ownedProcessHandle(t, dir, strconv.Itoa(i)))
	}
	if err := os.WriteFile(filepath.Join(dir, "claims.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitOwnedFile(t, filepath.Join(dir, "7.claimed"))
	assertOwnedExited(t, handles[:7]...)
	if status, err := syscall.WaitForSingleObject(handles[7], 0); err != nil || status != syscall.WAIT_TIMEOUT {
		t.Fatal("newest claimant was terminated")
	}
	finishOwnedTestPicker(t, dir, done)
	assertOwnedExited(t, handles[7])
}

func TestOwnedClaimsRejectDelayedLaunchAfterNewestExits(t *testing.T) {
	dir, done := launchOwnedTestPicker(t, "picker-delayed")
	current := ownedProcessHandle(t, dir, "new")
	awaitOwnedFile(t, filepath.Join(dir, "new.claimed"))
	if err := syscall.TerminateProcess(current, 1); err != nil {
		t.Fatal(err)
	}
	assertOwnedExited(t, current)
	if err := os.WriteFile(filepath.Join(dir, "old.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitOwnedFile(t, filepath.Join(dir, "old.rejected"))
	finishOwnedTestPicker(t, dir, done)
}

func TestOwnedPickerJobsAreIndependent(t *testing.T) {
	first, firstDone := launchOwnedTestPicker(t, "picker-unrecorded")
	second, secondDone := launchOwnedTestPicker(t, "picker-unrecorded")
	firstWorker := ownedProcessHandle(t, first, "worker")
	secondWorker := ownedProcessHandle(t, second, "worker")
	finishOwnedTestPicker(t, first, firstDone)
	assertOwnedExited(t, firstWorker)
	if status, err := syscall.WaitForSingleObject(secondWorker, 0); err != nil || status != syscall.WAIT_TIMEOUT {
		t.Fatal("another picker session was killed")
	}
	finishOwnedTestPicker(t, second, secondDone)
	assertOwnedExited(t, secondWorker)
}

func TestOwnedPreviewReplacementPreservesReload(t *testing.T) {
	dir, done := launchOwnedTestPicker(t, "picker-roles")
	reload := ownedProcessHandle(t, dir, "reload")
	old := ownedProcessHandle(t, dir, "preview-old")
	if err := os.WriteFile(filepath.Join(dir, "roles.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	current := ownedProcessHandle(t, dir, "preview-new")
	assertOwnedExited(t, old)
	for _, handle := range []syscall.Handle{reload, current} {
		if status, err := syscall.WaitForSingleObject(handle, 0); err != nil || status != syscall.WAIT_TIMEOUT {
			t.Fatal("replacement stopped another role or the current preview")
		}
	}
	finishOwnedTestPicker(t, dir, done)
	assertOwnedExited(t, reload, current)
}

func TestOwnedPublicationRejectsClosedPicker(t *testing.T) {
	dir, done := launchOwnedTestPicker(t, "picker-publication")
	_ = ownedProcessHandle(t, dir, "publication")
	owner := string(awaitOwnedFile(t, filepath.Join(dir, "owner.path")))
	if err := os.WriteFile(filepath.Join(owner, "closed"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "publication.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitOwnedFile(t, filepath.Join(dir, "publication.rejected"))
	finishOwnedTestPicker(t, dir, done)
}

func TestOwnedPickerLauncherDeathStopsItsTree(t *testing.T) {
	dir := t.TempDir()
	cmd := ownedTestCommand("launch-host", dir, "host")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	picker := ownedProcessHandle(t, dir, "picker")
	helper := ownedProcessHandle(t, dir, "helper")
	worker := ownedProcessHandle(t, dir, "worker")
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	assertOwnedExited(t, picker, helper, worker)
	// Hard termination cannot run filesystem defers. Remove only this test's
	// captured private owner directory after kernel process cleanup is proved.
	owner := string(awaitOwnedFile(t, filepath.Join(dir, "owner.path")))
	if filepath.Dir(owner) != os.TempDir() || !strings.HasPrefix(filepath.Base(owner), "catclip-picker-owner-") {
		t.Fatal("invalid test owner path")
	}
	if err := os.RemoveAll(owner); err != nil {
		t.Fatal(err)
	}
}

func TestClaimIdentityMismatchDoesNotKillProcess(t *testing.T) {
	cmd := ownedTestCommand("worker", t.TempDir(), "identity")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	identity, err := processIdentityForPID(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	identity.Created++
	if err := terminateClaimedProcess(identity); err != nil {
		t.Fatal(err)
	}
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(h)
	if status, err := syscall.WaitForSingleObject(h, 0); err != nil || status != syscall.WAIT_TIMEOUT {
		t.Fatal("mismatched identity was killed")
	}
}

func TestOwnedPickerFailureBeforeResumeStopsSuspendedChild(t *testing.T) {
	cmd := ownedTestCommand("worker", t.TempDir(), "suspended")
	var handle syscall.Handle
	func() {
		defer func() {
			if recover() != "abort before resume" {
				t.Fatal("missing injected failure")
			}
		}()
		_, _ = PickerOutput(cmd, func(err error) {
			if err != nil {
				t.Fatal(err)
			}
			var openErr error
			handle, openErr = syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
			if openErr != nil {
				t.Fatal(openErr)
			}
			panic("abort before resume")
		})
	}()
	defer syscall.CloseHandle(handle)
	assertOwnedExited(t, handle)
}

func TestOwnedPickerRepeatedExitReleasesHandles(t *testing.T) {
	getCount := processKernel.NewProc("GetProcessHandleCount")
	current, err := syscall.GetCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	count := func() uint32 {
		var n uint32
		if ok, _, err := getCount.Call(uintptr(current), uintptr(unsafe.Pointer(&n))); ok == 0 {
			t.Fatal(err)
		}
		return n
	}
	dir := t.TempDir()
	// Warm the Windows I/O scheduler/thread pool as well as lazy DLL loading.
	// Compare equally sized steady batches, not a cold process to a warm one.
	for i := 0; i < 20; i++ {
		if _, err := PickerOutput(ownedTestCommand("picker-fast", dir, "fast"), nil); err != nil {
			t.Fatal(err)
		}
	}
	before := count()
	threads := func() uint32 {
		snapshot, err := syscall.CreateToolhelp32Snapshot(2, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.CloseHandle(snapshot)
		entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
		for err := syscall.Process32First(snapshot, &entry); err == nil; err = syscall.Process32Next(snapshot, &entry) {
			if entry.ProcessID == uint32(os.Getpid()) {
				return entry.Threads
			}
		}
		t.Fatal("missing own process entry")
		return 0
	}
	stable := 0
	for batch := 0; batch < 6; batch++ {
		beforeThreads := threads()
		for i := 0; i < 20; i++ {
			if _, err := PickerOutput(ownedTestCommand("picker-fast", dir, "fast"), nil); err != nil {
				t.Fatal(err)
			}
		}
		after := count()
		t.Logf("batch %d: handles %d -> %d; OS threads %d -> %d", batch, before, after, beforeThreads, threads())
		// Windows' Go scheduler can lazily allocate thread/timer handles even
		// after initial I/O warmup. Require two non-growing steady batches;
		// an accumulating per-picker leak cannot satisfy that condition.
		if after <= before {
			stable++
		} else {
			stable = 0
		}
		if stable == 2 {
			return
		}
		before = after
	}
	t.Fatal("picker exits never reached stable handle use")
}

func TestOwnedProcessHelper(t *testing.T) {
	mode := os.Getenv(ownedTestMode)
	if mode == "" {
		return
	}
	dir, name := os.Getenv("CATCLIP_TEST_OWNED_DIR"), os.Getenv("CATCLIP_TEST_OWNED_NAME")
	write := func(suffix string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+suffix), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	spawn := func(mode, name string) *exec.Cmd {
		t.Helper()
		cmd := ownedTestCommand(mode, dir, name)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	switch mode {
	case "picker-fast":
		fmt.Println("chosen\tchosen/path.ts")
	case "worker":
		write(".pid", []byte(strconv.Itoa(os.Getpid())))
		time.Sleep(time.Minute)
	case "helper", "claim-helper", "gated-claim", "role-helper":
		if err := BeginInternalWork(); err != nil {
			t.Fatal(err)
		}
		if mode == "gated-claim" {
			write(".pid", []byte(strconv.Itoa(os.Getpid())))
			awaitOwnedFile(t, filepath.Join(dir, "claims.go"))
		}
		if mode == "claim-helper" || mode == "gated-claim" {
			err := ClaimPickerWork(filepath.Join(dir, "preview-pid.txt"))
			if errors.Is(err, ErrWorkSuperseded) {
				write(".rejected", nil)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			write(".claimed", nil)
		}
		if mode == "role-helper" {
			kind := "internal-file-preview"
			if name == "reload" {
				kind = "internal-content-match-list"
			}
			if err := ClaimInternalWork(kind); err != nil {
				t.Fatal(err)
			}
			if !InternalWorkCanPublish() {
				t.Fatal("live current helper cannot publish")
			}
		}
		write(".pid", []byte(strconv.Itoa(os.Getpid())))
		if name == "publication" {
			awaitOwnedFile(t, filepath.Join(dir, "publication.go"))
			if InternalWorkCanPublish() {
				t.Fatal("closed picker permitted publication")
			}
			write(".rejected", nil)
			return
		}
		if mode == "helper" {
			spawn("worker", "worker")
		}
		time.Sleep(time.Minute)
	case "old-launcher":
		write(".launched", nil)
		awaitOwnedFile(t, filepath.Join(dir, "old.go"))
		cmd := spawn("claim-helper", "old")
		_ = cmd.Wait()
	case "new-launcher":
		cmd := spawn("claim-helper", "new")
		_ = cmd.Wait()
	case "launch-host":
		_, err := PickerOutput(ownedTestCommand("picker-unrecorded", dir, "picker"), nil)
		if err != nil {
			t.Fatal(err)
		}
	case "picker-unrecorded", "picker-delayed", "picker-concurrent", "picker-roles", "picker-publication":
		_, _ = io.Copy(io.Discard, os.Stdin)
		write(".pid", []byte(strconv.Itoa(os.Getpid())))
		if err := os.WriteFile(filepath.Join(dir, "owner.path"), []byte(os.Getenv(pickerOwnerEnv)), 0600); err != nil {
			t.Fatal(err)
		}
		if mode == "picker-unrecorded" {
			spawn("helper", "helper")
		} else if mode == "picker-delayed" {
			spawn("old-launcher", "old")
			awaitOwnedFile(t, filepath.Join(dir, "old.launched"))
			spawn("new-launcher", "new")
		} else if mode == "picker-publication" {
			spawn("role-helper", "publication")
		} else if mode == "picker-roles" {
			spawn("role-helper", "reload")
			awaitOwnedFile(t, filepath.Join(dir, "reload.pid"))
			spawn("role-helper", "preview-old")
			awaitOwnedFile(t, filepath.Join(dir, "preview-old.pid"))
			awaitOwnedFile(t, filepath.Join(dir, "roles.go"))
			spawn("role-helper", "preview-new")
		} else {
			for i := 0; i < 8; i++ {
				name := strconv.Itoa(i)
				spawn("gated-claim", name)
				awaitOwnedFile(t, filepath.Join(dir, name+".pid"))
			}
		}
		awaitOwnedFile(t, filepath.Join(dir, "exit"))
		fmt.Println("chosen\tchosen/path.ts")
	default:
		t.Fatalf("unknown owned helper mode %q", mode)
	}
}
