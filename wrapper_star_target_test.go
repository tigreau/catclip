package catclip

import (
	"bytes"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRunWrapperStarTargetsAreDeterministicFileGlobs(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"utils/format.ts":                          "export const format = 1\n",
		"todos/utils/root.ts":                      "export const root = 1\n",
		"src/direct.ts":                            "export const direct = 1\n",
		"src/utils.ts":                             "export const value = 1\n",
		"src/utils/api.ts":                         "export const api = 1\n",
		"src/lib/utils":                            "extensionless utility\n",
		"src/components/utils/classNames.ts":       "export const names = 1\n",
		"src/features/file-leaf/utils":             "extensionless feature utility\n",
		"src/features/todos/utils/dateUtils.ts":    "export const date = 1\n",
		"matcher-cases/generated.go/inside.txt":    "not Go source\n",
		"matcher-cases/generated.go/deeper/log.md": "generated details\n",
	})
	// A glob target must never need fzf, even when matching directory names
	// also exist and the corresponding plain query would be ambiguous.
	t.Setenv("CATCLIP_FZF", filepath.Join(project, "missing-fzf"))

	assertTargetPaths(t, project, "util*", []string{
		"src/features/file-leaf/utils",
		"src/lib/utils",
		"src/utils.ts",
	})
	assertTargetPaths(t, project, "*util*", []string{
		"src/features/file-leaf/utils",
		"src/lib/utils",
		"src/utils.ts",
	})
	assertTargetPaths(t, project, "*utils*", []string{
		"src/features/file-leaf/utils",
		"src/lib/utils",
		"src/utils.ts",
	})
	assertTargetPaths(t, project, "*/utils/*", []string{
		"src/utils/api.ts",
		"todos/utils/root.ts",
	})
	assertTargetPaths(t, project, "src/*", []string{
		"src/direct.ts",
		"src/utils.ts",
	})
	assertTargetPaths(t, project, "matcher-cases/*.go", nil)
	assertTargetPaths(t, project, "matcher-cases/generated.go", []string{
		"matcher-cases/generated.go/deeper/log.md",
		"matcher-cases/generated.go/inside.txt",
	})
	assertTargetPaths(t, project, ".", []string{
		"src/components/utils/classNames.ts",
		"src/features/todos/utils/dateUtils.ts",
		"src/utils/api.ts",
		"todos/utils/root.ts",
		"utils/format.ts",
	}, "--only", "utils/")
}

func TestRunWrapperStarZeroMatchStaysGlobAndOffersPlainFuzzyMigration(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/layout/Header.tsx": "export const Header = 1\n",
	})

	cfg := parseInProject(t, project, []string{"*layout/Footer*", "--headless", "--print", "--paths"})
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected zero-match wrapper glob to return a partial/no-results error")
	}
	message := stderr.String()
	for _, want := range []string{
		"No files matched '*layout/Footer*'",
		"Target globs select files",
		"For fuzzy file and folder navigation:",
		"catclip layout/Footer",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("zero-match wrapper diagnostic missing %q:\n%s", want, message)
		}
	}
	for _, unwanted := range []string{"Searching for", "Directory shorthand is resolved by fzf", "catclip /layout/Footer"} {
		if strings.Contains(message, unwanted) {
			t.Fatalf("zero-match wrapper diagnostic contains obsolete or unsafe text %q:\n%s", unwanted, message)
		}
	}
}

func TestRunSlashlessGlobZeroMatchDoesNotUsePlainTargetVocabulary(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main_test.go": "package main\n",
	})

	cfg := parseInProject(t, project, []string{"*_test", "--headless", "--print", "--paths"})
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected anchored slashless glob to match no files")
	}
	message := stderr.String()
	for _, want := range []string{"No files matched '*_test'", "Target globs match complete filenames"} {
		if !strings.Contains(message, want) {
			t.Fatalf("slashless glob diagnostic missing %q:\n%s", want, message)
		}
	}
	for _, unwanted := range []string{"No file or directory", "Directory shorthand", "fzf", "Searching for"} {
		if strings.Contains(message, unwanted) {
			t.Fatalf("slashless glob diagnostic contains plain-target vocabulary %q:\n%s", unwanted, message)
		}
	}
}

func TestRunRejectsWrapperDoublestarWithTwoTruthfulMigrations(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/auth.ts": "export const auth = 1\n",
	})

	err := parseErrorInProject(t, project, []string{"**auth**", "--headless", "--print"})
	if err == nil {
		t.Fatal("expected positional doublestar rejection")
	}
	for _, want := range []string{
		"Positional target patterns do not support '**'",
		"Use a directory target plus --only",
		`catclip . --only '*auth*'`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("doublestar migration error missing %q:\n%s", want, err)
		}
	}
}

func assertTargetPaths(t *testing.T, project, target string, want []string, extra ...string) {
	t.Helper()
	args := []string{target}
	args = append(args, extra...)
	args = append(args, "--headless", "--quiet", "--print", "--paths")
	cfg := parseInProject(t, project, args)
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if len(want) == 0 {
		if err == nil {
			t.Fatalf("target %q unexpectedly matched:\n%s", target, stdout.String())
		}
		if strings.TrimSpace(stdout.String()) != "" {
			t.Fatalf("target %q emitted paths on zero match:\n%s", target, stdout.String())
		}
		return
	}
	if err != nil {
		t.Fatalf("target %q failed: %v\nstderr:\n%s", target, err, stderr.String())
	}
	got := strings.Fields(stdout.String())
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("target %q paths mismatch\nwant:\n%s\ngot:\n%s", target, strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}
