package catclip

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// withBundleStub replaces fileclipCopy with a stub that records the path it was
// asked to copy. Returns a pointer that will be populated when emit runs, and a
// cleanup function.
func withBundleStub(t *testing.T) (*string, func()) {
	t.Helper()
	var captured string
	prev := fileclipCopy
	fileclipCopy = func(paths ...string) error {
		if len(paths) > 0 {
			captured = paths[0]
		}
		return nil
	}
	return &captured, func() { fileclipCopy = prev }
}

// withCatclipTempDir points os.TempDir to a fresh directory for the duration of
// the test so bundle cleanup doesn't touch real /tmp/catclip/.
func withCatclipTempDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("TEMP", dir)
		t.Setenv("TMP", dir)
	}
}

func TestBundleProjectNameNormalizesPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/Users/chris/Desktop/myapp", "myapp"},
		{"/Users/chris/Desktop/my app", "my-app"},
		{"/Users/chris/Desktop/my.app.v2", "my-app-v2"},
		{".", "bundle"},
		{"/", "bundle"},
		{"", "bundle"},
		{"/Users/chris/Desktop/" + strings.Repeat("x", 64), strings.Repeat("x", 32)},
	}
	for _, tc := range cases {
		if got := bundleProjectName(tc.in); got != tc.want {
			t.Errorf("bundleProjectName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBundleTempPathShape(t *testing.T) {
	dir := "/tmp/catclip"
	stamp := time.Date(2026, 5, 12, 14, 30, 22, 0, time.UTC)
	got := bundleTempPath(dir, "myapp", stamp)
	want := filepath.Join(dir, "myapp-143022.txt")
	if got != want {
		t.Fatalf("bundleTempPath = %q, want %q", got, want)
	}
}

func TestBundleBelowThresholdSkipsBundleBranch(t *testing.T) {
	withCatclipTempDir(t)
	captured, restore := withBundleStub(t)
	defer restore()

	project := setupTestProject(t, map[string]string{
		"small.txt": "tiny\n",
	})
	cfg := parseInProject(t, project, []string{"-p", "small.txt"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if *captured != "" {
		t.Fatalf("expected no bundle for small payload, fileclipCopy was called with %q", *captured)
	}
}

func TestBundleAtOrAboveThresholdCreatesFile(t *testing.T) {
	withCatclipTempDir(t)
	captured, restore := withBundleStub(t)
	defer restore()

	// 5KB of 'a' — comfortably over the 4096-byte threshold once wrapped.
	big := strings.Repeat("a", 5000) + "\n"
	cfg := emitConfig{
		OutputMode: outputModeClipboard,
	}
	env := emitEnvironment{
		WorkingDir: t.TempDir(),
	}

	stats, err := withPayloadWriter(cfg, env, io.Discard, colorPalette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, big)
		return werr
	})
	if err != nil {
		t.Fatalf("withPayloadWriter returned error: %v", err)
	}
	if stats.SinkName != "bundle" {
		t.Fatalf("expected SinkName=bundle, got %q", stats.SinkName)
	}
	if stats.BundlePath == "" {
		t.Fatalf("expected BundlePath to be set, got empty")
	}
	if *captured != stats.BundlePath {
		t.Fatalf("fileclipCopy was called with %q, stats.BundlePath = %q", *captured, stats.BundlePath)
	}

	body, err := os.ReadFile(stats.BundlePath)
	if err != nil {
		t.Fatalf("read bundle file: %v", err)
	}
	if string(body) != big {
		t.Fatalf("bundle content mismatch (got %d bytes, want %d)", len(body), len(big))
	}
}

func TestBundleFilePermissionsAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode semantics; Windows perms work differently.")
	}
	withCatclipTempDir(t)
	_, restore := withBundleStub(t)
	defer restore()

	cfg := emitConfig{
		OutputMode: outputModeClipboard,
	}
	env := emitEnvironment{
		WorkingDir: t.TempDir(),
	}
	big := strings.Repeat("z", 5000)
	stats, err := withPayloadWriter(cfg, env, io.Discard, colorPalette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, big)
		return werr
	})
	if err != nil {
		t.Fatalf("withPayloadWriter returned error: %v", err)
	}
	info, err := os.Stat(stats.BundlePath)
	if err != nil {
		t.Fatalf("stat bundle file: %v", err)
	}
	got := info.Mode().Perm()
	if got != 0o600 {
		t.Fatalf("bundle file mode = %o, want 0600", got)
	}
}

