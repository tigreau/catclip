package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLatestURL = "https://api.github.com/repos/tigreau/catclip/releases/latest"
	InstallDocsURL   = "https://github.com/tigreau/catclip#installation"
	receiptFileName  = "INSTALL_METHOD"
	checkTimeout     = 2 * time.Second
	maxResponseBytes = 1 << 20
)

type Status string

const (
	StatusCurrent   Status = "current"
	StatusAvailable Status = "available"
	StatusAhead     Status = "ahead"
)

type InstallMethod string

const (
	InstallHomebrew      InstallMethod = "homebrew"
	InstallDirectRelease InstallMethod = "direct-release"
	InstallSource        InstallMethod = "source"
	InstallUnknown       InstallMethod = "unknown"
)

type Result struct {
	Status         Status
	CurrentVersion string
	LatestVersion  string
	InstallMethod  InstallMethod
	Instruction    string
}

type Options struct {
	CurrentVersion string
	Platform       string
	LatestURL      string
	ExecutablePath string
	HTTPClient     *http.Client
	Timeout        time.Duration
	HomebrewOwner  func(context.Context, string) bool
}

type CurrentVersionError struct {
	Version string
}

func (e CurrentVersionError) Error() string {
	return fmt.Sprintf("cannot compare current version %q", e.Version)
}

type releaseResponse struct {
	TagName string `json:"tag_name"`
}

