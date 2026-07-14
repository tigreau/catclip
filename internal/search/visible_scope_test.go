package search

import (
	"path/filepath"
	"testing"
)

// TestResolveVisibleFileSetNarrowingPreservesAncestorGitignore locks the
// v0.6.7 Pin #1 finding: narrowing the visible-file walk to a positional
// target still applies ancestor .gitignore rules (rg's add_parents), so the
// narrowed set equals the project-wide set restricted to the target subtree.
// If this ever regresses, catclip would silently leak ancestor-ignored files
// from scoped commands.
func TestResolveVisibleFileSetNarrowingPreservesAncestorGitignore(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	mustWrite(t, filepath.Join(dir, "keep.txt"), "x")
	mustMkdir(t, filepath.Join(dir, "sub"))
	mustWrite(t, filepath.Join(dir, "sub", "keep.go"), "package x")
	mustWrite(t, filepath.Join(dir, "sub", "drop.log"), "ignored by ancestor *.log")
	// A monorepo-style nested gitignore in a sibling subtree.
	mustMkdir(t, filepath.Join(dir, "frontend"))
	mustWrite(t, filepath.Join(dir, "frontend", ".gitignore"), "*.tmp\n")
	mustWrite(t, filepath.Join(dir, "frontend", "a.tmp"), "ignored by frontend *.tmp")
	mustWrite(t, filepath.Join(dir, "frontend", "b.js"), "kept")

	resetVisibleFileSetCache()

	wide, err := ResolveVisibleFileSet(dir, "", nil)
	if err != nil {
		t.Fatalf("project-wide visible set: %v", err)
	}
	// Baseline: ancestor and nested gitignores both applied project-wide.
	assertHas(t, wide, "sub/keep.go", "keep.txt", "frontend/b.js")
	assertLacks(t, wide, "sub/drop.log", "frontend/a.tmp")

	// Narrowed to "sub": ancestor *.log must STILL drop sub/drop.log, and the
	// narrowed set must equal the project-wide set restricted to sub/.
	narrowed, err := ResolveVisibleFileSet(dir, "", []string{"sub"})
	if err != nil {
		t.Fatalf("narrowed visible set: %v", err)
	}
	assertHas(t, narrowed, "sub/keep.go")
	assertLacks(t, narrowed, "sub/drop.log")
	if got := len(narrowed); got != 1 {
		t.Fatalf("narrowed set should contain only sub/keep.go, got %d entries: %v", got, narrowed)
	}
	assertSubtreeMatches(t, wide, narrowed, "sub")

	// Narrowed to a monorepo subtree: its own *.tmp and the root ancestor both
	// apply, so only frontend/b.js and frontend/.gitignore survive.
	front, err := ResolveVisibleFileSet(dir, "", []string{"frontend"})
	if err != nil {
		t.Fatalf("frontend visible set: %v", err)
	}
	assertHas(t, front, "frontend/b.js")
	assertLacks(t, front, "frontend/a.tmp")
	assertSubtreeMatches(t, wide, front, "frontend")
}

// TestResolveVisibleFileSetNarrowingPreservesHissAnchor locks Pin #2: a
// leading-slash-anchored .hiss pattern anchors at workingDir consistently
// whether the walk is project-wide or narrowed to a positional target.
func TestResolveVisibleFileSetNarrowingPreservesHissAnchor(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "sub"))
	mustWrite(t, filepath.Join(dir, "sub", "keep.go"), "package x")
	mustWrite(t, filepath.Join(dir, "sub", "drop.js"), "hidden by anchored hiss")
	hiss := filepath.Join(dir, "root.hiss")
	mustWrite(t, hiss, "/sub/drop.js\n") // anchored at workingDir

	resetVisibleFileSetCache()

	wide, err := ResolveVisibleFileSet(dir, hiss, nil)
	if err != nil {
		t.Fatalf("project-wide with hiss: %v", err)
	}
	assertHas(t, wide, "sub/keep.go")
	assertLacks(t, wide, "sub/drop.js")

	narrowed, err := ResolveVisibleFileSet(dir, hiss, []string{"sub"})
	if err != nil {
		t.Fatalf("narrowed with hiss: %v", err)
	}
	assertHas(t, narrowed, "sub/keep.go")
	assertLacks(t, narrowed, "sub/drop.js")
	assertSubtreeMatches(t, wide, narrowed, "sub")
}

func resetVisibleFileSetCache() {
	visibleFileSetCacheMu.Lock()
	visibleFileSetCache = map[string]map[string]struct{}{}
	visibleFileSetCacheMu.Unlock()
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := mkdir(p); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := writeFile(p, content); err != nil {
		t.Fatal(err)
	}
}

func assertHas(t *testing.T, set map[string]struct{}, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, ok := set[rel]; !ok {
			t.Errorf("expected %q in set, missing; set=%v", rel, set)
		}
	}
}

func assertLacks(t *testing.T, set map[string]struct{}, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, ok := set[rel]; ok {
			t.Errorf("did not expect %q in set; set=%v", rel, set)
		}
	}
}

// assertSubtreeMatches asserts the narrowed set equals the project-wide set
// restricted to entries under prefix/. This is the "output-preserving"
// guarantee: narrowing changes only the universe, never which files survive.
func assertSubtreeMatches(t *testing.T, wide, narrowed map[string]struct{}, prefix string) {
	t.Helper()
	want := map[string]struct{}{}
	for rel := range wide {
		if rel == prefix || len(rel) > len(prefix) && rel[:len(prefix)+1] == prefix+"/" {
			want[rel] = struct{}{}
		}
	}
	for rel := range want {
		if _, ok := narrowed[rel]; !ok {
			t.Errorf("narrowed set missing %q that project-wide had under %s/", rel, prefix)
		}
	}
	for rel := range narrowed {
		if _, ok := want[rel]; !ok {
			t.Errorf("narrowed set has %q not present in project-wide %s/ subtree", rel, prefix)
		}
	}
}
