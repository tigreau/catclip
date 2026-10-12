//go:build windows

package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var process32First = processKernel.NewProc("Process32FirstW")
var process32Next = processKernel.NewProc("Process32NextW")

type processEntry struct {
	Size, Usage, PID        uint32
	Heap                    uintptr
	Module, Threads, Parent uint32
	Priority                int32
	Flags                   uint32
	Exe                     [260]uint16
}

// Use the verified shell directly below fzf, rather than the helper's delayed
// entry into the claim lock, as the request's ordering identity. If a shell
// starts an old helper late, its earlier launch still loses to a newer request.
func pickerLaunchIdentity(owner string, self processIdentity) (processIdentity, error) {
	if _, err := os.Stat(filepath.Join(owner, "closed")); err == nil {
		return processIdentity{}, ErrWorkSuperseded
	}
	data, err := os.ReadFile(filepath.Join(owner, "picker.json"))
	if err != nil {
		return processIdentity{}, ErrWorkSuperseded
	}
	var metadata pickerOwner
	if err := json.Unmarshal(data, &metadata); err != nil {
		return processIdentity{}, err
	}
	root, err := processIdentityForPID(metadata.Root.PID)
	if err != nil || root != metadata.Root {
		return processIdentity{}, ErrWorkSuperseded
	}
	snapshot, err := syscall.CreateToolhelp32Snapshot(2, 0)
	if err != nil {
		return processIdentity{}, err
	}
	defer syscall.CloseHandle(snapshot)
	parents := make(map[int]int)
	entry := processEntry{Size: uint32(unsafe.Sizeof(processEntry{}))}
	for ok, _, _ := process32First.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry))); ok != 0; ok, _, _ = process32Next.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry))) {
		parents[int(entry.PID)] = int(entry.Parent)
	}
	current := self
	for steps := 0; steps < 32; steps++ {
		parent := parents[current.PID]
		if parent == root.PID {
			return current, nil
		}
		if parent <= 0 || parent == current.PID {
			break
		}
		identity, err := processIdentityForPID(parent)
		// A dead/reused ancestor means the owning fzf shell was already
		// cancelled. Reject it instead of giving late work a new timestamp.
		if err != nil || identity.Created > current.Created {
			break
		}
		current = identity
	}
	return processIdentity{}, ErrWorkSuperseded
}

type workClaim struct {
	Process processIdentity
	Launch  processIdentity
	Owner   string
}

