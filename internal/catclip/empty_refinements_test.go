package catclip

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRunEmptyRefinementContract(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/auth.txt":  "PRIVATE_FULL_AUTH\n",
		"src/keep.txt":  "KEEP_SIBLING\n",
		"src/sub/a.txt": "MATCH_ME\n",
		"src/sub/b.txt": "PRIVATE_FULL_B\n",
	})
	initGitRepo(t, project)
	for _, tc := range []struct {
		name    string
		args    []string
		omitted []string
		present []string
		notice  string
		code    int
	}{
		{"snippet", []string{"src", "--then", "src/auth.txt", "--snippet", "ABSENT"}, []string{"PRIVATE_FULL_AUTH"}, []string{"KEEP_SIBLING"}, "Scope 2 produced no output.\nFull files omitted:\n  src/auth.txt", 0},
		{"reverse", []string{"src/auth.txt", "--snippet", "ABSENT", "--then", "src"}, []string{"PRIVATE_FULL_AUTH"}, []string{"KEEP_SIBLING"}, "Scope 1 produced no output.\nFull files omitted:\n  src/auth.txt", 0},
		{"subdirectory", []string{"src", "--then", "src/sub", "--snippet", "ABSENT"}, []string{"PRIVATE_FULL_B", "MATCH_ME"}, []string{"KEEP_SIBLING"}, "Full files omitted:\n  src/sub/a.txt\n  src/sub/b.txt", 0},
		{"partial snippet", []string{"src", "--then", "src/sub", "--snippet", "MATCH_ME"}, []string{"PRIVATE_FULL_B"}, []string{"KEEP_SIBLING", "MATCH_ME"}, "", 0},
		{"prior filter", []string{"src", "--then", "src/sub", "--only", "a.txt", "--snippet", "ABSENT"}, []string{"MATCH_ME"}, []string{"PRIVATE_FULL_B", "KEEP_SIBLING"}, "Full files omitted:\n  src/sub/a.txt", 0},
		{"prior filter empty", []string{"src", "--then", "src/sub", "--only", "*.missing", "--snippet", "ABSENT"}, nil, []string{"MATCH_ME", "PRIVATE_FULL_B", "KEEP_SIBLING"}, "", 0},
		{"later filter", []string{"src", "--then", "src/sub", "--snippet", "ABSENT", "--only", "a.txt"}, []string{"MATCH_ME", "PRIVATE_FULL_B"}, []string{"KEEP_SIBLING"}, "Full files omitted:\n  src/sub/a.txt\n  src/sub/b.txt", 0},
		{"diff", []string{"src", "--then", "src/auth.txt", "--changed-diff"}, []string{"PRIVATE_FULL_AUTH"}, []string{"KEEP_SIBLING"}, "Full files omitted:\n  src/auth.txt", 0},
		{"diff reverse", []string{"src/auth.txt", "--changed-diff", "--then", "src"}, []string{"PRIVATE_FULL_AUTH"}, []string{"KEEP_SIBLING"}, "Scope 1 produced no output.", 0},
		{"staged", []string{"src", "--then", "src/auth.txt", "--staged-diff"}, []string{"PRIVATE_FULL_AUTH"}, []string{"KEEP_SIBLING"}, "Full files omitted:\n  src/auth.txt", 0},
		{"unstaged", []string{"src", "--then", "src/auth.txt", "--unstaged-diff"}, []string{"PRIVATE_FULL_AUTH"}, []string{"KEEP_SIBLING"}, "Full files omitted:\n  src/auth.txt", 0},
		{"paths", []string{"src", "--paths", "--then", "src/auth.txt", "--snippet", "ABSENT"}, []string{"PRIVATE_FULL_AUTH"}, []string{"src/auth.txt"}, "Scope 2 produced no output.", 0},
		{"paths and full", []string{"src", "--paths", "--then", "src", "--then", "src/auth.txt", "--snippet", "ABSENT"}, []string{"PRIVATE_FULL_AUTH"}, []string{"src/auth.txt", "KEEP_SIBLING"}, "Scope 3 produced no output.\nFull files omitted:\n  src/auth.txt", 0},
		{"empty", []string{"src/auth.txt", "--then", "src/auth.txt", "--snippet", "ABSENT"}, []string{"PRIVATE_FULL_AUTH"}, nil, "Full files omitted:\n  src/auth.txt", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parseInProject(t, project, append([]string{"--headless", "--print"}, tc.args...))
			var out, diag bytes.Buffer
			err := run(cfg, &out, &diag)
			code := 0
			if err != nil {
				var e exitError
				if !errors.As(err, &e) {
					t.Fatal(err)
				}
				code = e.code
			}
			if code != tc.code {
				t.Fatalf("code=%d want %d; %s", code, tc.code, &diag)
			}
			for _, s := range tc.omitted {
				if strings.Contains(out.String(), s) {
					t.Fatalf("full-body fallback: %s", &out)
				}
			}
			for _, s := range tc.present {
				if !strings.Contains(out.String(), s) {
					t.Fatalf("missing %s: %s", s, &out)
				}
			}
			if !strings.Contains(diag.String(), tc.notice) {
				t.Fatalf("missing notice %q: %s", tc.notice, &diag)
			}
			if tc.notice == "" && strings.Contains(diag.String(), "produced no output") {
				t.Fatalf("partial scope warning: %s", &diag)
			}
			if strings.Contains(out.String(), "omitted") || strings.Contains(diag.String(), "\x1b") {
				t.Fatalf("diagnostic leaked or colored: out=%s stderr=%q", &out, diag.String())
			}
			if tc.code == 1 && out.Len() != 0 {
				t.Fatal("empty request emitted output")
			}
		})
	}
}

