package catclip

import (
	_ "embed"
	"path"
	"strings"
	"time"
)

// sourceVersion is the repository release metadata used by ordinary source
// builds. Published release builds set releaseVersion with -ldflags -X so the
// tag remains the final authority for shipped binaries.
//
//go:embed VERSION
var sourceVersion string

var releaseVersion string

// normalizeRelPath is a root dup of discovery.normalizeRelPath (private).
// Kept here so the wide constellation of root files (pickers, output
// plan, tree bridge, startup-args mutation) that only need this string
// normalization don't need to import internal/discovery for one helper.
// Two-line function; sync if either side changes (none expected — it's
// path normalization, not policy).
func normalizeRelPath(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "./")
	if value == "." || value == "/" {
		return "."
	}
	return value
}

// loadVersion resolves Catclip's version entirely from data compiled into the
// binary. It performs no runtime filesystem lookup, so project and installed
// VERSION files cannot redefine the running executable.
func loadVersion() string {
	return resolvedVersion(releaseVersion, sourceVersion)
}

func resolvedVersion(release, source string) string {
	if version := strings.TrimSpace(release); version != "" {
		return version
	}
	if version := strings.TrimSpace(source); version != "" {
		return version
	}
	return "dev"
}

// formatDuration keeps verbose runtime timings compact without making root
// depend on a presentation package.
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}
