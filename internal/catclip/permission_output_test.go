package catclip

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/search"
)

func TestPermissionBroadCopy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires non-root Unix permission enforcement")
	}
	for _, mode := range []string{"no-search", "no-list", "no-read"} {
		for _, headless := range []bool{false, true} {
			for _, metadata := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/headless=%t/metadata=%t", mode, headless, metadata), func(t *testing.T) {
					root := t.TempDir()
					locked := filepath.Join(root, "dir1")
					if err := os.Mkdir(locked, 0755); err != nil {
						t.Fatal(err)
					}
					bad := filepath.Join(locked, "f1.txt")
					for name, data := range map[string]string{filepath.Join(root, "good.txt"): "accessible body\n", bad: "restricted body\n"} {
						if err := os.WriteFile(name, []byte(data), 0644); err != nil {
							t.Fatal(err)
						}
					}
					target, permissions := locked, os.FileMode(0644)
					if mode == "no-list" {
						permissions = 0
					}
					if mode == "no-read" {
						target, permissions = bad, 0
					}
					if err := os.Chmod(target, permissions); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(target, 0755) })
					args := []string{".", "--print"}
					if headless {
						args = append(args, "--headless")
					}
					if metadata {
						args = append(args, "--metadata")
					}
					cmd := exec.Command(os.Args[0], args...)
					cmd.Dir, cmd.Env = root, nativePreviewTestEnv(t)
					var stdout, stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					if err := cmd.Run(); err != nil {
						t.Fatalf("%v\n%s", err, stderr.String())
					}
					if !strings.Contains(stdout.String(), "good.txt") || strings.Contains(stdout.String(), "[permission error]") {
						t.Fatalf("bad payload: %s", stdout.String())
					}
					wantSkip := mode != "no-read" || !metadata
					if strings.Contains(stderr.String(), "[permission error]") != wantSkip {
						t.Fatalf("wrong diagnostics: %s", stderr.String())
					}
					if strings.Contains(stdout.String(), "f1.txt") == wantSkip {
						t.Fatalf("wrong membership: %s", stdout.String())
					}
					wantSummaries := 0
					if wantSkip {
						wantSummaries = 1
					}
					if strings.Count(stderr.String(), "Skipped ") != wantSummaries {
						t.Fatalf("duplicate/missing summary: %s", stderr.String())
					}
				})
			}
		}
	}
}

func TestPermissionSummaryBoundedAndScoped(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 12; i++ {
		search.RecordAccessFailure(&os.PathError{Op: "open", Path: filepath.Join(root, "selected", fmt.Sprintf("%02d.txt", i)), Err: os.ErrPermission}, false)
	}
	for _, name := range []string{"selected/00\ncontrol.txt", "elsewhere/private.txt"} {
		search.RecordAccessFailure(&os.PathError{Op: "open", Path: filepath.Join(root, name), Err: os.ErrPermission}, false)
	}
	var stderr bytes.Buffer
	ctx := outputExecutionContext{Invocation: command.Invocation{WorkingDir: root}, Stderr: &stderr}
	state := outputExecutionState{DiscoveredScopes: []discovery.Scope{{Scope: command.ExecutionScope{Targets: []string{"selected"}}}}}
	if err := writeAccessSummary(ctx, state); err != nil {
		t.Fatal(err)
	}
	got := stderr.String()
	if !strings.Contains(got, "Skipped 13 inaccessible") || !strings.Contains(got, "3 more inaccessible paths") || strings.Count(got, "[permission error]") != 10 {
		t.Fatalf("unbounded or incorrect summary: %s", got)
	}
	if strings.Contains(got, "elsewhere") || strings.Contains(got, "00\ncontrol") || !strings.Contains(got, `00\ncontrol`) {
		t.Fatalf("wrong scope or unsafe filename: %q", got)
	}
}

