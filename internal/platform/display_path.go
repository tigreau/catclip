package platform

import (
	"os"
	"runtime"
	"strings"
)

// DisplayPath collapses an absolute path's home prefix to a shell-paste-runnable
// shorthand for the host's default shell.
func DisplayPath(p string) string {
	home, _ := os.UserHomeDir()
	return displayPathFor(runtime.GOOS, p, home)
}

// displayPathFor is the pure formatter behind DisplayPath, parameterized by
// goos so any (goos, p, home) combination is exercisable on any runner.
//
// Windows uses "$HOME" instead of "~" because PowerShell does NOT expand "~"
// for external programs (notepad, code, explorer, …): a printed
// "~\Documents\foo.txt" fails to paste-run. PowerShell DOES expand "$HOME"
// because it's an automatic variable.
//
// The separator is hardcoded per branch, not filepath.Separator: that is a
// compile-time per-platform constant, so using it would make the Windows
// branch produce "/" when the test runner is POSIX — defeating the whole
// point of factoring out goos.
func displayPathFor(goos, p, home string) string {
	if p == "" {
		return ""
	}
	if home == "" {
		return p
	}
	prefix := "~"
	sep := "/"
	if goos == "windows" {
		prefix = "$HOME"
		sep = `\`
	}
	if p == home {
		return prefix
	}
	if strings.HasPrefix(p, home+sep) {
		return prefix + sep + strings.TrimPrefix(p, home+sep)
	}
	return p
}
