package platform

import (
	"os"
	"runtime"
	"strings"
)

// Detect returns the catclip platform classification (macos, linux, wsl,
// windows, …). WSL is detected by reading /proc/version for the Microsoft /
// WSL signature.
func Detect() string {
	procVersion := ""
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/version"); err == nil {
			procVersion = string(data)
		}
	}
	return DetectForGOOS(runtime.GOOS, procVersion)
}

// DetectForGOOS is the testable core of Detect.
func DetectForGOOS(goos, procVersion string) string {
	switch goos {
	case "darwin":
		return "macos"
	case "linux":
		if isWSLProcVersion(procVersion) {
			return "wsl"
		}
		return "linux"
	case "windows":
		return "windows"
	default:
		return goos
	}
}

func isWSLProcVersion(procVersion string) bool {
	version := strings.ToLower(procVersion)
	return strings.Contains(version, "microsoft") || strings.Contains(version, "wsl")
}
