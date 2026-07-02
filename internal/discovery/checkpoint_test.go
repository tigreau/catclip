package discovery

import (
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/git"
)

func TestCheckpointNoIgnoreRoundtrip(t *testing.T) {
	cases := []struct {
		name     string
		noIgnore bool
	}{
		{"noIgnore=false (default)", false},
		{"noIgnore=true (parent had --include)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scope.json")
			in := CheckpointData{
				GitContext: git.Context{},
				GitStatus:  map[string]string{},
				Entries:    []Entry{{RelPath: "a.txt"}},
				NoIgnore:   tc.noIgnore,
			}
			if err := WriteCheckpoint(path, t.TempDir(), in); err != nil {
				t.Fatalf("WriteCheckpoint: %v", err)
			}
			out, err := ReadCheckpoint(path)
			if err != nil {
				t.Fatalf("ReadCheckpoint: %v", err)
			}
			if out.NoIgnore != tc.noIgnore {
				t.Fatalf("NoIgnore round-trip: got %v, want %v", out.NoIgnore, tc.noIgnore)
			}
		})
	}
}
