package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
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

	scopeViewMemoReset()
	defer scopeViewMemoReset()

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

	// Revisiting an older state must remain a hit after another state was
	// derived. The old single-entry memo lost this state and rediscovered it.
	if err := os.Remove(filepath.Join(project, "src", "b.go")); err != nil {
		t.Fatal(err)
	}
	revisited, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatalf("revisited derivation: %v", err)
	}
	if len(revisited.Entries) != 2 {
		t.Fatalf("undo-style revisit did not retain its state: %v", entryRelPaths(revisited.Entries))
	}
}

func TestResolvedScopeViewMemoReturnsDetachedCommandState(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n// TODO\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"src", "--only", "*.go", "--contains", "TODO"}
	first, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	first.Scope.Targets[0] = "corrupted-scope"
	first.Scope.Stages[0].Values[0] = "corrupted-stage"
	first.Scopes[0].Only[0] = "corrupted-only"
	first.Render.Scopes[0].Exclude = append(first.Render.Scopes[0].Exclude, "corrupted-render")

	second, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Scope.Targets[0]; got != "src" {
		t.Fatalf("caller mutation leaked into retained scope target: %q", got)
	}
	if got := second.Scope.Stages[0].Values[0]; got != "*.go" {
		t.Fatalf("caller mutation leaked into retained stage: %q", got)
	}
	if got := second.Scopes[0].Only[0]; got != "*.go" {
		t.Fatalf("caller mutation leaked into retained scope list: %q", got)
	}
	if got := second.Render.Scopes[0].Exclude; len(got) != 0 {
		t.Fatalf("caller mutation leaked into retained render scopes: %v", got)
	}
}

func TestResolvedCurrentScopeViewDerivesPathOnlyStagesFromRetainedParent(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":          "package a\n",
		"src/a.md":          "a\n",
		"src/nested/b.go":   "package b\n",
		"src/nested/deep/c": "c\n",
	})
	t.Chdir(project)

	tests := []struct {
		name  string
		stage []string
	}{
		{name: "only", stage: []string{"--only", "*.go"}},
		{name: "exclude", stage: []string{"--exclude", "*.md"}},
		{name: "depth", stage: []string{"--depth", "1"}},
		{name: "paths", stage: []string{"--paths"}},
		{name: "lines", stage: []string{"--lines", "1", "2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scopeViewMemoReset()
			baseArgs := []string{"--quiet", "--print", "src"}
			if _, err := resolvedCurrentScopeViewForArgs(baseArgs); err != nil {
				t.Fatalf("base derivation: %v", err)
			}
			childArgs := append(append([]string(nil), baseArgs...), tc.stage...)
			derived, err := resolvedCurrentScopeViewForArgs(childArgs)
			if err != nil {
				t.Fatalf("derived view: %v", err)
			}

			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			key := wd + "\x00\x00" + strings.Join(childArgs, "\x00")
			entry, ok := scopeViewMemoLookup(key)
			if !ok || entry.parentID == 0 {
				t.Fatalf("%s did not retain a derived parent relationship: %+v", tc.name, entry)
			}
			scopeViewMemoMu.Lock()
			storedChild := scopeViewMemoValues[key]
			scopeViewMemoMu.Unlock()
			if len(storedChild.view.Entries) != 0 || storedChild.inventory == nil || storedChild.inventory != entry.inventory {
				t.Fatalf("%s retained a copied Entry slice instead of compact IDs", tc.name)
			}

			// A cold evaluation is the behavioral oracle. Reuse may change how
			// the state is obtained, never which ordered entries it contains.
			scopeViewMemoReset()
			canonical, err := resolvedCurrentScopeViewForArgs(childArgs)
			if err != nil {
				t.Fatalf("canonical view: %v", err)
			}
			if !reflect.DeepEqual(derived.Entries, canonical.Entries) {
				t.Fatalf("derived %s differs from canonical:\nderived=%+v\ncanonical=%+v", tc.name, derived.Entries, canonical.Entries)
			}
		})
	}
	scopeViewMemoReset()
}

func TestResolvedCurrentScopeViewRetainedGitStagesMatchColdFailureOutsideRepo(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.go": "package a\n",
	})
	t.Chdir(project)

	for _, flag := range []string{
		"--changed", "--staged", "--unstaged", "--untracked",
		"--changed-diff", "--staged-diff", "--unstaged-diff",
	} {
		t.Run(flag, func(t *testing.T) {
			scopeViewMemoReset()
			baseArgs := []string{"--quiet", "--print", "."}
			if _, err := resolvedCurrentScopeViewForArgs(baseArgs); err != nil {
				t.Fatalf("base derivation: %v", err)
			}
			childArgs := append(append([]string(nil), baseArgs...), flag)
			derived, err := resolvedCurrentScopeViewForArgs(childArgs)
			if err != nil {
				t.Fatalf("retained derivation: %v", err)
			}
			if len(derived.Entries) != 0 {
				t.Fatalf("retained %s kept files outside Git: %v", flag, entryRelPaths(derived.Entries))
			}
			wantDiagnostic := discovery.GitSelectionUnavailableDiagnostic(0)
			if got := derived.Discovered.Diagnostics; !reflect.DeepEqual(got, []discovery.Diagnostic{wantDiagnostic}) {
				t.Fatalf("retained %s diagnostics = %+v, want %+v", flag, got, wantDiagnostic)
			}

			scopeViewMemoReset()
			cold, err := resolvedCurrentScopeViewForArgs(childArgs)
			if err != nil {
				t.Fatalf("cold derivation: %v", err)
			}
			if !reflect.DeepEqual(derived.Discovered.Diagnostics, cold.Discovered.Diagnostics) ||
				!reflect.DeepEqual(derived.Entries, cold.Entries) {
				t.Fatalf("retained %s differs from cold:\nretained=%+v\ncold=%+v", flag, derived.Discovered, cold.Discovered)
			}
		})
	}
	scopeViewMemoReset()
}

func TestResolvedCurrentScopeViewDerivesMultiTargetDepth(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":          "package a\n",
		"src/nested/b.go":   "package b\n",
		"docs/a.md":         "a\n",
		"docs/nested/b.md":  "b\n",
		"docs/nested/c.txt": "c\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src", "docs"}
	if _, err := resolvedCurrentScopeViewForArgs(baseArgs); err != nil {
		t.Fatal(err)
	}
	childArgs := append(append([]string(nil), baseArgs...), "--depth", "1")
	derived, err := resolvedCurrentScopeViewForArgs(childArgs)
	if err != nil {
		t.Fatal(err)
	}

	scopeViewMemoReset()
	canonical, err := resolvedCurrentScopeViewForArgs(childArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(derived.Entries, canonical.Entries) {
		t.Fatalf("multi-target depth drifted:\nderived=%+v\ncanonical=%+v", derived.Entries, canonical.Entries)
	}
}

