package discovery

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

func TestNoIgnoreWalkExcludesCatclipControlHissInsideWorkingDirectory(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", project)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	if err := os.WriteFile(filepath.Join(project, "visible.txt"), []byte("visible\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hissPath, err := EnsureGlobalHiss()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hissPath, filepath.Join(project, "config", "catclip", ".hiss"); got != want {
		t.Fatalf("hiss path = %q, want %q", got, want)
	}

	rels, err := ripgrepListUnder(project, ".", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range rels {
		if rel == "config/catclip/.hiss" {
			t.Fatalf("no-ignore walk admitted Catclip control file: %v", rels)
		}
	}
}

func TestNoIgnoreScopeExcludesCatclipControlHissInsideWorkingDirectory(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", project)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(project, "config"))
	for rel, body := range map[string]string{
		".gitignore":                "node_modules/\n",
		"src/main.ts":               "main\n",
		"node_modules/pkg/index.js": "ignored\n",
	} {
		full := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EnsureGlobalHiss(); err != nil {
		t.Fatal(err)
	}
	scope := command.ExecutionScope{
		Targets:  []string{"."},
		NoIgnore: true,
		Exclude:  []string{"node_modules/*"},
		Stages: []command.Stage{
			{Kind: command.StageNoIgnore},
			{Kind: command.StageExclude, Values: []string{"node_modules/*"}},
		},
	}
	result, err := EvaluateScope(command.Invocation{WorkingDir: project, Headless: true}, git.Context{}, 0, scope, io.Discard, platform.Palette{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range result.Entries {
		if entry.RelPath == "config/catclip/.hiss" {
			t.Fatalf("no-ignore scope admitted Catclip control file: %v", result.Entries)
		}
	}
}