// ClaimPickerWork serializes read/terminate/replace. Production managed helpers
// validate creation identities; legacy plain PID files are never trusted there.
func ClaimPickerWork(pidPath string) error {
	if pidPath == "" {
		return nil
	}
	managed := os.Getenv(pickerOwnerEnv) != ""
	if managed && internalWork.claimPath != "" && internalWork.claimPath != pidPath {
		// Main already claimed this fzf stream before tool checks. Legacy
		// artifact-based renderers delegate without a second claim/lock.
		return nil
	}
	if managed {
		if err := BeginInternalWork(); err != nil {
			return err
		}
	}
	self, err := processIdentityForPID(os.Getpid())
	if err != nil {
		return err
	}
	launch := self
	owner := ""
	if managed {
		launch, owner = internalWork.launch, internalWork.owner
	}
	claim := workClaim{self, launch, owner}
	lock, err := os.OpenFile(pidPath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		if managed && os.IsNotExist(err) {
			return ErrWorkSuperseded
		}
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(time.Second)
	for !TryLockFile(lock) {
		if time.Now().After(deadline) {
			return errors.New("interactive work claim lock timed out")
		}
		time.Sleep(time.Millisecond)
	}
	if managed {
		if _, err := os.Stat(filepath.Join(owner, "closed")); err == nil || os.IsNotExist(statOwner(owner)) {
			return ErrWorkSuperseded
		}
	}
	var prior workClaim
	data, readErr := os.ReadFile(pidPath + ".claim")
	if readErr == nil {
		if err := json.Unmarshal(data, &prior); err != nil {
			return fmt.Errorf("read interactive work claim: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	priorLive, err := claimedProcessAlive(prior.Process)
	if err != nil {
		return err
	}
	// Managed ordering survives the newer helper's exit: a delayed older
	// request must not revive after the newest result has already completed.
	// Direct library callers can reuse a checkpoint after a child exits.
	if (managed || priorLive) && prior.Owner == owner && prior.Process.PID != self.PID {
		if prior.Launch.Created > launch.Created {
			return ErrWorkSuperseded
		}
		if prior.Launch.Created == launch.Created && prior.Launch.PID != 0 {
			// Creation timestamps can tie; PID order is not request order.
			return ErrWorkSuperseded
		}
	}
	if priorLive && prior.Owner == owner && prior.Process.PID != 0 {
		if err := terminateClaimedProcess(prior.Process); err != nil {
			return err
		}
	} else if !managed && readErr != nil {
		// Direct library/diagnostic callers historically seeded plain PIDs.
		// Do not use this migration path for actual fzf-owned helpers.
		if data, err := os.ReadFile(pidPath); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				if identity, err := processIdentityForPID(pid); err == nil {
					_ = terminateClaimedProcess(identity)
				}
			}
		}
	}
	encoded, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	if err := atomicWorkFile(pidPath+".claim", encoded); err != nil {
		return err
	}
	return atomicWorkFile(pidPath, []byte(strconv.Itoa(self.PID)))
}

func claimedProcessAlive(identity processIdentity) (bool, error) {
	if identity.PID <= 0 {
		return false, nil
	}
	h, err := syscall.OpenProcess(0x1000, false, uint32(identity.PID))
	if errors.Is(err, syscall.Errno(87)) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer syscall.CloseHandle(h)
	actual, err := identityForHandle(h, identity.PID)
	if err != nil || actual != identity {
		return false, err
	}
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		return false, err
	}
	return exitCode == 259, nil // STILL_ACTIVE
}

// ClaimInternalWork covers every generated helper route at the executable
// boundary. fzf has one preview stream and a separate input-reload stream per
// picker. Artifact directories can survive undo, but picker lifetimes do not.
func ClaimInternalWork(kind string) error {
	if os.Getenv(pickerOwnerEnv) == "" || !strings.HasPrefix(kind, "internal-") {
		return nil
	}
	if err := BeginInternalWork(); err != nil {
		return err
	}
	role := "preview"
	switch kind {
	case "internal-content-match-list":
		role = "reload"
	case "internal-sink-toggle":
		role = "sink-toggle"
	}
	path := filepath.Join(internalWork.owner, role+"-pid.txt")
	if err := ClaimPickerWork(path); err != nil {
		return err
	}
	internalWork.claimPath = path
	return nil
}

func InternalWorkClaimPath() string { return internalWork.claimPath }

// InternalWorkCanPublish guards retained memo/frame storage independently of
// its longer artifact lifetime. Unmanaged CLI/library calls keep their policy.
func InternalWorkCanPublish() bool {
	if internalWork.owner == "" {
		return true
	}
	if internalWork.claimPath == "" || statOwner(internalWork.owner) != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(internalWork.owner, "closed")); !os.IsNotExist(err) {
		return false
	}
	data, err := os.ReadFile(internalWork.claimPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return err == nil && pid == os.Getpid()
}

func statOwner(owner string) error { _, err := os.Stat(owner); return err }

func StopPickerWork(pidPath string) error {
	data, err := os.ReadFile(pidPath + ".claim")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var claim workClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		return err
	}
	err = terminateClaimedProcess(claim.Process)
	_ = os.Remove(pidPath)
	_ = os.Remove(pidPath + ".claim")
	return err
}

func atomicWorkFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".work-claim-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func terminateClaimedProcess(identity processIdentity) error {
	if identity.PID == os.Getpid() || identity.PID == os.Getppid() {
		return nil
	}
	h, err := syscall.OpenProcess(0x1000|syscall.PROCESS_TERMINATE, false, uint32(identity.PID))
	if err != nil {
		if errors.Is(err, syscall.Errno(87)) { // ERROR_INVALID_PARAMETER: process gone
			return nil
		}
		return err
	}
	defer syscall.CloseHandle(h)
	actual, err := identityForHandle(h, identity.PID)
	if err != nil {
		return err
	}
	if actual != identity {
		return nil
	} // PID recycled; never terminate it
	return syscall.TerminateProcess(h, 1)
}
