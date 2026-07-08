package discovery

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMain mirrors internal/search's shim: discovery tests that exercise
// rg-backed paths (snippet stage producer, text sets) run with
// cwd=internal/discovery, where there is no bin/, so point CATCLIP_RG at
// the repo bin/ directly. rg-dependent tests still guard with
// search.RipgrepBinary() so tool-less environments skip.
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
	os.Exit(m.Run())
}
