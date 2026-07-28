package discovery

import (
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestIgnoreAttributionDefersCaseFoldIndexesOnExactHit(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Resolver) error
	}{
		{
			name: "file",
			run: func(r *Resolver) error {
				_, err := r.fileBlockedBy("src/main.go")
				return err
			},
		},
		{
			name: "directory",
			run: func(r *Resolver) error {
				_, err := r.dirBlockedBy("src")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolver := Resolver{
				Cfg: command.Invocation{WorkingDir: t.TempDir()},
				visibleAll: map[string]struct{}{
					"src/main.go": {},
				},
				visibleWithHiss: map[string]struct{}{
					"src/main.go": {},
				},
				visibleAllDirs: map[string]struct{}{
					"src": {},
				},
				visibleWithHissDirs: map[string]struct{}{
					"src": {},
				},
				ignoreSetsReady: true,
			}

			if err := tc.run(&resolver); err != nil {
				t.Fatal(err)
			}
			if resolver.caseFoldIndexesReady {
				t.Fatal("exact-cased lookup built case-fold indexes")
			}
			if resolver.visibleAllFold != nil ||
				resolver.visibleWithHissFold != nil ||
				resolver.visibleAllDirsFold != nil ||
				resolver.visibleWithHissDirsFold != nil {
				t.Fatal("exact-cased lookup populated a case-fold map")
			}
		})
	}
}

func TestIgnoreAttributionBuildsCaseFoldIndexesAfterExactMiss(t *testing.T) {
	resolver := Resolver{
		Cfg: command.Invocation{WorkingDir: t.TempDir()},
		visibleAll: map[string]struct{}{
			"src/main.go": {},
		},
		visibleWithHiss: map[string]struct{}{
			"src/main.go": {},
		},
		visibleAllDirs: map[string]struct{}{
			"src": {},
		},
		visibleWithHissDirs: map[string]struct{}{
			"src": {},
		},
		ignoreSetsReady: true,
	}

	if _, err := resolver.fileBlockedBy("SRC/MAIN.GO"); err != nil {
		t.Fatal(err)
	}
	if !resolver.caseFoldIndexesReady {
		t.Fatal("case-fold fallback did not build its indexes")
	}
	if got := resolver.visibleWithHissFold["src/main.go"]; len(got) != 1 || got[0] != "src/main.go" {
		t.Fatalf("case-fold candidates = %v", got)
	}
}