// Check compares the embedded Catclip version with GitHub's latest stable
// release. It performs no caching, installation, or background work.
func Check(opts Options) (Result, error) {
	current, ok := parseVersion(opts.CurrentVersion)
	if !ok || current.prerelease != "" {
		return Result{}, CurrentVersionError{Version: strings.TrimSpace(opts.CurrentVersion)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeoutFor(opts))
	defer cancel()

	latestRaw, err := fetchLatest(ctx, opts)
	if err != nil {
		return Result{}, err
	}
	latest, ok := parseVersion(latestRaw)
	if !ok || latest.prerelease != "" {
		return Result{}, fmt.Errorf("latest release returned invalid version %q", latestRaw)
	}

	result := Result{
		CurrentVersion: normalizedVersion(current),
		LatestVersion:  normalizedVersion(latest),
	}
	switch compareVersions(current, latest) {
	case 0:
		result.Status = StatusCurrent
	case 1:
		result.Status = StatusAhead
	default:
		result.Status = StatusAvailable
		result.InstallMethod, result.Instruction = updateInstruction(ctx, opts)
	}
	return result, nil
}

func fetchLatest(ctx context.Context, opts Options) (string, error) {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	url := opts.LatestURL
	if url == "" {
		url = defaultLatestURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "catclip/"+strings.TrimSpace(opts.CurrentVersion))
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxResponseBytes {
		return "", fmt.Errorf("GitHub response exceeds %d bytes", maxResponseBytes)
	}
	var release releaseResponse
	if err := json.Unmarshal(body, &release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

func updateInstruction(ctx context.Context, opts Options) (InstallMethod, string) {
	executable := opts.ExecutablePath
	if executable == "" {
		executable, _ = os.Executable()
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	owner := opts.HomebrewOwner
	if owner == nil {
		owner = homebrewOwnsExecutable
	}
	if executable != "" && owner(ctx, executable) {
		return InstallHomebrew, "brew upgrade catclip"
	}

	switch readInstallMethod(executable) {
	case string(InstallSource):
		return InstallSource, "git pull --ff-only\n" + sourceInstallCommand(opts.Platform, executable)
	case string(InstallDirectRelease):
		return InstallDirectRelease, directInstallCommand(opts.Platform, executable)
	default:
		return InstallUnknown, InstallDocsURL
	}
}

func sourceInstallCommand(platform, executable string) string {
	root := installRoot(platform, executable)
	if platform == "windows" {
		command := ".\\install.ps1"
		if root != "" && !sameWindowsPath(root, defaultWindowsInstallRoot()) {
			return command + " -InstallRoot " + powershellQuote(root)
		}
		return command
	}
	command := "./install.sh"
	if root != "" && filepath.Clean(root) != filepath.Clean("/usr/local") {
		return "PREFIX=" + shellQuote(root) + " " + command
	}
	return command
}

func directInstallCommand(platform, executable string) string {
	root := installRoot(platform, executable)
	if platform == "windows" {
		command := "irm https://raw.githubusercontent.com/tigreau/catclip/main/install.ps1 | iex"
		if root != "" && !sameWindowsPath(root, defaultWindowsInstallRoot()) {
			return "$env:CATCLIP_INSTALL_ROOT = " + powershellQuote(root) + "; " + command
		}
		return command
	}
	command := "curl -fsSL https://raw.githubusercontent.com/tigreau/catclip/main/install.sh | "
	if root != "" && filepath.Clean(root) != filepath.Clean("/usr/local") {
		return command + "PREFIX=" + shellQuote(root) + " bash"
	}
	return command + "bash"
}

func installRoot(platform, executable string) string {
	if executable == "" {
		return ""
	}
	if platform == "windows" {
		usesBackslashes := strings.Contains(executable, "\\")
		normalized := strings.ReplaceAll(executable, "\\", "/")
		isUNC := strings.HasPrefix(normalized, "//")
		root := path.Clean(path.Join(path.Dir(normalized), ".."))
		if isUNC && !strings.HasPrefix(root, "//") {
			root = "/" + root
		}
		if usesBackslashes {
			return strings.ReplaceAll(root, "/", "\\")
		}
		return root
	}
	return filepath.Clean(filepath.Join(filepath.Dir(executable), ".."))
}

func defaultWindowsInstallRoot() string {
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		return filepath.Join(localAppData, "Programs", "catclip")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "AppData", "Local", "Programs", "catclip")
}

func sameWindowsPath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	normalize := func(value string) string {
		value = strings.ReplaceAll(value, "\\", "/")
		return path.Clean(value)
	}
	return strings.EqualFold(normalize(left), normalize(right))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func powershellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func timeoutFor(opts Options) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	return checkTimeout
}

func readInstallMethod(executable string) string {
	if executable == "" {
		return ""
	}
	dir := filepath.Dir(executable)
	path := filepath.Clean(filepath.Join(dir, "..", "share", "catclip", receiptFileName))
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	method := strings.TrimSpace(string(data))
	if method == string(InstallSource) || method == string(InstallDirectRelease) {
		return method
	}
	return ""
}

func homebrewOwnsExecutable(ctx context.Context, executable string) bool {
	if isHomebrewCellarExecutable(executable) {
		return true
	}
	brew, err := exec.LookPath("brew")
	if err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, brew, "--prefix", "catclip")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	prefix := strings.TrimSpace(string(out))
	if prefix == "" {
		return false
	}
	return sameFile(executable, filepath.Join(prefix, "bin", executableName(executable)))
}

func isHomebrewCellarExecutable(executable string) bool {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	path := "/" + strings.Trim(strings.ReplaceAll(resolved, "\\", "/"), "/") + "/"
	return strings.Contains(strings.ToLower(path), "/cellar/catclip/")
}

func executableName(path string) string {
	name := filepath.Base(path)
	if strings.EqualFold(filepath.Ext(name), ".exe") {
		return "catclip.exe"
	}
	return "catclip"
}

func sameFile(left, right string) bool {
	left, err := filepath.EvalSymlinks(left)
	if err != nil {
		return false
	}
	right, err = filepath.EvalSymlinks(right)
	if err != nil {
		return false
	}
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(right)
	return err == nil && os.SameFile(leftInfo, rightInfo)
}

type version struct {
	major      int
	minor      int
	patch      int
	prerelease string
}

func parseVersion(raw string) (version, bool) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	raw = strings.SplitN(raw, "+", 2)[0]
	parts := strings.SplitN(raw, "-", 2)
	core := strings.Split(parts[0], ".")
	if len(core) != 3 {
		return version{}, false
	}
	values := [3]int{}
	for i, part := range core {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return version{}, false
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return version{}, false
		}
		values[i] = value
	}
	parsed := version{major: values[0], minor: values[1], patch: values[2]}
	if len(parts) == 2 {
		parsed.prerelease = parts[1]
		if parsed.prerelease == "" {
			return version{}, false
		}
	}
	return parsed, true
}

func compareVersions(left, right version) int {
	for _, pair := range [][2]int{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if left.prerelease == right.prerelease {
		return 0
	}
	if left.prerelease == "" {
		return 1
	}
	if right.prerelease == "" {
		return -1
	}
	return strings.Compare(left.prerelease, right.prerelease)
}

func normalizedVersion(value version) string {
	version := fmt.Sprintf("%d.%d.%d", value.major, value.minor, value.patch)
	if value.prerelease != "" {
		version += "-" + value.prerelease
	}
	return version
}
