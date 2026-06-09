package catclip

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
)

func TestBuildContextSnippetRanges(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f", "g"} // 1-indexed lines 1..7
	tests := []struct {
		name    string
		matched []int
		context int
		want    []output.SnippetRange
	}{
		{"zero context is match line only", []int{4}, 0, []output.SnippetRange{{Start: 4, End: 4}}},
		{"clamps at file start", []int{1}, 2, []output.SnippetRange{{Start: 1, End: 3}}},
		{"clamps at file end", []int{7}, 2, []output.SnippetRange{{Start: 5, End: 7}}},
		{"overlapping windows merge", []int{4, 6}, 2, []output.SnippetRange{{Start: 2, End: 7}}},
		{"adjacent windows merge", []int{2, 5}, 1, []output.SnippetRange{{Start: 1, End: 6}}},
		{"far apart stay separate", []int{1, 7}, 1, []output.SnippetRange{{Start: 1, End: 2}, {Start: 6, End: 7}}},
		{"out-of-range matches skipped", []int{0, 4, 99}, 1, []output.SnippetRange{{Start: 3, End: 5}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := output.BuildContextSnippetRanges(lines, tt.matched, tt.context)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("output.BuildContextSnippetRanges(%v, ctx=%d) = %v, want %v", tt.matched, tt.context, got, tt.want)
			}
		})
	}
}

func snippetScopeFromArgs(t *testing.T, args []string) command.ExecutionScope {
	t.Helper()
	cmd, err := cli.ParseArgs(args)
	if err != nil {
		t.Fatalf("cli.ParseArgs(%v) returned error: %v", args, err)
	}
	scopes := command.ExecutionScopesFromSpec(cmd.Command)
	if len(scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(scopes))
	}
	return scopes[0]
}

func TestParseArgsSnippetContext(t *testing.T) {
	t.Run("regex plus context", func(t *testing.T) {
		s := snippetScopeFromArgs(t, []string{"src", "--snippet", "TODO", "3"})
		if s.SnippetPattern != "TODO" || !s.SnippetContextSet || s.SnippetContextLines != 3 {
			t.Fatalf("pattern=%q set=%v lines=%d", s.SnippetPattern, s.SnippetContextSet, s.SnippetContextLines)
		}
	})
	t.Run("numeric regex without context is block mode", func(t *testing.T) {
		s := snippetScopeFromArgs(t, []string{"src", "--snippet", "404"})
		if s.SnippetPattern != "404" || s.SnippetContextSet {
			t.Fatalf("pattern=%q set=%v (404 should be the regex, not context)", s.SnippetPattern, s.SnippetContextSet)
		}
	})
	t.Run("numeric regex with context", func(t *testing.T) {
		s := snippetScopeFromArgs(t, []string{"src", "--snippet", "404", "2"})
		if s.SnippetPattern != "404" || !s.SnippetContextSet || s.SnippetContextLines != 2 {
			t.Fatalf("pattern=%q set=%v lines=%d", s.SnippetPattern, s.SnippetContextSet, s.SnippetContextLines)
		}
	})
	t.Run("zero context is allowed", func(t *testing.T) {
		s := snippetScopeFromArgs(t, []string{"src", "--snippet", "TODO", "0"})
		if !s.SnippetContextSet || s.SnippetContextLines != 0 {
			t.Fatalf("set=%v lines=%d (0 must be a valid context)", s.SnippetContextSet, s.SnippetContextLines)
		}
	})
	for _, bad := range [][]string{
		{"src", "--snippet", "TODO", "201"},
		{"src", "--snippet", "TODO", "-1"},
	} {
		t.Run("out-of-range "+strings.Join(bad[2:], " "), func(t *testing.T) {
			_, err := cli.ParseArgs(bad)
			if err == nil || !strings.Contains(err.Error(), "context must be between 0 and 200") {
				t.Fatalf("expected context-range error, got %v", err)
			}
		})
	}
}

func TestParseArgsRegexModifierExtraValueHint(t *testing.T) {
	// A bare token after a regex modifier (likely an unquoted spaced regex the
	// shell split) gets the quote hint — for both --snippet and --contains.
	for _, flag := range []string{"--snippet", "--contains"} {
		_, err := cli.ParseArgs([]string{"main.go", flag, "func", "a"})
		if err == nil {
			t.Fatalf("%s: expected error for a bare token after the regex", flag)
		}
		if !strings.Contains(err.Error(), "quote it if it contains spaces") || !strings.Contains(err.Error(), flag+" 'func a'") {
			t.Fatalf("%s: expected regex quote hint, got: %v", flag, err)
		}
	}
	// The hint is scoped to regex modifiers: a non-regex modifier keeps the
	// generic positional-after-modifier error.
	if _, err := cli.ParseArgs([]string{"src", "--depth", "2", "extra"}); err == nil || !strings.Contains(err.Error(), "positional targets must come before modifiers") {
		t.Fatalf("non-regex modifier should keep the generic positional error, got: %v", err)
	}
	// A following flag is fine — only bare tokens trigger the hint.
	if _, err := cli.ParseArgs([]string{"main.go", "--snippet", "func", "3", "--print"}); err != nil {
		t.Fatalf("--snippet regex N --print should parse, got: %v", err)
	}
}

func TestCanonicalScopeArgsSnippetContext(t *testing.T) {
	// Block mode renders the regex with enforced single quotes and no number.
	if got := strings.Join(command.CanonicalScopeArgs(snippetScopeFromArgs(t, []string{"src", "--snippet", "TODO"})), " "); got != "src --snippet 'TODO'" {
		t.Fatalf("block render = %q, want \"src --snippet 'TODO'\"", got)
	}
	// Context mode appends the number after the quoted regex.
	if got := strings.Join(command.CanonicalScopeArgs(snippetScopeFromArgs(t, []string{"src", "--snippet", "TODO", "3"})), " "); got != "src --snippet 'TODO' 3" {
		t.Fatalf("context render = %q, want \"src --snippet 'TODO' 3\"", got)
	}
	// 0 is rendered explicitly so the resolved command reproduces it.
	if got := strings.Join(command.CanonicalScopeArgs(snippetScopeFromArgs(t, []string{"src", "--snippet", "TODO", "0"})), " "); got != "src --snippet 'TODO' 0" {
		t.Fatalf("zero-context render = %q, want \"src --snippet 'TODO' 0\"", got)
	}
}

func TestSnippetContextCheckpointRoundtrip(t *testing.T) {
	data := discovery.CheckpointData{
		Entries: []discovery.Entry{{
			RelPath:             "a.go",
			Mode:                command.EntryModeSnippet,
			SnippetPattern:      "TODO",
			SnippetContextSet:   true,
			SnippetContextLines: 3,
		}},
	}
	raw, err := discovery.MarshalCheckpoint(data)
	if err != nil {
		t.Fatalf("marshal returned error: %v", err)
	}
	if !strings.Contains(string(raw), "snippet_context_set") || !strings.Contains(string(raw), "snippet_context_lines") {
		t.Fatalf("checkpoint json missing snippet-context keys: %s", raw)
	}
	got, err := discovery.UnmarshalCheckpoint(raw)
	if err != nil {
		t.Fatalf("unmarshal returned error: %v", err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(got.Entries))
	}
	e := got.Entries[0]
	if !e.SnippetContextSet || e.SnippetContextLines != 3 {
		t.Fatalf("roundtrip lost context: set=%v lines=%d", e.SnippetContextSet, e.SnippetContextLines)
	}
}
