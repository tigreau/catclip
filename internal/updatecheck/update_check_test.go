package updatecheck

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckStatuses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current string
		latest  string
		want    Status
	}{
		{name: "current", current: "0.7.2", latest: "v0.7.2", want: StatusCurrent},
		{name: "available", current: "0.7.1", latest: "v0.7.2", want: StatusAvailable},
		{name: "ahead", current: "0.7.3", latest: "v0.7.2", want: StatusAhead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Check(Options{
				CurrentVersion: tc.current,
				HTTPClient:     releaseClient(t, tc.latest),
				ExecutablePath: filepath.Join(t.TempDir(), "bin", "catclip"),
				HomebrewOwner:  func(context.Context, string) bool { return false },
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tc.want || result.CurrentVersion != tc.current || result.LatestVersion != "0.7.2" {
				t.Fatalf("Check() = %#v, want status %q", result, tc.want)
			}
		})
	}
}

func TestCheckRejectsUnusableCurrentVersion(t *testing.T) {
	for _, version := range []string{"dev", "0.7", "0.7.2-beta.1"} {
		_, err := Check(Options{CurrentVersion: version})
		var versionErr CurrentVersionError
		if !errors.As(err, &versionErr) || versionErr.Version != version {
			t.Fatalf("Check(%q) error = %v", version, err)
		}
	}
}

func TestCheckReturnsExplicitNetworkFailures(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("rate limited")),
		}, nil
	})}
	if _, err := Check(Options{CurrentVersion: "0.7.1", HTTPClient: client}); err == nil {
		t.Fatal("HTTP failure returned nil error")
	}
}

func TestCheckAcceptsLargeReleaseNotes(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"tag_name":"v0.7.2","body":"` + strings.Repeat("x", 96*1024) + `"}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	result, err := Check(Options{CurrentVersion: "0.7.2", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCurrent {
		t.Fatalf("Check() = %#v", result)
	}
}

func TestCheckRejectsOversizedReleaseResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1))),
		}, nil
	})}
	if _, err := Check(Options{CurrentVersion: "0.7.2", HTTPClient: client}); err == nil {
		t.Fatal("oversized response returned nil error")
	}
}

func TestCheckBoundsRequest(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	started := time.Now()
	_, err := Check(Options{CurrentVersion: "0.7.1", HTTPClient: client, Timeout: 25 * time.Millisecond})
	if err == nil {
		t.Fatal("timed-out request returned nil error")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded request took %s", elapsed)
	}
}

func TestAvailableReleaseUsesInstallationOwner(t *testing.T) {
	sourceExe := writeInstallReceipt(t, string(InstallSource))
	directExe := writeInstallReceipt(t, string(InstallDirectRelease))
	notBrew := func(context.Context, string) bool { return false }

	for _, tc := range []struct {
		name         string
		opts         Options
		wantMethod   InstallMethod
		wantCommands []string
	}{
		{name: "homebrew", opts: Options{ExecutablePath: directExe, HomebrewOwner: func(context.Context, string) bool { return true }}, wantMethod: InstallHomebrew, wantCommands: []string{"brew upgrade catclip"}},
		{name: "source unix", opts: Options{ExecutablePath: sourceExe, HomebrewOwner: notBrew}, wantMethod: InstallSource, wantCommands: []string{"git pull --ff-only", "PREFIX=", "./install.sh"}},
		{name: "source windows", opts: Options{ExecutablePath: sourceExe, Platform: "windows", HomebrewOwner: notBrew}, wantMethod: InstallSource, wantCommands: []string{"git pull --ff-only", ".\\install.ps1", "-InstallRoot"}},
		{name: "direct unix", opts: Options{ExecutablePath: directExe, Platform: "linux", HomebrewOwner: notBrew}, wantMethod: InstallDirectRelease, wantCommands: []string{"curl "}},
		{name: "direct windows", opts: Options{ExecutablePath: directExe, Platform: "windows", HomebrewOwner: notBrew}, wantMethod: InstallDirectRelease, wantCommands: []string{"irm "}},
		{name: "unknown", opts: Options{ExecutablePath: filepath.Join(t.TempDir(), "bin", "catclip"), HomebrewOwner: notBrew}, wantMethod: InstallUnknown, wantCommands: []string{InstallDocsURL}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.CurrentVersion = "0.7.1"
			tc.opts.HTTPClient = releaseClient(t, "v0.7.2")
			result, err := Check(tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if result.InstallMethod != tc.wantMethod {
				t.Fatalf("Check() = %#v", result)
			}
			for _, want := range tc.wantCommands {
				if !strings.Contains(result.Instruction, want) {
					t.Fatalf("Check() instruction = %q, want %q", result.Instruction, want)
				}
			}
		})
	}
}

