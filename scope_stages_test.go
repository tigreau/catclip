package catclip

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func testStageEntries(paths ...string) []fileEntry {
	entries := make([]fileEntry, 0, len(paths))
	for _, relPath := range paths {
		entries = append(entries, fileEntry{RelPath: relPath})
	}
	return entries
}

func testStageRelPaths(entries []fileEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.RelPath)
	}
	return paths
}

func TestFilterEntriesByStagePatternsMatchesTrailingSlashSubtrees(t *testing.T) {
	entries := testStageEntries(
		"tests",
		"tests/e2e/login.spec.ts",
		"tests/integration/api.test.ts",
		"src/tests/unit.ts",
		"src/features/todos/__tests__/TodoList.test.tsx",
		"src/app.ts",
	)

	filtered, err := filterEntriesByStagePatterns(entries, []string{"tests/"}, true)
	if err != nil {
		t.Fatalf("filterEntriesByStagePatterns returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{
		"tests/e2e/login.spec.ts",
		"tests/integration/api.test.ts",
		"src/tests/unit.ts",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected trailing slash subtree matches %v, got %v", want, got)
	}
}

func TestFilterEntriesByStagePatternsMatchesAnchoredTrailingSlashSubtreesOnlyAtThatPath(t *testing.T) {
	entries := testStageEntries(
		"src/utils/api.ts",
		"src/utils/helpers/array.ts",
		"src/components/utils/classNames.ts",
		"src/features/authentication/utils/validation.ts",
	)

	filtered, err := filterEntriesByStagePatterns(entries, []string{"src/utils/"}, true)
	if err != nil {
		t.Fatalf("filterEntriesByStagePatterns returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{
		"src/utils/api.ts",
		"src/utils/helpers/array.ts",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected anchored trailing slash subtree matches %v, got %v", want, got)
	}
}

func TestFilterEntriesByStagePatternsMatchesBareTrailingSlashDirectorySegments(t *testing.T) {
	entries := testStageEntries(
		"src/utils/api.ts",
		"src/utils/helpers/array.ts",
		"src/components/utils/classNames.ts",
		"src/features/authentication/utils/validation.ts",
		"src/utils.ts",
	)

	filtered, err := filterEntriesByStagePatterns(entries, []string{"utils/"}, true)
	if err != nil {
		t.Fatalf("filterEntriesByStagePatterns returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{
		"src/utils/api.ts",
		"src/utils/helpers/array.ts",
		"src/components/utils/classNames.ts",
		"src/features/authentication/utils/validation.ts",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected bare trailing slash subtree matches %v, got %v", want, got)
	}
}

func TestFilterEntriesByStagePatternsMatchesAnchoredPathsAsExactOrSubtree(t *testing.T) {
	entries := testStageEntries(
		"src/features/authentication/index.ts",
		"src/features/authentication/hooks/useAuth.ts",
		"src/features/profile/index.ts",
	)

	filtered, err := filterEntriesByStagePatterns(entries, []string{"src/features/authentication"}, true)
	if err != nil {
		t.Fatalf("filterEntriesByStagePatterns returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{
		"src/features/authentication/index.ts",
		"src/features/authentication/hooks/useAuth.ts",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected anchored path matches %v, got %v", want, got)
	}
}

func TestFilterEntriesByStagePatternsMatchesBareBasenamesExactly(t *testing.T) {
	entries := testStageEntries(
		"README.md",
		"docs/README.md",
		"README.mdx",
	)

	filtered, err := filterEntriesByStagePatterns(entries, []string{"README.md"}, true)
	if err != nil {
		t.Fatalf("filterEntriesByStagePatterns returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{
		"README.md",
		"docs/README.md",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected exact bare basename matches %v, got %v", want, got)
	}
}

func TestFilterEntriesByStagePatternsMatchesBareDirectorySegments(t *testing.T) {
	entries := testStageEntries(
		"src/components/Button.tsx",
		"src/features/authentication/components/Login.tsx",
		"src/shared/components/Badge.tsx",
		"src/components.tsx",
		"src/ui/Button.tsx",
	)

	filtered, err := filterEntriesByStagePatterns(entries, []string{"components"}, true)
	if err != nil {
		t.Fatalf("filterEntriesByStagePatterns returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{
		"src/components/Button.tsx",
		"src/features/authentication/components/Login.tsx",
		"src/shared/components/Badge.tsx",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected bare directory-segment matches %v, got %v", want, got)
	}
}

func TestFilterEntriesByStagePatternsBareNamesMatchFilesAndDirectorySegments(t *testing.T) {
	entries := testStageEntries(
		"tests",
		"tests/e2e/login.spec.ts",
		"src/tests/unit.ts",
		"src/app.ts",
	)

	filtered, err := filterEntriesByStagePatterns(entries, []string{"tests"}, false)
	if err != nil {
		t.Fatalf("filterEntriesByStagePatterns returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{
		"src/app.ts",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected bare-name exclude to drop exact basename and directory segments, got %v", got)
	}
}

func TestClassifyStageValueNormalizesDirectorySyntax(t *testing.T) {
	cases := []string{"./tests/", "tests//", "src/../tests/"}
	for _, value := range cases {
		matcher, err := classifyStageValue(value)
		if err != nil {
			t.Fatalf("classifyStageValue(%q) returned error: %v", value, err)
		}
		if matcher.kind != stageValueMatchSubtree || matcher.value != "tests" {
			t.Fatalf("expected %q to normalize to subtree tests, got kind=%q value=%q", value, matcher.kind, matcher.value)
		}
	}
}

func TestApplyRecentStageSortsByNewestThenPath(t *testing.T) {
	now := time.Now()
	entries := []fileEntry{
		{RelPath: "src/c.ts", ModTime: now.Add(-1 * time.Hour)},
		{RelPath: "src/a.ts", ModTime: now},
		{RelPath: "src/b.ts", ModTime: now},
	}

	filtered, err := applyRecentStage(entries, "", nil)
	if err != nil {
		t.Fatalf("applyRecentStage returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{"src/a.ts", "src/b.ts", "src/c.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected newest-first path-tiebroken order %v, got %v", want, got)
	}
}

func TestApplyRecentStageAppliesLimit(t *testing.T) {
	now := time.Now()
	limit := 2
	entries := []fileEntry{
		{RelPath: "src/a.ts", ModTime: now.Add(-3 * time.Hour)},
		{RelPath: "src/b.ts", ModTime: now.Add(-1 * time.Hour)},
		{RelPath: "src/c.ts", ModTime: now.Add(-2 * time.Hour)},
	}

	filtered, err := applyRecentStage(entries, "", &limit)
	if err != nil {
		t.Fatalf("applyRecentStage returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{"src/b.ts", "src/c.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected limited newest-first order %v, got %v", want, got)
	}
}

func TestApplyRecentStageBackfillsModTimesFromDisk(t *testing.T) {
	project := t.TempDir()
	now := time.Now()
	for relPath, modTime := range map[string]time.Time{
		"a.txt": now.Add(-2 * time.Hour),
		"b.txt": now.Add(-1 * time.Hour),
	} {
		absPath := filepath.Join(project, relPath)
		if err := os.WriteFile(absPath, []byte(relPath+"\n"), 0o644); err != nil {
			t.Fatalf("write %s failed: %v", relPath, err)
		}
		if err := os.Chtimes(absPath, modTime, modTime); err != nil {
			t.Fatalf("chtimes %s failed: %v", relPath, err)
		}
	}

	resolver := &scopeResolver{cfg: runConfig{WorkingDir: project}}
	entries := []fileEntry{{RelPath: "a.txt"}, {RelPath: "b.txt"}}
	scope := executionScope{Stages: []scopeStage{{Kind: scopeStageRecent}}}

	filtered, err := applyScopeStages(resolver, gitContext{}, scope, entries)
	if err != nil {
		t.Fatalf("applyScopeStages returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{"b.txt", "a.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected disk-backed recent order %v, got %v", want, got)
	}
	if filtered[0].ModTime.IsZero() || filtered[1].ModTime.IsZero() {
		t.Fatalf("expected mod times to be populated, got %+v", filtered)
	}
}
