package catclip

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Packaged installs are expected to carry private rg/fzf binaries under
// share/catclip/bin. Env overrides remain available for tests and developer
// runs, but runtime should not silently fall back to arbitrary PATH copies.
func bundledToolBinary(envVar, toolName string) (string, bool) {
	if path, ok := configuredBinaryOverride(envVar); ok {
		return path, true
	}
	return firstExistingBinary(bundledToolCandidates(toolName))
}

func companionBinary(envVar, toolName string) (string, bool) {
	if path, ok := configuredBinaryOverride(envVar); ok {
		return path, true
	}
	return firstExistingBinary(companionBinaryCandidatesForGOOS(runtime.GOOS, toolName, executableCandidateDirs()))
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
	return bundledToolCandidatesForGOOS(runtime.GOOS, toolName, executableCandidateDirs())
}

func bundledToolCandidatesForGOOS(goos, toolName string, dirs []string) []string {
	name := platformToolBinaryNameForGOOS(goos, toolName)
	var candidates []string
	for _, dir := range dirs {
		candidates = append(candidates,
			filepath.Join(dir, name),
			filepath.Join(dir, "bin", name),
			filepath.Join(dir, "..", "share", "catclip", "bin", name),
		)
	}
	return dedupePreserveOrder(candidates)
}

func companionBinaryCandidatesForGOOS(goos, toolName string, dirs []string) []string {
	name := platformToolBinaryNameForGOOS(goos, toolName)
	candidates := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	return dedupePreserveOrder(candidates)
}

func executableCandidateDirs() []string {
	if exe, err := os.Executable(); err == nil {
		dirs := []string{filepath.Dir(exe)}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			dirs = append(dirs, filepath.Dir(resolved))
		}
		return dedupePreserveOrder(dirs)
	}
	return nil
}

func platformToolBinaryName(toolName string) string {
	return platformToolBinaryNameForGOOS(runtime.GOOS, toolName)
}

func platformToolBinaryNameForGOOS(goos, toolName string) string {
	if goos == "windows" && !strings.HasSuffix(strings.ToLower(toolName), ".exe") {
		return toolName + ".exe"
	}
	return toolName
}