func TestPermissionExclusionsAndEmptySelection(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires non-root Unix permissions")
	}
	for _, tc := range []struct {
		name                   string
		args                   []string
		good, success, summary bool
	}{
		{"excluded", []string{".", "--exclude", "blocked.txt", "--print"}, true, true, false},
		{"no-tree", []string{".", "--headless", "--no-tree", "--print"}, true, true, true},
		{"glob", []string{"*.txt", "--headless", "--print"}, true, true, true},
		{"glob-no-ignore", []string{"*.txt", "--no-ignore", "--headless", "--print"}, true, true, true},
		{"no-ignore", []string{".", "--no-ignore", "--headless", "--print"}, true, true, true},
		{"only-readable", []string{".", "--only", "good.txt", "--headless", "--print"}, true, true, false},
		{"all-inaccessible", []string{".", "--headless", "--print"}, false, false, true},
		{"explicit-file", []string{"blocked.txt", "--headless", "--print"}, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bad := filepath.Join(root, "blocked.txt")
			if err := os.WriteFile(bad, []byte("private"), 0000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(bad, 0600) })
			if tc.good {
				if err := os.WriteFile(filepath.Join(root, "good.txt"), []byte("accessible"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(os.Args[0], tc.args...)
			cmd.Dir, cmd.Env = root, nativePreviewTestEnv(t)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if (err == nil) != tc.success {
				t.Fatalf("exit=%v stderr=%s", err, stderr.String())
			}
			if strings.Contains(stderr.String(), "Skipped ") != tc.summary {
				t.Fatalf("summary: %s", stderr.String())
			}
			if !tc.success && (stdout.Len() != 0 || strings.Contains(stderr.String(), "Copied ")) {
				t.Fatalf("false success: %s / %s", stdout.String(), stderr.String())
			}
			if tc.success && !strings.Contains(stdout.String(), "accessible") {
				t.Fatalf("lost readable file: %s", stdout.String())
			}
		})
	}
}

func TestPermissionSummaryBeforeClipboardSuccess(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if runtime.GOOS != "darwin" || os.Geteuid() == 0 {
		t.Skip("uses a private macOS pbcopy stub")
	}
	root := t.TempDir()
	tools := t.TempDir()
	clipboard := filepath.Join(tools, "clipboard")
	if err := os.WriteFile(filepath.Join(tools, "pbcopy"), []byte("#!/bin/sh\ncat > \"$CATCLIP_TEST_CLIPBOARD\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "good.txt"), []byte("accessible"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "blocked.txt")
	if err := os.WriteFile(bad, []byte("restricted"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0600) })
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CATCLIP_TEST_CLIPBOARD", clipboard)
	cfg, err := cli.ParseArgs([]string{".", "-y", "--no-tree", "--no-bundle"})
	if err != nil {
		t.Fatal(err)
	}
	cfg.WorkingDir, cfg.OutputMode = root, command.OutputModeClipboard
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("%v: %s", err, stderr.String())
	}
	text := stderr.String()
	summary, success := strings.Index(text, "Skipped 1 inaccessible"), strings.Index(text, "Copied good.txt")
	if summary < 0 || success < summary || strings.Count(text, "Skipped ") != 1 {
		t.Fatalf("bad summary/success order: %s", text)
	}
	payload, err := os.ReadFile(clipboard)
	if err != nil || !bytes.Contains(payload, []byte("accessible")) || bytes.Contains(payload, []byte("restricted")) {
		t.Fatalf("bad clipboard: %s %v", payload, err)
	}
	// No readable body remains. The export must fail without starting pbcopy
	// and overwriting the previous clipboard payload.
	if err := os.Remove(filepath.Join(root, "good.txt")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := run(cfg, &stdout, &stderr); err == nil || strings.Contains(stderr.String(), "Copied") {
		t.Fatalf("all-inaccessible export reported success: %v %s", err, stderr.String())
	}
	after, err := os.ReadFile(clipboard)
	if err != nil || !bytes.Equal(after, payload) {
		t.Fatalf("all-inaccessible export changed clipboard: %q %v", after, err)
	}
}
