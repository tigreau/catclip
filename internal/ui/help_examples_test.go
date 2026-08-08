package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
)

func TestInteractiveHelpExamplesRouteToStartupPicker(t *testing.T) {
	examples, err := cli.RegisteredHelpExamples()
	if err != nil {
		t.Fatal(err)
	}

	project, err := filepath.Abs(filepath.Join("..", "..", "testdata", "help-react-go-project"))
	if err != nil {
		t.Fatalf("resolve help fixture: %v", err)
	}
	oldWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("enter help fixture: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWorkingDir) })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	count := 0
	for _, example := range examples {
		if example.Kind != cli.HelpExampleInteractive {
			continue
		}
		count++
		t.Run(example.ID, func(t *testing.T) {
			args, ok, err := example.CatclipArgs()
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatalf("interactive example has no catclip argv: %q", example.Command)
			}
			enabled, err := shouldUseStartupPicker(args)
			if err != nil {
				t.Fatalf("startup picker classification failed: %v", err)
			}
			if !enabled {
				t.Fatalf("documented interactive example bypasses startup picker: %s", example.Command)
			}
			resolver, err := newStartupPickerResolverForArgs(args)
			if err != nil {
				t.Fatalf("create startup resolver: %v", err)
			}
			direct, err := startupCommandCanRunDirectly(resolver, args)
			if err != nil {
				t.Fatalf("probe startup example: %v", err)
			}
			if direct {
				t.Fatalf("documented interactive example would run without its picker: %s", example.Command)
			}
		})
	}
	if count == 0 {
		t.Fatal("expected interactive help examples")
	}
}
