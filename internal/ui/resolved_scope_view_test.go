package ui

import (
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestResolvedCurrentScopeViewForArgsUsesCurrentScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.go":    "package main\n",
		"src/notes.txt":  "notes\n",
		"docs/guide.md":  "# guide\n",
		"docs/ignore.js": "console.log('skip')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	view, err := resolvedCurrentScopeViewForArgs([]string{"src", "--then", "docs", "--only", "*.md"})
	if err != nil {
		t.Fatalf("resolvedCurrentScopeViewForArgs returned error: %v", err)
	}
	if got, want := view.ScopeIndex, 1; got != want {
		t.Fatalf("ScopeIndex = %d, want %d", got, want)
	}
	gotRels := make([]string, 0, len(view.Entries))
	for _, entry := range view.Entries {
		gotRels = append(gotRels, entry.RelPath)
	}
	if want := []string{"docs/guide.md"}; !reflect.DeepEqual(gotRels, want) {
		t.Fatalf("resolved entries = %#v, want %#v", gotRels, want)
	}

	if got, want := len(view.Scopes), 2; got != want {
		t.Fatalf("expected %d command scopes, got %d", want, got)
	}
	if got, want := view.Scopes[1].OutputMode(), command.EntryModeFull; got != want {
		t.Fatalf("current scope OutputMode() = %q, want %q", got, want)
	}
}

func TestStartupResolvedCurrentScopeViewForArgsSkipsUnresolvedScope(t *testing.T) {
	tests := [][]string{
		{"--"},
		{"src", "--"},
	}
	for _, args := range tests {
		view, ok, err := startupResolvedCurrentScopeViewForArgs(args)
		if err != nil {
			t.Fatalf("startupResolvedCurrentScopeViewForArgs(%#v) returned error: %v", args, err)
		}
		if ok {
			t.Fatalf("expected unresolved scope %#v to be skipped, got view %+v", args, view)
		}
	}
}

func TestStartupResolvedCurrentScopeViewForArgsReturnsParseErrors(t *testing.T) {
	_, ok, err := startupResolvedCurrentScopeViewForArgs([]string{"src", "--diff"})
	if err == nil {
		t.Fatal("expected parse error for invalid startup args")
	}
	if ok {
		t.Fatal("did not expect resolved view for invalid startup args")
	}
}
