package platform

import "testing"

func TestDetectLinuxSessionForEnv(t *testing.T) {
	t.Parallel()

	const (
		linuxKernel = "Linux version 6.5.0-generic ...\n"
		wslKernel   = "Linux version 5.15.0-microsoft-standard-WSL2 ...\n"
	)

	envFrom := func(pairs map[string]string) func(string) string {
		return func(key string) string {
			return pairs[key]
		}
	}

	cases := []struct {
		name       string
		goos       string
		procVer    string
		env        map[string]string
		want       LinuxSessionKind
	}{
		{
			name:    "non-linux returns unknown",
			goos:    "darwin",
			procVer: "",
			env:     map[string]string{},
			want:    LinuxSessionUnknown,
		},
		{
			name:    "WSL detected via /proc/version signature",
			goos:    "linux",
			procVer: wslKernel,
			env:     map[string]string{"DISPLAY": ":0"}, // DISPLAY should not override WSL
			want:    LinuxSessionWSL,
		},
		{
			name:    "WSL detected via WSL_DISTRO_NAME",
			goos:    "linux",
			procVer: linuxKernel,
			env:     map[string]string{"WSL_DISTRO_NAME": "Ubuntu-24.04"},
			want:    LinuxSessionWSL,
		},
		{
			name:    "Wayland via WAYLAND_DISPLAY",
			goos:    "linux",
			procVer: linuxKernel,
			env:     map[string]string{"WAYLAND_DISPLAY": "wayland-0"},
			want:    LinuxSessionWayland,
		},
		{
			name:    "Wayland via XDG_SESSION_TYPE",
			goos:    "linux",
			procVer: linuxKernel,
			env:     map[string]string{"XDG_SESSION_TYPE": "wayland"},
			want:    LinuxSessionWayland,
		},
		{
			name:    "Wayland via XDG_SESSION_TYPE case-insensitive",
			goos:    "linux",
			procVer: linuxKernel,
			env:     map[string]string{"XDG_SESSION_TYPE": "Wayland"},
			want:    LinuxSessionWayland,
		},
		{
			name:    "XWayland: both display vars set -> Wayland wins",
			goos:    "linux",
			procVer: linuxKernel,
			env: map[string]string{
				"WAYLAND_DISPLAY": "wayland-0",
				"DISPLAY":         ":0",
			},
			want: LinuxSessionWayland,
		},
		{
			name:    "stale XDG_SESSION_TYPE=x11 with WAYLAND_DISPLAY -> Wayland wins",
			goos:    "linux",
			procVer: linuxKernel,
			env: map[string]string{
				"XDG_SESSION_TYPE": "x11",
				"WAYLAND_DISPLAY":  "wayland-0",
			},
			want: LinuxSessionWayland,
		},
		{
			name:    "X11 via XDG_SESSION_TYPE",
			goos:    "linux",
			procVer: linuxKernel,
			env:     map[string]string{"XDG_SESSION_TYPE": "x11"},
			want:    LinuxSessionX11,
		},
		{
			name:    "X11 via DISPLAY with no Wayland signal",
			goos:    "linux",
			procVer: linuxKernel,
			env:     map[string]string{"DISPLAY": ":0"},
			want:    LinuxSessionX11,
		},
		{
			name:    "displayless: no env vars set",
			goos:    "linux",
			procVer: linuxKernel,
			env:     map[string]string{},
			want:    LinuxSessionUnknown,
		},
		{
			name:    "displayless: XDG_SESSION_TYPE=tty without DISPLAY",
			goos:    "linux",
			procVer: linuxKernel,
			env:     map[string]string{"XDG_SESSION_TYPE": "tty"},
			want:    LinuxSessionUnknown,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DetectLinuxSessionForEnv(tc.goos, envFrom(tc.env), tc.procVer)
			if got != tc.want {
				t.Errorf("DetectLinuxSessionForEnv(%q, %v) = %q, want %q", tc.goos, tc.env, got, tc.want)
			}
		})
	}
}
