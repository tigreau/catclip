package catclip

import (
	"bytes"
	"github.com/tigreau/catclip/internal/search"
	"strings"
	"testing"
)

func TestIgnoredMixedTargetsShareDiagnosticWalk(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{
		".gitignore":                    "docs/\npackages/\n.git/\n",
		".git/refs/tags/v0.7.11":        "tag\n",
		"docs/versions/v0.7.11/note.md": "old\n",
		"docs/versions/v0.7.12/note.md": "new\n",
		"packages/a/node_modules/a.js":  "a\n",
		"packages/b/node_modules/b.js":  "b\n",
		"visible.go":                    "package visible\n",
	})
	cfg := parseInProject(t, project, []string{"v0.7.12", "v0.7.11", "node_modules", "--headless"})
	var scans int
	restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) {
		if e.Context.Reason == search.MembershipReasonIgnoredAncestor {
			scans++
		}
	})
	defer restore()
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err == nil {
		t.Fatal("expected failed targets")
	}
	if stdout.Len() != 0 || scans != 1 {
		t.Fatalf("stdout=%q diagnostic walks=%d", stdout.String(), scans)
	}
	for _, want := range []string{"./docs/versions/v0.7.11", "./.git/refs/tags/v0.7.11", "./docs/versions/v0.7.12", "./packages/a/node_modules", "./packages/b/node_modules", "Name one of the complete paths above:", "Name the complete path above:"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %q:\n%s", want, stderr.String())
		}
	}
	for _, target := range []string{"v0.7.12", "v0.7.11", "node_modules"} {
		if strings.Count(stderr.String(), "catclip "+target+" --no-ignore") != 1 {
			t.Fatal(stderr.String())
		}
	}
	scans = 0
	stdout.Reset()
	stderr.Reset()
	cfg = parseInProject(t, project, []string{"visible.go", "--headless"})
	if err := run(cfg, &stdout, &stderr); err != nil || scans != 0 {
		t.Fatalf("successful target: err=%v walks=%d", err, scans)
	}
}

func TestNoIgnoreAliasResolvesHiddenTarget(t *testing.T) {
	setupAncestorXDG(t)
	project := setupTestProject(t, map[string]string{".gitignore": "docs/\n", "docs/v0.7.12/a.go": "package a\n"})
	cfg := parseInProject(t, project, []string{"v0.7.12", "-ni", "--paths", "--headless"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "docs/v0.7.12/a.go" {
		t.Fatal(stdout.String())
	}
}
