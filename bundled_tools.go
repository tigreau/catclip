package catclip

import (
	"fmt"
	"io"
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
	if envOverrideSet(envVar) {
		// Env var is explicitly set — it is authoritative. If the path is
		// valid, use it. If not, fail rather than falling through to
		// bundled candidates (which could silently pick a wrong version).
		return configuredBinaryOverride(envVar)
	}
	return firstExistingBinary(bundledToolCandidates(toolName))
}

func envOverrideSet(envVar string) bool {
	return strings.TrimSpace(os.Getenv(envVar)) != ""
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
	return bundledToolCandidatesForGOOS(runtime.GOOS, toolName, executableCandidateDirs(), devBinDir())
}

func bundledToolCandidatesForGOOS(goos, toolName string, dirs []string, devDir string) []string {
	name := platformToolBinaryNameForGOOS(goos, toolName)
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

func ensureRequiredTools(stderr io.Writer) error {
	var missing []string
	if _, ok := ripgrepBinary(); !ok {
		missing = append(missing, "ripgrep (rg)")
	}
	if _, ok := fzfBinary(); !ok {
		missing = append(missing, "fzf")
	}
	if len(missing) == 0 {
		return nil
	}
	fmt.Fprintf(stderr, "catclip v%s\n\n", loadVersion())
	fmt.Fprintf(stderr, "Error: missing required dependencies: %s\n\n", strings.Join(missing, ", "))
	fmt.Fprintf(stderr, "  catclip ships with bundled copies of ripgrep and fzf.\n")
	fmt.Fprintf(stderr, "  If they are missing, your installation is incomplete.\n\n")
	fmt.Fprintf(stderr, "  Reinstall:\n")
	fmt.Fprintf(stderr, "    Homebrew:  brew reinstall catclip\n")
	fmt.Fprintf(stderr, "    Script:    curl -fsSL https://raw.githubusercontent.com/tigreau/catclip/main/install.sh | bash\n")
	fmt.Fprintf(stderr, "    Source:    ./install.sh\n")
	fmt.Fprintf(stderr, "    Dev:       make dev\n")
	return fmt.Errorf("missing required dependencies")
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
