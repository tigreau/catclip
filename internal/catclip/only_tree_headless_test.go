package catclip

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
)

func TestOnlyTreeHeadlessReports(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	root := t.TempDir()
	body := "package main\n// SOURCE_SECRET help\nvar x = 1\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range [][]string{nil, {"--snippet", "help", "0"}, {"--lines", "2", "2"}, {"--metadata"}, {"--paths"}, {"--token-limit", "2"}} {
		for _, flag := range []string{"--only-tree", "-ot"} {
			args := append([]string{"."}, mode...)
			args = append(args, flag, "--headless")
			cfg, err := cli.ParseArgs(args)
			if err != nil {
				t.Fatal(err)
			}
			cfg.WorkingDir = root
			var out, diag bytes.Buffer
			if err := run(cfg, &out, &diag); err != nil {
				t.Fatalf("%q: %v %s", args, err, &diag)
			}
			if !strings.Contains(out.String(), "main.go") || !strings.Contains(out.String(), "Tokens:") {
				t.Fatalf("%q: %s", args, &out)
			}
			for _, forbidden := range []string{"SOURCE_SECRET", "<file ", "Copied ", "Proceed?", "\x1b["} {
				if strings.Contains(out.String()+diag.String(), forbidden) {
					t.Fatalf("%q leaked %q: %s / %s", args, forbidden, &out, &diag)
				}
			}
			if len(mode) > 0 && mode[0] == "--token-limit" && !strings.Contains(out.String(), "[partial]") {
				t.Fatal(out.String())
			}
		}
	}
	for _, args := range [][]string{{".", "--headless"}, {".", "--only", "*.absent", "-ot", "--headless"}} {
		cfg, err := cli.ParseArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		cfg.WorkingDir = root
		var out, diag bytes.Buffer
		err = run(cfg, &out, &diag)
		if len(args) == 2 {
			if err != nil || !strings.Contains(out.String(), "SOURCE_SECRET") || strings.Contains(out.String(), "Count:") {
				t.Fatalf("ordinary headless changed: %v %s", err, &out)
			}
		} else if err == nil || out.Len() != 0 || !strings.Contains(diag.String(), "No text files found") {
			t.Fatalf("empty result: %v %s / %s", err, &out, &diag)
		}
	}
}
