package catclip

import (
	"reflect"
	"testing"
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
