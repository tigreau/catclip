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
	"syscall"
	"time"
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
	return strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") || os.Getenv("WAYLAND_DISPLAY") != ""
}

func isGNOMEDesktop() bool {
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	for _, part := range strings.FieldsFunc(desktop, func(r rune) bool {
		return r == ':' || r == ';' || r == ',' || r == ' '
	}) {
		if part == "gnome" {
			return true
		}
	}
	return false
}

func gnomeMajorVersion() (int, bool) {
	raw := strings.TrimSpace(os.Getenv("CATCLIP_GNOME_VERSION"))
	if raw == "" {
		out, err := exec.Command("gnome-shell", "--version").Output()
		if err != nil {
			return 0, false
		}
		raw = string(out)
	}

	for _, field := range strings.Fields(raw) {
		field = strings.TrimLeft(field, "vV")
		majorText := field
		if idx := strings.IndexAny(majorText, ".-+"); idx >= 0 {
			majorText = majorText[:idx]
		}
		if majorText == "" {
			continue
		}
		var major int
		if _, err := fmt.Sscanf(majorText, "%d", &major); err == nil {
			return major, true
		}
	}
	return 0, false
}

func legacyGNOMEWaylandUnsupported() bool {
	if !isGNOMEDesktop() {
		return false
	}
	major, ok := gnomeMajorVersion()
	return ok && major < MinimumGNOMEFileClipboardMajor
}

// pathToFileURI converts an absolute path to a file:// URI with proper
// percent-encoding (spaces -> %20, etc.), matching the text/uri-list spec.
func pathToFileURI(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String()
}

// linuxClipboardPayload holds the MIME type and body for a Wayland clipboard
// write. GNOME Wayland browser upload paste works from a live text/uri-list
// offer.
type linuxClipboardPayload struct {
	MIMEType string
	Body     string
}

func linuxFileURIs(paths []string) []string {
	uris := make([]string, 0, len(paths))
	for _, p := range paths {
		uris = append(uris, pathToFileURI(p))
	}
	return uris
}

// linuxClipboardPayloadForWayland builds the clipboard payload for the Wayland
// backend. X11 file-reference clipboard writes are intentionally unsupported.
func linuxClipboardPayloadForWayland(paths []string) linuxClipboardPayload {
	uris := linuxFileURIs(paths)
	return linuxClipboardPayload{
		MIMEType: "text/uri-list",
		// Standard text/uri-list payloads use CRLF line endings (RFC 2483).
		Body: strings.Join(uris, "\r\n") + "\r\n",
	}
}

// ---------------------------------------------------------------------------
// Copy
// ---------------------------------------------------------------------------

func copyPlatform(paths []string) error {
	if isWSL() {
		return copyWSL(paths)
	}

	if isWayland() {
		if legacyGNOMEWaylandUnsupported() {
			return ErrLegacyGNOMEUnsupported
		}
		payload := linuxClipboardPayloadForWayland(paths)
		return copyWayland(payload.Body, payload.MIMEType)
	}
	return ErrX11Unsupported
}

func copyWayland(payload, mimeType string) error {
	if _, err := exec.LookPath("wl-copy"); err != nil {
		// Keep the message minimal; catclip provides the install hint.
		return fmt.Errorf("%w: wl-copy not found", ErrToolNotFound)
	}

	payloadFile, err := os.CreateTemp("", "fileclip-wayland-payload-*")
	if err != nil {
		return fmt.Errorf("%w: wl-copy: %v", ErrToolFailed, err)
	}
	payloadPath := payloadFile.Name()
	defer os.Remove(payloadPath)
	if _, err := payloadFile.WriteString(payload); err != nil {
		_ = payloadFile.Close()
		return fmt.Errorf("%w: wl-copy: %v", ErrToolFailed, err)
	}
	if err := payloadFile.Close(); err != nil {
		return fmt.Errorf("%w: wl-copy: %v", ErrToolFailed, err)
	}

	stdin, err := os.Open(payloadPath)
	if err != nil {
		return fmt.Errorf("%w: wl-copy: %v", ErrToolFailed, err)
	}
	defer stdin.Close()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("%w: wl-copy: %v", ErrToolFailed, err)
	}
	defer devNull.Close()

	cmd := exec.Command("wl-copy", "--foreground", "--type", mimeType)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = stdin
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: wl-copy: %v", ErrToolFailed, err)
	}
	time.Sleep(200 * time.Millisecond)

	// wl-copy must remain alive to serve the Wayland clipboard. Running it in
	// foreground mode and releasing the process lets this short-lived CLI exit.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("%w: wl-copy: %v", ErrToolFailed, err)
	}
	return nil
}

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
	return nil, ErrX11Unsupported
}

func pasteWayland() ([]string, error) {
	// Try standard text/uri-list first, then fall back to
	// x-special/gnome-copied-files for GNOME-family desktops.
	cmd := exec.Command("wl-paste", "--type", "text/uri-list")
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.Command("wl-paste", "--type", "x-special/gnome-copied-files")
		out, err = cmd.Output()
		if err != nil {
			return nil, ErrNoFileRefs
		}
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

// parseURIList parses a text/uri-list payload (RFC 2483) or an
// x-special/gnome-copied-files payload into file paths.
// Lines starting with # are comments. URIs are percent-decoded.
func parseURIList(raw string) ([]string, error) {
	var paths []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// x-special/gnome-copied-files starts with "copy" or "cut".
		if line == "copy" || line == "cut" {
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
	return false, ErrX11Unsupported
}

func hasWayland() (bool, error) {
	// Check available types for either text/uri-list (KDE, standard) or
	// x-special/gnome-copied-files (GNOME-family desktops).
	cmd := exec.Command("wl-paste", "--list-types")
	out, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	outStr := string(out)
	return strings.Contains(outStr, "text/uri-list") || strings.Contains(outStr, "x-special/gnome-copied-files"), nil
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