func TestResolvedCurrentScopeViewRetainsNoIgnoreGenerationAndNewMetadata(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":              "src/generated/\ndocs/private/\noutside/\n",
		"src/visible.go":          "package visible\n",
		"src/generated/keep.go":   "package generated\n",
		"docs/visible.md":         "visible\n",
		"docs/private/keep.md":    "private\n",
		"outside/must-not-appear": "outside\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src", "docs"}
	base, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataForMemoEntries(t, base.Entries)
	scopeViewMemoReset()
	if !scopeViewMemoAdoptTargetSelection(baseArgs, git.Detect(project), base.Entries, metadata) {
		t.Fatal("visible generation was not sealed")
	}
	base, err = resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	gitObservationCalls := 0
	if _, cached, err := base.inventory.cachedGitStatus(base.Entries, func([]discovery.Entry) (map[string]string, error) {
		gitObservationCalls++
		return map[string]string{"src/visible.go": "M"}, nil
	}); err != nil || cached {
		t.Fatalf("initial Git observation: cached=%v err=%v", cached, err)
	}

	benchLog := filepath.Join(t.TempDir(), "bench.log")
	t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", benchLog)
	noIgnoreArgs := append(append([]string(nil), baseArgs...), "--no-ignore")
	expanded, err := resolvedCurrentScopeViewForArgs(noIgnoreArgs)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryRelPaths(expanded.Entries), []string{
		"docs/private/keep.md", "docs/visible.md", "src/generated/keep.go", "src/visible.go",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded no-ignore generation = %v, want %v", got, want)
	}
	if expanded.inventory != base.inventory {
		t.Fatal("no-ignore generation replaced existing path identity inventory")
	}
	if _, cached, err := expanded.inventory.cachedGitStatus(expanded.Entries, func([]discovery.Entry) (map[string]string, error) {
		gitObservationCalls++
		return map[string]string{"src/visible.go": "M"}, nil
	}); err != nil || cached || gitObservationCalls != 2 {
		t.Fatalf("expanded generation reused stale Git observation: cached=%v calls=%d err=%v", cached, gitObservationCalls, err)
	}
	if len(expanded.fileIDs) != 4 || expanded.fileIDs[1] != base.fileIDs[0] || expanded.fileIDs[3] != base.fileIDs[1] {
		t.Fatalf("visible path IDs did not survive canonical expansion: base=%v expanded=%v", base.fileIDs, expanded.fileIDs)
	}
	for _, entry := range expanded.Entries {
		switch entry.RelPath {
		case "src/generated/keep.go":
			if entry.TargetRoot != "src" || !entry.IgnoreBypassed {
				t.Fatalf("src addition lost target/ignore attribution: %+v", entry)
			}
		case "docs/private/keep.md":
			if entry.TargetRoot != "docs" || !entry.IgnoreBypassed {
				t.Fatalf("docs addition lost target/ignore attribution: %+v", entry)
			}
		}
	}

	// Undo and exact re-entry are argv memo hits. Neither may walk or stat the
	// expanded universe again.
	undone, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedStrings(entryRelPaths(undone.Entries)); !reflect.DeepEqual(got, []string{"docs/visible.md", "src/visible.go"}) {
		t.Fatalf("undo did not restore visible generation: %v", got)
	}
	reentered, err := resolvedCurrentScopeViewForArgs(noIgnoreArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entryRelPaths(reentered.Entries), entryRelPaths(expanded.Entries)) {
		t.Fatalf("re-entry changed expanded membership:\nfirst=%v\nagain=%v", entryRelPaths(expanded.Entries), entryRelPaths(reentered.Entries))
	}

	logBytes, err := os.ReadFile(benchLog)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBytes)
	if got := strings.Count(logText, `event="discovery.no_ignore_generation"`); got != 1 {
		t.Fatalf("no-ignore enumeration count = %d, want 1\n%s", got, logText)
	}
	noIgnoreRGCalls := 0
	for _, line := range strings.Split(logText, "\n") {
		if strings.Contains(line, `event="search.rg.files"`) && strings.Contains(line, `no_ignore="true"`) {
			noIgnoreRGCalls++
		}
	}
	if noIgnoreRGCalls != 1 {
		t.Fatalf("no-ignore rg union walks = %d, want 1\n%s", noIgnoreRGCalls, logText)
	}
	if got := strings.Count(logText, `event="search.text_size_capture"`); got != 1 || !strings.Contains(logText, `captured="2"`) {
		t.Fatalf("secondary metadata capture did not stat exactly the two new paths (events=%d):\n%s", got, logText)
	}
}

func TestNoIgnoreRetainedGenerationPreservesGlobBoundary(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":        "generated/\n",
		"visible.go":        "package visible\n",
		"visible.md":        "visible\n",
		"generated/keep.go": "package generated\n",
		"generated/drop.md": "drop\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "*.go"}
	base, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	scopeViewMemoReset()
	if !scopeViewMemoAdoptTargetSelection(baseArgs, git.Detect(project), base.Entries, metadataForMemoEntries(t, base.Entries)) {
		t.Fatal("glob generation was not sealed")
	}
	derivedArgs := append(append([]string(nil), baseArgs...), "--no-ignore")
	derived, err := resolvedCurrentScopeViewForArgs(derivedArgs)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryRelPaths(derived.Entries), []string{"generated/keep.go", "visible.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("no-ignore escaped glob boundary: got %v want %v", got, want)
	}

	scopeViewMemoReset()
	canonical, err := resolvedCurrentScopeViewForArgs(derivedArgs)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryRelPaths(derived.Entries), entryRelPaths(canonical.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("retained glob membership differs from canonical: got %v want %v", got, want)
	}
}

