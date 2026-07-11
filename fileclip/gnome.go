package fileclip

// GNOME desktop/version detection. Kept untagged (not linux-only) so
// cross-platform callers such as catclip's BundleWarnings can reference
// LegacyGNOMEWayland; the probes are inert off GNOME desktops.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func isGNOMEDesktop() bool {
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	for _, part := range strings.FieldsFunc(desktop, func(r rune) bool {
		return r == ':' || r == ';' || r == ',' || r == ' '
	}) {
		if part == "gnome" {
			return true
		}
	}
	return false
}

func gnomeMajorVersion() (int, bool) {
	raw := strings.TrimSpace(os.Getenv("CATCLIP_GNOME_VERSION"))
	if raw == "" {
		out, err := exec.Command("gnome-shell", "--version").Output()
		if err != nil {
			return 0, false
		}
		raw = string(out)
	}

	for _, field := range strings.Fields(raw) {
		field = strings.TrimLeft(field, "vV")
		majorText := field
		if idx := strings.IndexAny(majorText, ".-+"); idx >= 0 {
			majorText = majorText[:idx]
		}
		if majorText == "" {
			continue
		}
		var major int
		if _, err := fmt.Sscanf(majorText, "%d", &major); err == nil {
			return major, true
		}
	}
	return 0, false
}

// LegacyGNOMEWayland reports whether the current desktop is a GNOME session
// older than [MinimumGNOMEFileClipboardMajor]. Copy no longer refuses these
// sessions (the gate was demoted in v0.6.6); callers use this to warn that
// the attempted text/uri-list offer may not be accepted by the file manager
// or browser.
func LegacyGNOMEWayland() (major int, legacy bool) {
	if !isGNOMEDesktop() {
		return 0, false
	}
	major, ok := gnomeMajorVersion()
	if !ok {
		return 0, false
	}
	return major, major < MinimumGNOMEFileClipboardMajor
}
