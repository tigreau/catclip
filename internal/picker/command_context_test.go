package picker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCommandContextNativeHelper(t *testing.T) {
	if os.Getenv("CATCLIP_TEST_CONTEXT_CHILD") != "1" {
		return
	}
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	args, err := RestoreCommandContext([]string{commandContextFlag, "--internal-tree-preview", "src/relative.go"})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.Getwd()
	data, err := os.ReadFile(SelectionFilePath("./fzf-temp-test"))
	if err != nil {
		t.Fatal(err)
	}
	project, err := os.ReadFile("project.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := struct {
		Before, After, Selection, Project string
		Args                              []string
		Env                               map[string]*string
	}{before, after, string(data), string(project), args, map[string]*string{}}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if value, ok := os.LookupEnv(key); ok {
			out.Env[key] = &value
		} else {
			out.Env[key] = nil
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestPrepareCommandRestoresProjectAndTempSettings(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "project.txt"), []byte("project data"), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestCommandContextNativeHelper$")
	cmd.Dir = project
	cmd.Env = withCommandEnv(os.Environ(), "CATCLIP_TEST_CONTEXT_CHILD", "1")
	// Preserve an unset value, an explicitly empty value, and a relative value.
	filtered := cmd.Env[:0]
	for _, pair := range cmd.Env {
		key, _, _ := strings.Cut(pair, "=")
		if !strings.EqualFold(key, "TMPDIR") && !strings.EqualFold(key, "TMP") && !strings.EqualFold(key, "TEMP") {
			filtered = append(filtered, pair)
		}
	}
	cmd.Env = append(filtered, "TMP=", "TEMP=relative user temp")
	originalArgs := append([]string(nil), cmd.Args...)
	originalCwd, _ := os.Getwd()
	originalEnv := os.Environ()
	cleanup, err := PrepareCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if after, _ := os.Getwd(); after != originalCwd || !reflect.DeepEqual(originalEnv, os.Environ()) {
		t.Fatal("preparing a child changed parent cwd/environment")
	}
	if !containsArgPair(cmd.Args, "--with-shell", "") {
		t.Fatal("automatic fzf executor was not pinned")
	}
	if err := os.WriteFile(filepath.Join(cmd.Dir, "fzf-temp-test"), []byte("exact selection"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd.Args = originalArgs // helper is Go, not fzf
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v: %s", err, data)
	}
	var got struct {
		Before, After, Selection, Project string
		Args                              []string
		Env                               map[string]*string
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	for _, paths := range [][2]string{{got.Before, cmd.Dir}, {got.After, project}} {
		a, err := os.Stat(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.Stat(paths[1])
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(a, b) {
			t.Fatalf("cwd mismatch: %q", paths)
		}
	}
	if got.Selection != "exact selection" || got.Project != "project data" {
		t.Fatalf("path ownership changed: %+v", got)
	}
	if !reflect.DeepEqual(got.Args, []string{"--internal-tree-preview", "src/relative.go"}) {
		t.Fatalf("project args changed: %q", got.Args)
	}
	if got.Env["TMPDIR"] != nil || got.Env["TMP"] == nil || *got.Env["TMP"] != "" || got.Env["TEMP"] == nil || *got.Env["TEMP"] != "relative user temp" {
		t.Fatalf("temp environment not restored: %+v", got.Env)
	}
	cleanup()
	if _, err := os.Stat(cmd.Dir); !os.IsNotExist(err) {
		t.Fatalf("session directory leaked: %v", err)
	}
}

func TestRestoreCommandContextRequiresExplicitMarker(t *testing.T) {
	t.Setenv(commandContextEnv, "invalid JSON")
	args := []string{"--print", "."}
	got, err := RestoreCommandContext(args)
	if err != nil || !reflect.DeepEqual(got, args) {
		t.Fatalf("public command was changed: %q %v", got, err)
	}
	if _, err := RestoreCommandContext([]string{commandContextFlag}); err == nil {
		t.Fatal("accepted broken internal context")
	}
	if got := SelectionFilePath("relative.txt"); got != "relative.txt" {
		t.Fatal(got)
	}
}
