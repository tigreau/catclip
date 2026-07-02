package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// BundledToolBinary returns the resolved path for a bundled tool (rg / fzf).
// Packaged installs are expected to carry private rg/fzf binaries under
// share/catclip/bin. Env overrides remain available for tests and developer
// runs, but runtime should not silently fall back to arbitrary PATH copies.
func BundledToolBinary(envVar, toolName string) (string, bool) {
	if envOverrideSet(envVar) {
		// Env var is explicitly set — it is authoritative. If the path is
		// valid, use it. If not, fail rather than falling through to
		// bundled candidates (which could silently pick a wrong version).
		return configuredBinaryOverride(envVar)
	}
	return firstExistingBinary(bundledToolCandidates(toolName))
}

// EnvOverrideSet reports whether the given env var is set to a
// non-empty value. Exported so the --version renderer can label
// resolved tool paths with the override name (e.g. "(CATCLIP_FZF)")
// when the user has explicitly pointed catclip at a specific binary.
func EnvOverrideSet(envVar string) bool {
	return strings.TrimSpace(os.Getenv(envVar)) != ""
}

func envOverrideSet(envVar string) bool {
	return EnvOverrideSet(envVar)
}

func configuredBinaryOverride(envVar string) (string, bool) {
	override := strings.TrimSpace(os.Getenv(envVar))
	if override == "" {
		return "", false
	}
	if strings.ContainsRune(override, os.PathSeparator) {
		if info, err := os.Stat(override); err == nil && !info.IsDir() {
			return override, true
		}
		return "", false
	}
	path, err := exec.LookPath(override)
	return path, err == nil
}

func firstExistingBinary(candidates []string) (string, bool) {
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func bundledToolCandidates(toolName string) []string {
	return bundledToolCandidatesForGOOS(runtime.GOOS, toolName, ExecutableCandidateDirs(), devBinDir())
}

func bundledToolCandidatesForGOOS(goos, toolName string, dirs []string, devDir string) []string {
	name := toolBinaryNameForGOOS(goos, toolName)
	var candidates []string
	for _, dir := range dirs {
		candidates = append(candidates,
			filepath.Join(dir, name),
			filepath.Join(dir, "bin", name),
			filepath.Join(dir, "..", "share", "catclip", "bin", name),
		)
	}
	// Dev builds (go run, go test): check cwd bin/ last. Populated by "make dev".
	// Production installs always match an earlier candidate above.
	if devDir != "" {
		candidates = append(candidates, filepath.Join(devDir, name))
	}
	return dedupePreserveOrder(candidates)
}

// resolvedDevBinDir is computed once at init from the initial working directory.
// Tests that chdir into temp projects still find the dev bin/ from the original cwd.
var resolvedDevBinDir = initDevBinDir()

func devBinDir() string {
	return resolvedDevBinDir
}

func initDevBinDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := filepath.Join(wd, "bin")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}

// ExecutableCandidateDirs returns the candidate directories where the running
// binary lives (resolving symlinks once). Used both for bundled-tool lookup
// and as a "where is catclip installed?" surface for help text.
func ExecutableCandidateDirs() []string {
	if exe, err := os.Executable(); err == nil {
		dirs := []string{filepath.Dir(exe)}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			dirs = append(dirs, filepath.Dir(resolved))
		}
		return dedupePreserveOrder(dirs)
	}
	return nil
}

func toolBinaryNameForGOOS(goos, toolName string) string {
	if goos == "windows" && !strings.HasSuffix(strings.ToLower(toolName), ".exe") {
		return toolName + ".exe"
	}
	return toolName
}

func dedupePreserveOrder(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
