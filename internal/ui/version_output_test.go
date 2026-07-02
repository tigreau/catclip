package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

// TestRenderVersionOutput_HappyPath verifies the three-line shape
// with valid bundled tools. Uses fake POSIX shell binaries via
// CATCLIP_FZF/CATCLIP_RG so the test is hermetic and doesn't depend
// on the actual bundled tools being present in the working tree.
func TestRenderVersionOutput_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary tests use POSIX shell scripts; see CI --version smoke for Windows")
	}
	dir := t.TempDir()
	fzfBin := writeFakeVersionBinary(t, dir, "fake-fzf", "0.44.1 (26f37b8)")
	rgBin := writeFakeVersionBinary(t, dir, "fake-rg", "ripgrep 14.1.0")

	t.Setenv("CATCLIP_FZF", fzfBin)
	t.Setenv("CATCLIP_RG", rgBin)

	var buf bytes.Buffer
	if err := RenderVersionOutput("0.6.4", &buf); err != nil {
		t.Fatalf("RenderVersionOutput err = %v", err)
	}
	out := buf.String()
	wantLines := []string{
		"catclip 0.6.4",
		"fzf 0.44.1",
		"rg  14.1.0",
		fzfBin,
		rgBin,
		"(CATCLIP_FZF)",
		"(CATCLIP_RG)",
	}
	for _, want := range wantLines {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderVersionOutput output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestRenderVersionOutput_EnvMarkerOnlyWhenSet checks that a bundled
// (non-env) resolution does NOT print the (CATCLIP_*) marker.
func TestRenderVersionOutput_EnvMarkerOnlyWhenSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary tests use POSIX shell scripts")
	}
	// Explicitly unset overrides so BundledToolBinary falls back to
	// its normal search path. It won't find fake binaries there, so
	// the resolution will fail — which is fine for this assertion
	// (we're only checking that the env-marker branch is off).
	t.Setenv("CATCLIP_FZF", "")
	t.Setenv("CATCLIP_RG", "")

	var buf bytes.Buffer
	_ = RenderVersionOutput("0.6.4", &buf)
	out := buf.String()
	if strings.Contains(out, "(CATCLIP_FZF)") {
		t.Fatalf("RenderVersionOutput printed (CATCLIP_FZF) marker with no env override\n%s", out)
	}
	if strings.Contains(out, "(CATCLIP_RG)") {
		t.Fatalf("RenderVersionOutput printed (CATCLIP_RG) marker with no env override\n%s", out)
	}
}

// TestRenderVersionOutput_MissingBinaryHint verifies the "not found"
// branch emits an actionable hint pointing at the env var.
func TestRenderVersionOutput_MissingBinaryHint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary tests use POSIX shell scripts")
	}
	// Point env vars at nonexistent paths so BundledToolBinary
	// returns (_, false).
	t.Setenv("CATCLIP_FZF", filepath.Join(t.TempDir(), "nope-fzf"))
	t.Setenv("CATCLIP_RG", filepath.Join(t.TempDir(), "nope-rg"))

	var buf bytes.Buffer
	_ = RenderVersionOutput("0.6.4", &buf)
	out := buf.String()
	if !strings.Contains(out, "<not found") {
		t.Fatalf("RenderVersionOutput missing 'not found' hint for missing binaries\n%s", out)
	}
	// The hint should name the env var so users know what to fix.
	if !strings.Contains(out, "CATCLIP_FZF") || !strings.Contains(out, "CATCLIP_RG") {
		t.Fatalf("RenderVersionOutput missing env var name in hint\n%s", out)
	}
}

// TestRenderVersionOutput_FirstLineIsCatclipVersion ensures the
// order stays stable — catclip first, then indented tool lines.
func TestRenderVersionOutput_FirstLineIsCatclipVersion(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderVersionOutput("dev", &buf); err != nil {
		t.Fatalf("RenderVersionOutput err = %v", err)
	}
	lines := strings.Split(buf.String(), "\n")
	if len(lines) < 1 || lines[0] != "catclip dev" {
		t.Fatalf("first line = %q, want 'catclip dev'\nfull output:\n%s", lines[0], buf.String())
	}
}

// TestVersionResolverMatchesRuntimeResolver is the runtime-parity
// guard: --version's rendered paths MUST be identical to what
// catclip's runtime call sites resolve for the same tools. If a
// future refactor moves the runtime call sites off
// platform.BundledToolBinary (or adds an indirection --version
// doesn't share), this test fails loudly instead of --version
// silently reporting a path different from what --contains / the
// picker actually spawns.
func TestVersionResolverMatchesRuntimeResolver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-binary tests use POSIX shell scripts")
	}
	dir := t.TempDir()
	fzfBin := writeFakeVersionBinary(t, dir, "fzf", "0.44.1")
	rgBin := writeFakeVersionBinary(t, dir, "rg", "ripgrep 14.1.0")
	t.Setenv("CATCLIP_FZF", fzfBin)
	t.Setenv("CATCLIP_RG", rgBin)

	// Runtime-side resolution — the exact functions the walker,
	// picker, and content-match paths call before spawning a tool.
	runtimeRg, runtimeRgOK := search.RipgrepBinary()
	runtimeFzf, runtimeFzfOK := discovery.FzfBinary()
	if !runtimeRgOK {
		t.Fatal("runtime rg resolver returned not-ok with env override set")
	}
	if !runtimeFzfOK {
		t.Fatal("runtime fzf resolver returned not-ok with env override set")
	}

	// --version-side resolution — same package-level resolver
	// RenderVersionOutput consults. If these ever diverge from the
	// runtime paths above, the diagnostic is lying.
	versionRg, versionRgOK := platform.BundledToolBinary("CATCLIP_RG", "rg")
	versionFzf, versionFzfOK := platform.BundledToolBinary("CATCLIP_FZF", "fzf")
	if !versionRgOK || versionRg != runtimeRg {
		t.Fatalf("--version rg = %q (ok=%v); runtime rg = %q — must match", versionRg, versionRgOK, runtimeRg)
	}
	if !versionFzfOK || versionFzf != runtimeFzf {
		t.Fatalf("--version fzf = %q (ok=%v); runtime fzf = %q — must match", versionFzf, versionFzfOK, runtimeFzf)
	}

	// Cross-check the rendered output includes those exact paths.
	var buf bytes.Buffer
	if err := RenderVersionOutput("test", &buf); err != nil {
		t.Fatalf("RenderVersionOutput err = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, versionRg) {
		t.Fatalf("rendered --version output missing runtime rg path %q\n%s", versionRg, out)
	}
	if !strings.Contains(out, versionFzf) {
		t.Fatalf("rendered --version output missing runtime fzf path %q\n%s", versionFzf, out)
	}
}

// writeFakeVersionBinary matches the same helper in
// internal/platform/tools_version_test.go. Duplicated here because
// the root package can't import the platform package's test helpers.
func writeFakeVersionBinary(t *testing.T, dir, name, versionLine string) string {
	t.Helper()
	bin := filepath.Join(dir, name)
	script := "#!/bin/sh\nprintf '%s\\n' \"" + versionLine + "\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return bin
}
