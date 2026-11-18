package discovery

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

func TestIgnoredDiagnosticsBatchTenUnrelatedTargets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	names := []string{"a", "z", "config.ts", "omega.md", "first", "last", "v0.7.11", "settings.json", "middle", "xyz"}
	write := func(rel, body string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "blocked/\nrefs/\n")
	for _, name := range names {
		write("blocked/"+name+"/note.md", "hidden directory\n")
		write("refs/"+name, "hidden file\n")
	}
	var scans int
	defer search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) {
		if e.Context.Reason == search.MembershipReasonIgnoredAncestor {
			scans++
		}
	})()
	result, err := EvaluateScope(command.Invocation{WorkingDir: root, Headless: true}, git.Context{}, 0, command.ExecutionScope{Targets: names}, io.Discard, platform.Palette{})
	if err != nil {
		t.Fatal(err)
	}
	if scans != 1 || len(result.Diagnostics) != len(names) {
		t.Fatalf("walks=%d diagnostics=%+v", scans, result.Diagnostics)
	}
	for i, d := range result.Diagnostics {
		for _, rel := range []string{"blocked/" + names[i], "refs/" + names[i]} {
			if !strings.Contains(d.Message, "./"+rel) {
				t.Fatalf("missing %s: %s", rel, d.Message)
			}
		}
	}
}

func TestIgnoredDiagnosticBatchScopesParentsAndReusesRawInventory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	r := Resolver{Cfg: command.Invocation{WorkingDir: root}, ignoreSetsReady: true}
	paths := []string{"a/x/file.md", "b/y/file.md", "b/x/file.md", "c/y/file.md"}
	r.directoryInventories = map[directoryInventoryKey][]string{
		{root: ".", noIgnore: true}: paths,
	}
	var calls int
	defer search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { calls++ })()
	found := r.findIgnoredAncestorsBatch([]string{"a/x", "b/y", "a/x"})
	if calls != 0 || len(found["a/x"]) != 1 || len(found["b/y"]) != 1 || len(found) != 2 {
		t.Fatalf("calls=%d results=%+v", calls, found)
	}
	if found["a/x"][0].Path != "a/x" || found["b/y"][0].Path != "b/y" {
		t.Fatal(found)
	}
}

func TestIgnoredDiagnosticBatchKeepsCapAndRawPaths(t *testing.T) {
	r := Resolver{ignoreSetsReady: true}
	var paths []string
	for i := 0; i < 8; i++ {
		paths = append(paths, fmt.Sprintf("blocked/%02d/version/note.md", i))
	}
	matches := r.ignoredCandidatesFromPaths(paths, map[string][]ignoredDiagnosticQuery{
		"version": {{target: "version", parent: "."}},
	})["version"]
	message := ignoredAncestorMessage("version", 0, matches, platform.Palette{})
	if !strings.Contains(message, "…and 3 more") || strings.Contains(message, "./blocked/05/version") ||
		strings.Count(message, "catclip version --no-ignore") != 1 ||
		strings.Count(message, "    catclip 'blocked/00/version'") != 1 {
		t.Fatal(message)
	}
}

func TestIgnoredDiagnosticBatchKeepsOutermostDirectoryExamples(t *testing.T) {
	r := Resolver{ignoreSetsReady: true}
	found := r.ignoredCandidatesFromPaths([]string{
		"a/node_modules/one/node_modules/a.js",
		"a/node_modules/two/node_modules/b.js",
		"b/node_modules/c.js",
	}, map[string][]ignoredDiagnosticQuery{"node_modules": {{target: "node_modules", parent: "."}}})["node_modules"]
	if len(found) != 2 || found[0].Path != "a/node_modules" || found[1].Path != "b/node_modules" {
		t.Fatal(found)
	}
}

func TestIgnoredDiagnosticBatchOverlappingParents(t *testing.T) {
	r := Resolver{ignoreSetsReady: true}
	found := r.ignoredCandidatesFromPaths([]string{"a/x/x/note.md"}, map[string][]ignoredDiagnosticQuery{
		"x": {{target: "a/x", parent: "a"}, {target: "a/x/x", parent: "a/x"}},
	})
	if len(found["a/x"]) != 1 || found["a/x"][0].Path != "a/x" || len(found["a/x/x"]) != 1 || found["a/x/x"][0].Path != "a/x/x" {
		t.Fatal(found)
	}
}

func TestIgnoredDiagnosticBatchTreatsQueryGlobPunctuationLiterally(t *testing.T) {
	root := t.TempDir()
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	for _, rel := range []string{"blocked/a{b,c}/note.md", "refs/a{b,c}", "refs/ab", "refs/ac", "refs/!odd"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := Resolver{Cfg: command.Invocation{WorkingDir: root}, ignoreSetsReady: true}
	found := r.findIgnoredAncestorsBatch([]string{"a{b,c}", "!odd"})
	if len(found["a{b,c}"]) != 2 || found["a{b,c}"][0].Path != "blocked/a{b,c}" || found["a{b,c}"][1].Path != "refs/a{b,c}" || len(found["!odd"]) != 1 || found["!odd"][0].Path != "refs/!odd" {
		t.Fatal(found)
	}
}
