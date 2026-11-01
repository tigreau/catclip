package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/search"
)

func TestDirectoryInventoryReusePreservesExplicitIgnoreBypass(t *testing.T) {
	root := writeNoIgnoreTargetFixture(t, map[string]string{
		".gitignore":  "parent/hidden/\nparent/visible/nested/\n",
		"parent/a.go": "package a\n", "parent/visible/b.go": "package b\n",
		"parent/visible/nested/c.go": "package c\n", "parent/hidden/d.go": "package d\n",
		"parent/binary/x.unknown": "\x00", "parent/prefix-other/e.go": "package e\n",
	})
	if err := os.Mkdir(filepath.Join(root, "parent", "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, noIgnore := range []bool{false, true} {
		r := Resolver{Cfg: command.Invocation{WorkingDir: root}, NoIgnore: noIgnore, ScopeTargets: []string{"parent", "parent/visible", "parent/hidden"}}
		walk := func(r *Resolver, target string) ([]Entry, error) {
			if noIgnore {
				return r.discoverFilesUnderNoIgnore(target)
			}
			return r.discoverVisibleFilesUnder(target)
		}
		if _, err := walk(&r, "parent"); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"parent/visible", "parent/hidden", "parent/visible/nested", "parent/binary", "parent/empty"} {
			var events []search.MembershipEnumerationEvent
			restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) { events = append(events, e) })
			got, err := walk(&r, target)
			restore()
			if err != nil {
				t.Fatalf("walk %s noIgnore=%v: %v", target, noIgnore, err)
			}
			fresh := Resolver{Cfg: r.Cfg, NoIgnore: noIgnore}
			want, err := walk(&fresh, target)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s noIgnore=%v got=%+v want=%+v", target, noIgnore, got, want)
			}
			if target == "parent/visible" && len(events) != 0 {
				t.Fatalf("repeated visible walk: %+v", events)
			}
			if !noIgnore && target == "parent/hidden" && len(events) == 0 {
				t.Fatal("ignored root incorrectly reused parent")
			}
		}
	}
}

func TestDirectoryInventoryReuseSeparatesIgnorePolicies(t *testing.T) {
	root := writeNoIgnoreTargetFixture(t, map[string]string{".gitignore": "parent/hidden.go\n", "parent/a.go": "a", "parent/hidden.go": "h"})
	r := Resolver{Cfg: command.Invocation{WorkingDir: root}, ScopeTargets: []string{"parent", "parent"}}
	visible, err := r.discoverVisibleFilesUnder("parent")
	if err != nil {
		t.Fatal(err)
	}
	r.NoIgnore = true
	all, err := r.discoverFilesUnderNoIgnore("parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || len(all) != 2 {
		t.Fatalf("visible=%v all=%v", visible, all)
	}
}

func TestDirectoryInventoryDoesNotReuseConfiguredDepthLimit(t *testing.T) {
	root := writeNoIgnoreTargetFixture(t, map[string]string{"parent/child/a.go": "a", "parent/child/deep/b.go": "b"})
	config := filepath.Join(t.TempDir(), "rg.conf")
	if err := os.WriteFile(config, []byte("--max-depth\n2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIPGREP_CONFIG_PATH", config)
	r := Resolver{Cfg: command.Invocation{WorkingDir: root}, ScopeTargets: []string{"parent", "parent/child"}}
	if _, err := r.discoverVisibleFilesUnder("parent"); err != nil {
		t.Fatal(err)
	}
	got, err := r.discoverVisibleFilesUnder("parent/child")
	if err != nil {
		t.Fatal(err)
	}
	fresh := Resolver{Cfg: r.Cfg}
	want, err := fresh.discoverVisibleFilesUnder("parent/child")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || len(got) != 2 {
		t.Fatalf("got=%v want=%v", got, want)
	}
}