func TestNoIgnoreRetainedGenerationFeedsLaterFiltersAndOutputShapes(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":               "src/generated/\n",
		"src/visible.go":           "package visible\n// KEEP\n",
		"src/visible.md":           "KEEP docs\n",
		"src/nested/visible.go":    "package nested\n",
		"src/generated/ignored.go": "package ignored\n// KEEP\n",
		"src/generated/ignored.md": "ignored docs\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src"}
	base, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	scopeViewMemoReset()
	if !scopeViewMemoAdoptTargetSelection(baseArgs, git.Detect(project), base.Entries, metadataForMemoEntries(t, base.Entries)) {
		t.Fatal("visible generation was not sealed")
	}
	noIgnoreArgs := append(append([]string(nil), baseArgs...), "--no-ignore")
	if _, err := resolvedCurrentScopeViewForArgs(noIgnoreArgs); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
		want []string
		mode command.EntryMode
	}{
		{name: "only", args: []string{"--only", "*.go"}, want: []string{"src/generated/ignored.go", "src/nested/visible.go", "src/visible.go"}, mode: command.EntryModeFull},
		{name: "only-exclude", args: []string{"--only", "*.go", "--exclude", "*/nested/*"}, want: []string{"src/generated/ignored.go", "src/visible.go"}, mode: command.EntryModeFull},
		{name: "depth", args: []string{"--depth", "1"}, want: []string{"src/visible.go", "src/visible.md"}, mode: command.EntryModeFull},
		{name: "contains", args: []string{"--contains", "KEEP"}, want: []string{"src/generated/ignored.go", "src/visible.go", "src/visible.md"}, mode: command.EntryModeFull},
		{name: "size-recent", args: []string{"--size", "0", "--recent", "5"}, want: []string{"src/generated/ignored.go", "src/generated/ignored.md", "src/nested/visible.go", "src/visible.go", "src/visible.md"}, mode: command.EntryModeFull},
		{name: "paths", args: []string{"--paths"}, want: []string{"src/generated/ignored.go", "src/generated/ignored.md", "src/nested/visible.go", "src/visible.go", "src/visible.md"}, mode: command.EntryModeFull},
		{name: "lines", args: []string{"--lines", "1", "1"}, want: []string{"src/generated/ignored.go", "src/generated/ignored.md", "src/nested/visible.go", "src/visible.go", "src/visible.md"}, mode: command.EntryModeLines},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string(nil), noIgnoreArgs...), tc.args...)
			view, err := resolvedCurrentScopeViewForArgs(args)
			if err != nil {
				t.Fatal(err)
			}
			got := entryRelPaths(view.Entries)
			if tc.name == "size-recent" {
				// Metadata stages deliberately order by size/recency rather than path.
				got = sortedStrings(got)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("membership = %v, want %v", got, tc.want)
			}
			for _, entry := range view.Entries {
				if entry.Mode != tc.mode {
					t.Fatalf("%s mode = %v, want %v", entry.RelPath, entry.Mode, tc.mode)
				}
			}
		})
	}
}

func metadataForMemoEntries(t *testing.T, entries []discovery.Entry) map[string]search.FileMetadata {
	t.Helper()
	out := make(map[string]search.FileMetadata, len(entries))
	for _, entry := range entries {
		info, err := os.Lstat(entry.AbsPath)
		if err != nil {
			t.Fatal(err)
		}
		out[entry.RelPath] = search.FileMetadata{SizeBytes: info.Size(), ModTime: info.ModTime(), Mode: info.Mode()}
	}
	return out
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func TestResolvedCurrentScopeViewDerivesContentStagesFromRetainedParent(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n// TODO keep\n",
		"src/b.go": "package b\n",
		"src/c.md": "TODO docs\n",
	})
	t.Chdir(project)

	tests := []struct {
		name  string
		stage []string
		want  []string
	}{
		{name: "contains", stage: []string{"--contains", "TODO"}, want: []string{"src/a.go", "src/c.md"}},
		{name: "not_contains", stage: []string{"--not-contains", "TODO"}, want: []string{"src/b.go"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scopeViewMemoReset()
			defer scopeViewMemoReset()
			baseArgs := []string{"--quiet", "--print", "src"}
			if _, err := resolvedCurrentScopeViewForArgs(baseArgs); err != nil {
				t.Fatalf("base derivation: %v", err)
			}
			childArgs := append(append([]string(nil), baseArgs...), tc.stage...)
			derived, err := resolvedCurrentScopeViewForArgs(childArgs)
			if err != nil {
				t.Fatalf("derived view: %v", err)
			}
			if got := entryRelPaths(derived.Entries); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("derived entries = %v, want %v", got, tc.want)
			}

			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			key := wd + "\x00\x00" + strings.Join(childArgs, "\x00")
			entry, ok := scopeViewMemoLookup(key)
			if !ok || entry.parentID == 0 {
				t.Fatalf("%s did not retain a derived parent relationship: %+v", tc.name, entry)
			}

			scopeViewMemoReset()
			canonical, err := resolvedCurrentScopeViewForArgs(childArgs)
			if err != nil {
				t.Fatalf("canonical view: %v", err)
			}
			if !reflect.DeepEqual(derived.Entries, canonical.Entries) {
				t.Fatalf("derived %s differs from canonical:\nderived=%+v\ncanonical=%+v", tc.name, derived.Entries, canonical.Entries)
			}
		})
	}
}

func TestResolvedScopeViewMemoClonesPinnedSnippetLines(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n// TODO first\n// TODO second\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"src", "--snippet", "TODO", "0"}
	first, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 1 || len(first.Entries[0].SnippetMatchLines) != 2 {
		t.Fatalf("expected two pinned snippet lines, got %+v", first.Entries)
	}
	wantFirstLine := first.Entries[0].SnippetMatchLines[0]
	first.Entries[0].SnippetMatchLines[0] = 999

	second, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Entries[0].SnippetMatchLines[0]; got != wantFirstLine {
		t.Fatalf("caller mutation leaked into retained snippet offsets: got %d, want %d", got, wantFirstLine)
	}
}

func TestResolvedCurrentScopeViewAdoptsSnippetMembershipAndOffsets(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n// TODO first\n// TODO second\n",
		"src/b.go": "package b\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src"}
	base, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatalf("base derivation: %v", err)
	}
	matched, err := discovery.FilterEntriesBySnippetContent(
		discovery.EnsureEntryAbsPaths(base.Entries, project),
		"TODO",
	)
	if err != nil {
		t.Fatalf("snippet membership scan: %v", err)
	}

	snippetArgs := append(append([]string(nil), baseArgs...), "--snippet", "TODO", "0")
	scopeViewMemoAdoptSnippetStage(snippetArgs, "TODO", matched)
	view, err := resolvedCurrentScopeViewForArgs(snippetArgs)
	if err != nil {
		t.Fatalf("retained snippet derivation: %v", err)
	}
	if got, want := entryRelPaths(view.Entries), []string{"src/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retained snippet entries = %v, want %v", got, want)
	}
	if got, want := view.Entries[0].SnippetMatchLines, []int{2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retained snippet lines = %v, want %v", got, want)
	}

	// A later output/path stage inherits the snippet offsets, while revisiting
	// the sibling base state remains free of snippet-specific observations.
	pathsView, err := resolvedCurrentScopeViewForArgs(append(append([]string(nil), snippetArgs...), "--only", "src/a.go"))
	if err != nil {
		t.Fatalf("snippet subset derivation: %v", err)
	}
	if got, want := pathsView.Entries[0].SnippetMatchLines, []int{2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snippet child lines = %v, want %v", got, want)
	}
	revisited, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatalf("base revisit: %v", err)
	}
	for _, entry := range revisited.Entries {
		if len(entry.SnippetMatchLines) != 0 {
			t.Fatalf("snippet offsets leaked into sibling base entry %q: %v", entry.RelPath, entry.SnippetMatchLines)
		}
	}
}

