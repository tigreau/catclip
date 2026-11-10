package catclip

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
	"github.com/tigreau/catclip/internal/ui"
)

func TestVanishedFileDuringBroadDiscovery(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, args := range [][]string{
		{".", "--no"}, {".", "--metadata", "--no"},
		{".", "--print"}, {".", "--headless"}, {".", "--metadata", "--print"},
		{"*.json", "--no-ignore", "--no"},
		{".", "--exclude", "rotating.json", "--no"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := t.TempDir()
			vanished := filepath.Join(root, "rotating.json")
			for _, name := range []string{vanished, filepath.Join(root, "good.json")} {
				if err := os.WriteFile(name, []byte("{\"ok\":true}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			removed := false
			restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) {
				if !removed && e.Kind == search.MembershipEnumerationFiles && !e.Failed {
					if err := os.Remove(vanished); err != nil {
						t.Fatal(err)
					}
					removed = true
				}
			})
			defer restore()
			cfg, err := cli.ParseArgs(args)
			if err != nil {
				t.Fatal(err)
			}
			cfg.WorkingDir = root
			var stdout, stderr bytes.Buffer
			if err := run(cfg, &stdout, &stderr); err != nil {
				t.Fatalf("%v\n%s", err, stderr.String())
			}
			if !removed || !strings.Contains(stdout.String(), "good.json") || strings.Contains(stdout.String(), "rotating.json") {
				t.Fatalf("incorrect membership: removed=%v stdout=%s", removed, stdout.String())
			}
			wantWarning := !strings.Contains(strings.Join(args, " "), "--exclude")
			if strings.Contains(stderr.String(), "[no longer exists]") != wantWarning || strings.Contains(stdout.String(), "[no longer exists]") {
				t.Fatalf("wrong diagnostics: %s / %s", stdout.String(), stderr.String())
			}
		})
	}
}

func TestVanishedTargetPreviewUsesAvailableSnapshot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	for _, name := range []string{"good.json", "rotating.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	removed := false
	restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) {
		if !removed && e.Kind == search.MembershipEnumerationFiles && !e.Failed {
			if err := os.Remove(filepath.Join(root, "rotating.json")); err != nil {
				t.Fatal(err)
			}
			removed = true
		}
	})
	defer restore()
	r := discovery.Resolver{Cfg: command.Invocation{WorkingDir: root}, CaptureTargetPreviewSizes: true}
	if err := r.BuildVisibleFileList(); err != nil {
		t.Fatal(err)
	}
	entries := discovery.EnsureEntryAbsPaths(r.VisibleFileList, root)
	if !removed {
		t.Fatal("fixture did not remove the enumerated file")
	}
	for i := range entries {
		entries[i].Mode = command.EntryModeFull
	}
	var preview bytes.Buffer
	if err := ui.RenderFreshTargetTreePreview(context.Background(), &preview, ui.RenderConfig{}, git.Context{}, entries); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.String(), "good.json") || !strings.Contains(preview.String(), `[no longer exists]`) || strings.Count(preview.String(), "rotating.json") != 1 {
		t.Fatalf("incorrect preview: %s", preview.String())
	}
}

func TestVanishedEmptyAndExplicitRequestsFail(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, explicit := range []bool{false, true} {
		root := t.TempDir()
		filename := filepath.Join(root, "rotating.json")
		target := "rotating.json"
		if !explicit {
			target = "."
			if err := os.WriteFile(filename, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		removed := explicit
		restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) {
			if !removed && e.Kind == search.MembershipEnumerationFiles && !e.Failed {
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				removed = true
			}
		})
		cfg, err := cli.ParseArgs([]string{target, "--headless"})
		if err != nil {
			t.Fatal(err)
		}
		cfg.WorkingDir = root
		var stdout, stderr bytes.Buffer
		err = run(cfg, &stdout, &stderr)
		restore()
		if err == nil || stdout.Len() != 0 || strings.Contains(stderr.String(), "Copied ") {
			t.Fatalf("false success: explicit=%v err=%v stdout=%s stderr=%s", explicit, err, stdout.String(), stderr.String())
		}
		if !explicit && !strings.Contains(stderr.String(), "[no longer exists]") {
			t.Fatalf("missing omission summary: %s", stderr.String())
		}
	}
}
