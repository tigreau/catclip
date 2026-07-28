package catclip

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHeadlessFuzzyTargetRejectsMixedFileAndDirectoryCandidates(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/components/ui/Button.tsx":           "export const Button = 1\n",
		"matcher-cases/beta/leaf-token/beta.txt": "fixture\n",
	})

	cfg := parseInProject(t, project, []string{"btn", "--headless", "--quiet", "--print", "--paths"})
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected mixed fuzzy ambiguity")
	}
	if stdout.Len() != 0 {
		t.Fatalf("mixed ambiguity must not emit a guessed target, got:\n%s", stdout.String())
	}
	for _, want := range []string{
		"Multiple files and directories match 'btn'",
		"[file] src/components/ui/Button.tsx",
		"[dir] matcher-cases/beta/leaf-token",
		"[file] matcher-cases/beta/leaf-token/beta.txt",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("mixed ambiguity missing %q:\n%s", want, err)
		}
	}
}

func TestRunHeadlessFileLookingFuzzyTargetStillChecksDirectories(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/Button.tsx": "export const Button = 1\n",
		"matcher-cases/Button.ts-cache/inside.txt": "fixture\n",
	})

	cfg := parseInProject(t, project, []string{"Button.ts", "--headless", "--quiet", "--print", "--paths"})
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected file-looking query to report mixed fuzzy ambiguity")
	}
	if stdout.Len() != 0 {
		t.Fatalf("mixed ambiguity must not emit a guessed file, got:\n%s", stdout.String())
	}
	for _, want := range []string{
		"Multiple files and directories match 'Button.ts'",
		"[file] src/Button.tsx",
		"[dir] matcher-cases/Button.ts-cache",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("file-looking mixed ambiguity missing %q:\nerror: %q\nstderr:\n%s", want, err.Error(), stderr.String())
		}
	}
}

func TestRunHeadlessSingleCombinedFuzzyCandidateStillResolves(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/Button.tsx": "export const Button = 1\n",
		"src/Card.tsx":   "export const Card = 1\n",
	})

	cfg := parseInProject(t, project, []string{"btn", "--headless", "--quiet", "--print", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("expected one combined fuzzy candidate to resolve: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "src/Button.tsx" {
		t.Fatalf("resolved paths = %q, want src/Button.tsx", got)
	}
}

func TestRunHeadlessExactTargetsStillBeatFuzzyCandidates(t *testing.T) {
	t.Run("exact path", func(t *testing.T) {
		project := setupTestProject(t, map[string]string{
			"Button.tsx":                      "root\n",
			"src/Button.tsx-cache/inside.txt": "fixture\n",
		})

		cfg := parseInProject(t, project, []string{"Button.tsx", "--headless", "--quiet", "--print", "--paths"})
		var stdout, stderr bytes.Buffer
		if err := run(cfg, &stdout, &stderr); err != nil {
			t.Fatalf("expected exact path to resolve: %v", err)
		}
		if got := strings.TrimSpace(stdout.String()); got != "Button.tsx" {
			t.Fatalf("resolved paths = %q, want Button.tsx", got)
		}
	})

	t.Run("unique exact directory basename", func(t *testing.T) {
		project := setupTestProject(t, map[string]string{
			"src/features/authentication/auth.ts": "export const auth = 1\n",
			"docs/authentication-guide.md":        "guide\n",
		})

		cfg := parseInProject(t, project, []string{"authentication", "--headless", "--quiet", "--print", "--paths"})
		var stdout, stderr bytes.Buffer
		if err := run(cfg, &stdout, &stderr); err != nil {
			t.Fatalf("expected exact directory basename to resolve: %v", err)
		}
		if got := strings.TrimSpace(stdout.String()); got != "src/features/authentication/auth.ts" {
			t.Fatalf("resolved paths = %q, want authentication subtree", got)
		}
	})
}
