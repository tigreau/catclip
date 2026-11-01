package catclip

import (
	"bytes"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
	"path/filepath"
	"strings"
	"testing"
)

func TestTargetInventoryPreviewAcceptsInclusionButRejectsFilters(t *testing.T) {
	project := setupTestProject(t, nil)
	inventory := filepath.Join(t.TempDir(), "targets.bin")
	if err := discovery.WriteTargetPreviewInventory(inventory, git.Context{}, []discovery.TargetMatch{{Path: "lib.jar", Kind: "file", SizeKnown: true, SizeBytes: 42}}); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{{"--with-binaries"}, {"--no-ignore", "-b"}, {"-b", "--no-ignore"}} {
		args := append([]string{"--internal-tree-preview", "--internal-target-inventory", inventory, "."}, flags...)
		cfg := parseInProject(t, project, args)
		var out, diagnostic bytes.Buffer
		var scans int
		restore := search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { scans++ })
		err := run(cfg, &out, &diagnostic)
		restore()
		if err != nil {
			t.Fatal(err)
		}
		if scans != 0 || !strings.Contains(out.String(), "lib.jar") || !strings.Contains(out.String(), "[binary]") {
			t.Fatalf("scans=%d output=%s", scans, out.String())
		}
	}
	for _, stage := range []string{"--paths", "--lines"} {
		cfg := parseInProject(t, project, []string{"--internal-tree-preview", "--internal-target-inventory", inventory, ".", "-b", stage})
		var out, diagnostic bytes.Buffer
		if err := run(cfg, &out, &diagnostic); err == nil || !strings.Contains(err.Error(), "does not accept filter or output stages") {
			t.Fatalf("guard accepted %s: %v", stage, err)
		}
	}
}
