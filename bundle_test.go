package catclip

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

// withBundleStub replaces output.FileclipCopy with a stub that records the path it was
// asked to copy. Returns a pointer that will be populated when emit runs, and a
// cleanup function.
func withBundleStub(t *testing.T) (*string, func()) {
	t.Helper()
	var captured string
	prev := output.FileclipCopy
	output.FileclipCopy = func(paths ...string) error {
		if len(paths) > 0 {
			captured = paths[0]
		}
		return nil
	}
	return &captured, func() { output.FileclipCopy = prev }
}

// withCatclipBundleDir points CATCLIP_BUNDLE_DIR to a fresh directory for the
// duration of tests that should not touch the real Documents/catclip folder.
func withCatclipBundleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", filepath.Join(dir, "catclip"))
	return filepath.Join(dir, "catclip")
}

func TestBundleDirDefaultsToDocumentsForSnapReadableFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-style home path expectation")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".config")
	if err := os.MkdirAll(config, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	userDirs := strings.Join([]string{
		`XDG_DOCUMENTS_DIR="$HOME/My Documents"`,
		`XDG_DOWNLOAD_DIR="$HOME/My Downloads"`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(config, "user-dirs.dirs"), []byte(userDirs), 0o644); err != nil {
		t.Fatalf("write user-dirs.dirs: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)

	got := output.BundleDirForEnv(output.EmitEnvironment{Platform: "linux"})
	want := filepath.Join(home, "My Documents", "catclip")
	if got != want {
		t.Fatalf("output.BundleDirForEnv = %q, want %q", got, want)
	}
}

func TestBundleDirDefaultsToDocumentsForNonLinuxPlatforms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-style home path expectation")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := output.BundleDirForEnv(output.EmitEnvironment{Platform: "macos"})
	want := filepath.Join(home, "Documents", "catclip")
	if got != want {
		t.Fatalf("output.BundleDirForEnv = %q, want %q", got, want)
	}
}

func TestBundleDirFallsBackToDefaultDocuments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-style home path expectation")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".config")
	if err := os.MkdirAll(config, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(config, "user-dirs.dirs"), []byte(`XDG_DOWNLOAD_DIR="$HOME/My Downloads"`+"\n"), 0o644); err != nil {
		t.Fatalf("write user-dirs.dirs: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)

	got := output.BundleDirForEnv(output.EmitEnvironment{Platform: "linux"})
	want := filepath.Join(home, "Documents", "catclip")
	if got != want {
		t.Fatalf("output.BundleDirForEnv = %q, want %q", got, want)
	}
}

func TestBundleDirOverrideWins(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom-bundles")
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	if got := output.BundleDirForEnv(output.EmitEnvironment{Platform: "linux"}); got != dir {
		t.Fatalf("output.BundleDirForEnv override = %q, want %q", got, dir)
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
		if got := output.BundleProjectName(tc.in); got != tc.want {
			t.Errorf("output.BundleProjectName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBundleTempPathShape(t *testing.T) {
	dir := "/tmp/catclip"
	stamp := time.Date(2026, 5, 12, 14, 30, 22, 0, time.UTC)
	got := output.BundleTempPath(dir, "myapp", stamp)
	want := filepath.Join(dir, "myapp-143022.txt")
	if got != want {
		t.Fatalf("output.BundleTempPath = %q, want %q", got, want)
	}
}

func TestBundleBelowThresholdSkipsBundleBranch(t *testing.T) {
	withCatclipBundleDir(t)
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
		t.Fatalf("expected no bundle for small payload, output.FileclipCopy was called with %q", *captured)
	}
}

func TestBundleAtOrAboveThresholdCreatesFile(t *testing.T) {
	withCatclipBundleDir(t)
	captured, restore := withBundleStub(t)
	defer restore()

	// 5KB of 'a' — comfortably over the 4096-byte threshold once wrapped.
	big := strings.Repeat("a", 5000) + "\n"
	cfg := output.EmitConfig{
		OutputMode: command.OutputModeClipboard,
	}
	env := output.EmitEnvironment{
		WorkingDir: t.TempDir(),
	}

	stats, err := output.WithPayloadWriter(cfg, env, io.Discard, platform.Palette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, big)
		return werr
	})
	if err != nil {
		t.Fatalf("output.WithPayloadWriter returned error: %v", err)
	}
	if stats.SinkName != "bundle" {
		t.Fatalf("expected SinkName=bundle, got %q", stats.SinkName)
	}
	if stats.BundlePath == "" {
		t.Fatalf("expected BundlePath to be set, got empty")
	}
	if *captured != stats.BundlePath {
		t.Fatalf("output.FileclipCopy was called with %q, stats.BundlePath = %q", *captured, stats.BundlePath)
	}

	body, err := os.ReadFile(stats.BundlePath)
	if err != nil {
		t.Fatalf("read bundle file: %v", err)
	}
	if string(body) != big {
		t.Fatalf("bundle content mismatch (got %d bytes, want %d)", len(body), len(big))
	}
}

func TestBundleWarnsForOldPortalOnWayland(t *testing.T) {
	withCatclipBundleDir(t)
	_, restore := withBundleStub(t)
	defer restore()

	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("CATCLIP_XDG_DESKTOP_PORTAL_VERSION", "xdg-desktop-portal 1.18.4")

	cfg := output.EmitConfig{
		OutputMode: command.OutputModeClipboard,
	}
	env := output.EmitEnvironment{
		Platform:   "linux",
		WorkingDir: t.TempDir(),
	}

	stats, err := output.WithPayloadWriter(cfg, env, io.Discard, platform.Palette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, strings.Repeat("a", 5000))
		return werr
	})
	if err != nil {
		t.Fatalf("output.WithPayloadWriter returned error: %v", err)
	}
	if len(stats.Warnings) != 1 {
		t.Fatalf("expected one portal warning, got %#v", stats.Warnings)
	}
	for _, want := range []string{"xdg-desktop-portal 1.18", "recommended 1.21 baseline", "Firefox Snap", "drag and drop"} {
		if !strings.Contains(stats.Warnings[0], want) {
			t.Fatalf("portal warning missing %q: %q", want, stats.Warnings[0])
		}
	}
	if strings.Contains(stats.Warnings[0], "--no-bundle") {
		t.Fatalf("portal warning should not repeat --no-bundle guidance: %q", stats.Warnings[0])
	}
}

func TestBundleWarnsWhenPortalVersionCannotBeDetectedOnWayland(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("CATCLIP_XDG_DESKTOP_PORTAL_BIN", filepath.Join(t.TempDir(), "missing-portal"))

	warnings := output.BundleWarnings(output.EmitEnvironment{Platform: "linux"})
	if len(warnings) != 1 {
		t.Fatalf("expected one portal warning, got %#v", warnings)
	}
	for _, want := range []string{"xdg-desktop-portal was not found", "could not be verified", "Firefox Snap", "drag and drop"} {
		if !strings.Contains(warnings[0], want) {
			t.Fatalf("portal warning missing %q: %q", want, warnings[0])
		}
	}
	if strings.Contains(warnings[0], "--no-bundle") {
		t.Fatalf("portal warning should not repeat --no-bundle guidance: %q", warnings[0])
	}
}

func TestBundleDoesNotWarnForNewPortalOnWayland(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("CATCLIP_XDG_DESKTOP_PORTAL_VERSION", "xdg-desktop-portal 1.21.0")

	warnings := output.BundleWarnings(output.EmitEnvironment{Platform: "linux"})
	if len(warnings) != 0 {
		t.Fatalf("expected no portal warnings, got %#v", warnings)
	}
}

func TestParseMajorMinorVersion(t *testing.T) {
	major, minor, ok := output.ParseMajorMinorVersion("xdg-desktop-portal 1.21.0")
	if !ok {
		t.Fatal("expected version to parse")
	}
	if major != 1 || minor != 21 {
		t.Fatalf("version = %d.%d, want 1.21", major, minor)
	}
}

func TestXDGDesktopPortalVersionUsesConfiguredBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	bin := filepath.Join(t.TempDir(), "xdg-desktop-portal")
	script := "#!/bin/sh\nprintf 'xdg-desktop-portal 1.18.4\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write portal stub: %v", err)
	}
	t.Setenv("CATCLIP_XDG_DESKTOP_PORTAL_BIN", bin)

	major, minor, ok := output.XdgDesktopPortalVersion()
	if !ok {
		t.Fatal("expected configured portal binary version to parse")
	}
	if major != 1 || minor != 18 {
		t.Fatalf("version = %d.%d, want 1.18", major, minor)
	}
}

