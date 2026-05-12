package fileclip

import "errors"

var (
	// ErrFileNotFound is returned when a path does not exist on disk.
	ErrFileNotFound = errors.New("fileclip: file does not exist")

	// ErrNotAFile is returned when a path points to a directory instead of
	// a regular file. Web UIs do not handle directory pastes — use files only.
	ErrNotAFile = errors.New("fileclip: path is a directory, not a file")

	// ErrUnsupportedPlatform is returned on platforms where fileclip does not
	// yet have a clipboard implementation.
	ErrUnsupportedPlatform = errors.New("fileclip: unsupported platform")

	// ErrToolNotFound is returned when the required clipboard tool
	// (osascript, xclip, wl-copy, powershell) is not installed.
	// Callers can use this to fall back to text clipboard or prompt the
	// user to install the missing tool.
	ErrToolNotFound = errors.New("fileclip: clipboard tool not found")

	// ErrToolFailed is returned when the underlying clipboard tool
	// exists but failed to execute the operation. The tool's stderr
	// output is wrapped in the error for debugging.
	ErrToolFailed = errors.New("fileclip: clipboard tool failed")

	// ErrNoFileRefs is returned by [Paste] when the clipboard does not
	// contain file references (e.g. it contains text or image data instead).
	ErrNoFileRefs = errors.New("fileclip: clipboard does not contain file references")
)