func TestResolvedCurrentScopeViewDerivesGitStagesFromRetainedStatus(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"clean.txt":    "clean\n",
		"staged.txt":   "before staged\n",
		"unstaged.txt": "before unstaged\n",
		"both.txt":     "before both\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "staged.txt", "after staged\n")
	runGit(t, project, "add", "staged.txt")
	writeProjectFile(t, project, "unstaged.txt", "after unstaged\n")
	writeProjectFile(t, project, "both.txt", "staged both\n")
	runGit(t, project, "add", "both.txt")
	writeProjectFile(t, project, "both.txt", "unstaged both\n")
	writeProjectFile(t, project, "untracked.txt", "untracked\n")
	t.Chdir(project)
	defer scopeViewMemoReset()

	tests := []struct {
		flag string
		want []string
		mode command.EntryMode
	}{
		{flag: "--changed", want: []string{"both.txt", "staged.txt", "unstaged.txt", "untracked.txt"}, mode: command.EntryModeFull},
		{flag: "--staged", want: []string{"both.txt", "staged.txt"}, mode: command.EntryModeFull},
		{flag: "--unstaged", want: []string{"both.txt", "unstaged.txt"}, mode: command.EntryModeFull},
		{flag: "--untracked", want: []string{"untracked.txt"}, mode: command.EntryModeFull},
		{flag: "--changed-diff", want: []string{"both.txt", "staged.txt", "unstaged.txt", "untracked.txt"}, mode: command.EntryModeDiff},
		{flag: "--staged-diff", want: []string{"both.txt", "staged.txt"}, mode: command.EntryModeDiff},
		{flag: "--unstaged-diff", want: []string{"both.txt", "unstaged.txt"}, mode: command.EntryModeDiff},
	}
	for _, tc := range tests {
		t.Run(tc.flag, func(t *testing.T) {
			scopeViewMemoReset()
			baseArgs := []string{"--quiet", "--print", "clean.txt", "staged.txt", "unstaged.txt", "both.txt", "untracked.txt"}
			base, err := resolvedCurrentScopeViewForArgs(baseArgs)
			if err != nil {
				t.Fatalf("base derivation: %v", err)
			}
			childArgs := append(append([]string(nil), baseArgs...), tc.flag)
			derived, err := resolvedCurrentScopeViewForArgs(childArgs)
			if err != nil {
				t.Fatalf("retained %s derivation: %v", tc.flag, err)
			}
			if derived.inventory != base.inventory {
				t.Fatal("Git stage did not retain the parent inventory")
			}
			if got := entryRelPaths(derived.Entries); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("retained %s entries = %v, want %v", tc.flag, got, tc.want)
			}
			for _, entry := range derived.Entries {
				if entry.Mode != tc.mode {
					t.Fatalf("%s entry %q mode = %v, want %v", tc.flag, entry.RelPath, entry.Mode, tc.mode)
				}
			}

			scopeViewMemoReset()
			canonical, err := resolvedCurrentScopeViewForArgs(childArgs)
			if err != nil {
				t.Fatalf("canonical %s derivation: %v", tc.flag, err)
			}
			if !reflect.DeepEqual(derived.Entries, canonical.Entries) {
				t.Fatalf("retained %s differs from canonical:\nretained=%+v\ncanonical=%+v", tc.flag, derived.Entries, canonical.Entries)
			}
		})
	}
}

func TestResolvedCurrentScopeViewAdoptsLiveContentPickerMembership(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n// TODO keep\n",
		"src/b.go": "package b\n",
		"src/c.md": "TODO docs\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src"}
	if _, err := resolvedCurrentScopeViewForArgs(baseArgs); err != nil {
		t.Fatalf("base derivation: %v", err)
	}
	containsArgs := append(append([]string(nil), baseArgs...), "--contains", "TODO")
	scopeViewMemoAdoptContentStage(containsArgs, command.StageContains, "TODO", []string{
		filepath.Join(project, "src", "a.go"),
		filepath.Join(project, "src", "c.md"),
	})

	// Removing the files proves this transition imports the picker's completed
	// membership rather than starting a second content scan after Enter.
	for _, path := range []string{"src/a.go", "src/b.go", "src/c.md"} {
		if err := os.Remove(filepath.Join(project, path)); err != nil {
			t.Fatal(err)
		}
	}
	view, err := resolvedCurrentScopeViewForArgs(containsArgs)
	if err != nil {
		t.Fatalf("retained content derivation: %v", err)
	}
	if got, want := entryRelPaths(view.Entries), []string{"src/a.go", "src/c.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retained content entries = %v, want %v", got, want)
	}

	// A picker subset appends --only after the content stage. Once the imported
	// intermediate state is materialized, the existing path-stage derivation
	// handles that generated command without rediscovery.
	subsetArgs := append(append([]string(nil), containsArgs...), "--only", "src/c.md")
	subset, err := resolvedCurrentScopeViewForArgs(subsetArgs)
	if err != nil {
		t.Fatalf("retained subset derivation: %v", err)
	}
	if got, want := entryRelPaths(subset.Entries), []string{"src/c.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retained subset entries = %v, want %v", got, want)
	}
}

