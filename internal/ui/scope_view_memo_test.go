package ui

import (
	"testing"
)

// The scope-view memo (cross_picker_scope_view_thread Item 4): identical
// argv reuses the derived view instead of re-running discovery; any argv
// change derives fresh; and returned entry slices are clones so callers
// may reorder without corrupting the memo.
func TestResolvedCurrentScopeViewForArgsMemoizesByArgs(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":  "package a\n",
		"src/b.go":  "package b\n",
		"docs/c.md": "c\n",
	})
	t.Chdir(project)

	resetScopeViewMemo := func() {
		scopeViewMemoMu.Lock()
		scopeViewMemoSet = false
		scopeViewMemoKey = ""
		scopeViewMemoVal = resolvedScopeView{}
		scopeViewMemoMu.Unlock()
	}
	resetScopeViewMemo()
	defer resetScopeViewMemo()

	args := []string{"--quiet", "--print", "src"}
	first, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatalf("first derivation: %v", err)
	}
	if len(first.Entries) != 2 {
		t.Fatalf("expected 2 entries for src scope, got %v", entryRelPaths(first.Entries))
	}

	// Same args → memo hit; mutating the first result must not leak in.
	first.Entries[0], first.Entries[1] = first.Entries[1], first.Entries[0]
	first.Entries[0].RelPath = "corrupted"
	second, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatalf("second derivation: %v", err)
	}
	if len(second.Entries) != 2 || second.Entries[0].RelPath == "corrupted" || second.Entries[1].RelPath == "corrupted" {
		t.Fatalf("memo returned caller-corrupted entries: %v", entryRelPaths(second.Entries))
	}

	// Changed args → fresh derivation for the new scope.
	third, err := resolvedCurrentScopeViewForArgs([]string{"--quiet", "--print", "docs"})
	if err != nil {
		t.Fatalf("third derivation: %v", err)
	}
	if len(third.Entries) != 1 || third.Entries[0].RelPath != "docs/c.md" {
		t.Fatalf("args change must derive the new scope, got %v", entryRelPaths(third.Entries))
	}
}
