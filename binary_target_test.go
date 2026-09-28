package catclip

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunExactBinaryOnlyTargetGuidance(t *testing.T) {
	for _, target := range []string{"bin/tool", "bin", "./bin/"} {
		for _, shape := range []string{"", "--paths", "--metadata"} {
			t.Run(target+shape, func(t *testing.T) {
				project := setupTestProject(t, map[string]string{".gitignore": "bin/\n", "bin/tool": "a\x00b"})
				args := []string{target, "--print", "--headless"}
				if shape != "" {
					args = append(args, shape)
				}
				var stdout, stderr bytes.Buffer
				if err := run(parseInProject(t, project, args), &stdout, &stderr); err == nil {
					t.Fatal("expected empty result")
				}
				message := stderr.String()
				if !strings.Contains(message, "Binary files were excluded") || !strings.Contains(message, "--with-binaries") {
					t.Fatalf("missing classification guidance: %s", message)
				}
				for _, bad := range []string{"hidden by", "Name the ignored path", "Possible causes:"} {
					if strings.Contains(message, bad) {
						t.Fatalf("misleading %q in %s", bad, message)
					}
				}
				if shape == "" {
					return
				} // Binary full-body transport has separate sink rules.
				stdout.Reset()
				stderr.Reset()
				args = append(args, "--with-binaries")
				if err := run(parseInProject(t, project, args), &stdout, &stderr); err != nil {
					t.Fatalf("binary override failed: %v\n%s", err, stderr.String())
				}
				if !strings.Contains(stdout.String(), "bin/tool") || strings.Contains(stderr.String(), "Binary files were excluded") {
					t.Fatalf("binary override lost target: stdout=%s stderr=%s", stdout.String(), stderr.String())
				}
			})
		}
	}
}

func TestRunEmptyOrFilteredTargetDoesNotSuggestBinaries(t *testing.T) {
	for _, args := range [][]string{
		{"empty"}, {"text", "--only", "*.rs"}, {"text", "--contains", "absent-pattern"},
		{"text", "--exclude", "*.go"}, {"text", "--snippet", "absent-pattern"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			project := setupTestProject(t, map[string]string{
				".gitignore": "empty/\n", "text/main.go": "package main\n", "text/tool": "a\x00b",
			})
			if err := os.Mkdir(filepath.Join(project, "empty"), 0755); err != nil {
				t.Fatal(err)
			}
			args = append(append([]string(nil), args...), "--headless", "--print")
			var stdout, stderr bytes.Buffer
			if err := run(parseInProject(t, project, args), &stdout, &stderr); err == nil {
				t.Fatal("expected empty result")
			}
			if strings.Contains(stderr.String(), "--with-binaries") || strings.Contains(stderr.String(), "hidden by") {
				t.Fatalf("unrelated binary/ignore advice: %s", stderr.String())
			}
		})
	}
}

func TestRunNoIgnoreBinaryOnlyTargetGuidance(t *testing.T) {
	project := setupTestProject(t, map[string]string{".gitignore": "bin/\n", "bin/tool": "a\x00b"})
	var stdout, stderr bytes.Buffer
	args := []string{"bin", "--no-ignore", "--metadata", "--headless", "--print"}
	if err := run(parseInProject(t, project, args), &stdout, &stderr); err == nil {
		t.Fatal("expected empty text selection")
	}
	if !strings.Contains(stderr.String(), "--with-binaries") || strings.Contains(stderr.String(), "hidden by") {
		t.Fatalf("no-ignore changed diagnostic: %s", stderr.String())
	}
}
