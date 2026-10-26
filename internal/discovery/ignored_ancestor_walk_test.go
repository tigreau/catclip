package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/search"
)

func TestIgnoredAncestorReusesCandidateDescendantEvidence(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(GlobalHissPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalHissPath(), []byte("hiss-hidden/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	write(".gitignore", "git-hidden/\nmixed/*\n!mixed/keep/\n")
	write("mixed/keep/visible.txt", "visible\n")
	var paths []string
	for i := 1; i <= 10; i++ {
		name := fmt.Sprintf("git-hidden/b%02d/%s.env.local", i, strings.Repeat("deep/", i*2))
		write(name, "synthetic=yes\n")
		paths = append(paths, name)
	}
	write("hiss-hidden/deep/.env.local", "synthetic=yes\n")
	write("mixed/blocked/deep/.env.local", "synthetic=yes\n")
	paths = append(paths, "hiss-hidden/deep/.env.local", "mixed/blocked/deep/.env.local")
	r := Resolver{Cfg: command.Invocation{WorkingDir: root}, WantedBasenames: CollectWantedBasenames([]string{".env.local"})}
	var events []search.MembershipEnumerationEvent
	defer search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) { events = append(events, e) })()
	visible, skipped, err := r.resolveVisibleFilesByBasename(".", ".env.local")
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 0 || len(skipped) != len(paths) {
		t.Fatalf("visible=%d skipped=%d want %d ignored", len(visible), len(skipped), len(paths))
	}
	candidates := r.ignoredExactFileCandidates(skipped)
	if len(candidates) != len(paths) {
		t.Fatalf("candidates=%d want %d", len(candidates), len(paths))
	}
	for _, c := range candidates {
		wantBlocker, wantSource := "git-hidden", ".gitignore"
		if strings.HasPrefix(c.Path, "hiss-hidden/") {
			wantBlocker, wantSource = "hiss-hidden", ".hiss"
		}
		if strings.HasPrefix(c.Path, "mixed/") {
			wantBlocker = "mixed/blocked"
		}
		if c.Blocker != wantBlocker || c.Source != wantSource {
			t.Errorf("candidate=%+v want blocker=%s source=%s", c, wantBlocker, wantSource)
		}
	}
	// A directory candidate also comes from a known descendant in rg's output.
	if c := r.findBlockerForPath("git-hidden/b01/deep", true); c == nil || c.Blocker != "git-hidden" {
		t.Fatalf("directory candidate=%+v", c)
	}
	if len(events) != 3 {
		t.Fatalf("membership subprocesses=%d want 3 (basename + two visibility inventories); ancestor checks must not walk", len(events))
	}
}

func TestDirBlockedByStillChecksUnprovenEmptyDirectory(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	for _, name := range []string{"empty", "nonempty"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "nonempty", "file.txt"), []byte("synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Both directories are absent from the visible inventories. Without a
	// retained descendant the ordinary resolver must still distinguish them.
	r := Resolver{Cfg: command.Invocation{WorkingDir: root}, ignoreSetsReady: true}
	block, err := r.dirBlockedBy("empty")
	if err != nil || block != nil {
		t.Fatalf("empty block=%+v err=%v", block, err)
	}
	block, err = r.dirBlockedBy("nonempty")
	if err != nil || block == nil || block.Source != ".gitignore" {
		t.Fatalf("nonempty block=%+v err=%v", block, err)
	}
}
