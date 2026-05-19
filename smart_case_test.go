package catclip

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSmartCaseInsensitive(t *testing.T) {
	tests := []struct {
		pattern  string
		expected bool
	}{
		{"todo", true},
		{"TODO", false},
		{"Config", false},
		{"config", true},
		{"handle_click", true},
		{"handleClick", false},
		{"[a-z]+", true},
		{"[A-Z]+", false},
		{"", true},
		{"(?i)foo", true},
		{"(?i)Foo", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got := isSmartCaseInsensitive(tt.pattern)
			if got != tt.expected {
				t.Errorf("isSmartCaseInsensitive(%q) = %v, want %v", tt.pattern, got, tt.expected)
			}
		})
	}
}

func TestSmartCaseIntegration(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	content := `This is a config file.
It contains Config settings.
And some CONFIG keys.
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	runContains := func(pattern string) map[string]struct{} {
		res, err := runRipgrepMatches(pattern, []string{testFile})
		if err != nil {
			t.Fatalf("runRipgrepMatches failed: %v", err)
		}
		return res
	}

	runSnippet := func(pattern string) map[string][]int {
		res, err := runRipgrepMatchLines(pattern, []string{testFile})
		if err != nil {
			t.Fatalf("runRipgrepMatchLines failed: %v", err)
		}
		return res
	}

	// 1. Verify --contains 'config' matches all three (the file itself matches)
	if len(runContains("config")) != 1 {
		t.Errorf("expected 'config' to match test file")
	}

	// 2. Verify --contains 'Config' matches only Config (the file matches)
	if len(runContains("Config")) != 1 {
		t.Errorf("expected 'Config' to match test file")
	}

	// Wait, runContains just returns the files that matched. The test file matched both.
	// We need runSnippet (runRipgrepMatchLines) to verify the *number of lines* matched.
	
	linesConfig := runSnippet("config")
	if len(linesConfig[testFile]) != 3 {
		t.Errorf("expected 'config' to match 3 lines, got %v", linesConfig[testFile])
	}

	linesConfigUpper := runSnippet("Config")
	if len(linesConfigUpper[testFile]) != 1 {
		t.Errorf("expected 'Config' to match 1 line, got %v", linesConfigUpper[testFile])
	}

	linesExplicitIgnore := runSnippet("(?i)Config")
	if len(linesExplicitIgnore[testFile]) != 3 {
		t.Errorf("expected '(?i)Config' to match 3 lines, got %v", linesExplicitIgnore[testFile])
	}
}
