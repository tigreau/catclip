package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/search"
)

// TestMain ensures rg and fzf are available before running ui tests
// and registers the discovery scope-view callback (the same wiring
// root Main() does). The picker / preview tests call discovery
// primitives that expect a scope-view resolver to be registered, and
// many tests shell out via search.RipgrepBinary.
//
// Since `go test ./internal/ui/...` runs with cwd=internal/ui, the
// bundled-tool dev-dir lookup (cwd/bin) misses the project's bin/
// dir. Point CATCLIP_RG and CATCLIP_FZF at the project bin before
// search.RipgrepBinary / discovery.FzfBinary are first consulted.
func TestMain(m *testing.M) {
	resolveDevBinForUITests()

	if _, ok := search.RipgrepBinary(); !ok {
		fmt.Fprintln(os.Stderr, "FATAL: rg not found. Run 'make dev' to set up dev tools.")
		os.Exit(1)
	}
	if _, ok := discovery.FzfBinary(); !ok {
		fmt.Fprintln(os.Stderr, "FATAL: fzf not found. Run 'make dev' to set up dev tools.")
		os.Exit(1)
	}
	discovery.SetScopeViewResolver(ScopeViewForDiscoveryArgs)
	os.Exit(m.Run())
}

// resolveDevBinForUITests sets CATCLIP_RG / CATCLIP_FZF if unset and
// the project's bin/ holds dev binaries. _, file from runtime.Caller
// gives this file's path; walking up to the project root lets us find
// the bin/ regardless of which package's tests run go test from.
func resolveDevBinForUITests() {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	dir := filepath.Dir(file)
	for i := 0; i < 6; i++ {
		bin := filepath.Join(dir, "bin")
		if info, err := os.Stat(bin); err == nil && info.IsDir() {
			rgPath := filepath.Join(bin, rgBinaryName())
			if _, err := os.Stat(rgPath); err == nil {
				if os.Getenv("CATCLIP_RG") == "" {
					os.Setenv("CATCLIP_RG", rgPath)
				}
			}
			fzfPath := filepath.Join(bin, fzfBinaryName())
			if _, err := os.Stat(fzfPath); err == nil {
				if os.Getenv("CATCLIP_FZF") == "" {
					os.Setenv("CATCLIP_FZF", fzfPath)
				}
			}
			return
		}
		dir = filepath.Dir(dir)
	}
}

func rgBinaryName() string {
	if runtime.GOOS == "windows" {
		return "rg.exe"
	}
	return "rg"
}

func fzfBinaryName() string {
	if runtime.GOOS == "windows" {
		return "fzf.exe"
	}
	return "fzf"
}