func TestRunEmptyRefinementInvalidRegexStillErrors(t *testing.T) {
	project := setupTestProject(t, map[string]string{"a.txt": "body\n"})
	var out, diag bytes.Buffer
	err := run(parseInProject(t, project, []string{".", "--then", "a.txt", "--snippet", "[", "--headless", "--print"}), &out, &diag)
	if err == nil || out.Len() != 0 || strings.Contains(diag.String(), "produced no output") {
		t.Fatalf("err=%v stdout=%q stderr=%q", err, out.String(), diag.String())
	}
}

func TestRunEmptyRefinementPartlyChangedDirectory(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/clean.txt": "NO_FULL_FALLBACK\n", "src/changed.txt": "before\n", "keep.txt": "KEEP\n"})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/changed.txt", "after\n")
	var out, diag bytes.Buffer
	err := run(parseInProject(t, project, []string{".", "--then", "src", "--unstaged-diff", "--headless", "--print"}), &out, &diag)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "NO_FULL_FALLBACK") || !strings.Contains(out.String(), "+after") || !strings.Contains(out.String(), "KEEP") || strings.Contains(diag.String(), "produced no output") {
		t.Fatalf("stdout=%s stderr=%s", &out, &diag)
	}
}

func TestRunEmptyRefinementNoticeBound(t *testing.T) {
	for _, n := range []int{1, 5, 10, 15, 100} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			files := map[string]string{"keep.txt": "KEEP\n"}
			for i := 0; i < n; i++ {
				files[fmt.Sprintf("sub/%03d.txt", i)] = "omit\n"
			}
			project := setupTestProject(t, files)
			var out, diag bytes.Buffer
			if err := run(parseInProject(t, project, []string{".", "--then", "sub", "--snippet", "ABSENT", "--headless", "--print"}), &out, &diag); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "omit") || !strings.Contains(out.String(), "KEEP") {
				t.Fatal(out.String())
			}
			if n <= 5 {
				for i := 0; i < n; i++ {
					if !strings.Contains(diag.String(), fmt.Sprintf("\n  sub/%03d.txt", i)) {
						t.Fatal(diag.String())
					}
				}
			} else if !strings.Contains(diag.String(), fmt.Sprintf("%d full files omitted.", n)) || strings.Contains(diag.String(), "sub/") {
				t.Fatal(diag.String())
			}
		})
	}
}

func TestRunEmptyRefinementUntrackedDiff(t *testing.T) {
	project := setupTestProject(t, map[string]string{"tracked.txt": "tracked\n"})
	initGitRepo(t, project)
	writeProjectFile(t, project, "new.txt", "UNTRACKED_BODY\n")
	var out, diag bytes.Buffer
	err := run(parseInProject(t, project, []string{".", "--then", "new.txt", "--changed-diff", "--headless", "--print"}), &out, &diag)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "UNTRACKED_BODY") != 1 || strings.Contains(diag.String(), "omitted") {
		t.Fatalf("out=%s stderr=%s", &out, &diag)
	}
}
