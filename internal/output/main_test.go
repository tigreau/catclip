package output

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tigreau/catclip/internal/search"
)

// TestMain mirrors internal/search's shim: output-package tests that
// exercise the rg-backed snippet paths (BatchSnippetMatches equivalence)
// run with cwd=internal/output, where there is no bin/, so point
// CATCLIP_RG at the repo bin/ directly. Tests that need rg must still
// guard with search.RipgrepBinary() so environments without the bundled
// tool skip instead of failing.
func TestMain(m *testing.M) {
	if os.Getenv("CATCLIP_RG") == "" {
		_, thisFile, _, _ := runtime.Caller(0)
		repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
		rgName := "rg"
		if runtime.GOOS == "windows" {
			rgName = "rg.exe"
		}
		rg := filepath.Join(repoRoot, "bin", rgName)
		if _, err := os.Stat(rg); err == nil {
			os.Setenv("CATCLIP_RG", rg)
		}
	}
	_ = search.ReloadWasCancelled // keep the search import obviously load-bearing
	os.Exit(m.Run())
}
