//go:build linux

package fileclip

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/platform"
)

func TestLinuxClipboardPayloadWaylandUsesURIListForBrowserPaste(t *testing.T) {
	payload := linuxClipboardPayloadForWayland([]string{"/tmp/file with spaces.txt"})

	if payload.MIMEType != "text/uri-list" {
		t.Fatalf("MIMEType = %q", payload.MIMEType)
	}

	want := "file:///tmp/file%20with%20spaces.txt\r\n"
	if payload.Body != want {
		t.Fatalf("Body = %q, want %q", payload.Body, want)
	}
}

func TestLinuxClipboardPayloadWaylandAlwaysUsesURIList(t *testing.T) {
	payload := linuxClipboardPayloadForWayland([]string{"/tmp/a.txt", "/tmp/b.txt"})

	if payload.MIMEType != "text/uri-list" {
		t.Fatalf("MIMEType = %q", payload.MIMEType)
	}

	want := "file:///tmp/a.txt\r\nfile:///tmp/b.txt\r\n"
	if payload.Body != want {
		t.Fatalf("Body = %q, want %q", payload.Body, want)
	}
}

func TestGNOMEMajorVersionParsesOverride(t *testing.T) {
	t.Setenv("CATCLIP_GNOME_VERSION", "GNOME Shell 42.9")

	major, ok := gnomeMajorVersion()
	if !ok {
		t.Fatal("expected GNOME version override to parse")
	}
	if major != 42 {
		t.Fatalf("major = %d, want 42", major)
	}
}

func TestCopyReturnsLegacyGNOMEUnsupportedOnOldGNOMEWayland(t *testing.T) {
	if linuxSessionKind() == platform.LinuxSessionWSL {
		t.Skip("WSL uses the Windows clipboard path")
	}
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_CURRENT_DESKTOP", "ubuntu:GNOME")
	t.Setenv("CATCLIP_GNOME_VERSION", "GNOME Shell 42.9")

	path := filepath.Join(t.TempDir(), "gnome42.txt")
	if err := os.WriteFile(path, []byte("legacy gnome unsupported\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	err := Copy(path)
	if !errors.Is(err, ErrLegacyGNOMEUnsupported) {
		t.Fatalf("Copy error = %v, want ErrLegacyGNOMEUnsupported", err)
	}
}

// TestCopyReturnsClipboardSessionUnsupportedOnUnknownLinux pins the behavior
// for library callers that bypass Main()'s X11 gate and reach Copy from an
// unknown/displayless Linux session. (Detected X11 desktops are blocked at
// startup by main.linuxSessionGateError, so they don't reach this branch in
// practice.) Replaces the v0.5.3 X11-sentinel test deleted in v0.6.0.
func TestCopyReturnsClipboardSessionUnsupportedOnUnknownLinux(t *testing.T) {
	if linuxSessionKind() == platform.LinuxSessionWSL {
		t.Skip("WSL uses the Windows clipboard path")
	}
	t.Setenv("XDG_SESSION_TYPE", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")

	path := filepath.Join(t.TempDir(), "unknown.txt")
	if err := os.WriteFile(path, []byte("unknown session unsupported\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	err := Copy(path)
	if !errors.Is(err, ErrLinuxClipboardSessionUnsupported) {
		t.Fatalf("Copy error = %v, want ErrLinuxClipboardSessionUnsupported", err)
	}
}
