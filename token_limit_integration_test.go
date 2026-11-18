package catclip

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
)

func TestTokenLimitEndToEnd(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\nvar help = 12\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		args          []string
		raw, metadata bool
	}{
		{args: []string{".", "--headless", "--token-limit", "2"}},
		{args: []string{"--token-limit", "2", "a.go", "--then", "b.go", "--headless"}},
		{args: []string{"a.go", "--headless", "-tl", "2", "--raw"}, raw: true},
		{args: []string{".", "--metadata", "--headless", "-tl", "2"}, metadata: true},
		{args: []string{"a.go", "--then", "b.go", "--metadata", "--headless", "-tl", "8"}, metadata: true},
		{args: []string{".", "--snippet", "help", "1", "--headless", "-tl", "2"}},
		{args: []string{".", "--lines", "2", "2", "--headless", "-tl", "2"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cfg, err := cli.ParseArgs(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			cfg.WorkingDir = root
			var out, diag bytes.Buffer
			if err := run(cfg, &out, &diag); err != nil {
				t.Fatalf("%v\n%s", err, diag.String())
			}
			if tc.raw {
				if out.String() != "package " || !strings.Contains(diag.String(), "output truncated") {
					t.Fatalf("%q / %q", out.String(), diag.String())
				}
			} else {
				if !strings.Contains(out.String(), "output truncated") || strings.Contains(diag.String(), "Proceed?") {
					t.Fatalf("%q / %q", out.String(), diag.String())
				}
				if tc.metadata && !strings.Contains(out.String(), "metadata report is incomplete") {
					t.Fatal(out.String())
				}
			}
		})
	}
}

func TestTokenLimitNoOutputTreeAndWarning(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.go"), []byte(strings.Repeat("abcd\n", 90000)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{".", "--only-tree", "--token-limit"}, {".", "--only-tree", "-tl", "200000"}} {
		cfg, err := cli.ParseArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		cfg.WorkingDir = root
		var out, diag bytes.Buffer
		if err := run(cfg, &out, &diag); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String()+diag.String(), "may exceed") {
			t.Fatalf("explicit budget warned: %s / %s", out.String(), diag.String())
		}
		if args[2] == "--token-limit" && (!strings.Contains(out.String(), "partial") || !strings.Contains(out.String(), "100000")) {
			t.Fatal(out.String())
		}
	}
}