func TestDirectInstallCommandPreservesCustomRoot(t *testing.T) {
	unix := directInstallCommand("linux", "/tmp/catclip path/bin/catclip")
	if !strings.Contains(unix, "PREFIX='/tmp/catclip path'") {
		t.Fatalf("Unix command did not preserve root: %q", unix)
	}
	windows := directInstallCommand("windows", `C:\Tools\Catclip\bin\catclip.exe`)
	if !strings.Contains(windows, "CATCLIP_INSTALL_ROOT") || !strings.Contains(windows, `C:\Tools\Catclip`) {
		t.Fatalf("Windows command did not preserve root: %q", windows)
	}
	unc := directInstallCommand("windows", `\\server\tools\catclip\bin\catclip.exe`)
	if !strings.Contains(unc, `\\server\tools\catclip`) {
		t.Fatalf("Windows UNC command did not preserve root: %q", unc)
	}
}

func TestSourceInstallCommandPreservesCustomRoot(t *testing.T) {
	unix := sourceInstallCommand("linux", "/tmp/catclip path/bin/catclip")
	if unix != "PREFIX='/tmp/catclip path' ./install.sh" {
		t.Fatalf("Unix source command = %q", unix)
	}
	windows := sourceInstallCommand("windows", `C:\Tools\Catclip\bin\catclip.exe`)
	if windows != `.\install.ps1 -InstallRoot 'C:\Tools\Catclip'` {
		t.Fatalf("Windows source command = %q", windows)
	}
	unc := sourceInstallCommand("windows", `\\server\tools\catclip\bin\catclip.exe`)
	if !strings.Contains(unc, `-InstallRoot '\\server\tools\catclip'`) {
		t.Fatalf("Windows UNC source command = %q", unc)
	}
}

func TestDirectWindowsInstallCommandOmitsDefaultRootOverride(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\Chris\AppData\Local`)
	command := directInstallCommand("windows", `C:\Users\Chris\AppData\Local\Programs\catclip\bin\catclip.exe`)
	if strings.Contains(command, "CATCLIP_INSTALL_ROOT") {
		t.Fatalf("default Windows root received an override: %q", command)
	}
	if !strings.HasPrefix(command, "irm ") {
		t.Fatalf("Windows update command = %q", command)
	}
}

func TestSourceWindowsInstallCommandOmitsDefaultRootOverride(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\Chris\AppData\Local`)
	command := sourceInstallCommand("windows", `C:\Users\Chris\AppData\Local\Programs\catclip\bin\catclip.exe`)
	if command != `.\install.ps1` {
		t.Fatalf("Windows source update command = %q", command)
	}
}

func TestSourceUnixInstallCommandOmitsDefaultRootOverride(t *testing.T) {
	if command := sourceInstallCommand("linux", "/usr/local/bin/catclip"); command != "./install.sh" {
		t.Fatalf("Unix source update command = %q", command)
	}
}

func TestVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		left  string
		right string
		want  int
	}{
		{left: "0.7.10", right: "0.7.2", want: 1},
		{left: "v1.0.0", right: "1.0.0", want: 0},
		{left: "1.0.0", right: "1.0.0-beta.1", want: 1},
		{left: "1.0.0-beta.1", right: "1.0.0", want: -1},
		{left: "1.2.0", right: "1.10.0", want: -1},
	} {
		left, leftOK := parseVersion(tc.left)
		right, rightOK := parseVersion(tc.right)
		if !leftOK || !rightOK {
			t.Fatalf("parseVersion(%q, %q) failed", tc.left, tc.right)
		}
		got := compareVersions(left, right)
		if got < 0 {
			got = -1
		} else if got > 0 {
			got = 1
		}
		if got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}

func releaseClient(t *testing.T, version string) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "catclip/") {
			t.Errorf("User-Agent = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"tag_name":"` + version + `"}`)),
		}, nil
	})}
}

func writeInstallReceipt(t *testing.T, method string) string {
	t.Helper()
	root := t.TempDir()
	executable := filepath.Join(root, "bin", "catclip")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(root, "share", "catclip", receiptFileName)
	if err := os.MkdirAll(filepath.Dir(receipt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, []byte(method+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return executable
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
