package catclip

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupIgnoredTargetXDG(t *testing.T, hiss string) {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	path := filepath.Join(config, "catclip", ".hiss")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(hiss), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runIgnoredTarget(t *testing.T, project string, args ...string) (string, string, error) {
	t.Helper()
	cfg := parseInProject(t, project, append([]string{"--print"}, args...))
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func TestExactGitIgnoredFileIsDirectTarget(t *testing.T) {
	setupIgnoredTargetXDG(t, "")
	project := setupTestProject(t, map[string]string{
		".gitignore":              "src/generated/\n",
		"src/generated/client.ts": "export const generated = true\n",
		"src/visible.ts":          "export const visible = true\n",
	})

	stdout, _, err := runIgnoredTarget(t, project, "src/generated/client.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "export const generated") || strings.Contains(stdout, "src/visible.ts") {
		t.Fatalf("exact ignored file output:\n%s", stdout)
	}
}

func TestExactGitIgnoredDirectoryPreservesNestedIgnoreRules(t *testing.T) {
	setupIgnoredTargetXDG(t, "")
	project := setupTestProject(t, map[string]string{
		".gitignore":              "ignored/\n",
		"ignored/.gitignore":      "nested/\n",
		"ignored/keep.txt":        "keep\n",
		"ignored/nested/drop.txt": "drop\n",
	})

	stdout, _, err := runIgnoredTarget(t, project, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "ignored/keep.txt") || strings.Contains(stdout, "ignored/nested/drop.txt") {
		t.Fatalf("exact ignored directory did not preserve nested rules:\n%s", stdout)
	}
}

func TestExactHissIgnoredTargetIsDirectTarget(t *testing.T) {
	setupIgnoredTargetXDG(t, "private/\n")
	project := setupTestProject(t, map[string]string{
		"private/secret.txt": "secret\n",
		"public/readme.txt":  "public\n",
	})

	stdout, _, err := runIgnoredTarget(t, project, "private/secret.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "secret") || strings.Contains(stdout, "public/readme.txt") {
		t.Fatalf("exact .hiss target output:\n%s", stdout)
	}
}

func TestExactHissIgnoredDirectoryIsDirectTarget(t *testing.T) {
	setupIgnoredTargetXDG(t, "private/\n")
	project := setupTestProject(t, map[string]string{
		"private/secret.txt": "secret\n",
		"public/readme.txt":  "public\n",
	})

	stdout, _, err := runIgnoredTarget(t, project, "private")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "private/secret.txt") || strings.Contains(stdout, "public/readme.txt") {
		t.Fatalf("exact .hiss directory output:\n%s", stdout)
	}
}

func TestExactIgnoredDirectoryNoIgnoreDisablesNestedRules(t *testing.T) {
	setupIgnoredTargetXDG(t, "")
	project := setupTestProject(t, map[string]string{
		".gitignore":              "ignored/\n",
		"ignored/.gitignore":      "nested/\n",
		"ignored/keep.txt":        "keep\n",
		"ignored/nested/drop.txt": "drop\n",
	})

	stdout, _, err := runIgnoredTarget(t, project, "ignored", "--no-ignore")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ignored/keep.txt", "ignored/nested/drop.txt"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("no-ignore exact directory missing %q:\n%s", want, stdout)
		}
	}
}

func TestOnlyFilterDoesNotAuthorizeIgnoredTraversal(t *testing.T) {
	setupIgnoredTargetXDG(t, "")
	project := setupTestProject(t, map[string]string{
		".gitignore":              "src/generated/\n",
		"src/main.ts":             "main\n",
		"src/generated/client.ts": "generated\n",
	})

	stdout, _, _ := runIgnoredTarget(t, project, ".", "--only", "src/generated/")
	if strings.Contains(stdout, "src/generated/client.ts") {
		t.Fatalf("--only authorized an ignored path:\n%s", stdout)
	}
}

func TestVisibleParentAndExactIgnoredChildBothSurvive(t *testing.T) {
	setupIgnoredTargetXDG(t, "")
	project := setupTestProject(t, map[string]string{
		".gitignore":              "src/generated/\n",
		"src/main.ts":             "main\n",
		"src/generated/client.ts": "generated\n",
	})

	stdout, _, err := runIgnoredTarget(t, project, "src", "src/generated/client.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"src/main.ts", "src/generated/client.ts"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("combined exact targets missing %q:\n%s", want, stdout)
		}
	}
}

func TestIgnoredBasenameStillTeachesCompletePath(t *testing.T) {
	setupIgnoredTargetXDG(t, "")
	project := setupTestProject(t, map[string]string{
		".gitignore":        "blocked/\n",
		"blocked/target.md": "hidden\n",
		"visible/keepit.go": "visible\n",
	})

	_, stderr, err := runIgnoredTarget(t, project, "--headless", "target.md")
	if err == nil {
		t.Fatal("expected ignored basename to require a complete path")
	}
	for _, want := range []string{"hidden by an ignored ancestor", "catclip 'blocked/target.md'", "catclip . --no-ignore"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("ignored basename diagnostic missing %q:\n%s", want, stderr)
		}
	}
}
