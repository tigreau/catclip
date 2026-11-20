package ui

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

// The transient preview must not claim that cached search work cannot recur,
// or that no matches exist while the completed result's preview is loading.
func TestBuildInternalSearchingHintDocument(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderTreeDocument(&buf, buildInternalSearchingHintDocument(goos), FzfFilterTreeRenderOptions(), platform.ANSIPalette()); err != nil {
				t.Fatal(err)
			}
			text := string(ansiEscape.ReplaceAll(buf.Bytes(), nil))
			if !strings.Contains(text, "Searching file contents") {
				t.Fatalf("missing search progress: %q", text)
			}
			if strings.Contains(text, "Microsoft Defender may inspect files") != (goos == "windows") {
				t.Fatalf("wrong platform explanation: %q", text)
			}
			for _, misleading := range []string{"no matches", "reboot", "~30s", "will not recur"} {
				if strings.Contains(text, misleading) {
					t.Fatalf("search progress contains %q: %q", misleading, text)
				}
			}
		})
	}
}

// When the reload for the current pattern has finished with no results (its
// memo holds this pattern and no paths), the preview says "No matches" (or,
// for --not-contains, that every file matches) instead of "Searching file
// contents..."; while the memo still holds an older pattern, it is searching
// (user, 2026-11-20).
func TestFinishedSearchWithoutRowsSaysNoMatches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scope command.ExecutionScope
		want  string
	}{
		{"snippet", command.ExecutionScope{Stages: []command.Stage{{Kind: command.StageSnippet, Values: []string{"zzz"}}}, Snippet: true, SnippetPattern: "zzz"}, "No line in the selected files matches"},
		{"contains", command.ExecutionScope{Contains: "zzz", Stages: []command.Stage{{Kind: command.StageContains, Values: []string{"zzz"}}}}, "No line in the selected files matches"},
		{"not-contains", command.ExecutionScope{NotContains: []string{"zzz"}, Stages: []command.Stage{{Kind: command.StageNotContains, Values: []string{"zzz"}}}}, "excluding them would keep no files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			checkpoint := filepath.Join(dir, "scope.json")
			cfg := filePreviewConfig{SearchingHint: true, CheckpointPath: checkpoint, Scopes: []command.ExecutionScope{tc.scope}}
			render := func() string {
				var buf bytes.Buffer
				if err := runInternalFilePreview(cfg, &buf); err != nil {
					t.Fatal(err)
				}
				return string(ansiEscape.ReplaceAll(buf.Bytes(), nil))
			}
			if got := render(); !strings.Contains(got, "Searching file contents") {
				t.Fatalf("no memo yet must be searching:\n%s", got)
			}
			writeContentMatchMemo(contentMatchMemoPath(checkpoint), "zz", nil)
			if got := render(); !strings.Contains(got, "Searching file contents") {
				t.Fatalf("a memo for an older pattern must be searching:\n%s", got)
			}
			writeContentMatchMemo(contentMatchMemoPath(checkpoint), "zzz", nil)
			if got := render(); !strings.Contains(got, tc.want) || strings.Contains(got, "Searching file contents") {
				t.Fatalf("finished search without results:\n%s", got)
			}
			writeContentMatchMemo(contentMatchMemoPath(checkpoint), "zzz", []string{filepath.Join(dir, "a.go")})
			if got := render(); strings.Contains(got, "No matches") || strings.Contains(got, "Every file matches") {
				t.Fatalf("a search with results must not say no matches:\n%s", got)
			}
		})
	}
}

// With a focused row (here the [all current matches] row), the preview is the
// normal one, never "No matches".
func TestFinishedSearchWithRowsLeavesThePreviewToTheFocusedRow(t *testing.T) {
	dir := t.TempDir()
	checkpoint := filepath.Join(dir, "scope.json")
	writeContentMatchMemo(contentMatchMemoPath(checkpoint), "func", nil)
	var buf bytes.Buffer
	cfg := filePreviewConfig{SearchingHint: true, CheckpointPath: checkpoint, FocusedLabel: contentMatchAllMatchesLabel,
		Scopes: []command.ExecutionScope{{Snippet: true, SnippetPattern: "func", Stages: []command.Stage{{Kind: command.StageSnippet, Values: []string{"func"}}}}}}
	_ = runInternalFilePreview(cfg, &buf)
	if strings.Contains(buf.String(), "No matches") {
		t.Fatalf("a focused row said no matches:\n%s", buf.String())
	}
}

// A pattern ripgrep refused (e.g. "Main(") says so instead of "Searching file
// contents..." forever; the marker is separate from the memo, so it never
// narrows the next search (user, 2026-11-20).
func TestRefusedPatternSaysNotAValidRegularExpression(t *testing.T) {
	dir := t.TempDir()
	checkpoint := filepath.Join(dir, "scope.json")
	scope := command.ExecutionScope{Snippet: true, SnippetPattern: "Main(", Stages: []command.Stage{{Kind: command.StageSnippet, Values: []string{"Main("}}}}
	cfg := filePreviewConfig{SearchingHint: true, CheckpointPath: checkpoint, Scopes: []command.ExecutionScope{scope}}
	render := func() string {
		var buf bytes.Buffer
		if err := runInternalFilePreview(cfg, &buf); err != nil {
			t.Fatal(err)
		}
		return string(ansiEscape.ReplaceAll(buf.Bytes(), nil))
	}
	if got := render(); !strings.Contains(got, "Searching file contents") {
		t.Fatalf("before ripgrep answered it must be searching:\n%s", got)
	}
	markContentMatchInvalid(checkpoint, "Main")
	if got := render(); strings.Contains(got, "Not a valid regular expression") {
		t.Fatalf("a refusal of another pattern must not count:\n%s", got)
	}
	markContentMatchInvalid(checkpoint, "Main(")
	if got := render(); !strings.Contains(got, "Not a valid regular expression") || !strings.Contains(got, `Main\( finds "Main("`) || strings.Contains(got, "Searching") {
		t.Fatalf("refused pattern:\n%s", got)
	}
	if _, ok := readContentMatchMemo(contentMatchMemoPath(checkpoint)); ok {
		t.Fatal("a refused pattern must not write the memo, which narrows the next search")
	}
}
