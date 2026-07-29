package discovery

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestMain mirrors internal/search's shim: discovery tests that exercise
// rg-backed paths or the real fzf field contract run with cwd=internal/discovery,
// where there is no bin/, so point CATCLIP_RG and CATCLIP_FZF at the repo bin/
// directly. Tool-dependent tests still guard their own lookup.
func TestMain(m *testing.M) {
	if os.Getenv("CATCLIP_RG") == "" || os.Getenv("CATCLIP_FZF") == "" {
		_, thisFile, _, _ := runtime.Caller(0)
		repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
		rgName := "rg"
		fzfName := "fzf"
		if runtime.GOOS == "windows" {
			rgName = "rg.exe"
			fzfName = "fzf.exe"
		}
		rg := filepath.Join(repoRoot, "bin", rgName)
		if os.Getenv("CATCLIP_RG") == "" {
			if _, err := os.Stat(rg); err == nil {
				os.Setenv("CATCLIP_RG", rg)
			}
		}
		fzf := filepath.Join(repoRoot, "bin", fzfName)
		if os.Getenv("CATCLIP_FZF") == "" {
			if _, err := os.Stat(fzf); err == nil {
				os.Setenv("CATCLIP_FZF", fzf)
			}
		}
	}
	os.Exit(m.Run())
}
