package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectForGOOS(t *testing.T) {
	tests := []struct {
		name        string
		goos        string
		procVersion string
		want        string
	}{
		{name: "native Windows", goos: "windows", want: "windows"},
		{name: "WSL from proc version", goos: "linux", procVersion: "Linux version 5.15.167.4-microsoft-standard-WSL2", want: "wsl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectForGOOS(tt.goos, tt.procVersion); got != tt.want {
				t.Fatalf("DetectForGOOS() = %q, want %q", got, tt.want)
			}
		})
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

func TestMultiSelectToggleAllForGOOS(t *testing.T) {
	tests := []struct {
		goos        string
		wantBinding string
		wantKey     string
	}{
		{goos: "darwin", wantBinding: "ctrl-a:toggle-all", wantKey: "Ctrl-A"},
		{goos: "linux", wantBinding: "alt-a:toggle-all", wantKey: "Alt-A"},
		{goos: "windows", wantBinding: "alt-a:toggle-all", wantKey: "Alt-A"},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if got := multiSelectToggleAllBindingForGOOS(tt.goos); got != tt.wantBinding {
				t.Fatalf("binding = %q, want %q", got, tt.wantBinding)
			}
			if got := multiSelectToggleAllKeyForGOOS(tt.goos); got != tt.wantKey {
				t.Fatalf("key = %q, want %q", got, tt.wantKey)
			}
		})
	}
}
