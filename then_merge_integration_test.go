package catclip

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestThenHeadlessMergePayloadAndDiagnostics(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/auth.go":   "alpha\nbravo\ncharlie\n",
		"src/helper.go": "helper\n",
	})
	cases := []struct {
		name                        string
		args                        []string
		code, units                 int
		present, absent, diagnostic string
	}{
		{"different snippets", []string{"src/auth.go", "--snippet", "alpha", "0", "--then", "src/auth.go", "--snippet", "charlie", "0"}, 0, 2, "charlie", "bravo", ""},
		{"snippet and range", []string{"src/auth.go", "--snippet", "alpha", "0", "--then", "src/auth.go", "--lines", "3", "3"}, 0, 2, "charlie", "bravo", ""},
		{"empty snippet with sibling", []string{"src", "--then", "src/auth.go", "--snippet", "NEVER_MATCH"}, 0, 1, "helper", "alpha", "Scope 2 produced no output."},
		{"empty snippet reversed", []string{"src/auth.go", "--snippet", "NEVER_MATCH", "--then", "src"}, 0, 1, "helper", "alpha", "Scope 1 produced no output."},
		{"empty snippet only overlap", []string{"src/auth.go", "--then", "src/auth.go", "--snippet", "NEVER_MATCH"}, 1, 0, "", "alpha", "No output was emitted."},
		{"invalid lines with sibling", []string{"src", "--then", "src/auth.go", "--lines", "99", "100"}, 0, 1, "helper", "alpha", "3 lines"},
		{"empty excerpt does not erase valid excerpt", []string{"src/auth.go", "--snippet", "NEVER_MATCH", "--then", "src/auth.go", "--lines", "3", "3"}, 0, 1, "charlie", "alpha", "Scope 1 produced no output."},
		{"different range orders", []string{"src/auth.go", "--lines", "1", "1", "--then", "src/auth.go", "--lines", "3", "3", "--then", "src/auth.go", "--lines", "1", "1"}, 0, 2, "charlie", "bravo", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--headless", "--print"}, tc.args...)
			cfg := parseInProject(t, project, args)
			var stdout, stderr bytes.Buffer
			err := run(cfg, &stdout, &stderr)
			code := 0
			if err != nil {
				var e exitError
				if !errors.As(err, &e) {
					t.Fatal(err)
				}
				code = e.code
			}
			if code != tc.code || strings.Count(stdout.String(), "<file ") != tc.units {
				t.Fatalf("code=%d output=%s diagnostic=%s", code, stdout.String(), stderr.String())
			}
			if tc.present != "" && !strings.Contains(stdout.String(), tc.present) {
				t.Fatal(stdout.String())
			}
			if tc.absent != "" && strings.Contains(stdout.String(), tc.absent) {
				t.Fatal(stdout.String())
			}
			if tc.diagnostic != "" && !strings.Contains(stderr.String(), tc.diagnostic) {
				t.Fatal(stderr.String())
			}
			if strings.Contains(stdout.String()+stderr.String(), "\x1b") || strings.Contains(stderr.String(), "Proceed?") {
				t.Fatal("headless prompted or colored output")
			}
			if strings.Contains(stdout.String(), "Scope ") || strings.Contains(stdout.String(), "Omitted") {
				t.Fatal("diagnostics in payload")
			}
			if tc.units == 0 && strings.Contains(stderr.String(), "Possible causes:") {
				t.Fatal("generic diagnosis duplicated known refinement outcome")
			}
		})
	}
}