func TestXDGDesktopPortalVersionCandidatePathsIncludeDistroLibexecLocations(t *testing.T) {
	candidates := output.XdgDesktopPortalVersionCandidatePaths()
	for _, want := range []string{
		"xdg-desktop-portal",
		"/usr/libexec/xdg-desktop-portal",
		"/usr/lib/xdg-desktop-portal",
		"/usr/lib/xdg-desktop-portal/xdg-desktop-portal",
	} {
		if !slices.Contains(candidates, want) {
			t.Fatalf("portal candidate paths missing %q: %#v", want, candidates)
		}
	}
}

func TestBundleFilePermissionsAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode semantics; Windows perms work differently.")
	}
	withCatclipBundleDir(t)
	_, restore := withBundleStub(t)
	defer restore()

	cfg := output.EmitConfig{
		OutputMode: command.OutputModeClipboard,
	}
	env := output.EmitEnvironment{
		WorkingDir: t.TempDir(),
	}
	big := strings.Repeat("z", 5000)
	stats, err := output.WithPayloadWriter(cfg, env, io.Discard, platform.Palette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, big)
		return werr
	})
	if err != nil {
		t.Fatalf("output.WithPayloadWriter returned error: %v", err)
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
	withCatclipBundleDir(t)
	captured, restore := withBundleStub(t)
	defer restore()

	wd := filepath.Join(t.TempDir(), "myapp")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatalf("setup wd: %v", err)
	}
	cfg := output.EmitConfig{
		OutputMode: command.OutputModeClipboard,
	}
	env := output.EmitEnvironment{
		WorkingDir: wd,
	}
	_, err := output.WithPayloadWriter(cfg, env, io.Discard, platform.Palette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, strings.Repeat("x", 5000))
		return werr
	})
	if err != nil {
		t.Fatalf("output.WithPayloadWriter returned error: %v", err)
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
	withCatclipBundleDir(t)
	_, restore := withBundleStub(t)
	defer restore()

	env := output.EmitEnvironment{
		WorkingDir: t.TempDir(),
	}
	dir := output.BundleDirForEnv(env)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(dir, "old-bundle-000000.txt")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	keep := filepath.Join(dir, "keep-me.txt")
	if err := os.WriteFile(keep, []byte("not a catclip bundle"), 0o600); err != nil {
		t.Fatalf("write keep file: %v", err)
	}

	cfg := output.EmitConfig{
		OutputMode: command.OutputModeClipboard,
	}
	_, err := output.WithPayloadWriter(cfg, env, io.Discard, platform.Palette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, strings.Repeat("y", 5000))
		return werr
	})
	if err != nil {
		t.Fatalf("output.WithPayloadWriter returned error: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale bundle %q removed, stat err = %v", stale, err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("expected non-bundle file %q preserved, stat err = %v", keep, err)
	}
}

