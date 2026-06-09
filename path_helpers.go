package catclip

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/platform"
)

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

// path_helpers.go holds path/version utilities that have callers across
// help, emit, preview, ignore-rules, and the runtime tool-check.
// Extracted out of help.go before the help.go move into internal/cli
// because preview.go and ignore_rules.go also call displayPath, and
// required_tools.go calls loadVersion — none of those root files
// should import cli to reach these.

// displayPath collapses a home-relative absolute path into ~/...
// notation so user-facing help / preview output stays short. Falls back
// to the raw path if the home directory is undeterminable.
func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(p, home+string(filepath.Separator))
	}
	return p
}

// loadVersion resolves the bundled VERSION file: cwd, the dir holding
// this source file (dev runs), the executable's directory tree, and
// finally a "dev" fallback. Used both by --version output and the
// missing-tool discovery.Diagnostic in required_tools.go.
func loadVersion() string {
	const fallback = "dev"

	candidates := []string{"VERSION"}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "VERSION"))
	}
	for _, dir := range platform.ExecutableCandidateDirs() {
		candidates = append(candidates,
			filepath.Join(dir, "VERSION"),
			filepath.Join(dir, "..", "share", "catclip", "VERSION"),
		)
	}

	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		version := strings.TrimSpace(string(data))
		if version != "" {
			return version
		}
	}

	return fallback
}

// formatDuration is a root dup of internal/ui's formatDuration (was in
// root date_format.go before the v0.6.0 ui extraction). Kept here so
// cli.go's verbose timing logging stays render-free.
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}
