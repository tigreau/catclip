//go:build windows

package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const pickerOwnerEnv = "CATCLIP_INTERNAL_PICKER_OWNER"

var (
	processKernel        = syscall.NewLazyDLL("kernel32.dll")
	createJob            = processKernel.NewProc("CreateJobObjectW")
	setJobInfo           = processKernel.NewProc("SetInformationJobObject")
	assignJob            = processKernel.NewProc("AssignProcessToJobObject")
	terminateJob         = processKernel.NewProc("TerminateJobObject")
	initializeAttributes = processKernel.NewProc("InitializeProcThreadAttributeList")
	updateAttributes     = processKernel.NewProc("UpdateProcThreadAttribute")
	deleteAttributes     = processKernel.NewProc("DeleteProcThreadAttributeList")
	resumeThread         = processKernel.NewProc("ResumeThread")
	queryJobInfo         = processKernel.NewProc("QueryInformationJobObject")
)

type jobBasicLimits struct {
	ProcessTime, JobTime                 int64
	Flags                                uint32
	MinimumWorkingSet, MaximumWorkingSet uintptr
	ActiveProcesses                      uint32
	Affinity                             uintptr
	Priority, Scheduling                 uint32
}

type jobExtendedLimits struct {
	Basic                                                      jobBasicLimits
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func newProcessJob() (syscall.Handle, error) {
	h, _, err := createJob.Call(0, 0)
	if h == 0 {
		return 0, fmt.Errorf("create picker job: %w", err)
	}
	job := syscall.Handle(h)
	limits := jobExtendedLimits{}
	limits.Basic.Flags = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	ok, _, err := setJobInfo.Call(h, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		syscall.CloseHandle(job)
		return 0, fmt.Errorf("configure picker job: %w", err)
	}
	return job, nil
}

func stopProcessJob(job syscall.Handle) error {
	if ok, _, err := terminateJob.Call(uintptr(job), 1); ok == 0 {
		return fmt.Errorf("stop picker descendants: %w", err)
	}
	// Termination initiation is not proof that every descendant has exited.
	// Query only this owned job, rather than enumerating the machine's tree.
	deadline := time.Now().Add(time.Second)
	for {
		var accounting struct {
			Times                                 [4]int64
			PageFaults, Total, Active, Terminated uint32
		}
		ok, _, err := queryJobInfo.Call(uintptr(job), 1, uintptr(unsafe.Pointer(&accounting)), unsafe.Sizeof(accounting), 0)
		if ok == 0 {
			return fmt.Errorf("wait for picker descendants: %w", err)
		}
		if accounting.Active == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("picker descendants did not terminate promptly")
		}
		time.Sleep(time.Millisecond)
	}
}

type processIdentity struct {
	PID     int
	Created uint64
}

func identityForHandle(h syscall.Handle, pid int) (processIdentity, error) {
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return processIdentity{}, err
	}
	return processIdentity{pid, uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)}, nil
}

func processIdentityForPID(pid int) (processIdentity, error) {
	h, err := syscall.OpenProcess(0x1000, false, uint32(pid)) // QUERY_LIMITED_INFORMATION
	if err != nil {
		return processIdentity{}, err
	}
	defer syscall.CloseHandle(h)
	return identityForHandle(h, pid)
}

type pickerOwner struct{ Root processIdentity }

// Helpers retain a non-inherited job handle until process exit. Closing it in
// a normal deferred cleanup would terminate the helper itself before output
// and main's final bookkeeping finish.
var internalWork struct {
	sync.Once
	job          syscall.Handle
	self, launch processIdentity
	owner        string
	claimPath    string
	err          error
}

