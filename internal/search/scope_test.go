package search

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var (
	osMkdirAll  = os.MkdirAll
	osWriteFile = os.WriteFile
)

func TestScopedCacheTargetsReturnsNilForEmpty(t *testing.T) {
	if got := scopedCacheTargets("/wd", nil); got != nil {
		t.Errorf("scopedCacheTargets(nil) = %v, want nil", got)
	}
	if got := scopedCacheTargets("/wd", []string{}); got != nil {
		t.Errorf("scopedCacheTargets([]) = %v, want nil", got)
	}
}

func TestScopedCacheTargetsReturnsNilForDotOrEmptyTarget(t *testing.T) {
	cases := [][]string{
		{"."},
		{""},
		{".", "src"},
		{"src", "."},
	}
	for _, in := range cases {
		dir := t.TempDir()
		got := scopedCacheTargets(dir, in)
		if got != nil {
			t.Errorf("scopedCacheTargets(%v) = %v, want nil (one target is project-wide)", in, got)
		}
	}
}

func TestScopedCacheTargetsReturnsNilForGlob(t *testing.T) {
	dir := t.TempDir()
	if got := scopedCacheTargets(dir, []string{"src/*.go"}); got != nil {
		t.Errorf("scopedCacheTargets glob = %v, want nil (rg positional is not a glob)", got)
	}
}

func TestScopedCacheTargetsNormalizesAndDedupes(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"src", "tests"} {
		if err := mkdir(filepath.Join(dir, sub)); err != nil {
			t.Fatal(err)
		}
	}
	got := scopedCacheTargets(dir, []string{"src/", "tests", "src"})
	want := []string{"src", "tests"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scopedCacheTargets dedupe = %v, want %v", got, want)
	}
}

func TestScopedCacheTargetsFallsBackToProjectWideOnFuzzyTarget(t *testing.T) {
	// When any target doesn't exist as a literal path on disk, the user
	// has typed a fuzzy query — resolution may land paths anywhere in the
	// project, so the text-set scope must be project-wide for correctness.
	// (A scoped text-set keyed on just "src" would miss the fuzzy-resolved
	// path under "shared/" or wherever, classifying it as binary.)
	dir := t.TempDir()
	if err := mkdir(filepath.Join(dir, "src")); err != nil {
		t.Fatal(err)
	}
	got := scopedCacheTargets(dir, []string{"src", "fuzzy-query"})
	if got != nil {
		t.Errorf("scopedCacheTargets mixed real+fuzzy = %v, want nil (project-wide)", got)
	}
}

func TestScopedCacheTargetsReturnsNilWhenAllMissing(t *testing.T) {
	dir := t.TempDir()
	got := scopedCacheTargets(dir, []string{"nonexistent1", "nonexistent2"})
	if got != nil {
		t.Errorf("scopedCacheTargets all-fuzzy = %v, want nil", got)
	}
}

func TestResolveTextFileSetCachesPerTargetSet(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"src", "tests"} {
		if err := mkdir(filepath.Join(dir, sub)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(filepath.Join(dir, sub, "f.go"), "package x"); err != nil {
			t.Fatal(err)
		}
	}
	// Clear the cache to get a clean baseline. Each call populates one
	// entry per (workingDir, normTargets) cache key; we assert three
	// distinct entries land for three distinct target sets, and that
	// repeated calls with the same target set don't create new entries.
	textFileSetCacheMu.Lock()
	textFileSetCache = map[string]map[string]struct{}{}
	textFileSetCacheMu.Unlock()

	if _, err := ResolveTextFileSet(dir, []string{"src"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTextFileSet(dir, []string{"src"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTextFileSet(dir, []string{"tests"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTextFileSet(dir, nil); err != nil {
		t.Fatal(err)
	}

	textFileSetCacheMu.Lock()
	got := len(textFileSetCache)
	textFileSetCacheMu.Unlock()
	const want = 3
	if got != want {
		t.Fatalf("expected %d distinct cache entries (per distinct target set), got %d", want, got)
	}
}

func TestResolveTextFileSetWithTargetsRestrictsResults(t *testing.T) {
	dir := t.TempDir()
	if err := mkdir(filepath.Join(dir, "src")); err != nil {
		t.Fatal(err)
	}
	if err := mkdir(filepath.Join(dir, "tests")); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "src", "a.go"), "package x"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "tests", "b.go"), "package x"); err != nil {
		t.Fatal(err)
	}
	textFileSetCacheMu.Lock()
	textFileSetCache = map[string]map[string]struct{}{}
	textFileSetCacheMu.Unlock()

	scoped, err := ResolveTextFileSet(dir, []string{"src"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scoped["src/a.go"]; !ok {
		t.Errorf("expected src/a.go in scoped set, got %v", scoped)
	}
	if _, ok := scoped["tests/b.go"]; ok {
		t.Errorf("did not expect tests/b.go in scoped set (scope was src only)")
	}

	projectWide, err := ResolveTextFileSet(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projectWide["src/a.go"]; !ok {
		t.Errorf("expected src/a.go in project-wide set, got %v", projectWide)
	}
	if _, ok := projectWide["tests/b.go"]; !ok {
		t.Errorf("expected tests/b.go in project-wide set, got %v", projectWide)
	}
}

// mkdir / writeFile mirror os.MkdirAll / os.WriteFile but with t.Helper-style
// error wrapping for use in the helpers above. Kept local to avoid importing
// new packages.
func mkdir(p string) error {
	return osMkdirAll(p, 0o755)
}

func writeFile(p, content string) error {
	return osWriteFile(p, []byte(content), 0o644)
}
