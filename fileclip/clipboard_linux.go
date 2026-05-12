//go:build linux

package fileclip

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// isWSL detects Windows Subsystem for Linux.
// In WSL, runtime.GOOS is "linux" but we need Windows clipboard tools.
func isWSL() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
}

func isWayland() bool {
	return os.Getenv("WAYLAND_DISPLAY") != ""
}

// pathToFileURI converts an absolute path to a file:// URI with proper
// percent-encoding (spaces → %20, etc.), matching the text/uri-list spec.
func pathToFileURI(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String()
}

// ---------------------------------------------------------------------------
// Copy
// ---------------------------------------------------------------------------

func copyPlatform(paths []string) error {
	if isWSL() {
		return copyWSL(paths)
	}

	// Build text/uri-list payload with CRLF line endings (per RFC 2483).
	var uris []string
	for _, p := range paths {
		uris = append(uris, pathToFileURI(p))
	}
	payload := strings.Join(uris, "\r\n") + "\r\n"

	if isWayland() {
		return copyWayland(payload)
	}
	return copyX11(payload)
}

func copyX11(payload string) error {
	if _, err := exec.LookPath("xclip"); err != nil {
		return fmt.Errorf("%w: xclip not found (install: sudo apt install xclip)", ErrToolNotFound)
	}
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "text/uri-list")
	cmd.Stdin = strings.NewReader(payload)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: xclip: %v", ErrToolFailed, err)
	}
	return nil
}

func copyWayland(payload string) error {
	if _, err := exec.LookPath("wl-copy"); err != nil {
		return fmt.Errorf("%w: wl-copy not found (install: sudo apt install wl-clipboard)", ErrToolNotFound)
	}
	cmd := exec.Command("wl-copy", "--type", "text/uri-list")
	cmd.Stdin = strings.NewReader(payload)
	// Do not capture stderr. wl-copy forks into the background holding the pipe,
	// which causes cmd.Run() to block forever.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: wl-copy: %v", ErrToolFailed, err)
	}
	return nil
}

// copyWSL uses powershell.exe to set file references on the Windows clipboard.
// Note: clip.exe is text-only and cannot set file references — we must use
// PowerShell's Set-Clipboard -Path which sets the CF_HDROP format.
// Paths are converted from WSL format (/mnt/c/...) to Windows format (C:\...)
// using wslpath.
func copyWSL(paths []string) error {
	var winPaths []string
	for _, p := range paths {
		cmd := exec.Command("wslpath", "-w", p)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("%w: wslpath failed for %s: %v", ErrToolFailed, p, err)
		}
		winPath := strings.TrimSpace(string(out))
		// Escape single quotes for PowerShell literal strings.
		winPath = strings.ReplaceAll(winPath, "'", "''")
		winPaths = append(winPaths, fmt.Sprintf("'%s'", winPath))
	}

	script := fmt.Sprintf("Set-Clipboard -Path %s", strings.Join(winPaths, ","))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", "-")
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%w: powershell.exe not found", ErrToolNotFound)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%w: powershell.exe: %s", ErrToolFailed, msg)
		}
		return fmt.Errorf("%w: powershell.exe: %v", ErrToolFailed, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Paste
// ---------------------------------------------------------------------------

func pastePlatform() ([]string, error) {
	has, err := hasPlatform()
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrNoFileRefs
	}

	if isWSL() {
		return pasteWSL()
	}
	if isWayland() {
		return pasteWayland()
	}
	return pasteX11()
}

func pasteX11() ([]string, error) {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "text/uri-list", "-o")
	out, err := cmd.Output()
	if err != nil {
		return nil, ErrNoFileRefs
	}
	return parseURIList(string(out))
}

func pasteWayland() ([]string, error) {
	cmd := exec.Command("wl-paste", "--type", "text/uri-list")
	out, err := cmd.Output()
	if err != nil {
		return nil, ErrNoFileRefs
	}
	return parseURIList(string(out))
}

func pasteWSL() ([]string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command",
		"Get-Clipboard -Format FileDropList | ForEach-Object { $_.FullName }")
	out, err := cmd.Output()
	if err != nil {
		return nil, ErrNoFileRefs
	}

	// Convert Windows paths back to WSL paths via wslpath.
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		p := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if p == "" {
			continue
		}
		wslCmd := exec.Command("wslpath", "-u", p)
		wslOut, err := wslCmd.Output()
		if err != nil {
			paths = append(paths, p) // fallback: keep Windows path
			continue
		}
		paths = append(paths, strings.TrimSpace(string(wslOut)))
	}
	if len(paths) == 0 {
		return nil, ErrNoFileRefs
	}
	return paths, nil
}

// parseURIList parses a text/uri-list payload (RFC 2483) into file paths.
// Lines starting with # are comments. URIs are percent-decoded.
func parseURIList(raw string) ([]string, error) {
	var paths []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" {
			continue
		}
		paths = append(paths, u.Path)
	}
	if len(paths) == 0 {
		return nil, ErrNoFileRefs
	}
	return paths, nil
}

// ---------------------------------------------------------------------------
// Has
// ---------------------------------------------------------------------------

func hasPlatform() (bool, error) {
	if isWSL() {
		return hasWSL()
	}
	if isWayland() {
		return hasWayland()
	}
	return hasX11()
}

func hasX11() (bool, error) {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	out, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), "text/uri-list"), nil
}

func hasWayland() (bool, error) {
	cmd := exec.Command("wl-paste", "--list-types")
	out, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), "text/uri-list"), nil
}

func hasWSL() (bool, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command",
		"if (Get-Clipboard -Format FileDropList) { 'true' } else { 'false' }")
	out, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "true", nil
}