func BeginInternalWork() error {
	owner := os.Getenv(pickerOwnerEnv)
	if owner == "" {
		return nil
	} // direct diagnostic/CLI calls are not picker-owned
	internalWork.Do(func() {
		internalWork.owner = owner
		internalWork.self, internalWork.err = processIdentityForPID(os.Getpid())
		if internalWork.err != nil {
			return
		}
		internalWork.launch, internalWork.err = pickerLaunchIdentity(owner, internalWork.self)
		if internalWork.err != nil {
			return
		}
		internalWork.job, internalWork.err = newProcessJob()
		if internalWork.err != nil {
			return
		}
		current, err := syscall.GetCurrentProcess()
		if err != nil {
			syscall.CloseHandle(internalWork.job)
			internalWork.job = 0
			internalWork.err = err
			return
		}
		ok, _, err := assignJob.Call(uintptr(internalWork.job), uintptr(current))
		if ok == 0 {
			syscall.CloseHandle(internalWork.job)
			internalWork.job = 0
			internalWork.err = fmt.Errorf("assign helper job: %w", err)
		}
	})
	return internalWork.err
}

// PickerOutput launches only this picker in a kill-on-close job. JOB_LIST
// assigns ownership inside CreateProcess, including if the launcher dies.
// No shell selection, console-group flag or executable launcher is added.
func PickerOutput(cmd *exec.Cmd, started func(error)) (out []byte, runErr error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	owner, err := os.MkdirTemp("", "catclip-picker-owner-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(owner)
	job, err := newProcessJob()
	if err != nil {
		return nil, err
	}
	defer func() {
		if job != 0 {
			syscall.CloseHandle(job)
		}
	}()
	defer os.WriteFile(filepath.Join(owner, "closed"), nil, 0600)
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer stdinR.Close()
	defer stdinW.Close()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	stderr, ok := cmd.Stderr.(*os.File)
	if !ok {
		return nil, errors.New("picker stderr must be an inherited file")
	}
	files := []*os.File{stdinR, stdoutW, stderr}
	var handles [3]syscall.Handle
	defer func() {
		for _, handle := range handles {
			if handle != 0 {
				syscall.CloseHandle(handle)
			}
		}
	}()
	current, err := syscall.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	for i, file := range files {
		if err := syscall.DuplicateHandle(current, syscall.Handle(file.Fd()), current, &handles[i], 0, true, syscall.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
	}
	var size uintptr
	initializeAttributes.Call(0, 2, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return nil, errors.New("picker process attributes unavailable")
	}
	attributes := make([]byte, size)
	attrs := uintptr(unsafe.Pointer(&attributes[0]))
	if ok, _, err := initializeAttributes.Call(attrs, 2, 0, uintptr(unsafe.Pointer(&size))); ok == 0 {
		return nil, err
	}
	defer func() {
		deleteAttributes.Call(attrs)
		runtime.KeepAlive(attributes)
	}()
	if ok, _, err := updateAttributes.Call(attrs, 0, 0x20002, uintptr(unsafe.Pointer(&handles[0])), unsafe.Sizeof(handles), 0, 0); ok == 0 {
		return nil, fmt.Errorf("picker inherited handles: %w", err)
	}
	if ok, _, err := updateAttributes.Call(attrs, 0, 0x2000d, uintptr(unsafe.Pointer(&job)), unsafe.Sizeof(job), 0, 0); ok == 0 {
		return nil, fmt.Errorf("picker job-at-creation: %w", err)
	}
	startup := struct {
		syscall.StartupInfo
		Attributes uintptr
	}{Attributes: attrs}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = syscall.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = handles[0], handles[1], handles[2]
	flags := uint32(0x80000 | syscall.CREATE_UNICODE_ENVIRONMENT | 0x4)
	if sys := cmd.SysProcAttr; sys != nil {
		if sys.Token != 0 || sys.ParentProcess != 0 || sys.ProcessAttributes != nil || sys.ThreadAttributes != nil || sys.NoInheritHandles || len(sys.AdditionalInheritedHandles) != 0 {
			return nil, errors.New("unsupported picker process attributes")
		}
		flags |= sys.CreationFlags
		if sys.HideWindow {
			startup.Flags |= syscall.STARTF_USESHOWWINDOW
		}
	}
	args := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		args[i] = syscall.EscapeArg(arg)
	}
	commandLine, err := syscall.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return nil, err
	}
	if sys := cmd.SysProcAttr; sys != nil && sys.CmdLine != "" {
		commandLine, err = syscall.UTF16PtrFromString(sys.CmdLine)
		if err != nil {
			return nil, err
		}
	}
	application, err := syscall.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return nil, err
	}
	var cwd *uint16
	if cmd.Dir != "" {
		cwd, err = syscall.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return nil, err
		}
	}
	env := cmd.Environ()
	filtered := env[:0]
	for _, entry := range env {
		if !strings.EqualFold(strings.SplitN(entry, "=", 2)[0], pickerOwnerEnv) {
			filtered = append(filtered, entry)
		}
	}
	env = append(filtered, pickerOwnerEnv+"="+owner)
	environment, err := windowsEnvironment(env)
	if err != nil {
		return nil, err
	}
	var info syscall.ProcessInformation
	err = syscall.CreateProcess(application, commandLine, nil, nil, true, flags, &environment[0], cwd, &startup.StartupInfo, &info)
	runtime.KeepAlive(attributes)
	runtime.KeepAlive(handles)
	runtime.KeepAlive(job)
	if err != nil {
		if started != nil {
			started(err)
		}
		return nil, fmt.Errorf("start owned picker: %w", err)
	}
	defer syscall.CloseHandle(info.Process)
	defer syscall.CloseHandle(info.Thread)
	process, err := os.FindProcess(int(info.ProcessId))
	if err != nil {
		return nil, err
	}
	cmd.Process = process
	waited := false
	defer func() {
		if !waited {
			_ = process.Release()
		}
	}()
	if started != nil {
		started(nil)
	}
	root, err := identityForHandle(info.Process, int(info.ProcessId))
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(pickerOwner{root})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(owner, "picker.json"), metadata, 0600); err != nil {
		return nil, err
	}
	if previous, _, err := resumeThread.Call(uintptr(info.Thread)); previous == 0xffffffff {
		return nil, fmt.Errorf("resume picker: %w", err)
	}
	stdinR.Close()
	stdoutW.Close()
	for i, handle := range handles {
		syscall.CloseHandle(handle)
		handles[i] = 0
	}
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		if cmd.Stdin != nil {
			_, _ = io.Copy(stdinW, cmd.Stdin)
		}
		stdinW.Close()
	}()
	type outputResult struct {
		bytes []byte
		err   error
	}
	outputDone := make(chan outputResult, 1)
	go func() { bytes, err := io.ReadAll(stdoutR); outputDone <- outputResult{bytes, err} }()
	state, waitErr := process.Wait()
	waited = true
	cmd.ProcessState = state
	_ = os.WriteFile(filepath.Join(owner, "closed"), nil, 0600)
	runErr = stopProcessJob(job)
	syscall.CloseHandle(job)
	job = 0
	result := <-outputDone
	<-inputDone
	if waitErr == nil && !state.Success() {
		waitErr = &exec.ExitError{ProcessState: state}
	}
	if result.err == nil && runErr == nil {
		return result.bytes, waitErr
	}
	return result.bytes, errors.Join(waitErr, result.err, runErr)
}

func windowsEnvironment(entries []string) ([]uint16, error) {
	// Cmd.Environ already handles duplicate/case-insensitive environment keys.
	var block []uint16
	sort.Slice(entries, func(i, j int) bool { return strings.ToUpper(entries[i]) < strings.ToUpper(entries[j]) })
	for _, entry := range entries {
		encoded, err := syscall.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, encoded...)
	}
	block = append(block, 0)
	if len(block) == 1 {
		block = append(block, 0)
	}
	return block, nil
}