func TestBundleFilenameMatchesProjectAndTimestamp(t *testing.T) {
	withCatclipTempDir(t)
	captured, restore := withBundleStub(t)
	defer restore()

	wd := filepath.Join(t.TempDir(), "myapp")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatalf("setup wd: %v", err)
	}
	cfg := emitConfig{
		OutputMode: outputModeClipboard,
	}
	env := emitEnvironment{
		WorkingDir: wd,
	}
	_, err := withPayloadWriter(cfg, env, io.Discard, colorPalette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, strings.Repeat("x", 5000))
		return werr
	})
	if err != nil {
		t.Fatalf("withPayloadWriter returned error: %v", err)
	}

	pattern := regexp.MustCompile(`^myapp-\d{6}\.txt$`)
	base := filepath.Base(*captured)
	if !pattern.MatchString(base) {
		t.Fatalf("bundle basename %q does not match %s", base, pattern)
	}
	parent := filepath.Base(filepath.Dir(*captured))
	if parent != "catclip" {
		t.Fatalf("bundle parent dir = %q, want %q", parent, "catclip")
	}
}

func TestBundleClearsPriorBundles(t *testing.T) {
	withCatclipTempDir(t)
	_, restore := withBundleStub(t)
	defer restore()

	dir := bundleTempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(dir, "old-bundle-000000.txt")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	cfg := emitConfig{
		OutputMode: outputModeClipboard,
	}
	env := emitEnvironment{
		WorkingDir: t.TempDir(),
	}
	_, err := withPayloadWriter(cfg, env, io.Discard, colorPalette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, strings.Repeat("y", 5000))
		return werr
	})
	if err != nil {
		t.Fatalf("withPayloadWriter returned error: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale bundle %q removed, stat err = %v", stale, err)
	}
}

func TestBundleNoBundleFlagIsCanonicalGlobalArg(t *testing.T) {
	flags := canonicalGlobalArgsFromConfig(invocationConfig{}, emitConfig{NoBundle: true}, false, false, false)
	found := false
	for _, f := range flags {
		if f == "--no-bundle" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --no-bundle in resolved global flags, got %v", flags)
	}
}

func TestBundleNoBundleParsesToConfig(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	cfg := parseInProject(t, project, []string{"--no-bundle", "src/main.go"})
	if !cfg.NoBundle {
		t.Fatalf("expected cfg.NoBundle=true after --no-bundle, got false")
	}
}

func TestBundleNoBundleSkipsBundleBranchAtLargePayload(t *testing.T) {
	withCatclipTempDir(t)
	captured, restore := withBundleStub(t)
	defer restore()

	// Force a fake clipboard subprocess that just drains stdin.
	fakeClipboardOnPath(t)

	cfg := emitConfig{
		OutputMode: outputModeClipboard,
		NoBundle:   true,
	}
	env := emitEnvironment{
		Platform:   detectPlatform(),
		WorkingDir: t.TempDir(),
	}
	stats, err := withPayloadWriter(cfg, env, io.Discard, colorPalette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, strings.Repeat("q", 8000))
		return werr
	})
	if err != nil {
		t.Fatalf("withPayloadWriter returned error: %v", err)
	}
	if stats.SinkName == "bundle" {
		t.Fatalf("expected text-clipboard sink with --no-bundle, got bundle")
	}
	if *captured != "" {
		t.Fatalf("fileclipCopy should not have been called, was called with %q", *captured)
	}
}

// fakeClipboardOnPath installs a no-op script named after the platform's
// clipboard command into PATH so withPayloadWriter's text path succeeds without
// touching the real clipboard. Skips the test on platforms where the trick
// can't run (no /bin/sh).
func fakeClipboardOnPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fakeClipboardOnPath uses /bin/sh; Windows path tested separately")
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "")
	dir := t.TempDir()
	var name string
	switch detectPlatform() {
	case "macos":
		name = "pbcopy"
	case "linux":
		name = "xclip"
	default:
		t.Skipf("no fake clipboard wired for %s", detectPlatform())
	}
	script := "#!/bin/sh\ncat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake clipboard: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
