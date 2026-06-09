package search

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMain ensures the search-package tests can find the bundled rg binary.
// Root-package tests rely on initDevBinDir() observing a ./bin directory at
// the cwd, which is the repo root for those tests. The search package's
// tests run with cwd=internal/search, where there is no bin/, so we point
// CATCLIP_RG at the repo bin/ directly. Resolved via runtime.Caller so the
// path is independent of go test's invocation.
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
	if _, ok := RipgrepBinary(); !ok {
		fmt.Fprintln(os.Stderr, "FATAL: rg not found. Run 'make dev' to set up dev tools.")
		os.Exit(1)
	}
	os.Exit(m.Run())
}
