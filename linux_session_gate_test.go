package catclip

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/platform"
)

// TestLinuxSessionGateErrorForBlocksOnlyX11 pins the gate's policy: every
// non-X11 classification returns nil so SSH/Docker/TTY/CI/Wayland/WSL/macOS
// invocations are not affected; only detected X11 desktops are rejected.
func TestLinuxSessionGateErrorForBlocksOnlyX11(t *testing.T) {
	cases := []struct {
		kind    platform.LinuxSessionKind
		wantErr bool
	}{
		{platform.LinuxSessionWayland, false},
		{platform.LinuxSessionWSL, false},
		{platform.LinuxSessionUnknown, false},
		{platform.LinuxSessionX11, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			err := linuxSessionGateErrorFor(tc.kind)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for kind=%q, got nil", tc.kind)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error for kind=%q, got %v", tc.kind, err)
			}
		})
	}
}

// TestLinuxSessionGateErrorForX11MessageContent pins the actionable meaning,
// while leaving the diagnostic prose free to change.
func TestLinuxSessionGateErrorForX11MessageContent(t *testing.T) {
	err := linuxSessionGateErrorFor(platform.LinuxSessionX11)
	if err == nil {
		t.Fatal("expected non-nil error for X11")
	}
	msg := err.Error()
	for _, want := range []string{
		"X11",
		"Linux desktop sessions must use Wayland",
		"Wayland session",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("gate message missing %q\ngot:\n%s", want, msg)
		}
	}
}

// TestLinuxSessionGateErrorForX11IsUsageError pins the exit-code mapping:
// gate errors must map to exit code 2 via the existing exitWithError
// usageError check, not exit 1.
func TestLinuxSessionGateErrorForX11IsUsageError(t *testing.T) {
	err := linuxSessionGateErrorFor(platform.LinuxSessionX11)
	if err == nil {
		t.Fatal("expected non-nil error for X11")
	}
	var ue usageError
	if !errors.As(err, &ue) {
		t.Fatalf("gate error must be a usageError so exitWithError exits 2; got %T: %v", err, err)
	}
}

func TestMainX11GateRunsBeforeHelpAndToolChecks(t *testing.T) {
	skipUnlessLinux(t, "X11 startup gate placement")
	if platform.Detect() == "wsl" {
		t.Skip("WSL bypasses the Linux X11 gate")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	missingTool := filepath.Join(t.TempDir(), "missing-tool")
	cmd := exec.Command(exe, "--help")
	cmd.Env = append(envWithout("CATCLIP_TEST_RUN_MAIN", "XDG_SESSION_TYPE", "WAYLAND_DISPLAY", "DISPLAY", "WSL_DISTRO_NAME", "CATCLIP_RG", "CATCLIP_FZF"),
		"CATCLIP_TEST_RUN_MAIN=1",
		"XDG_SESSION_TYPE=x11",
		"WAYLAND_DISPLAY=",
		"DISPLAY=:0",
		"CATCLIP_RG="+missingTool,
		"CATCLIP_FZF="+missingTool,
	)
	out, runErr := cmd.CombinedOutput()
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exec.ExitError, got %T: %v\noutput:\n%s", runErr, runErr, out)
	}
	if got := exitErr.ExitCode(); got != 2 {
		t.Fatalf("exit code = %d, want 2\noutput:\n%s", got, out)
	}
	text := string(out)
	if !strings.Contains(text, "catclip requires a reliable Linux desktop session") {
		t.Fatalf("output missing X11 reliability message:\n%s", text)
	}
	if strings.Contains(text, "Quick Start:") || strings.Contains(text, "missing required dependencies") {
		t.Fatalf("X11 gate did not run before help/tool checks:\n%s", text)
	}
}

func envWithout(keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key] = struct{}{}
	}
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, skip := blocked[key]; skip {
			continue
		}
		out = append(out, kv)
	}
	return out
}
