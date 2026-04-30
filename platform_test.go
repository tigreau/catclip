package catclip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectPlatformForGOOSClassifiesNativeWindows(t *testing.T) {
	if got := detectPlatformForGOOS("windows", ""); got != "windows" {
		t.Fatalf("expected windows, got %q", got)
	}
}

func TestDetectPlatformForGOOSClassifiesWSLFromProcVersion(t *testing.T) {
	procVersion := "Linux version 5.15.167.4-microsoft-standard-WSL2"
	if got := detectPlatformForGOOS("linux", procVersion); got != "wsl" {
		t.Fatalf("expected wsl, got %q", got)
	}
}

func TestPlatformToolBinaryNameForGOOSAddsExeOnWindows(t *testing.T) {
	if got := platformToolBinaryNameForGOOS("windows", "rg"); got != "rg.exe" {
		t.Fatalf("expected rg.exe, got %q", got)
	}
	if got := platformToolBinaryNameForGOOS("windows", "fzf.exe"); got != "fzf.exe" {
		t.Fatalf("expected existing .exe suffix to be preserved, got %q", got)
	}
	if got := platformToolBinaryNameForGOOS("linux", "rg"); got != "rg" {
		t.Fatalf("expected linux binary name to stay unchanged, got %q", got)
	}
}

func TestBundledToolCandidatesForGOOSFindWindowsShareLayout(t *testing.T) {
	installRoot := t.TempDir()
	binDir := filepath.Join(installRoot, "bin")
	toolsDir := filepath.Join(installRoot, "share", "catclip", "bin")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatalf("mkdir tools dir: %v", err)
	}

	rgPath := filepath.Join(toolsDir, "rg.exe")
	if err := os.WriteFile(rgPath, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake rg.exe: %v", err)
	}

	got, ok := firstExistingBinary(bundledToolCandidatesForGOOS("windows", "rg", []string{binDir}, ""))
	if !ok {
		t.Fatal("expected bundled rg.exe to be found")
	}
	if got != rgPath {
		t.Fatalf("expected %q, got %q", rgPath, got)
	}
}

func TestClipboardCommandUsesClipExeOnWindows(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.exe")
	if err := os.WriteFile(clip, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatalf("write fake clip.exe: %v", err)
	}

	t.Setenv("PATH", dir)

	cmd, err := clipboardCommand("windows", colorPalette{})
	if err != nil {
		t.Fatalf("clipboardCommand returned error: %v", err)
	}
	if filepath.Base(cmd.Path) != "clip.exe" {
		t.Fatalf("expected clip.exe, got %q", cmd.Path)
	}
}

func TestClipboardCommandShowsNativeWindowsInstallHint(t *testing.T) {
	t.Setenv("PATH", "")

	_, err := clipboardCommand("windows", colorPalette{})
	if err == nil {
		t.Fatal("expected clipboard lookup error")
	}
	if !strings.Contains(err.Error(), "ships with Windows") {
		t.Fatalf("expected Windows install hint, got: %v", err)
	}
}

func TestClipboardCommandShowsWSLInteropHint(t *testing.T) {
	t.Setenv("PATH", "")

	_, err := clipboardCommand("wsl", colorPalette{})
	if err == nil {
		t.Fatal("expected clipboard lookup error")
	}
	if !strings.Contains(err.Error(), "WSL interop") {
		t.Fatalf("expected WSL interop hint, got: %v", err)
	}
}

func TestMultiSelectToggleAllBindingForGOOSUsesCtrlAOnDarwin(t *testing.T) {
	if got := multiSelectToggleAllBindingForGOOS("darwin"); got != "ctrl-a:toggle-all" {
		t.Fatalf("expected ctrl-a binding on darwin, got %q", got)
	}
	if got := multiSelectToggleAllKeyForGOOS("darwin"); got != "Ctrl-A" {
		t.Fatalf("expected Ctrl-A label on darwin, got %q", got)
	}
}

func TestMultiSelectToggleAllBindingForGOOSUsesAltAOffDarwin(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		if got := multiSelectToggleAllBindingForGOOS(goos); got != "alt-a:toggle-all" {
			t.Fatalf("expected alt-a binding on %s, got %q", goos, got)
		}
		if got := multiSelectToggleAllKeyForGOOS(goos); got != "Alt-A" {
			t.Fatalf("expected Alt-A label on %s, got %q", goos, got)
		}
	}
}
