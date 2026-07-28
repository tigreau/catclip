package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/search"
)

func TestAllIgnoredTargetsWithBinariesSkipsTextClassification(t *testing.T) {
	if _, ok := search.RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}

	dir := t.TempDir()
	name := fmt.Sprintf("binary-residue-%s.xyz", t.Name())
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.xyz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := Resolver{
		Cfg:          command.Invocation{WorkingDir: dir},
		WithBinaries: true,
	}
	targets, err := resolver.AllIgnoredTargets(nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, target := range targets {
		if target.Path == name && target.Kind == treeTargetKindFile {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ignored binary %q missing from targets: %#v", name, targets)
	}

	residue, _ := search.TextClassificationResidue()
	for _, rel := range residue {
		if rel == name {
			t.Fatalf("--with-binaries unnecessarily classified %q as text residue", name)
		}
	}
}
