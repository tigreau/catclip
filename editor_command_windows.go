//go:build windows

package catclip

import (
	"syscall"
	"unsafe"
)

func splitWindowsEditorCommand(command string) ([]string, error) {
	ptr, err := syscall.UTF16PtrFromString(command)
	if err != nil {
		return nil, err
	}

	var argc int32
	argv, err := syscall.CommandLineToArgv(ptr, &argc)
	if err != nil {
		return nil, err
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(argv)))

	args := make([]string, 0, argc)
	for i := 0; i < int(argc); i++ {
		args = append(args, syscall.UTF16ToString(argv[i][:]))
	}
	return args, nil
}
