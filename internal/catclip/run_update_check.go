package catclip

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/updatecheck"
)

var checkForUpdate = updatecheck.Check

type checkUpdateConfig struct {
	Version  string
	Platform string
}

func checkUpdateConfigFromParsedCommand(cfg command.Parsed) checkUpdateConfig {
	return checkUpdateConfig{
		Version:  cfg.Version,
		Platform: cfg.Platform,
	}
}

func runCheckUpdate(cfg checkUpdateConfig, w io.Writer) error {
	colors := platform.ActivePaletteForWriter(w)
	if _, err := fmt.Fprintf(w, "%sChecking for Catclip updates...%s\n\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	result, err := checkForUpdate(updatecheck.Options{
		CurrentVersion: cfg.Version,
		Platform:       cfg.Platform,
	})
	if err != nil {
		var versionErr updatecheck.CurrentVersionError
		if errors.As(err, &versionErr) {
			return newExitError(1, fmt.Sprintf(
				"Error: Cannot check for updates because this build reports version %q.",
				versionErr.Version,
			))
		}
		return newExitError(1,
			"Error: Could not check for Catclip updates.\n  Check your internet connection and try again.",
		)
	}
	if err := renderUpdateCheckResult(w, result, colors); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	return err
}

func renderUpdateCheckResult(w io.Writer, result updatecheck.Result, colors platform.Palette) error {
	// Release notes arrive in the existing bounded version-check response;
	// missing notes add neither another request nor an empty heading.
	if result.Status == updatecheck.StatusAvailable {
		if notes := printableReleaseNotes(result.ReleaseNotes); notes != "" {
			message := fmt.Sprintf("Catclip v%s is available. You have v%s.", result.LatestVersion, result.CurrentVersion)
			divider := strings.Repeat("-", len(message))
			if _, err := fmt.Fprintf(w, "%s%sRelease notes for v%s:%s\n%s\n\n%s%s%s\n\n", colors.Bold, colors.Prompt, result.LatestVersion, colors.Reset, formatReleaseNotes(notes, colors), colors.Dim, divider, colors.Reset); err != nil {
				return err
			}
		}
	}
	version := func(value string) string {
		return colors.Bold + colors.Prompt + "v" + value + colors.Reset
	}
	commandText := func(value string) string {
		return colors.Bold + colors.Prompt + value + colors.Reset
	}

	switch result.Status {
	case updatecheck.StatusCurrent:
		_, err := fmt.Fprintf(w, "Catclip %s %sis up to date.%s\n", version(result.CurrentVersion), colors.OK, colors.Reset)
		return err
	case updatecheck.StatusAhead:
		_, err := fmt.Fprintf(w,
			"You have Catclip %s.\nThe latest stable release is %s.\n\n%sThis build is newer than the latest stable release.%s\n",
			version(result.CurrentVersion), version(result.LatestVersion), colors.Dim, colors.Reset,
		)
		return err
	case updatecheck.StatusAvailable:
		if _, err := fmt.Fprintf(w, "Catclip %s %sis available.%s You have %s.\n\n",
			version(result.LatestVersion), colors.OK, colors.Reset, version(result.CurrentVersion)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown update-check status %q", result.Status)
	}

	switch result.InstallMethod {
	case updatecheck.InstallHomebrew, updatecheck.InstallDirectRelease:
		_, err := fmt.Fprintf(w, "%sUpdate with:%s\n  %s\n", colors.Bold, colors.Reset, commandText(result.Instruction))
		return err
	case updatecheck.InstallSource:
		if _, err := io.WriteString(w, "This copy was built from source. From your Catclip source checkout, run:\n"); err != nil {
			return err
		}
		for _, line := range strings.Split(result.Instruction, "\n") {
			if _, err := fmt.Fprintf(w, "  %s\n", commandText(line)); err != nil {
				return err
			}
		}
		return nil
	case updatecheck.InstallUnknown:
		_, err := fmt.Fprintf(w,
			"Catclip could not determine how this copy was installed.\nUpdate using the same installation method:\n  %s\n",
			result.Instruction,
		)
		return err
	default:
		return fmt.Errorf("unknown update-check installation method %q", result.InstallMethod)
	}
}

func printableReleaseNotes(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, body))
}
