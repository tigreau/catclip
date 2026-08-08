package ui

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
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

func TestStartupValidationUsesCanonicalCLIMessages(t *testing.T) {
	_, err := parseStartupInputTokens([]string{"src", "--snippet", "TODO", "201"})
	if err == nil {
		t.Fatal("invalid snippet context succeeded")
	}
	if want := cli.ValidateSnippetContext(201).Error(); err.Error() != want {
		t.Fatalf("snippet message mismatch\n got: %q\nwant: %q", err.Error(), want)
	}

	_, err = parseStartupInputTokens([]string{"src", "--wat"})
	if err == nil {
		t.Fatal("unknown startup option succeeded")
	}
	if want := cli.UnknownOptionError("--wat").Error(); err.Error() != want {
		t.Fatalf("unknown-option message mismatch\n got: %q\nwant: %q", err.Error(), want)
	}

	_, _, _, err = nextStartupInteractiveFrame(nil, []string{"src", "--changed"}, []string{"--wat"})
	if err == nil {
		t.Fatal("unknown undo/replay option succeeded")
	}
	if want := cli.UnknownOptionError("--wat").Error(); err.Error() != want {
		t.Fatalf("undo/replay message mismatch\n got: %q\nwant: %q", err.Error(), want)
	}
}

func TestTargetBoundaryValidationParityAcrossStartupAndDiscovery(t *testing.T) {
	abs := filepath.Join(string(filepath.Separator), "tmp", "project")
	if runtime.GOOS == "windows" {
		abs = `C:\project`
	}
	for _, target := range []string{abs, "src/../vendor"} {
		want := discovery.ValidateTargetBoundary(target)
		if want == nil {
			t.Fatalf("test target %q unexpectedly passed canonical validation", target)
		}
		usePicker, err := shouldUseStartupPicker([]string{target})
		if err == nil {
			t.Fatalf("startup preflight accepted %q", target)
		}
		if usePicker {
			t.Fatalf("startup preflight requested picker for invalid target %q", target)
		}
		if err.Error() != want.Error() {
			t.Fatalf("target %q message mismatch\n got: %q\nwant: %q", target, err.Error(), want.Error())
		}
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
