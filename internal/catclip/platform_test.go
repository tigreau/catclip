package catclip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func TestClipboardCommandUsesClipExeOnWindows(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.exe")
	if err := os.WriteFile(clip, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatalf("write fake clip.exe: %v", err)
	}

	t.Setenv("PATH", dir)

	cmd, err := output.ClipboardCommand("windows", platform.Palette{})
	if err != nil {
		t.Fatalf("output.ClipboardCommand returned error: %v", err)
	}
	if filepath.Base(cmd.Path) != "clip.exe" {
		t.Fatalf("expected clip.exe, got %q", cmd.Path)
	}
}

func TestClipboardCommandShowsNativeWindowsInstallHint(t *testing.T) {
	t.Setenv("PATH", "")

	_, err := output.ClipboardCommand("windows", platform.Palette{})
	if err == nil {
		t.Fatal("expected clipboard lookup error")
	}
	if !strings.Contains(err.Error(), "ships with Windows") {
		t.Fatalf("expected Windows install hint, got: %v", err)
	}
}

func TestClipboardCommandShowsWSLInteropHint(t *testing.T) {
	t.Setenv("PATH", "")

	_, err := output.ClipboardCommand("wsl", platform.Palette{})
	if err == nil {
		t.Fatal("expected clipboard lookup error")
	}
	if !strings.Contains(err.Error(), "WSL interop") {
		t.Fatalf("expected WSL interop hint, got: %v", err)
	}
}
