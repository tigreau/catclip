package catclip

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func TestHeadlessLineOmissionsIgnoreTerminalPalette(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.txt")
	if err := os.WriteFile(path, []byte("line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := output.PreparePlan(git.Context{}, []discovery.Entry{{AbsPath: path, RelPath: "short.txt", Lines: true, Mode: command.EntryModeLines, LinesStart: 10, LinesEnd: 20}})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err = executePlanOutput(outputExecutionContext{
		Invocation: command.Invocation{Headless: true, Quiet: true},
		Stdout:     &stdout, Stderr: &stderr, Colors: platform.ANSIPalette(),
	}, outputExecutionState{Plan: plan})
	var exitErr exitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected empty exit 1: %v", err)
	}
	const want = "Omitted short.txt: requested lines 10-20; file has 1 line.\n\nNo output was emitted.\n"
	if stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunLineOmissionContract(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cmd/short.txt": "one\ntwo\n",
		"cmd/long.txt":  "a\nb\nc\nd\ne\nf\n",
		"cmd/empty.txt": "",
	})
	for _, tc := range []struct {
		name                string
		args                []string
		code                int
		payload, diagnostic string
	}{
		{"partial selection", []string{"cmd", "--lines", "4", "10"}, 0, `lines="4-6"`, "Omitted cmd/short.txt: requested lines 4-10; file has 2 lines."},
		{"only empty selection", []string{"cmd/short.txt", "--lines", "4", "10"}, 1, "", "No output was emitted."},
		{"empty file", []string{"cmd/empty.txt", "--lines", "1", "10"}, 1, "", "file has 0 lines."},
		{"directory then focus", []string{"cmd", "--then", "cmd/short.txt", "--lines", "4", "10"}, 0, `path="cmd/long.txt"`, "Omitted cmd/short.txt:"},
		{"focus then directory", []string{"cmd/short.txt", "--lines", "4", "10", "--then", "cmd"}, 0, `path="cmd/long.txt"`, "Omitted cmd/short.txt:"},
		{"paths then focus", []string{"cmd", "--paths", "--then", "cmd/short.txt", "--lines", "4", "10"}, 0, "cmd/short.txt\n", "Omitted cmd/short.txt:"},
		{"raw", []string{"cmd", "--lines", "4", "10", "--raw"}, 0, "d\ne\nf\n", "Omitted cmd/short.txt:"},
		{"open ended", []string{"cmd/long.txt", "--lines", "4"}, 0, `lines="4-6"`, ""},
		{"report only", []string{"cmd", "--lines", "4", "10", "--no"}, 0, "", "Omitted cmd/short.txt:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--no-tree"}
			if tc.name != "report only" {
				args = append(args, "--headless", "--print")
			}
			args = append(args, tc.args...)
			cfg := parseInProject(t, project, args)
			var stdout, stderr bytes.Buffer
			err := run(cfg, &stdout, &stderr)
			code := 0
			if err != nil {
				var exitErr exitError
				if !errors.As(err, &exitErr) {
					t.Fatal(err)
				}
				code = exitErr.code
			}
			if code != tc.code {
				t.Fatalf("code=%d want %d; stderr=%s", code, tc.code, &stderr)
			}
			if tc.code == 1 && stdout.Len() != 0 {
				t.Fatalf("unexpected output: %s", &stdout)
			}
			if !strings.Contains(stdout.String(), tc.payload) || !strings.Contains(stderr.String(), tc.diagnostic) {
				t.Fatalf("stdout=%s\nstderr=%s", &stdout, &stderr)
			}
			if strings.Contains(stdout.String(), "Omitted") || strings.Contains(stdout.String(), "one\n") || strings.Contains(stderr.String(), "No files matched") {
				t.Fatalf("fallback, generic advice or leaked diagnostic: stdout=%s stderr=%s", &stdout, &stderr)
			}
			if tc.diagnostic == "" && stderr.Len() != 0 {
				t.Fatalf("unexpected warning: %s", &stderr)
			}
		})
	}
}

func TestRunLineOmissionExplicitDetailsWithinBroadSummary(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("cmd/%02d.txt", i)] = "first\nsecond\n"
	}
	project := setupTestProject(t, files)
	for _, sectioned := range []bool{false, true} {
		args := []string{"--headless", "--print", "--no-tree"}
		if sectioned {
			args = append(args, "cmd", "--paths", "--then")
		}
		args = append(args, "cmd", "--lines", "10", "20", "--then", "cmd/00.txt", "--lines", "10", "20")
		cfg := parseInProject(t, project, args)
		var stdout, stderr bytes.Buffer
		err := run(cfg, &stdout, &stderr)
		if sectioned && err != nil {
			t.Fatal(err)
		}
		if !sectioned {
			var exitErr exitError
			if !errors.As(err, &exitErr) || exitErr.code != 1 {
				t.Fatalf("expected empty-output exit 1: %v", err)
			}
		}
		if !strings.Contains(stderr.String(), "Omitted cmd/00.txt: requested lines 10-20; file has 2 lines.") || !strings.Contains(stderr.String(), "Omitted 11 files:") || strings.Contains(stderr.String(), "cmd/01.txt") {
			t.Fatalf("sectioned=%v diagnostics=%s", sectioned, &stderr)
		}
		if strings.Contains(stdout.String(), "Omitted") || strings.Contains(stdout.String(), "first") {
			t.Fatal(stdout.String())
		}
	}
}
