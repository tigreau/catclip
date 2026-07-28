package ui

import (
	"strings"
	"testing"
)

// Three tests that assert against the private startupInputParse.modifiers
// field — moved from root main_test.go during the v0.6.0 internal/ui
// extraction so the tests can read internal state without forcing the
// field public on a UI POD.

func TestParseStartupInputTokensAllowsModifierLikeRegexValues(t *testing.T) {
	parsed, err := parseStartupInputTokens([]string{".", "--contains", "--snippet", "--snippet", "--contains"})
	if err != nil {
		t.Fatalf("parseStartupInputTokens returned error: %v", err)
	}
	if got, want := strings.Join(parsed.modifiers, "\n"), "--contains\n--snippet\n--snippet\n--contains"; got != want {
		t.Fatalf("parsed.modifiers = %q, want %q", got, want)
	}
}

func TestParseStartupInputTokensKeepsSnippetContext(t *testing.T) {
	parsed, err := parseStartupInputTokens([]string{"src", "--snippet", "TODO", "3"})
	if err != nil {
		t.Fatalf("parseStartupInputTokens returned error: %v", err)
	}
	if got, want := strings.Join(parsed.modifiers, "\n"), "--snippet\nTODO\n3"; got != want {
		t.Fatalf("parsed.modifiers = %q, want %q", got, want)
	}
}

func TestParseStartupInputTokensRejectsInvalidIncludeValue(t *testing.T) {
	_, err := parseStartupInputTokens([]string{"--include", "src/../vendor"})
	if err == nil {
		t.Fatal("expected invalid startup include value to fail")
	}
	if !strings.Contains(err.Error(), "--include cannot traverse above the current directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}
