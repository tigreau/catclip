package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestAppendOnlyForNarrow(t *testing.T) {
	// Multiple patterns must remain values of one --only stage. Separate
	// --only flags would intersect disjoint ignored subtrees to an empty set.
	tests := []struct {
		name     string
		args     []string
		patterns []string
		want     []string
	}{
		{name: "single scope", args: []string{"cmd", "docs", "--include", "docs"}, patterns: []string{"docs/*"}, want: []string{"cmd", "docs", "--include", "docs", "--only", "docs/*"}},
		{name: "preserves earlier scopes", args: []string{"src", "--then", ".", "--include", "docs", "--depth", "2"}, patterns: []string{"docs/*"}, want: []string{"src", "--then", ".", "--include", "docs", "--depth", "2", "--only", "docs/*"}},
		{name: "multi-value OR union", args: []string{".", "--include", "docs/policy", "docs/versions"}, patterns: []string{"docs/policy/*", "docs/versions/*"}, want: []string{".", "--include", "docs/policy", "docs/versions", "--only", "docs/policy/*", "docs/versions/*"}},
		{name: "no ignore roots", args: []string{".", "--no-ignore"}, patterns: []string{"docs/*", "vendor/*", "build/*"}, want: []string{".", "--no-ignore", "--only", "docs/*", "vendor/*", "build/*"}},
		{name: "empty patterns", args: []string{".", "--include", "docs"}, want: []string{".", "--include", "docs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appendOnlyForNarrow(tt.args, tt.patterns); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("appendOnlyForNarrow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOnlyPatternsForFilesystemIncludes(t *testing.T) {
	tests := []struct {
		name     string
		includes []string
		setup    func(t *testing.T, root string)
		want     []string
	}{
		{
			name:     "directory",
			includes: []string{"docs"},
			setup: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"docs/*"},
		},
		{
			name:     "file",
			includes: []string{"docs/readme.md"},
			setup: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "docs", "readme.md"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"docs/readme.md"},
		},
		{
			name:     "multiple directories",
			includes: []string{"docs", "vendor"},
			setup: func(t *testing.T, root string) {
				for _, dir := range []string{"docs", "vendor"} {
					if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			},
			want: []string{"docs/*", "vendor/*"},
		},
		{name: "stat error defaults to recursive", includes: []string{"docs"}, want: []string{"docs/*"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, root)
			}
			if got := onlyPatternsForIncludes(root, tt.includes, false, nil, nil); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("onlyPatternsForIncludes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOnlyPatternsForNoIgnore(t *testing.T) {
	allWithVisibleSibling := []discovery.Entry{
		{RelPath: "src/main.ts"},
		{RelPath: "src/debug.log", AllowedByInclude: true},
		{RelPath: "docs/generated.md", AllowedByInclude: true},
	}
	bareCollision := []discovery.Entry{
		{RelPath: "debug.log", AllowedByInclude: true},
		{RelPath: "src/debug.log"},
	}
	tests := []struct {
		name    string
		all     []discovery.Entry
		ignored []discovery.Entry
		want    []string
	}{
		{
			name: "enumerates roots",
			all: []discovery.Entry{
				{RelPath: "docs/a.md"},
				{RelPath: "vendor/sdk/x.go"},
				{RelPath: "docs/sub/b.md"},
				{RelPath: "build/output.txt"},
			},
			ignored: []discovery.Entry{
				{RelPath: "docs/a.md"},
				{RelPath: "vendor/sdk/x.go"},
				{RelPath: "docs/sub/b.md"},
				{RelPath: "build/output.txt"},
			},
			want: []string{"docs/*", "vendor/*", "build/*"},
		},
		{name: "does not reinclude visible sibling", all: allWithVisibleSibling, ignored: allWithVisibleSibling[1:], want: []string{"src/debug.log", "docs/*"}},
		{name: "rejects literal glob metacharacters", all: []discovery.Entry{{RelPath: "[draft].txt", AllowedByInclude: true}}, ignored: []discovery.Entry{{RelPath: "[draft].txt", AllowedByInclude: true}}},
		{name: "rejects floating basename collision", all: bareCollision, ignored: bareCollision[:1]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := onlyPatternsForIncludes("/nonexistent", nil, true, tt.all, tt.ignored); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("onlyPatternsForIncludes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOnlyPatternsReplayExactlyIgnoredRejectsRootFileBasenameCollision(t *testing.T) {
	workingDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDir, "debug.log"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	all := []discovery.Entry{
		{RelPath: "debug.log", AllowedByInclude: true},
		{RelPath: "src/debug.log"},
	}
	ignored := []discovery.Entry{all[0]}
	patterns := onlyPatternsForIncludes(workingDir, []string{"debug.log"}, false, all, ignored)
	if got, want := patterns, []string{"debug.log"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("patterns = %v, want %v", got, want)
	}
	if onlyPatternsReplayExactlyIgnored(all, ignored, patterns) {
		t.Fatal("root-level basename replay must not retain a visible nested collision")
	}
}

func TestOnlyPatternsReplayExactlyIgnoredAcceptsAnchoredFile(t *testing.T) {
	all := []discovery.Entry{
		{RelPath: "ignored/debug.log", AllowedByInclude: true},
		{RelPath: "src/debug.log"},
	}
	ignored := []discovery.Entry{all[0]}
	patterns := []string{"ignored/debug.log"}
	if !onlyPatternsReplayExactlyIgnored(all, ignored, patterns) {
		t.Fatal("anchored file replay should reproduce the ignored set")
	}
}

func TestNarrowReplayCommandBytesIncludesExistingArgs(t *testing.T) {
	args := []string{strings.Repeat("a", maxNarrowReplayCommandBytes)}
	if got := narrowReplayCommandBytes(args, []string{"--only", "docs/*"}); got <= maxNarrowReplayCommandBytes {
		t.Fatalf("existing command must count toward replay cap; got %d", got)
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

func TestAllIncludesAreSubsetOfTargets(t *testing.T) {
	// --no-ignore is target-bounded before this helper is called, so it is a
	// subset by construction even for non-root and multi-target scopes.
	tests := []struct {
		name     string
		includes []string
		targets  []string
		noIgnore bool
		want     bool
	}{
		{name: "directory under dot", includes: []string{"docs"}, targets: []string{"."}, want: true},
		{name: "no ignore under dot", noIgnore: true, targets: []string{"."}, want: true},
		{name: "nested paths under dot", includes: []string{"docs/generated", "vendor/sdk"}, targets: []string{"."}, want: true},
		{name: "explicit descendant", includes: []string{"docs/generated"}, targets: []string{"docs"}, want: true},
		{name: "equal path", includes: []string{"docs"}, targets: []string{"docs"}, want: true},
		{name: "unrelated path", includes: []string{"docs"}, targets: []string{"src"}},
		{name: "include is target parent", includes: []string{"docs"}, targets: []string{"docs/foo"}},
		{name: "empty includes", targets: []string{"."}},
		{name: "no ignore under directory", noIgnore: true, targets: []string{"docs"}, want: true},
		{name: "no ignore under multiple targets", noIgnore: true, targets: []string{"docs", "src"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allIncludesAreSubsetOfTargets(tt.includes, tt.targets, tt.noIgnore); got != tt.want {
				t.Fatalf("allIncludesAreSubsetOfTargets() = %v, want %v", got, tt.want)
			}
		})
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

func TestMaybeNarrowConfirmNoopCases(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		includes []string
		targets  []string
	}{
		{name: "without includes", args: []string{".", "--depth", "2"}, targets: []string{"."}},
		{name: "include outside target", args: []string{"src", "--include", "docs"}, includes: []string{"docs"}, targets: []string{"src"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, used, err := maybeNarrowConfirm(tt.args, tt.includes, tt.targets)
			if err != nil {
				t.Fatalf("maybeNarrowConfirm() error = %v", err)
			}
			if used {
				t.Fatal("no-op case unexpectedly used fzf")
			}
			if !reflect.DeepEqual(out, tt.args) {
				t.Fatalf("args = %v, want %v", out, tt.args)
			}
		})
	}
}
