package ui

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tigreau/catclip/internal/platform"
)

// RenderVersionOutput writes the three-line `catclip --version` block:
//
//	catclip 0.6.4
//	  fzf 0.44.1  ~/Desktop/catclip/bin/fzf
//	  rg  14.1.0  ~/Desktop/catclip/bin/rg
//
// See docs/versions/v0.6.4/reports/RESOLVED_PLAN_version_shows_bundled_tools.md
// for the full edge-case gallery this function has to handle.
func RenderVersionOutput(catclipVersion string, w io.Writer) error {
	if _, err := fmt.Fprintf(w, "catclip %s\n", catclipVersion); err != nil {
		return err
	}
	if _, err := io.WriteString(w, formatBundledToolLine("fzf", "CATCLIP_FZF")); err != nil {
		return err
	}
	if _, err := io.WriteString(w, formatBundledToolLine("rg ", "CATCLIP_RG")); err != nil {
		return err
	}
	return nil
}

// formatBundledToolLine renders one indented tool line — resolving
// the binary, probing its --version, and applying DisplayPath so the
// output is paste-runnable on both POSIX and PowerShell (Windows).
// toolLabel is the visible column (e.g. "fzf", "rg " for alignment).
func formatBundledToolLine(toolLabel, envVar string) string {
	toolName := strings.TrimSpace(toolLabel)
	path, found := platform.BundledToolBinary(envVar, toolName)

	// Missing-binary branch — one-liner, no path column.
	if !found {
		reason := fmt.Sprintf("reinstall catclip with bundled tools, or set %s", envVar)
		if platform.EnvOverrideSet(envVar) {
			reason = fmt.Sprintf("%s points at missing file", envVar)
		}
		return fmt.Sprintf("  %s <not found — %s>\n", toolLabel, reason)
	}

	// Resolved — probe version, format DisplayPath, mark env overrides.
	versionCell := probedVersionCell(path)
	displayPath := platform.DisplayPath(path)
	envMarker := ""
	if platform.EnvOverrideSet(envVar) {
		envMarker = fmt.Sprintf("  (%s)", envVar)
	}
	return fmt.Sprintf("  %s %s  %s%s\n", toolLabel, versionCell, displayPath, envMarker)
}

// probedVersionCell returns the version string or a descriptive
// failure marker suitable for direct rendering in the version cell.
// Timeout gets its own marker so users can tell it apart from a
// generic exec failure.
func probedVersionCell(path string) string {
	version, err := platform.ProbeToolVersion(path)
	if err != nil {
		if errors.Is(err, platform.ErrProbeTimeout) {
			return "<version probe timed out>"
		}
		return "<version probe failed>"
	}
	return version
}

