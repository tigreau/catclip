package fileclip

import (
	"errors"
	"fmt"
)

// MinimumGNOMEFileClipboardMajor is the oldest GNOME major version verified to
// support browser-oriented Wayland file-reference clipboard writes.
const MinimumGNOMEFileClipboardMajor = 46

var (
	// ErrFileNotFound is returned when a path does not exist on disk.
	ErrFileNotFound = errors.New("fileclip: file does not exist")

	// ErrNotAFile is returned when a path points to a directory instead of
	// a regular file. Web UIs do not handle directory pastes — use files only.
	ErrNotAFile = errors.New("fileclip: path is a directory, not a file")

	// ErrUnsupportedPlatform is returned on platforms where fileclip does not
	// yet have a clipboard implementation.
	ErrUnsupportedPlatform = errors.New("fileclip: unsupported platform")

	// ErrX11Unsupported is returned when catclip refuses to write a
	// file-reference clipboard offer to the X11 selection. Browser upload paste
	// is unreliable end-to-end on X11 even for native Nautilus clipboard states,
	// so fileclip leaves the clipboard untouched on X11 sessions.
	ErrX11Unsupported = errors.New("fileclip: X11 file-reference clipboard is not supported")

	// ErrLegacyGNOMEUnsupported is returned on GNOME Wayland versions older
	// than the tested file-reference clipboard baseline. Below the baseline,
	// the combination of Mutter, xdg-desktop-portal, and shipping browsers
	// is not trusted to deliver a file-reference paste, so fileclip writes
	// nothing rather than a half-working offer.
	ErrLegacyGNOMEUnsupported = errors.New(fmt.Sprintf(
		"fileclip: GNOME below %d file-reference clipboard is not supported",
		MinimumGNOMEFileClipboardMajor,
	))

	// ErrToolNotFound is returned when the required clipboard tool
	// (osascript, wl-copy, powershell) is not installed.
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
