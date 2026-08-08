package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// testProbeTimeout is a generous budget for fake-binary probe tests so full-
// suite parallel load (process-spawn queueing, macOS first-exec assessment)
// cannot flake them; the production ProbeToolVersion still uses its own budget.
const testProbeTimeout = 30 * time.Second

func TestParseToolVersionOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "fzf", output: "0.44.1 (26f37b8)\n", want: "0.44.1"},
		{name: "ripgrep multiline", output: "ripgrep 14.1.0 (rev abcdef)\nPCRE2\nAll features enabled\n", want: "14.1.0"},
		{name: "unknown fallback", output: "banana\n", want: "banana"},
		{name: "Windows CRLF", output: "0.44.1 (26f37b8)\r\n", want: "0.44.1"},
		{name: "empty", output: "", want: ""},
		{name: "ripgrep prefix only fallback", output: "ripgrep\n", want: "ripgrep"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseToolVersionOutput(tt.output); got != tt.want {
				t.Fatalf("parseToolVersionOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProbeToolVersion_EmptyPath(t *testing.T) {
	_, err := ProbeToolVersion("")
	if err == nil {
		t.Fatal("ProbeToolVersion(\"\") returned nil error; want error")
	}
	if errors.Is(err, ErrProbeTimeout) {
		t.Fatalf("empty path should not report timeout, got %v", err)
	}
}

func TestProbeToolVersionSuccessFormats(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary tests use POSIX shell scripts; see fzf CI smoke for Windows coverage")
	}
	tests := []struct {
		name        string
		versionLine string
		want        string
	}{
		{name: "fzf", versionLine: "0.44.1 (26f37b8)", want: "0.44.1"},
		{name: "ripgrep", versionLine: "ripgrep 14.1.0", want: "14.1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := writeFakeVersionBinary(t, t.TempDir(), "fake-"+tt.name, tt.versionLine)
			got, err := probeToolVersion(bin, testProbeTimeout)
			if err != nil {
				t.Fatalf("probeToolVersion() err = %v", err)
			}
			if got != tt.want {
				t.Fatalf("probeToolVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProbeToolVersion_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary tests use POSIX shell scripts")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-broken")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake broken: %v", err)
	}
	_, err := probeToolVersion(bin, testProbeTimeout)
	if err == nil {
		t.Fatal("ProbeToolVersion(nonzero-exit) returned nil error; want error")
	}
	if errors.Is(err, ErrProbeTimeout) {
		t.Fatalf("nonzero-exit should not report timeout, got %v", err)
	}
}

func TestProbeToolVersion_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary tests use POSIX shell scripts")
	}
	if testing.Short() {
		t.Skip("timeout test uses subprocess sleep; skipped in -short")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-hang")
	// Sleep longer than the short probe timeout below (fast + deterministic;
	// no dependence on the multi-second production budget).
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatalf("write fake hang: %v", err)
	}
	_, err := probeToolVersion(bin, 100*time.Millisecond)
	if err == nil {
		t.Fatal("ProbeToolVersion(hang) returned nil error; want ErrProbeTimeout")
	}
	if !errors.Is(err, ErrProbeTimeout) {
		t.Fatalf("hang error = %v, want ErrProbeTimeout", err)
	}
}

// writeFakeVersionBinary drops a tiny shell script under dir that
// prints version on --version and exits 0. Returns its absolute path.
// POSIX only — Windows coverage is via the CI --version smoke test
// (which uses the actual bundled fzf/rg binaries).
func writeFakeVersionBinary(t *testing.T, dir, name, versionLine string) string {
	t.Helper()
	bin := filepath.Join(dir, name)
	script := "#!/bin/sh\nprintf '%s\\n' \"" + versionLine + "\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return bin
}
