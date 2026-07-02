package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

// TestAppendOnlyForNarrow_SingleScope guards the v0.6.4 additive form:
// `catclip cmd docs --include docs` → narrow → original argv UNCHANGED
// plus appended `--only "docs/**"`. No target replacement, no token drop.
func TestAppendOnlyForNarrow_SingleScope(t *testing.T) {
	args := []string{"cmd", "docs", "--include", "docs"}
	got := appendOnlyForNarrow(args, []string{"docs/**"})
	want := []string{"cmd", "docs", "--include", "docs", "--only", "docs/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAppendOnlyForNarrow_PreservesEarlierScopes(t *testing.T) {
	args := []string{"src", "--then", ".", "--include", "docs", "--depth", "2"}
	got := appendOnlyForNarrow(args, []string{"docs/**"})
	want := []string{"src", "--then", ".", "--include", "docs", "--depth", "2", "--only", "docs/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestAppendOnlyForNarrow_MultiValueNotSeparateFlags guards the
// load-bearing narrow-shape invariant: multiple --only patterns must
// be emitted as a SINGLE --only flag with multiple values (OR-union),
// NOT as separate --only flags (which would AND-intersect and produce
// zero output when the ignored subtrees are disjoint).
//
// This was the reverse invariant pre-2026-07-02; the flip fixed the
// user-reported bug where selecting docs/policy + docs/versions in
// the include picker and choosing "Keep only ignored" produced empty
// output — the two `docs/policy/**` and `docs/versions/**` patterns
// AND-intersected to nothing.
func TestAppendOnlyForNarrow_MultiValueNotSeparateFlags(t *testing.T) {
	args := []string{".", "--include", "docs/policy", "docs/versions"}
	got := appendOnlyForNarrow(args, []string{"docs/policy/**", "docs/versions/**"})
	want := []string{".", "--include", "docs/policy", "docs/versions", "--only", "docs/policy/**", "docs/versions/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v\n  intent: multi-value --only is OR-union; separate flags would AND-intersect and drop everything for disjoint subtrees", got, want)
	}
}

func TestAppendOnlyForNarrow_WildcardEnumeratesRoots(t *testing.T) {
	args := []string{".", "--include", "*"}
	got := appendOnlyForNarrow(args, []string{"docs/**", "vendor/**", "build/**"})
	want := []string{".", "--include", "*", "--only", "docs/**", "vendor/**", "build/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v\n  intent: three ignored roots → one --only with three values (OR-union across roots)", got, want)
	}
}

func TestAppendOnlyForNarrow_EmptyPatternsReturnsArgsUnchanged(t *testing.T) {
	args := []string{".", "--include", "docs"}
	got := appendOnlyForNarrow(args, nil)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("empty patterns should leave args untouched; got %v", got)
	}
}

func TestOnlyPatternsForIncludes_Directory(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := onlyPatternsForIncludes(tmp, []string{"docs"}, nil)
	want := []string{"docs/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dir include should emit recursive glob; got %v want %v", got, want)
	}
}

