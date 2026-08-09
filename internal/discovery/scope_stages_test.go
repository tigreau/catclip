package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
)

// intPtr is a tiny test helper for command.Stage.Limit literals.
// Same shape as internal/cli/helpers.go's parser-side dup.
func intPtr(v int) *int { return &v }

func testStageEntries(paths ...string) []Entry {
	entries := make([]Entry, 0, len(paths))
	for _, relPath := range paths {
		entries = append(entries, Entry{RelPath: relPath})
	}
	return entries
}

func testStageRelPaths(entries []Entry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.RelPath)
	}
	return paths
}

func TestFileEntryDefaultSizeUnknown(t *testing.T) {
	var entry Entry
	if entry.SizeKnown {
		t.Fatal("default Entry should leave SizeKnown false")
	}
	if entry.SizeBytes != 0 {
		t.Fatalf("default Entry SizeBytes = %d, want 0", entry.SizeBytes)
	}
}

func TestMergeFileEntryCarriesKnownSize(t *testing.T) {
	dst := Entry{RelPath: "a.txt"}
	incoming := Entry{RelPath: "a.txt", SizeBytes: 42, SizeKnown: true}

	MergeFileEntry(&dst, incoming)

	if !dst.SizeKnown || dst.SizeBytes != 42 {
		t.Fatalf("MergeFileEntry did not carry known size: %+v", dst)
	}
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

func TestFilterEntriesByStagePatternsGlobbedTrailingSlashMatchesNoFiles(t *testing.T) {
	entries := testStageEntries(
		"internal/cli/main.go",
		"internal/output/emit.go",
	)

	filtered, err := filterEntriesByStagePatterns(entries, []string{"*/"}, true)
	if err != nil {
		t.Fatalf("filterEntriesByStagePatterns returned error: %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("globbed trailing slash must not use literal subtree semantics: %#v", filtered)
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
		matcher, err := ClassifyStageValue(value)
		if err != nil {
			t.Fatalf("ClassifyStageValue(%q) returned error: %v", value, err)
		}
		if matcher.kind != stageValueMatchSubtree || matcher.value != "tests" {
			t.Fatalf("expected %q to normalize to subtree tests, got kind=%q value=%q", value, matcher.kind, matcher.value)
		}
	}
}

func TestFilterEntriesByStagePatternsCharacterizesLegacyGlobGrammar(t *testing.T) {
	entries := testStageEntries(
		"src/api/root.go",
		"src/api/v1/nested.go",
		"pkg/src/api/other.go",
		"src/utils/a.ts",
		"utils/root.ts",
		"config/root.ts",
		"src/config/nested.ts",
		"matcher/config-file.ts",
		"build/root.txt",
		"a/build/nested.txt",
		"keep.txt",
	)

	tests := []struct {
		name        string
		pattern     string
		keepMatches bool
		want        []string
	}{
		{
			name:        "slash glob is cwd anchored but star crosses folders",
			pattern:     "src/api/*",
			keepMatches: true,
			want:        []string{"src/api/root.go", "src/api/v1/nested.go"},
		},
		{
			name:        "question mark can consume separator",
			pattern:     "src?utils/*",
			keepMatches: true,
			want:        []string{"src/utils/a.ts"},
		},
		{
			name:        "bracket class can consume separator",
			pattern:     "src[/]utils/*",
			keepMatches: true,
			want:        []string{"src/utils/a.ts"},
		},
		{
			name:        "slashless glob matches basename or full path",
			pattern:     "config*",
			keepMatches: true,
			want:        []string{"config/root.ts", "matcher/config-file.ts"},
		},
		{
			name:        "literal subtree exclude floats",
			pattern:     "build/",
			keepMatches: false,
			want:        []string{"src/api/root.go", "src/api/v1/nested.go", "pkg/src/api/other.go", "src/utils/a.ts", "utils/root.ts", "config/root.ts", "src/config/nested.ts", "matcher/config-file.ts", "keep.txt"},
		},
		{
			name:        "globbed subtree spelling anchors",
			pattern:     "build/*",
			keepMatches: false,
			want:        []string{"src/api/root.go", "src/api/v1/nested.go", "pkg/src/api/other.go", "src/utils/a.ts", "utils/root.ts", "config/root.ts", "src/config/nested.ts", "matcher/config-file.ts", "a/build/nested.txt", "keep.txt"},
		},
		{
			name:        "glob normalization does not clean parent traversal",
			pattern:     "src/../utils/*",
			keepMatches: true,
			want:        []string{},
		},
		{
			name:        "literal normalization cleans to floating subtree",
			pattern:     "src/../utils/",
			keepMatches: true,
			want:        []string{"src/utils/a.ts", "utils/root.ts"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEntries, err := filterEntriesByStagePatterns(entries, []string{tt.pattern}, tt.keepMatches)
			if err != nil {
				t.Fatalf("filterEntriesByStagePatterns(%q) returned error: %v", tt.pattern, err)
			}
			if got := testStageRelPaths(gotEntries); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("pattern %q paths = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestApplyRecentStageSortsByNewestThenPath(t *testing.T) {
	now := time.Now()
	entries := []Entry{
		{RelPath: "src/c.ts", ModTime: now.Add(-1 * time.Hour)},
		{RelPath: "src/a.ts", ModTime: now},
		{RelPath: "src/b.ts", ModTime: now},
	}

	filtered, err := ApplyRecentStage(entries, "", nil)
	if err != nil {
		t.Fatalf("ApplyRecentStage returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{"src/a.ts", "src/b.ts", "src/c.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected newest-first path-tiebroken order %v, got %v", want, got)
	}
}

func TestApplyRecentStageAppliesLimit(t *testing.T) {
	now := time.Now()
	limit := 2
	entries := []Entry{
		{RelPath: "src/a.ts", ModTime: now.Add(-3 * time.Hour)},
		{RelPath: "src/b.ts", ModTime: now.Add(-1 * time.Hour)},
		{RelPath: "src/c.ts", ModTime: now.Add(-2 * time.Hour)},
	}

	filtered, err := ApplyRecentStage(entries, "", &limit)
	if err != nil {
		t.Fatalf("ApplyRecentStage returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{"src/b.ts", "src/c.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected limited newest-first order %v, got %v", want, got)
	}
}

func TestApplyRecentStageBackfillsModTimesFromDisk(t *testing.T) {
	project := t.TempDir()
	now := time.Now()
	contents := map[string]string{
		"a.txt": "a.txt\n",
		"b.txt": "b.txt\n",
	}
	for relPath, modTime := range map[string]time.Time{
		"a.txt": now.Add(-2 * time.Hour),
		"b.txt": now.Add(-1 * time.Hour),
	} {
		absPath := filepath.Join(project, relPath)
		if err := os.WriteFile(absPath, []byte(contents[relPath]), 0o644); err != nil {
			t.Fatalf("write %s failed: %v", relPath, err)
		}
		if err := os.Chtimes(absPath, modTime, modTime); err != nil {
			t.Fatalf("chtimes %s failed: %v", relPath, err)
		}
	}

	resolver := &Resolver{Cfg: command.Invocation{WorkingDir: project}}
	entries := []Entry{{RelPath: "a.txt"}, {RelPath: "b.txt"}}
	scope := command.ExecutionScope{Stages: []command.Stage{{Kind: command.StageRecent}}}

	filtered, err := applyScopeStages(resolver, git.Context{}, scope, entries)
	if err != nil {
		t.Fatalf("applyScopeStages returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{"b.txt", "a.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected disk-backed recent order %v, got %v", want, got)
	}
	if filtered[0].ModTime.IsZero() || filtered[1].ModTime.IsZero() {
		t.Fatalf("expected mod times to be populated, got %+v", filtered)
	}
	for _, entry := range filtered {
		if !entry.SizeKnown {
			t.Fatalf("expected --recent stat to populate SizeKnown for %s", entry.RelPath)
		}
		if got, want := entry.SizeBytes, int64(len(contents[entry.RelPath])); got != want {
			t.Fatalf("%s SizeBytes = %d, want %d", entry.RelPath, got, want)
		}
	}
}

func TestApplyDepthStageKeepsOnlyPathsAtOrAboveRequestedDepth(t *testing.T) {
	entries := testStageEntries(
		"README.md",
		"src/main.ts",
		"src/components/Button.tsx",
		"src/components/forms/Login.tsx",
	)

	filtered, err := ApplyDepthStage(entries, 2)
	if err != nil {
		t.Fatalf("ApplyDepthStage returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{
		"README.md",
		"src/main.ts",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected depth-filtered paths %v, got %v", want, got)
	}
}

func TestApplySizeStageSortsLargestFirst(t *testing.T) {
	entries := []Entry{
		{RelPath: "small.txt", SizeBytes: 512, SizeKnown: true},
		{RelPath: "large.txt", SizeBytes: 4 * 1024, SizeKnown: true},
		{RelPath: "same-a.txt", SizeBytes: 2 * 1024, SizeKnown: true},
		{RelPath: "same-b.txt", SizeBytes: 2 * 1024, SizeKnown: true},
	}

	filtered, err := ApplySizeStage(entries, "", nil)
	if err != nil {
		t.Fatalf("ApplySizeStage returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{
		"large.txt",
		"same-a.txt",
		"same-b.txt",
		"small.txt",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected size order %v, got %v", want, got)
	}
}

func TestApplySizeStageFiltersMinimumAndRange(t *testing.T) {
	entries := []Entry{
		{RelPath: "zero.txt", SizeBytes: 0, SizeKnown: true},
		{RelPath: "half.txt", SizeBytes: 512, SizeKnown: true},
		{RelPath: "one.txt", SizeBytes: 1024, SizeKnown: true},
		{RelPath: "two.txt", SizeBytes: 2048, SizeKnown: true},
		{RelPath: "three.txt", SizeBytes: 3072, SizeKnown: true},
	}

	minOnly, err := ApplySizeStage(entries, "", []int{1})
	if err != nil {
		t.Fatalf("ApplySizeStage min returned error: %v", err)
	}
	if got, want := testStageRelPaths(minOnly), []string{"three.txt", "two.txt", "one.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected min filter %v, got %v", want, got)
	}

	inRange, err := ApplySizeStage(entries, "", []int{0, 1})
	if err != nil {
		t.Fatalf("ApplySizeStage range returned error: %v", err)
	}
	if got, want := testStageRelPaths(inRange), []string{"one.txt", "half.txt", "zero.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected range filter %v, got %v", want, got)
	}
}

func TestApplySizeStageStatsMissingSizes(t *testing.T) {
	project := t.TempDir()
	contents := map[string]string{
		"a.txt": "a",
		"b.txt": strings.Repeat("b", 2048),
	}
	for relPath, content := range contents {
		absPath := filepath.Join(project, relPath)
		if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s failed: %v", relPath, err)
		}
	}

	resolver := &Resolver{Cfg: command.Invocation{WorkingDir: project}}
	entries := []Entry{{RelPath: "a.txt"}, {RelPath: "b.txt"}}
	scope := command.ExecutionScope{Stages: []command.Stage{{Kind: command.StageSize}}}

	filtered, err := applyScopeStages(resolver, git.Context{}, scope, entries)
	if err != nil {
		t.Fatalf("applyScopeStages returned error: %v", err)
	}

	if got, want := testStageRelPaths(filtered), []string{"b.txt", "a.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected disk-backed size order %v, got %v", want, got)
	}
	for _, entry := range filtered {
		if !entry.SizeKnown {
			t.Fatalf("expected --size stat to populate SizeKnown for %s", entry.RelPath)
		}
		if got, want := entry.SizeBytes, int64(len(contents[entry.RelPath])); got != want {
			t.Fatalf("%s SizeBytes = %d, want %d", entry.RelPath, got, want)
		}
	}
}

func TestApplyDepthStageRejectsValuesGreaterThanCurrentScopeMaxDepth(t *testing.T) {
	entries := testStageEntries(
		"README.md",
		"src/main.ts",
		"src/components/Button.tsx",
	)

	_, err := ApplyDepthStage(entries, 4)
	if err == nil {
		t.Fatal("expected out-of-range depth error")
	}
	if !strings.Contains(err.Error(), "current scope max depth 3") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyScopeStagesKeepsDepthOrderingSemanticWithRecent(t *testing.T) {
	now := time.Now()
	resolver := &Resolver{Cfg: command.Invocation{WorkingDir: ""}}
	entries := []Entry{
		{RelPath: "README.md", ModTime: now.Add(-3 * time.Hour)},
		{RelPath: "src/main.ts", ModTime: now.Add(-2 * time.Hour)},
		{RelPath: "src/components/Button.tsx", ModTime: now.Add(-1 * time.Hour)},
	}

	scope := command.ExecutionScope{Stages: []command.Stage{
		{Kind: command.StageRecent, Limit: intPtr(1)},
		{Kind: command.StageDepth, Limit: intPtr(2)},
	}}

	_, err := applyScopeStages(resolver, git.Context{}, scope, entries)
	if err == nil {
		t.Fatal("expected depth error when recent-then-depth leaves no files")
	}
	if !strings.Contains(err.Error(), "no files at depth 2") {
		t.Fatalf("expected depth-specific error, got: %v", err)
	}

	scope = command.ExecutionScope{Stages: []command.Stage{
		{Kind: command.StageDepth, Limit: intPtr(2)},
		{Kind: command.StageRecent, Limit: intPtr(1)},
	}}
	filtered, err := applyScopeStages(resolver, git.Context{}, scope, entries)
	if err != nil {
		t.Fatalf("applyScopeStages returned error: %v", err)
	}
	if got, want := testStageRelPaths(filtered), []string{"src/main.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected depth-then-recent order %v, got %v", want, got)
	}
}

// Every command.StageKind must have a stageApplierTable entry so the
// pipeline can dispatch the kind without falling through to the
// unknown-kind no-op branch. Adding a new kind without registering
// an applier is a silent regression — the new flag would parse
// successfully but apply no filter at runtime. This test catches
// that at compile-extend time. To register an output-shape-only kind
// (no file-set filtering), wire it to applyDiffStageCase or one of
// the no-op aliases.
func TestStageApplierTableCoversEveryStageKind(t *testing.T) {
	allKinds := []command.StageKind{
		command.StageOnly,
		command.StageExclude,
		command.StageRecent,
		command.StageSize,
		command.StageDepth,
		command.StageContains,
		command.StageSnippet,
		command.StageChanged,
		command.StageStaged,
		command.StageUnstaged,
		command.StageUntracked,
		command.StageChangedDiff,
		command.StageStagedDiff,
		command.StageUnstagedDiff,
		command.StageDiff,
		command.StagePaths,
		command.StageLines,
	}
	for _, kind := range allKinds {
		if _, ok := stageApplierTable[kind]; !ok {
			t.Errorf("stageApplierTable missing entry for %q", string(kind))
		}
	}
}