func TestResolvedCurrentScopeViewDerivesAndRetainsMetadataStage(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.go": "package b\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src"}
	if _, err := resolvedCurrentScopeViewForArgs(baseArgs); err != nil {
		t.Fatalf("base derivation: %v", err)
	}
	childArgs := append(append([]string(nil), baseArgs...), "--recent", "1")
	recent, err := resolvedCurrentScopeViewForArgs(childArgs)
	if err != nil {
		t.Fatalf("recent derivation: %v", err)
	}
	if len(recent.Entries) != 1 || recent.Entries[0].ModTime.IsZero() || !recent.Entries[0].SizeKnown {
		t.Fatalf("recent state did not project captured metadata: %+v", recent.Entries)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	key := wd + "\x00\x00" + strings.Join(childArgs, "\x00")
	entry, ok := scopeViewMemoLookup(key)
	if !ok {
		t.Fatal("recent state was not stored")
	}
	if entry.parentID == 0 {
		t.Fatalf("metadata-dependent --recent did not reuse its retained parent: %+v", entry)
	}

	// The recent stage captured both size and mtime for every parent entry.
	// Removing the selected file proves a sibling metadata stage consumes the
	// retained record instead of asking the filesystem again.
	if err := os.Remove(filepath.Join(project, recent.Entries[0].RelPath)); err != nil {
		t.Fatal(err)
	}
	sizeArgs := append(append([]string(nil), baseArgs...), "--size")
	sized, err := resolvedCurrentScopeViewForArgs(sizeArgs)
	if err != nil {
		t.Fatalf("size derivation repeated metadata lookup: %v", err)
	}
	if len(sized.Entries) != 2 || !sized.Entries[0].SizeKnown || !sized.Entries[1].SizeKnown {
		t.Fatalf("size state did not reuse retained metadata: %+v", sized.Entries)
	}
}

func TestDerivedStatesShareInventoryObservations(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.md": "b\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src"}
	base, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	childArgs := append(append([]string(nil), baseArgs...), "--only", "*.go")
	child, err := resolvedCurrentScopeViewForArgs(childArgs)
	if err != nil {
		t.Fatal(err)
	}
	if base.inventory == nil || child.inventory != base.inventory {
		t.Fatal("derived state did not retain its parent's inventory")
	}

	ignoredCalls := 0
	computeIgnored := func() (bool, error) {
		ignoredCalls++
		return true, nil
	}
	firstIgnored, cached, err := base.inventory.cachedScopedIgnored(computeIgnored)
	if err != nil || !firstIgnored || cached {
		t.Fatalf("first ignored observation: value=%v cached=%v err=%v", firstIgnored, cached, err)
	}
	secondIgnored, cached, err := child.inventory.cachedScopedIgnored(computeIgnored)
	if err != nil || !secondIgnored || !cached || ignoredCalls != 1 {
		t.Fatalf("reused ignored observation: value=%v cached=%v calls=%d err=%v", secondIgnored, cached, ignoredCalls, err)
	}

	gitCalls := 0
	computeGit := func(entries []discovery.Entry) (map[string]string, error) {
		gitCalls++
		if len(entries) != 2 {
			t.Fatalf("Git observation must cover the base inventory, got %d entries", len(entries))
		}
		return map[string]string{"src/a.go": "M"}, nil
	}
	firstStatus, cached, err := child.inventory.cachedGitStatus(child.Entries, computeGit)
	if err != nil || cached || firstStatus["src/a.go"] != "M" {
		t.Fatalf("first Git observation: status=%v cached=%v err=%v", firstStatus, cached, err)
	}
	firstStatus["src/a.go"] = "?"
	firstStatus["src/injected.go"] = "M"
	secondStatus, cached, err := base.inventory.cachedGitStatus(base.Entries, computeGit)
	if err != nil || !cached || secondStatus["src/a.go"] != "M" || secondStatus["src/injected.go"] != "" || gitCalls != 1 {
		t.Fatalf("reused Git observation: status=%v cached=%v calls=%d err=%v", secondStatus, cached, gitCalls, err)
	}
	secondStatus["src/a.go"] = "SM"
	thirdStatus, cached, err := base.inventory.cachedGitStatus(base.Entries, computeGit)
	if err != nil || !cached || thirdStatus["src/a.go"] != "M" || gitCalls != 1 {
		t.Fatalf("second returned map mutated cache: status=%v cached=%v calls=%d err=%v", thirdStatus, cached, gitCalls, err)
	}
}

func TestFailedDerivedStateDoesNotAdvanceMemo(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src"}
	if _, err := resolvedCurrentScopeViewForArgs(baseArgs); err != nil {
		t.Fatal(err)
	}
	childArgs := append(append([]string(nil), baseArgs...), "--depth", "99")
	if _, err := resolvedCurrentScopeViewForArgs(childArgs); err == nil {
		t.Fatal("expected depth error")
	}
	_, key := scopeViewMemoKey(childArgs)
	scopeViewMemoMu.Lock()
	_, stored := scopeViewMemoValues[key]
	stateCount := len(scopeViewMemoValues)
	scopeViewMemoMu.Unlock()
	if stored || stateCount != 1 {
		t.Fatalf("failed delta published state: stored=%v states=%d", stored, stateCount)
	}
}

func TestFailedContentDeltaDoesNotAdvanceMemo(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src"}
	if _, err := resolvedCurrentScopeViewForArgs(baseArgs); err != nil {
		t.Fatal(err)
	}
	childArgs := append(append([]string(nil), baseArgs...), "--contains", "(")
	if _, err := resolvedCurrentScopeViewForArgs(childArgs); err == nil {
		t.Fatal("expected invalid PCRE2 pattern error")
	}
	_, key := scopeViewMemoKey(childArgs)
	scopeViewMemoMu.Lock()
	_, stored := scopeViewMemoValues[key]
	stateCount := len(scopeViewMemoValues)
	scopeViewMemoMu.Unlock()
	if stored || stateCount != 1 {
		t.Fatalf("failed content delta published state: stored=%v states=%d", stored, stateCount)
	}
}

func TestScopeViewMemoCheckpointIsReusedAndCleanedWithSession(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.go": "package b\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()

	args := []string{"--quiet", "--print", "src"}
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatalf("scope derivation: %v", err)
	}
	first, owned, err := scopeViewMemoCheckpoint(args, view, nil)
	if err != nil || !owned || first == "" {
		t.Fatalf("first checkpoint: path=%q owned=%v err=%v", first, owned, err)
	}
	second, owned, err := scopeViewMemoCheckpoint(args, view, nil)
	if err != nil || !owned || second != first {
		t.Fatalf("checkpoint was rewritten: first=%q second=%q owned=%v err=%v", first, second, owned, err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("checkpoint is not readable: %v", err)
	}

	view, err = resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range view.Entries {
		if entry.SizeKnown {
			t.Fatalf("opportunistic checkpoint size leaked into ordinary state for %s", entry.RelPath)
		}
	}
	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	stored := scopeViewMemoValues[key]
	scopeViewMemoMu.Unlock()
	for _, entry := range stored.inventory.entries {
		if !entry.SizeKnown {
			t.Fatalf("checkpoint size was not retained internally for %s", entry.RelPath)
		}
	}

	scopeViewMemoReset()
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("session checkpoint survived reset: %v", err)
	}
}

func TestScopeViewMemoReusesTargetPickerMetadataWithoutRestat(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	wantModTime := time.Unix(1_700_000_000, 0)
	scopeViewMemoAdoptTargetMetadata(map[string]search.FileMetadata{
		"src/a.go": {
			SizeBytes: int64(len("package a\n")),
			ModTime:   wantModTime,
			Mode:      0o644,
		},
	})
	args := []string{"--quiet", "--print", "src"}
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 1 || !view.Entries[0].SizeKnown || !view.Entries[0].ModTime.Equal(wantModTime) {
		t.Fatalf("target metadata was not adopted: %+v", view.Entries)
	}
	if err := os.Remove(filepath.Join(project, "src", "a.go")); err != nil {
		t.Fatal(err)
	}
	checkpoint, owned, err := scopeViewMemoCheckpoint(args, view, nil)
	if err != nil || !owned || checkpoint == "" {
		t.Fatalf("checkpoint repeated target metadata lookup: path=%q owned=%v err=%v", checkpoint, owned, err)
	}
	data, err := discovery.ReadCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Entries) != 1 || data.Entries[0].SizeBytes != int64(len("package a\n")) || !data.Entries[0].ModTime.Equal(wantModTime) {
		t.Fatalf("checkpoint lost retained target metadata: %+v", data.Entries)
	}
}

