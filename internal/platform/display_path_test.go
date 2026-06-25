package platform

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestDisplayPathFor(t *testing.T) {
	cases := []struct {
		name string
		goos string
		p    string
		home string
		want string
	}{
		{"posix path equals home", "linux", "/Users/chris", "/Users/chris", "~"},
		{"posix path under home", "linux", "/Users/chris/.config/catclip/.hiss", "/Users/chris", "~/.config/catclip/.hiss"},
		{"posix path not under home", "linux", "/tmp/foo", "/Users/chris", "/tmp/foo"},
		{"posix prefix collision is not a match", "linux", "/Users/christopher/foo", "/Users/chris", "/Users/christopher/foo"},
		{"windows path equals home", "windows", `C:\Users\chris`, `C:\Users\chris`, "$HOME"},
		{"windows path under home", "windows", `C:\Users\chris\.config\catclip\.hiss`, `C:\Users\chris`, `$HOME\.config\catclip\.hiss`},
		{"windows path not under home", "windows", `D:\misc\foo`, `C:\Users\chris`, `D:\misc\foo`},
		{"empty path returns empty", "linux", "", "/Users/chris", ""},
		{"empty home returns path unchanged", "linux", "/Users/chris/foo", "", "/Users/chris/foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := displayPathFor(tc.goos, tc.p, tc.home)
			if got != tc.want {
				t.Fatalf("displayPathFor(%q, %q, %q) = %q; want %q", tc.goos, tc.p, tc.home, got, tc.want)
			}
		})
	}
}

func TestDisplayPathWiresRuntimeAndHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("UserHomeDir unavailable")
	}
	got := DisplayPath(home)
	wantPrefix := "~"
	if runtime.GOOS == "windows" {
		wantPrefix = "$HOME"
	}
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("DisplayPath(home) = %q; expected to start with %q", got, wantPrefix)
	}
}