func TestBundleNoBundleFlagIsCanonicalGlobalArg(t *testing.T) {
	flags := command.CanonicalGlobalArgs(command.Invocation{}, command.RenderFlags{NoBundle: true})
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
	withCatclipBundleDir(t)
	captured, restore := withBundleStub(t)
	defer restore()

	// Force a fake clipboard subprocess that just drains stdin.
	fakeClipboardOnPath(t)

	cfg := output.EmitConfig{
		OutputMode: command.OutputModeClipboard,
		NoBundle:   true,
	}
	env := output.EmitEnvironment{
		Platform:   platform.Detect(),
		WorkingDir: t.TempDir(),
	}
	stats, err := output.WithPayloadWriter(cfg, env, io.Discard, platform.Palette{}, func(w io.Writer) error {
		_, werr := io.WriteString(w, strings.Repeat("q", 8000))
		return werr
	})
	if err != nil {
		t.Fatalf("output.WithPayloadWriter returned error: %v", err)
	}
	if stats.SinkName == "bundle" {
		t.Fatalf("expected text-clipboard sink with --no-bundle, got bundle")
	}
	if *captured != "" {
		t.Fatalf("output.FileclipCopy should not have been called, was called with %q", *captured)
	}
}

// fakeClipboardOnPath installs a no-op script named after the platform's
// clipboard command into PATH so output.WithPayloadWriter's text path succeeds without
// touching the real clipboard. Skips the test on platforms where the trick
// can't run (no /bin/sh).
func fakeClipboardOnPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fakeClipboardOnPath uses /bin/sh; Windows path tested separately")
	}
	dir := t.TempDir()
	var name string
	switch platform.Detect() {
	case "macos":
		name = "pbcopy"
	case "linux":
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		t.Setenv("XDG_SESSION_TYPE", "wayland")
		name = "wl-copy"
	default:
		t.Skipf("no fake clipboard wired for %s", platform.Detect())
	}
	script := "#!/bin/sh\ncat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake clipboard: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