func TestScopeViewMemoAdoptsCommittedTargetMembershipWithoutRediscovery(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"--quiet", "--print", "src"}
	wantTime := time.Unix(1_700_000_000, 0)
	entries := []discovery.Entry{{
		AbsPath:    filepath.Join(project, "src", "a.go"),
		RelPath:    "src/a.go",
		GitVisible: true,
		Mode:       command.EntryModeFull,
	}}
	metadata := map[string]search.FileMetadata{
		"src/a.go": {SizeBytes: int64(len("package a\n")), ModTime: wantTime, Mode: 0o644},
	}
	if !scopeViewMemoAdoptTargetSelection(args, git.Context{}, entries, metadata) {
		t.Fatal("committed target selection was not sealed")
	}
	if err := os.Remove(filepath.Join(project, "src", "a.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "src", "later.go"), []byte("package later\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryRelPaths(view.Entries), []string{"src/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retained membership = %v, want %v", got, want)
	}
	if !view.Entries[0].SizeKnown || !view.Entries[0].ModTime.Equal(wantTime) {
		t.Fatalf("retained metadata = %+v", view.Entries[0])
	}

	pathsView, err := resolvedCurrentScopeViewForArgs(append(append([]string(nil), args...), "--paths"))
	if err != nil {
		t.Fatal(err)
	}
	if got := entryRelPaths(pathsView.Entries); !reflect.DeepEqual(got, []string{"src/a.go"}) {
		t.Fatalf("derived path state changed membership: %v", got)
	}
}

func TestBaseTargetStateUsesCommittedCompactInventoryBeforeJSONCheckpoint(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.go": "package b\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	entries := []discovery.Entry{
		{AbsPath: filepath.Join(project, "src", "a.go"), RelPath: "src/a.go", GitVisible: true, Mode: command.EntryModeFull},
		{AbsPath: filepath.Join(project, "src", "b.go"), RelPath: "src/b.go", GitVisible: true, Mode: command.EntryModeFull},
	}
	metadata := map[string]search.FileMetadata{
		"src/a.go": {SizeBytes: int64(len("package a\n")), Mode: 0o644},
		"src/b.go": {SizeBytes: int64(len("package b\n")), Mode: 0o644},
	}
	inventoryPath := filepath.Join(t.TempDir(), "targets.committed.bin")
	if err := discovery.WriteTargetPreviewInventory(inventoryPath, git.Context{}, []discovery.TargetMatch{
		{Path: "src/a.go", Kind: "file", SizeBytes: int64(len("package a\n")), SizeKnown: true},
		{Path: "src/b.go", Kind: "file", SizeBytes: int64(len("package b\n")), SizeKnown: true},
	}); err != nil {
		t.Fatal(err)
	}

	args := []string{"--quiet", "--print", "src"}
	if !scopeViewMemoAdoptTargetSelection(args, git.Context{}, entries, metadata, inventoryPath) {
		t.Fatal("committed target selection was not sealed")
	}
	state, view, err := startupCurrentScopeStateForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd, tmpdir := startupModifierCurrentScopePreviewCommand(args, state, view)
	if tmpdir != "" {
		t.Fatalf("base target preview created a temporary checkpoint directory: %q", tmpdir)
	}
	if !strings.Contains(cmd, "--internal-target-inventory "+discovery.ShellQuoteArg(inventoryPath)) {
		t.Fatalf("base target preview did not reuse compact inventory: %s", cmd)
	}
	if strings.Contains(cmd, "--internal-prediscovered") {
		t.Fatalf("base target preview unexpectedly used JSON checkpoint: %s", cmd)
	}
	if _, ok := scopeViewMemoCheckpointPath(args); ok {
		t.Fatal("base target preview materialized a JSON checkpoint")
	}

	derivedArgs := append(append([]string(nil), args...), "--only", "*.go")
	derivedState, derivedView, err := startupCurrentScopeStateForArgs(derivedArgs)
	if err != nil {
		t.Fatal(err)
	}
	derivedCmd, derivedTmpdir := startupModifierCurrentScopePreviewCommand(derivedArgs, derivedState, derivedView)
	if derivedTmpdir != "" {
		t.Fatalf("derived retained state used picker-local checkpoint: %q", derivedTmpdir)
	}
	if !strings.Contains(derivedCmd, "--internal-prediscovered") || strings.Contains(derivedCmd, "--internal-target-inventory") {
		t.Fatalf("derived state did not fall back to retained JSON checkpoint: %s", derivedCmd)
	}
}

func TestLazyCommittedTargetInventoryIsReusedAndRemovedWithSession(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()

	args := []string{"--quiet", "--print", "src"}
	entries := []discovery.Entry{{
		AbsPath:    filepath.Join(project, "src", "a.go"),
		RelPath:    "src/a.go",
		GitVisible: true,
		Mode:       command.EntryModeFull,
	}}
	metadata := map[string]search.FileMetadata{
		"src/a.go": {SizeBytes: int64(len("package a\n")), Mode: 0o644},
	}
	if !scopeViewMemoAdoptTargetSelection(args, git.Context{}, entries, metadata) {
		t.Fatal("committed target selection was not sealed")
	}
	state, view, err := startupCurrentScopeStateForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	firstCmd, firstTmpdir := startupModifierCurrentScopePreviewCommand(args, state, view)
	if firstCmd == "" || firstTmpdir != "" {
		t.Fatalf("lazy compact preview = %q, tmpdir=%q", firstCmd, firstTmpdir)
	}
	inventoryPath, ok := scopeViewMemoTargetPreviewInventoryPath(args)
	if !ok {
		t.Fatal("lazy compact inventory was not retained")
	}
	if _, err := os.Stat(inventoryPath); err != nil {
		t.Fatal(err)
	}
	secondCmd, secondTmpdir := startupModifierCurrentScopePreviewCommand(args, state, view)
	if secondCmd != firstCmd || secondTmpdir != "" {
		t.Fatalf("same-state compact inventory was not reused: first=%q second=%q tmpdir=%q", firstCmd, secondCmd, secondTmpdir)
	}

	inventoryDir := filepath.Dir(inventoryPath)
	scopeViewMemoReset()
	if _, err := os.Stat(inventoryDir); !os.IsNotExist(err) {
		t.Fatalf("session-owned target inventory survived reset: %v", err)
	}
}

