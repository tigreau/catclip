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

func TestParseToolVersionOutput_FzfFormat(t *testing.T) {
	got := parseToolVersionOutput("0.44.1 (26f37b8)\n")
	if got != "0.44.1" {
		t.Fatalf("parseToolVersionOutput(fzf) = %q, want 0.44.1", got)
	}
}

func TestParseToolVersionOutput_RgFormat(t *testing.T) {
	// rg prints multi-line; we should read only the first line.
	got := parseToolVersionOutput("ripgrep 14.1.0 (rev abcdef)\nPCRE2\nAll features enabled\n")
	if got != "14.1.0" {
		t.Fatalf("parseToolVersionOutput(rg) = %q, want 14.1.0", got)
	}
}

func TestParseToolVersionOutput_UnknownFormat(t *testing.T) {
	got := parseToolVersionOutput("banana\n")
	if got != "banana" {
		t.Fatalf("parseToolVersionOutput(unknown) = %q, want fallback 'banana'", got)
	}
}

func TestParseToolVersionOutput_CRLF(t *testing.T) {
	// Windows binaries print CRLF line endings.
	got := parseToolVersionOutput("0.44.1 (26f37b8)\r\n")
	if got != "0.44.1" {
		t.Fatalf("parseToolVersionOutput(crlf) = %q, want 0.44.1", got)
	}
}

func TestParseToolVersionOutput_Empty(t *testing.T) {
	got := parseToolVersionOutput("")
	if got != "" {
		t.Fatalf("parseToolVersionOutput(empty) = %q, want empty", got)
	}
}

func TestParseToolVersionOutput_RgPrefixOnly(t *testing.T) {
	// Defensive: "ripgrep" alone with no version token still returns
	// the first line via the fallback branch.
	got := parseToolVersionOutput("ripgrep\n")
	if got != "ripgrep" {
		t.Fatalf("parseToolVersionOutput(rg-prefix-only) = %q, want 'ripgrep' fallback", got)
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

func TestProbeToolVersion_FzfFormat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary tests use POSIX shell scripts; see fzf CI smoke for Windows coverage")
	}
	dir := t.TempDir()
	bin := writeFakeVersionBinary(t, dir, "fake-fzf", "0.44.1 (26f37b8)")
	got, err := probeToolVersion(bin, testProbeTimeout)
	if err != nil {
		t.Fatalf("ProbeToolVersion(fake-fzf) err = %v", err)
	}
	if got != "0.44.1" {
		t.Fatalf("ProbeToolVersion(fake-fzf) = %q, want 0.44.1", got)
	}
}

func TestProbeToolVersion_RgFormat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary tests use POSIX shell scripts; see rg CI smoke for Windows coverage")
	}
	dir := t.TempDir()
	bin := writeFakeVersionBinary(t, dir, "fake-rg", "ripgrep 14.1.0")
	got, err := probeToolVersion(bin, testProbeTimeout)
	if err != nil {
		t.Fatalf("ProbeToolVersion(fake-rg) err = %v", err)
	}
	if got != "14.1.0" {
		t.Fatalf("ProbeToolVersion(fake-rg) = %q, want 14.1.0", got)
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
