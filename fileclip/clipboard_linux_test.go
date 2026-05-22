//go:build linux

package fileclip

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	if isWSL() {
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

func TestCopyReturnsX11UnsupportedOnX11(t *testing.T) {
	if isWSL() {
		t.Skip("WSL uses the Windows clipboard path")
	}
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")

	path := filepath.Join(t.TempDir(), "x11.txt")
	if err := os.WriteFile(path, []byte("x11 unsupported\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	err := Copy(path)
	if !errors.Is(err, ErrX11Unsupported) {
		t.Fatalf("Copy error = %v, want ErrX11Unsupported", err)
	}
}
