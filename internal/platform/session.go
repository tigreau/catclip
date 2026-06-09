package platform

import (
	"os"
	"runtime"
	"strings"
)

// LinuxSessionKind classifies the kind of Linux session catclip is running
// inside. It exists to make the X11 vs Wayland policy decision in Main()
// without sprinkling env-var checks across packages, and to keep
// unknown/displayless environments (SSH, Docker, TTY, CI) distinct from
// detected X11 desktops.
type LinuxSessionKind string

const (
	// LinuxSessionWayland is a confirmed Wayland session: WAYLAND_DISPLAY
	// is set, or XDG_SESSION_TYPE=wayland.
	LinuxSessionWayland LinuxSessionKind = "wayland"

	// LinuxSessionX11 is a confirmed X11 desktop session: XDG_SESSION_TYPE=x11,
	// or DISPLAY is set with no Wayland signal at all. Catclip refuses to
	// run in this state.
	LinuxSessionX11 LinuxSessionKind = "x11"

	// LinuxSessionWSL is Windows Subsystem for Linux, where /proc/version
	// carries a Microsoft/WSL signature. Clipboard delivery goes through
	// the Windows host, so the X11/Wayland question is moot.
	LinuxSessionWSL LinuxSessionKind = "wsl"

	// LinuxSessionUnknown is any Linux invocation with no clear display
	// signal: SSH without DISPLAY, Docker, TTY, cron, CI. Catclip runs
	// normally for stdout/file-only invocations; clipboard sinks fail
	// later with a Wayland-required message.
	LinuxSessionUnknown LinuxSessionKind = "unknown"
)

// DetectLinuxSession reads the live environment and returns the Linux
// session classification. Returns LinuxSessionUnknown when
// runtime.GOOS != "linux".
func DetectLinuxSession() LinuxSessionKind {
	procVersion := ""
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/version"); err == nil {
			procVersion = string(data)
		}
	}
	return DetectLinuxSessionForEnv(runtime.GOOS, os.Getenv, procVersion)
}

// DetectLinuxSessionForEnv is the testable core of DetectLinuxSession. It
// takes getenv as a parameter so tests can inject env state without using
// t.Setenv, mirroring the DetectForGOOS pattern.
//
// Classification order (first match wins):
//  1. goos != "linux"        -> unknown
//  2. WSL signature in proc  -> wsl
//  3. Wayland signal         -> wayland
//     (WAYLAND_DISPLAY set, or XDG_SESSION_TYPE=wayland)
//  4. X11 signal             -> x11
//     (XDG_SESSION_TYPE=x11, or DISPLAY set with no Wayland signal)
//  5. otherwise              -> unknown
//
// A live WAYLAND_DISPLAY wins over a simultaneous DISPLAY (XWayland
// exports both) and over a stale XDG_SESSION_TYPE=x11.
func DetectLinuxSessionForEnv(goos string, getenv func(string) string, procVersion string) LinuxSessionKind {
	if goos != "linux" {
		return LinuxSessionUnknown
	}
	if isWSLProcVersion(procVersion) {
		return LinuxSessionWSL
	}
	if getenv("WSL_DISTRO_NAME") != "" {
		return LinuxSessionWSL
	}

	waylandDisplay := getenv("WAYLAND_DISPLAY")
	sessionType := strings.ToLower(getenv("XDG_SESSION_TYPE"))
	display := getenv("DISPLAY")

	if waylandDisplay != "" || sessionType == "wayland" {
		return LinuxSessionWayland
	}
	if sessionType == "x11" || display != "" {
		return LinuxSessionX11
	}
	return LinuxSessionUnknown
}