func TestSealedVanishedMetadataIsNotRepairedByLaterFilesystemState(t *testing.T) {
	project := setupTestProject(t, map[string]string{})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"--quiet", "--print", "gone.go"}
	entries := []discovery.Entry{{
		AbsPath:    filepath.Join(project, "gone.go"),
		RelPath:    "gone.go",
		GitVisible: true,
		Mode:       command.EntryModeFull,
	}}
	metadata := map[string]search.FileMetadata{
		"gone.go": {State: search.FileMetadataVanished, Error: "no such file"},
	}
	if !scopeViewMemoAdoptTargetSelection(args, git.Context{}, entries, metadata) {
		t.Fatal("vanished target observation was not sealed")
	}
	if err := os.WriteFile(filepath.Join(project, "gone.go"), []byte("created later\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pathsView, err := resolvedCurrentScopeViewForArgs(append(append([]string(nil), args...), "--paths"))
	if err != nil {
		t.Fatal(err)
	}
	if got := entryRelPaths(pathsView.Entries); !reflect.DeepEqual(got, []string{"gone.go"}) {
		t.Fatalf("path projection lost vanished membership: %v", got)
	}
	if _, err := resolvedCurrentScopeViewForArgs(append(append([]string(nil), args...), "--size")); err == nil {
		t.Fatal("metadata stage re-statted and repaired a sealed vanished observation")
	}
}

func TestRetainedCheckpointIsSharedAcrossPickerKinds(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.go": "package b\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"--quiet", "--print", "src"}
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	state := startupCurrentScopeState{Known: true, Scopes: view.Scopes}
	modifierCmd, modifierTmpdir := startupModifierCurrentScopePreviewCommand(args, state, view)
	if modifierCmd == "" || modifierTmpdir != "" {
		t.Fatalf("modifier did not use session checkpoint: cmd=%q tmpdir=%q", modifierCmd, modifierTmpdir)
	}
	fileSetCmd, fileSetTmpdir := buildFileSetCheckpointPreview(args, view, "--only")
	if fileSetCmd == "" || fileSetTmpdir != "" {
		t.Fatalf("file-set picker did not use session checkpoint: cmd=%q tmpdir=%q", fileSetCmd, fileSetTmpdir)
	}

	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	checkpointPath := scopeViewMemoValues[key].checkpointPath
	scopeViewMemoMu.Unlock()
	checkpointToken := discovery.ShellQuoteArg(checkpointPath)
	if checkpointPath == "" || !strings.Contains(modifierCmd, checkpointToken) || !strings.Contains(fileSetCmd, checkpointToken) {
		t.Fatalf("picker commands did not share checkpoint %q:\nmodifier=%s\nfile-set=%s", checkpointPath, modifierCmd, fileSetCmd)
	}
}

// TestModifierCheckpointMenuOpenCorpusTiming measures only the work that blocks
// opening filter-menu fzf after primary metadata is complete. The base state
// uses the committed compact target inventory; derived pickers retain the JSON
// checkpoint coverage elsewhere in this file. It is opt-in because the corpus
// is not part of the repository. Run:
//
//	CATCLIP_RUN_CORPUS_TESTS=1 go test ./internal/ui -run '^TestModifierCheckpointMenuOpenCorpusTiming$' -v -count=1 -timeout=10m
func TestModifierCheckpointMenuOpenCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("set CATCLIP_RUN_CORPUS_TESTS=1 to run the external corpus timing test")
	}
	corpus := filepath.Join(os.Getenv("HOME"), "Desktop", "catclip-test-data")
	if _, err := os.Stat(corpus); err != nil {
		t.Skipf("corpus not present: %s", corpus)
	}
	t.Chdir(corpus)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"--quiet", "--print", "."}
	started := time.Now()
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("source discovery: %s (%d entries)", time.Since(started).Round(time.Millisecond), len(view.Entries))

	// Model the target-selector contract: its primary stat completes before
	// target confirmation is allowed to advance into filter>.
	started = time.Now()
	entries, metadataOK := retainedScopeViewEntriesWithMetadata(view)
	metadataElapsed := time.Since(started)
	if !metadataOK {
		t.Fatal("failed to complete retained primary metadata")
	}
	view.Entries = entries
	t.Logf("primary metadata completion (excluded from menu-open interval): %s", metadataElapsed.Round(time.Millisecond))

	metadata := make(map[string]search.FileMetadata, len(view.fileIDs))
	view.inventory.mu.RLock()
	for _, id := range view.fileIDs {
		if uint64(id) >= uint64(len(view.inventory.entries)) || !view.inventory.metadataKnown[id] {
			view.inventory.mu.RUnlock()
			t.Fatal("completed metadata snapshot contains an unknown entry")
		}
		metadata[view.inventory.entries[id].RelPath] = view.inventory.metadata[id]
	}
	view.inventory.mu.RUnlock()
	selectedEntries := cloneDiscoveryEntries(view.Entries)
	gitCtx := view.GitContext
	transitionStarted := time.Now()
	scopeViewMemoReset()
	if !scopeViewMemoAdoptTargetSelection(args, gitCtx, selectedEntries, metadata) {
		t.Fatal("could not adopt the completed target selection")
	}

	stateStarted := time.Now()
	state, retainedView, err := startupCurrentScopeStateForArgs(args)
	stateElapsed := time.Since(stateStarted)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained filter state preparation: %s", stateElapsed.Round(time.Millisecond))

	started = time.Now()
	cmd, tmpdir := startupModifierCurrentScopePreviewCommand(args, state, retainedView)
	menuBlockElapsed := time.Since(started)
	if cmd == "" || tmpdir != "" {
		t.Fatalf("retained checkpoint preview: cmd=%q tmpdir=%q", cmd, tmpdir)
	}
	committedInventoryPath, ok := scopeViewMemoTargetPreviewInventoryPath(args)
	if !ok {
		t.Fatal("fast confirmation did not lazily publish a compact inventory")
	}
	committedInfo, err := os.Stat(committedInventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "--internal-tree-preview --internal-target-inventory "+discovery.ShellQuoteArg(committedInventoryPath)) || strings.Contains(cmd, "--internal-prediscovered") {
		t.Fatalf("filter preview command does not use committed target inventory: %s", cmd)
	}
	if _, ok := scopeViewMemoCheckpointPath(args); ok {
		t.Fatal("base target state unexpectedly wrote a JSON checkpoint")
	}
	t.Logf("filter-menu compact inventory preparation: %s (%d bytes)", menuBlockElapsed.Round(time.Millisecond), committedInfo.Size())
	t.Logf("target-confirmation to picker.Run boundary: %s", time.Since(transitionStarted).Round(time.Millisecond))

	started = time.Now()
	secondCmd, secondTmpdir := startupModifierCurrentScopePreviewCommand(args, state, retainedView)
	if secondCmd != cmd || secondTmpdir != "" {
		t.Fatalf("same-state corpus revisit did not reuse checkpoint: first=%q second=%q tmpdir=%q", cmd, secondCmd, secondTmpdir)
	}
	t.Logf("same-state target inventory hit: %s", time.Since(started).Round(time.Microsecond))

	// Model the common case where metadata and the picker-wide sized inventory
	// completed while the target screen remained open. Confirmation then adopts
	// that existing artifact and performs no committed-inventory write.
	readyInventoryPath := filepath.Join(t.TempDir(), "targets.sized.bin")
	if err := discovery.WriteTargetPreviewEntryInventory(readyInventoryPath, gitCtx, selectedEntries); err != nil {
		t.Fatal(err)
	}
	scopeViewMemoReset()
	readyTransitionStarted := time.Now()
	if !scopeViewMemoAdoptTargetSelection(args, gitCtx, selectedEntries, metadata, readyInventoryPath) {
		t.Fatal("could not adopt metadata-ready target selection")
	}
	readyState, readyView, err := startupCurrentScopeStateForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	readyCmd, readyTmpdir := startupModifierCurrentScopePreviewCommand(args, readyState, readyView)
	if readyCmd == "" || readyTmpdir != "" || !strings.Contains(readyCmd, "--internal-target-inventory") {
		t.Fatalf("metadata-ready transition did not reuse target inventory: cmd=%q tmpdir=%q", readyCmd, readyTmpdir)
	}
	t.Logf("metadata-ready target-confirmation to picker.Run boundary: %s", time.Since(readyTransitionStarted).Round(time.Millisecond))
}

