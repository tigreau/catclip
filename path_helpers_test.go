package catclip

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDisplayPathHomePrefixCollapsesOnPOSIX guards the legacy ~ shortening on
// POSIX shells, which expand `~` before passing args to external tools.
func TestDisplayPathHomePrefixCollapsesOnPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("displayPath returns the full path on Windows (see below)")
	}
	t.Setenv("HOME", "/home/test")
	got := displayPath(filepath.Join("/home/test", "Documents", "catclip", "bundle.txt"))
	want := "~" + string(filepath.Separator) + filepath.Join("Documents", "catclip", "bundle.txt")
	if got != want {
		t.Fatalf("POSIX home prefix collapse: want %q, got %q", want, got)
	}
}

// TestDisplayPathOutsideHomePassesThrough confirms the helper only collapses
// paths that are actually under $HOME.
func TestDisplayPathOutsideHomePassesThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows always returns the full path; the outside-home distinction is POSIX-only")
	}
	t.Setenv("HOME", "/home/test")
	outside := "/etc/hosts"
	if got := displayPath(outside); got != outside {
		t.Fatalf("outside-home POSIX path should pass through; got %q want %q", got, outside)
	}
}

// TestDisplayPathUsesDollarHomeOnWindows pins the v0.6.2 bug fix:
// PowerShell does not expand `~` when calling external programs
// (notepad, code, nano, explorer, start, ...) so a printed
// `~\Documents\foo.txt` failed to paste-run on Windows. The fix prints
// `$HOME\Documents\foo.txt` instead, which PowerShell DOES expand
// because `$HOME` is a PowerShell automatic variable.
func TestDisplayPathUsesDollarHomeOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only behavior")
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	p := filepath.Join(home, "Documents", "catclip", "bundle.txt")
	want := "$HOME" + string(filepath.Separator) + filepath.Join("Documents", "catclip", "bundle.txt")
	if got := displayPath(p); got != want {
		t.Fatalf("Windows displayPath should use $HOME shorthand; got %q want %q", got, want)
	}
	// Defense in depth: a literal `~` slipping through would re-trigger
	// the original bug (PowerShell doesn't expand it for external tools).
	if strings.HasPrefix(displayPath(p), "~") {
		t.Fatalf("Windows displayPath must not start with ~; got %q", displayPath(p))
	}
}
