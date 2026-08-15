package catclip

import (
	"errors"
	"fmt"
	"io"
	"strings"

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
	if _, err := io.WriteString(w, "Checking for Catclip updates...\n\n"); err != nil {
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
	return renderUpdateCheckResult(w, result, platform.ActivePaletteForWriter(w))
}

func renderUpdateCheckResult(w io.Writer, result updatecheck.Result, colors platform.Palette) error {
	version := func(value string) string {
		return colors.Bold + "v" + value + colors.Reset
	}
	commandText := func(value string) string {
		return colors.Prompt + value + colors.Reset
	}

	switch result.Status {
	case updatecheck.StatusCurrent:
		_, err := fmt.Fprintf(w, "Catclip %s is up to date.\n", version(result.CurrentVersion))
		return err
	case updatecheck.StatusAhead:
		_, err := fmt.Fprintf(w,
			"You have Catclip %s.\nThe latest stable release is %s.\n\nThis build is newer than the latest stable release.\n",
			version(result.CurrentVersion), version(result.LatestVersion),
		)
		return err
	case updatecheck.StatusAvailable:
		if _, err := fmt.Fprintf(w, "Catclip %s is available. You have %s.\n\n",
			version(result.LatestVersion), version(result.CurrentVersion)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown update-check status %q", result.Status)
	}

	switch result.InstallMethod {
	case updatecheck.InstallHomebrew, updatecheck.InstallDirectRelease:
		_, err := fmt.Fprintf(w, "Update with:\n  %s\n", commandText(result.Instruction))
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
