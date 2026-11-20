package catclip

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestScopeReportDiagnosticsAndResolvedOrder(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/a.go": "package a\n// KEEP\n", "src/b.go": "package b\n"})
	for _, headless := range []bool{false, true} {
		args := []string{"src/a.go", "--then", "missing (scope 2)", "--only-tree"}
		if headless {
			args = append(args, "--headless")
		}
		cfg := parseInProject(t, project, args)
		var out, diag bytes.Buffer
		if err := run(cfg, &out, &diag); err != nil {
			var e exitError
			if !errors.As(err, &e) || e.code != 1 {
				t.Fatal(err)
			}
		}
		if !strings.Contains(out.String(), "Scope 1:") || !strings.Contains(out.String(), "Scope 2:") {
			t.Fatal(out.String())
		}
		if headless {
			if !strings.Contains(diag.String(), "(scope 2)") || strings.Contains(out.String(), "Warning:") {
				t.Fatalf("%s / %s", &out, &diag)
			}
		} else {
			if strings.Contains(diag.String(), "No file or directory") || !strings.Contains(out.String(), "'missing (scope 2)' found.") {
				t.Fatalf("%s / %s", &out, &diag)
			}
		}
	}
	for _, extra := range [][]string{{"--only-tree"}, {"--only-tree", "--metadata"}, {"--headless"}, {"--only-tree", "--only", "*.absent"}} {
		args := append([]string{"src"}, extra...)
		cfg := parseInProject(t, project, args)
		var combined bytes.Buffer
		once := resolvedCommandOnce(&combined, args, true)
		err := runWithReportFromParsedCommand(cfg, &combined, &combined, nil, once)
		if fallbackErr := once(); fallbackErr != nil {
			t.Fatal(fallbackErr)
		}
		if err != nil {
			fmt.Fprintln(&combined, err)
		}
		text := combined.String()
		if strings.Count(text, "Resolved command:") != 1 {
			t.Fatal(text)
		}
		if idx := strings.Index(text, "Tokens:"); idx >= strings.Index(text, "Resolved command:") {
			t.Fatal(text)
		}
		if idx := strings.Index(text, "No text files found"); idx >= 0 && idx < strings.Index(text, "Resolved command:") {
			t.Fatal(text)
		}
	}
}
