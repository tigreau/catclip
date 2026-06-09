package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectForGOOSClassifiesNativeWindows(t *testing.T) {
	if got := DetectForGOOS("windows", ""); got != "windows" {
		t.Fatalf("expected windows, got %q", got)
	}
}

func TestDetectForGOOSClassifiesWSLFromProcVersion(t *testing.T) {
	procVersion := "Linux version 5.15.167.4-microsoft-standard-WSL2"
	if got := DetectForGOOS("linux", procVersion); got != "wsl" {
		t.Fatalf("expected wsl, got %q", got)
	}
}

func TestToolBinaryNameForGOOSAddsExeOnWindows(t *testing.T) {
	if got := toolBinaryNameForGOOS("windows", "rg"); got != "rg.exe" {
		t.Fatalf("expected rg.exe, got %q", got)
	}
	if got := toolBinaryNameForGOOS("windows", "fzf.exe"); got != "fzf.exe" {
		t.Fatalf("expected existing .exe suffix to be preserved, got %q", got)
	}
	if got := toolBinaryNameForGOOS("linux", "rg"); got != "rg" {
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