func TestOnlyPatternsForIncludes_File(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "docs", "readme.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := onlyPatternsForIncludes(tmp, []string{"docs/readme.md"}, nil)
	want := []string{"docs/readme.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("file include should emit literal path (no /**); got %v want %v", got, want)
	}
}

func TestOnlyPatternsForIncludes_MultiInclude(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{"docs", "vendor"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := onlyPatternsForIncludes(tmp, []string{"docs", "vendor"}, nil)
	want := []string{"docs/**", "vendor/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestOnlyPatternsForIncludes_Wildcard(t *testing.T) {
	// Wildcard ignores the include paths and uses topLevelRootsForNarrow.
	entries := []discovery.Entry{
		{RelPath: "docs/a.md"},
		{RelPath: "vendor/sdk/x.go"},
		{RelPath: "docs/sub/b.md"},
		{RelPath: "build/output.txt"},
	}
	got := onlyPatternsForIncludes("/nonexistent", []string{"*"}, entries)
	want := []string{"docs/**", "vendor/**", "build/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// v0.6.4: TestStrictAncestorInTargets and TestRewriteCandidateForBroaderInclude_*
// were deleted along with their helpers. The walker's per-entry
// targetIncluded check plus walkAuthorizedByInclude ancestor-authorization
// (internal/discovery/resolver.go) now handle deep-include narrowing at
// walk time, so the neutralize-helper pipeline that produced a broader
// candidate for EvaluateScope is unnecessary. See
// RESOLVED_PLAN_include_as_authorization.md §C and
// ACTIVE_NOTE_include_double_syntax_rationale.md for the walker
// semantic that replaced this.

func TestOnlyPatternsForIncludes_StatErrorDefaultsToRecursive(t *testing.T) {
	// Path doesn't exist → stat fails → default to /** recursive form
	// (conservative: wider than the file form, never silently drops
	// descendants if the path is later created as a directory).
	got := onlyPatternsForIncludes(t.TempDir(), []string{"docs"}, nil)
	want := []string{"docs/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAllIncludesAreSubsetOfTargets_DotCoversAll(t *testing.T) {
	if !allIncludesAreSubsetOfTargets([]string{"docs"}, []string{"."}) {
		t.Fatal("docs ⊆ .")
	}
	if !allIncludesAreSubsetOfTargets([]string{"*"}, []string{"."}) {
		t.Fatal("* ⊆ . (wildcard covered by root)")
	}
	if !allIncludesAreSubsetOfTargets([]string{"docs/generated", "vendor/sdk"}, []string{"."}) {
		t.Fatal("nested subset under .")
	}
}

func TestAllIncludesAreSubsetOfTargets_ExplicitSubset(t *testing.T) {
	if !allIncludesAreSubsetOfTargets([]string{"docs/generated"}, []string{"docs"}) {
		t.Fatal("docs/generated ⊆ docs")
	}
	if !allIncludesAreSubsetOfTargets([]string{"docs"}, []string{"docs"}) {
		t.Fatal("equal path is subset of itself")
	}
}

func TestAllIncludesAreSubsetOfTargets_NotSubset(t *testing.T) {
	if allIncludesAreSubsetOfTargets([]string{"docs"}, []string{"src"}) {
		t.Fatal("docs is NOT ⊆ src")
	}
	if allIncludesAreSubsetOfTargets([]string{"docs"}, []string{"docs/foo"}) {
		t.Fatal("docs is NOT ⊆ docs/foo (parent of target)")
	}
	if allIncludesAreSubsetOfTargets([]string{}, []string{"."}) {
		t.Fatal("empty includes → not a valid subset relation")
	}
}

// TestAllIncludesAreSubsetOfTargets_WildcardIsSubsetByConstruction
// guards the v0.6.4 fix: the include picker filters its candidate set
// through filterIgnoredTargetsByScopeTargets before ever returning the
// wildcard sentinel, so `*` is subset-of-targets regardless of which
// target the user picked. The narrow-confirm screen must fire for it
// even when the target is a non-root path like "docs".
func TestAllIncludesAreSubsetOfTargets_WildcardIsSubsetByConstruction(t *testing.T) {
	if !allIncludesAreSubsetOfTargets([]string{"*"}, []string{"docs"}) {
		t.Fatal("`*` must be treated as subset of any scope target (picker already scope-filtered)")
	}
	if !allIncludesAreSubsetOfTargets([]string{"*"}, []string{"docs", "src"}) {
		t.Fatal("`*` must be subset of multi-target scopes too")
	}
}

func TestTopLevelRootsForNarrow_StableUniqueOrder(t *testing.T) {
	entries := []discovery.Entry{
		{RelPath: "docs/a.md"},
		{RelPath: "docs/sub/b.md"},
		{RelPath: "vendor/x.go"},
		{RelPath: "docs/c.md"},
		{RelPath: "build/output.txt"},
	}
	got := topLevelRootsForNarrow(entries)
	want := []string{"docs", "vendor", "build"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestExtractIncludePathsFromPickerArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"single value", []string{"--include", "docs"}, []string{"docs"}},
		{"multi-value", []string{"--include", "docs", "vendor"}, []string{"docs", "vendor"}},
		{"wildcard", []string{"--include", "*"}, []string{"*"}},
		{"empty arg", nil, nil},
		{"missing --include token", []string{"docs"}, nil},
		{"drops blank entries", []string{"--include", "docs", "", "vendor"}, []string{"docs", "vendor"}},
		// v0.6.4 bug-fix: v0.5.7's basename+include flow can return shapes
		// like ["--include", "docs", "--only", "docs/architecture"] when
		// the user picks a descendant of a gitignored ancestor. Stop at the
		// modifier boundary so we don't treat "--only" + value as include
		// values (which broke the subset check and silently disabled the
		// narrow-confirm screen for this flow).
		{"stops at modifier boundary", []string{"--include", "docs", "--only", "docs/architecture"}, []string{"docs"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractIncludePathsFromPickerArgs(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestMaybeNarrowConfirm_DoesNothingWithoutIncludes(t *testing.T) {
	args := []string{".", "--depth", "2"}
	out, used, err := maybeNarrowConfirm(args, nil, []string{"."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used {
		t.Fatalf("should NOT report fzf use when no includes given")
	}
	if !reflect.DeepEqual(out, args) {
		t.Fatalf("args mutated unexpectedly: got %v want %v", out, args)
	}
}

func TestMaybeNarrowConfirm_DoesNothingWhenNotSubset(t *testing.T) {
	// Target=src, include=docs → not a subset of src, no screen.
	args := []string{"src", "--include", "docs"}
	out, used, err := maybeNarrowConfirm(args, []string{"docs"}, []string{"src"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used {
		t.Fatalf("non-subset includes should not fire the screen")
	}
	if !reflect.DeepEqual(out, args) {
		t.Fatalf("args mutated unexpectedly: got %v want %v", out, args)
	}
}

func TestIncludesContainWildcard(t *testing.T) {
	if !includesContainWildcard([]string{"docs", "*"}) {
		t.Fatal("expected wildcard detected")
	}
	if includesContainWildcard([]string{"docs", "vendor"}) {
		t.Fatal("no wildcard present")
	}
	if includesContainWildcard(nil) {
		t.Fatal("empty list")
	}
}
