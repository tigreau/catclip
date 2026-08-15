package catclip

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/updatecheck"
)

func TestRunDispatchesCheckUpdateWithParsedIdentity(t *testing.T) {
	restoreCheckUpdateHook(t)
	var received updatecheck.Options
	checkForUpdate = func(opts updatecheck.Options) (updatecheck.Result, error) {
		received = opts
		return updatecheck.Result{
			Status:         updatecheck.StatusCurrent,
			CurrentVersion: opts.CurrentVersion,
			LatestVersion:  opts.CurrentVersion,
		}, nil
	}

	var out bytes.Buffer
	err := run(command.Parsed{
		Action:   command.ActionCheckUpdate,
		Version:  "0.7.2",
		Platform: "windows",
	}, &out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if received.CurrentVersion != "0.7.2" || received.Platform != "windows" {
		t.Fatalf("update options = %#v", received)
	}
}

func TestRunCheckUpdateRendersEveryResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result updatecheck.Result
		want   string
	}{
		{
			name:   "current",
			result: updatecheck.Result{Status: updatecheck.StatusCurrent, CurrentVersion: "0.7.2", LatestVersion: "0.7.2"},
			want:   "Checking for Catclip updates...\n\nCatclip v0.7.2 is up to date.\n",
		},
		{
			name:   "ahead",
			result: updatecheck.Result{Status: updatecheck.StatusAhead, CurrentVersion: "0.7.3", LatestVersion: "0.7.2"},
			want:   "Checking for Catclip updates...\n\nYou have Catclip v0.7.3.\nThe latest stable release is v0.7.2.\n\nThis build is newer than the latest stable release.\n",
		},
		{
			name:   "homebrew",
			result: updatecheck.Result{Status: updatecheck.StatusAvailable, CurrentVersion: "0.7.1", LatestVersion: "0.7.2", InstallMethod: updatecheck.InstallHomebrew, Instruction: "brew upgrade catclip"},
			want:   "Checking for Catclip updates...\n\nCatclip v0.7.2 is available. You have v0.7.1.\n\nUpdate with:\n  brew upgrade catclip\n",
		},
		{
			name:   "direct",
			result: updatecheck.Result{Status: updatecheck.StatusAvailable, CurrentVersion: "0.7.1", LatestVersion: "0.7.2", InstallMethod: updatecheck.InstallDirectRelease, Instruction: "irm https://example.test/install.ps1 | iex"},
			want:   "Checking for Catclip updates...\n\nCatclip v0.7.2 is available. You have v0.7.1.\n\nUpdate with:\n  irm https://example.test/install.ps1 | iex\n",
		},
		{
			name:   "source",
			result: updatecheck.Result{Status: updatecheck.StatusAvailable, CurrentVersion: "0.7.1", LatestVersion: "0.7.2", InstallMethod: updatecheck.InstallSource, Instruction: "git pull --ff-only\n./install.sh"},
			want:   "Checking for Catclip updates...\n\nCatclip v0.7.2 is available. You have v0.7.1.\n\nThis copy was built from source. From your Catclip source checkout, run:\n  git pull --ff-only\n  ./install.sh\n",
		},
		{
			name:   "unknown",
			result: updatecheck.Result{Status: updatecheck.StatusAvailable, CurrentVersion: "0.7.1", LatestVersion: "0.7.2", InstallMethod: updatecheck.InstallUnknown, Instruction: updatecheck.InstallDocsURL},
			want:   "Checking for Catclip updates...\n\nCatclip v0.7.2 is available. You have v0.7.1.\n\nCatclip could not determine how this copy was installed.\nUpdate using the same installation method:\n  " + updatecheck.InstallDocsURL + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreCheckUpdateHook(t)
			checkForUpdate = func(updatecheck.Options) (updatecheck.Result, error) { return tc.result, nil }
			var out bytes.Buffer
			if err := runCheckUpdate(checkUpdateConfig{Version: "0.7.1"}, &out); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != tc.want {
				t.Fatalf("runCheckUpdate() output = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunCheckUpdateRendersErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{name: "network", err: errors.New("offline"), want: "Error: Could not check for Catclip updates.\n  Check your internet connection and try again."},
		{name: "development version", err: updatecheck.CurrentVersionError{Version: "dev"}, want: "Error: Cannot check for updates because this build reports version \"dev\"."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreCheckUpdateHook(t)
			checkForUpdate = func(updatecheck.Options) (updatecheck.Result, error) { return updatecheck.Result{}, tc.err }
			var out bytes.Buffer
			err := runCheckUpdate(checkUpdateConfig{Version: "0.7.1"}, &out)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("runCheckUpdate() error = %v, want %q", err, tc.want)
			}
			if out.String() != "Checking for Catclip updates...\n\n" {
				t.Fatalf("progress output = %q", out.String())
			}
		})
	}
}

func restoreCheckUpdateHook(t *testing.T) {
	t.Helper()
	original := checkForUpdate
	t.Cleanup(func() { checkForUpdate = original })
}
