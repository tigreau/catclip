package catclip

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestEmptyGitFilteredMessageDoesNotClaimCleanRepository(t *testing.T) {
	project := setupTestProject(t, map[string]string{"main.go": "package main\n"})
	initGitRepo(t, project)
	writeProjectFile(t, project, "main.go", "package main\n// modified\n")
	cfg := parseInProject(t, project, []string{"--headless", "--print", ".", "--unstaged-diff", "--only", "*.absent"})
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	var exitErr exitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 || stdout.Len() != 0 {
		t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	message := stderr.String()
	for _, want := range []string{"No files matched the requested targets and filters.", "Git selection requested: --unstaged", "git status --short", "--all-ignore-rules", "(.hiss + .gitignore)", "--hiss"} {
		if !strings.Contains(message, want) {
			t.Fatalf("missing %q: %s", want, message)
		}
	}
	for _, wrong := range []string{"No tracked files have uncommitted modifications", "Run without", "\x1b"} {
		if strings.Contains(message, wrong) {
			t.Fatalf("misleading or colored headless output: %s", message)
		}
	}
}
