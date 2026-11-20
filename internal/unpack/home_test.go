package unpack

import (
	"path/filepath"
	"testing"
)

// A repository with frontend/ and backend/: bundle paths are relative to the
// folder the copy was made in, so unpacking from another folder must point
// back there instead of creating the files in the wrong place.
func TestFindBundleHomeInARepositoryWithSubprojects(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "frontend", "src", "App.tsx"), "app\n")
	mustWrite(t, filepath.Join(repo, "backend", "src", "main.go"), "main\n")
	mustWrite(t, filepath.Join(repo, "README.md"), "readme\n")

	fromFrontend := Bundle{Entries: []Entry{{Path: "src/App.tsx"}}}      // copied inside frontend/
	fromRoot := Bundle{Entries: []Entry{{Path: "frontend/src/App.tsx"}}} // copied at the root
	brandNew := Bundle{Entries: []Entry{{Path: "src/Brand.tsx"}}}

	for _, tt := range []struct {
		name string
		root string
		b    Bundle
		want string // "" = unpack here
	}{
		{"same folder as the copy", filepath.Join(repo, "frontend"), fromFrontend, ""},
		{"repository root, copy made in frontend", repo, fromFrontend, filepath.Join(repo, "frontend")},
		{"sibling backend, copy made in frontend", filepath.Join(repo, "backend"), fromFrontend, filepath.Join(repo, "frontend")},
		{"deeper folder, copy made in frontend", filepath.Join(repo, "frontend", "src"), fromFrontend, filepath.Join(repo, "frontend")},
		{"inside frontend, copy made at the root", filepath.Join(repo, "frontend"), fromRoot, repo},
		{"root, copy made at the root", repo, fromRoot, ""},
		{"new files found nowhere unpack here", filepath.Join(repo, "frontend"), brandNew, ""},
	} {
		home, found := FindBundleHome(tt.root, tt.b)
		if tt.want == "" && found {
			t.Errorf("%s: should unpack here, got home %s", tt.name, home)
		}
		if tt.want != "" && (!found || home != tt.want) {
			t.Errorf("%s: home = %q (%v), want %q", tt.name, home, found, tt.want)
		}
	}
}