// TestNoIgnoreUnionExpansionCorpusTiming verifies the explicit secondary
// generation against the external full corpus. In particular, multiple target
// roots must still produce one no-ignore rg enumeration and canonical ordered
// membership. Run with the same opt-in as the menu-open corpus test.
func TestNoIgnoreUnionExpansionCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("set CATCLIP_RUN_CORPUS_TESTS=1 to run the external corpus timing test")
	}
	corpus := filepath.Join(os.Getenv("HOME"), "Desktop", "catclip-test-data")
	if _, err := os.Stat(corpus); err != nil {
		t.Skipf("corpus not present: %s", corpus)
	}
	t.Chdir(corpus)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	children, err := os.ReadDir(corpus)
	if err != nil {
		t.Fatal(err)
	}
	targets := make([]string, 0, len(children))
	for _, child := range children {
		if child.IsDir() {
			targets = append(targets, child.Name())
		}
	}
	if len(targets) < 2 {
		t.Skip("corpus does not contain multiple top-level target roots")
	}
	baseArgs := append([]string{"--quiet", "--print"}, targets...)
	base, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	entries, metadataOK := retainedScopeViewEntriesWithMetadata(base)
	if !metadataOK {
		t.Fatal("failed to complete retained primary metadata")
	}
	metadata := make(map[string]search.FileMetadata, len(entries))
	base.inventory.mu.RLock()
	for _, id := range base.fileIDs {
		metadata[base.inventory.entries[id].RelPath] = base.inventory.metadata[id]
	}
	base.inventory.mu.RUnlock()
	scopeViewMemoReset()
	if !scopeViewMemoAdoptTargetSelection(baseArgs, base.GitContext, entries, metadata) {
		t.Fatal("could not adopt the completed target selection")
	}

	benchLog := filepath.Join(t.TempDir(), "no-ignore-bench.log")
	t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", benchLog)
	started := time.Now()
	expanded, err := resolvedCurrentScopeViewForArgs(append(append([]string(nil), baseArgs...), "--no-ignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !sort.SliceIsSorted(expanded.Entries, func(i, j int) bool {
		return expanded.Entries[i].RelPath < expanded.Entries[j].RelPath
	}) {
		t.Fatal("no-ignore corpus generation is not in canonical path order")
	}
	logBytes, err := os.ReadFile(benchLog)
	if err != nil {
		t.Fatal(err)
	}
	noIgnoreRGCalls := 0
	for _, line := range strings.Split(string(logBytes), "\n") {
		if strings.Contains(line, `event="search.rg.files"`) && strings.Contains(line, `no_ignore="true"`) {
			noIgnoreRGCalls++
		}
	}
	if noIgnoreRGCalls != 1 {
		t.Fatalf("no-ignore rg union walks = %d, want 1\n%s", noIgnoreRGCalls, logBytes)
	}
	t.Logf("no-ignore retained expansion: %s (%d -> %d entries, one rg union walk)",
		time.Since(started).Round(time.Millisecond), len(entries), len(expanded.Entries))
}

func TestDerivedCheckpointReusesCapturedParentSizes(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.md": "b\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"--quiet", "--print", "src"}
	base, err := resolvedCurrentScopeViewForArgs(baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	baseCheckpoint, _, err := scopeViewMemoCheckpoint(baseArgs, base, nil)
	if err != nil {
		t.Fatal(err)
	}

	childArgs := append(append([]string(nil), baseArgs...), "--only", "*.go")
	child, err := resolvedCurrentScopeViewForArgs(childArgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(child.Entries) != 1 || child.Entries[0].SizeKnown {
		t.Fatalf("derived state exposed opportunistic size metadata: %+v", child.Entries)
	}
	if err := os.Remove(filepath.Join(project, "src", "a.go")); err != nil {
		t.Fatal(err)
	}
	childCheckpoint, _, err := scopeViewMemoCheckpoint(childArgs, child, nil)
	if err != nil {
		t.Fatalf("derived checkpoint repeated metadata I/O instead of reusing parent capture: %v", err)
	}
	if childCheckpoint == baseCheckpoint {
		t.Fatal("changed state reused its parent's checkpoint artifact")
	}
	data, err := discovery.ReadCheckpoint(childCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Entries) != 1 || !data.Entries[0].SizeKnown {
		t.Fatalf("derived checkpoint did not project retained size: %+v", data.Entries)
	}
}

func TestScopeViewMemoCheckpointSingleFlight(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.go": "package b\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"--quiet", "--print", "src"}
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	const callers = 4
	paths := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			paths[index], _, errs[index] = scopeViewMemoCheckpoint(args, view, nil)
		}(i)
	}
	wg.Wait()
	for i := 0; i < callers; i++ {
		if errs[i] != nil || paths[i] == "" || paths[i] != paths[0] {
			t.Fatalf("checkpoint call %d: path=%q first=%q err=%v", i, paths[i], paths[0], errs[i])
		}
	}
}

func TestScopeViewMemoCheckpointRejectsWrongEntryOrder(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.go": "package b\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"--quiet", "--print", "src"}
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	view.Entries[0], view.Entries[1] = view.Entries[1], view.Entries[0]
	path, owned, err := scopeViewMemoCheckpoint(args, view, nil)
	if err != nil || owned || path != "" {
		t.Fatalf("mismatched view should request fallback: path=%q owned=%v err=%v", path, owned, err)
	}
	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	stored := scopeViewMemoValues[key]
	scopeViewMemoMu.Unlock()
	if stored.checkpointBusy || stored.checkpointPath != "" {
		t.Fatalf("mismatched checkpoint left partial state: %+v", stored)
	}
}

func TestScopeViewMemoCheckpointRejectsWrongOutputProjection(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "one\ntwo\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"src/a.go", "--lines", "2", "2"}
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 1 || view.Entries[0].Mode != command.EntryModeLines {
		t.Fatalf("expected a lines-projected retained view, got %+v", view.Entries)
	}
	view.Entries[0].Mode = command.EntryModeFull
	view.Entries[0].Lines = false
	view.Entries[0].LinesStart = 0
	view.Entries[0].LinesEnd = 0

	path, owned, err := scopeViewMemoCheckpoint(args, view, nil)
	if err != nil || owned || path != "" {
		t.Fatalf("corrupted projection should request fallback: path=%q owned=%v err=%v", path, owned, err)
	}
	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	stored := scopeViewMemoValues[key]
	scopeViewMemoMu.Unlock()
	if stored.checkpointBusy || stored.checkpointPath != "" {
		t.Fatalf("rejected projection left partial checkpoint state: %+v", stored)
	}
}
