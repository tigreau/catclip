package catclip

import (
	"bytes"
	"strings"
	"testing"
)

func TestDirectBinaryInclusionAndContentDiagnostics(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore": "hidden/\n", "hidden/tool": "needle\n" + strings.Repeat("padding\n", 30000) + "\x00",
		"hidden/tool.jar": "PK\x00needle", "text.txt": "needle\n", "empty.jar": "",
	})
	for _, target := range []string{"hidden/tool", "hidden/tool.jar", "empty.jar"} {
		var stdout, stderr bytes.Buffer
		if err := run(parseInProject(t, project, []string{target, "--paths", "--headless", "--print"}), &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if stdout.String() != target+"\n" || strings.Contains(stderr.String(), "excluded") {
			t.Fatalf("%s: out=%q err=%q", target, stdout.String(), stderr.String())
		}
	}
	for _, flag := range []string{"--snippet", "--contains", "--not-contains"} {
		for _, binaryFlag := range []bool{false, true} {
			args := []string{"hidden/tool"}
			if binaryFlag {
				args = append(args, "-b")
			}
			args = append(args, flag, "needle", "--headless", "--print")
			var stdout, stderr bytes.Buffer
			if err := run(parseInProject(t, project, args), &stdout, &stderr); err == nil {
				t.Fatal("expected empty content result")
			}
			message := stderr.String()
			for _, want := range []string{"No text files available for " + flag, "'hidden/tool' is binary", "Remove " + flag + " and its pattern"} {
				if !strings.Contains(message, want) {
					t.Fatalf("missing %q in %s", want, message)
				}
			}
			if stdout.Len() != 0 || strings.Contains(message, "Possible causes") || strings.Contains(message, "--hiss") || strings.Contains(message, "\x1b") {
				t.Fatalf("misleading output: %q %q", stdout.String(), message)
			}
		}
		var stdout, stderr bytes.Buffer
		// A valid text sibling keeps producing output, with no per-binary spam.
		pattern := "needle"
		if flag == "--not-contains" {
			pattern = "absent"
		}
		args := []string{"hidden/tool", "text.txt", flag, pattern, "--headless", "--print"}
		if err := run(parseInProject(t, project, args), &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "text.txt") || strings.Contains(stdout.String(), "hidden/tool") || stderr.Len() != 0 {
			t.Fatalf("mixed: %q %q", stdout.String(), stderr.String())
		}
	}
}

func TestBinaryContentFilterBeforeMetadata(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"hit.txt": "needle\n", "miss.txt": "other\n", "blob.fixture": "opaque\x00bytes",
	})
	for _, flag := range []string{"--contains", "--not-contains"} {
		var stdout, stderr bytes.Buffer
		args := []string{"hit.txt", "miss.txt", "blob.fixture", "-b", flag, "needle", "--metadata", "--headless"}
		if err := run(parseInProject(t, project, args), &stdout, &stderr); err != nil {
			t.Fatalf("%s: %v: %s", flag, err, stderr.String())
		}
		want, absent := "hit.txt", "miss.txt"
		if flag == "--not-contains" {
			want, absent = absent, want
		}
		// The scope header intentionally retains all requested targets; only
		// the selected-file table must exclude nonmatching and binary files.
		table := strings.SplitN(stdout.String(), "\nPATH", 2)
		if len(table) != 2 || !strings.Contains(table[1], want) || strings.Contains(table[1], absent) || strings.Contains(table[1], "blob.fixture") || strings.Contains(stdout.String(), "\x1b") {
			t.Fatalf("%s metadata: %s", flag, stdout.String())
		}
	}
}

func TestBinaryOnlyDiagnosticFollowsOrderedFilters(t *testing.T) {
	project := setupTestProject(t, map[string]string{"tool.jar": "PK\x00", "text.txt": "needle\n"})
	for _, tc := range []struct {
		args   []string
		binary bool
	}{
		{[]string{".", "-b", "--only", "*.jar", "--snippet", "needle"}, true},
		{[]string{".", "-b", "--only", "missing", "--snippet", "needle"}, false},
		{[]string{".", "-b", "--snippet", "absent"}, false},
	} {
		var stdout, stderr bytes.Buffer
		args := append(tc.args, "--headless", "--print")
		if err := run(parseInProject(t, project, args), &stdout, &stderr); err == nil {
			t.Fatal("expected empty result")
		}
		if strings.Contains(stderr.String(), "searches text files only") != tc.binary {
			t.Fatalf("%v: %s", args, stderr.String())
		}
	}
}
