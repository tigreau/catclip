package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/search"
)

func TestContentMatchMemoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	checkpointPath := filepath.Join(dir, "scope.json")
	memoPath := contentMatchMemoPath(checkpointPath)
	if memoPath != filepath.Join(dir, "content-match-memo.json") {
		t.Fatalf("unexpected memo path: %s", memoPath)
	}

	writeContentMatchMemo(memoPath, "TODO", []string{"/abs/a.go", "/abs/b.go"})

	memo, ok := readContentMatchMemo(memoPath)
	if !ok {
		t.Fatal("expected memo read to succeed")
	}
	if memo.Pattern != "TODO" {
		t.Fatalf("want pattern TODO, got %q", memo.Pattern)
	}
	if len(memo.AbsPaths) != 2 {
		t.Fatalf("want 2 paths, got %v", memo.AbsPaths)
	}
}

func TestExactContentMatchMemoRejectsStalePattern(t *testing.T) {
	body := []byte(`{"pattern":"TODO","abs_paths":["/abs/a.go"]}`)
	if memo, ok := exactContentMatchMemo(body, "TODO"); !ok || len(memo.AbsPaths) != 1 {
		t.Fatalf("exact memo was not accepted: ok=%v memo=%+v", ok, memo)
	}
	if _, ok := exactContentMatchMemo(body, "FIXME"); ok {
		t.Fatal("memo for a prior query must not be reused")
	}
}

func TestContentMatchMemoMissingFileIsCacheMiss(t *testing.T) {
	dir := t.TempDir()
	_, ok := readContentMatchMemo(filepath.Join(dir, "nothing.json"))
	if ok {
		t.Fatal("missing memo should not report a hit")
	}
}

func TestContentMatchMemoMalformedJSONIsCacheMiss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memo.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ok := readContentMatchMemo(path)
	if ok {
		t.Fatal("malformed memo should not report a hit")
	}
}

func TestContentMatchMemoEmptyPathReturnsMiss(t *testing.T) {
	if got := contentMatchMemoPath(""); got != "" {
		t.Fatalf("empty checkpoint should yield empty memo path, got %q", got)
	}
	if _, ok := readContentMatchMemo(""); ok {
		t.Fatal("empty path should not report a hit")
	}
	// writeContentMatchMemo with empty path is a no-op (does not panic).
	writeContentMatchMemo("", "x", []string{"y"})
}

func TestRestrictEntriesByMemoPrefixExtensionHits(t *testing.T) {
	entries := []discovery.Entry{
		{RelPath: "a.go", AbsPath: "/abs/a.go"},
		{RelPath: "b.go", AbsPath: "/abs/b.go"},
		{RelPath: "c.go", AbsPath: "/abs/c.go"},
	}
	memo := contentMatchMemo{Pattern: "TO", AbsPaths: []string{"/abs/a.go", "/abs/b.go"}}

	got, hit := restrictEntriesByMemo(entries, memo, "TOD")
	if !hit {
		t.Fatal("prefix-extension should be a hit")
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries (a.go, b.go), got %v", got)
	}
	for _, e := range got {
		if e.RelPath != "a.go" && e.RelPath != "b.go" {
			t.Fatalf("unexpected entry %q in restricted set", e.RelPath)
		}
	}
}

func TestRestrictEntriesByMemoSamePatternRestrictsToCachedSet(t *testing.T) {
	entries := []discovery.Entry{
		{RelPath: "a.go", AbsPath: "/abs/a.go"},
		{RelPath: "b.go", AbsPath: "/abs/b.go"},
	}
	memo := contentMatchMemo{Pattern: "X", AbsPaths: []string{"/abs/a.go"}}

	got, hit := restrictEntriesByMemo(entries, memo, "X")
	if !hit {
		t.Fatal("identical pattern should still be a cache hit")
	}
	if len(got) != 1 || got[0].RelPath != "a.go" {
		t.Fatalf("want only a.go, got %v", got)
	}
}

func TestRestrictEntriesByMemoNonPrefixIsMiss(t *testing.T) {
	entries := []discovery.Entry{{RelPath: "a.go", AbsPath: "/abs/a.go"}}
	memo := contentMatchMemo{Pattern: "TODO", AbsPaths: []string{"/abs/a.go"}}

	// "FIX" is unrelated to "TODO" — not a prefix-extension.
	got, hit := restrictEntriesByMemo(entries, memo, "FIX")
	if hit {
		t.Fatal("non-prefix should not be a hit")
	}
	if len(got) != len(entries) {
		t.Fatalf("non-hit should return entries unchanged")
	}

	// Shortening ("TO" from "TODO") is also not a prefix-extension of the memo.
	if _, hit := restrictEntriesByMemo(entries, memo, "TO"); hit {
		t.Fatal("shorter pattern should not be a hit (memo holds the longer prior pattern)")
	}
}

func TestRestrictEntriesByMemoRegexPrefixIsMiss(t *testing.T) {
	entries := []discovery.Entry{
		{RelPath: "foo.go", AbsPath: "/abs/foo.go"},
		{RelPath: "bar.go", AbsPath: "/abs/bar.go"},
	}
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "alternation widens", old: "foo", new: "foo|bar"},
		{name: "quantifier changes token", old: "foo", new: "foo*"},
		{name: "old pattern already regex", old: "foo|b", new: "foo|ba"},
		{name: "escaped literal remains conservative", old: `foo\\.`, new: `foo\\.go`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			memo := contentMatchMemo{Pattern: tc.old, AbsPaths: []string{"/abs/foo.go"}}
			got, hit := restrictEntriesByMemo(entries, memo, tc.new)
			if hit {
				t.Fatalf("regex prefix %q -> %q must not restrict the candidate set", tc.old, tc.new)
			}
			if len(got) != len(entries) {
				t.Fatalf("cache miss changed candidates: got %v want %v", got, entries)
			}
		})
	}
}

func TestRestrictEntriesByMemoIdenticalRegexStillHits(t *testing.T) {
	entries := []discovery.Entry{
		{RelPath: "foo.go", AbsPath: "/abs/foo.go"},
		{RelPath: "bar.go", AbsPath: "/abs/bar.go"},
	}
	memo := contentMatchMemo{Pattern: "foo|bar", AbsPaths: []string{"/abs/bar.go"}}
	got, hit := restrictEntriesByMemo(entries, memo, "foo|bar")
	if !hit || len(got) != 1 || got[0].RelPath != "bar.go" {
		t.Fatalf("identical regex should reuse its exact set: hit=%v got=%v", hit, got)
	}
}

func TestRunInternalContentMatchRegexExtensionDoesNotDropNewFiles(t *testing.T) {
	if _, ok := search.RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	project := setupTestProject(t, map[string]string{
		"src/foo.txt": "foo\n",
		"src/bar.txt": "bar\n",
	})
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	entries := []discovery.Entry{{RelPath: "src/foo.txt"}, {RelPath: "src/bar.txt"}}
	if err := discovery.WriteCheckpoint(checkpointPath, project, discovery.CheckpointData{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	writeContentMatchMemo(contentMatchMemoPath(checkpointPath), "foo", []string{filepath.Join(project, "src", "foo.txt")})

	scope := command.ExecutionScope{
		Targets:  []string{"src"},
		Only:     []string{"*.txt"},
		Contains: "foo|bar",
		Stages: []command.Stage{
			{Kind: command.StageOnly, Values: []string{"*.txt"}},
			{Kind: command.StageContains, Values: []string{"foo|bar"}},
		},
	}
	cfg := prediscoveredCommandConfig{
		CheckpointPath: checkpointPath,
		Invocation:     command.Invocation{WorkingDir: project},
		Scopes:         []command.ExecutionScope{scope},
	}
	var stdout bytes.Buffer
	if err := RunInternalPrediscoveredContentMatchList(cfg, &stdout); err != nil {
		t.Fatal(err)
	}
	if out := stdout.String(); !strings.Contains(out, "src/foo.txt") || !strings.Contains(out, "src/bar.txt") {
		t.Fatalf("regex extension lost files outside the prior literal memo:\n%s", out)
	}
}

func TestRestrictEntriesByMemoEmptyMemoPatternIsMiss(t *testing.T) {
	entries := []discovery.Entry{{RelPath: "a.go", AbsPath: "/abs/a.go"}}
	memo := contentMatchMemo{Pattern: "", AbsPaths: nil}

	_, hit := restrictEntriesByMemo(entries, memo, "T")
	if hit {
		t.Fatal("empty memo pattern should not be a hit")
	}
}

func TestRestrictEntriesByMemoEmptyPriorMatchSetCarriesForward(t *testing.T) {
	entries := []discovery.Entry{{RelPath: "a.go", AbsPath: "/abs/a.go"}}
	memo := contentMatchMemo{Pattern: "TO", AbsPaths: nil}

	got, hit := restrictEntriesByMemo(entries, memo, "TOD")
	if !hit {
		t.Fatal("prefix-extension of empty match set should be a hit")
	}
	if len(got) != 0 {
		t.Fatalf("subset of empty must be empty, got %v", got)
	}
}

func TestMatchedAbsPathsFromRows(t *testing.T) {
	entries := []discovery.Entry{
		{RelPath: "a.go", AbsPath: "/abs/a.go"},
		{RelPath: "b.go", AbsPath: "/abs/b.go"},
		{RelPath: "c.go", AbsPath: "/abs/c.go"},
	}
	rows := []contentMatchRow{
		{RelPath: "a.go"},
		{RelPath: "c.go"},
	}
	got := matchedAbsPathsFromRows(rows, entries)
	want := []string{filepath.Clean("/abs/a.go"), filepath.Clean("/abs/c.go")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestWriteContentMatchMemoIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memo.json")
	writeContentMatchMemo(path, "TODO", []string{"/abs/x.go"})

	// No leftover .tmp after a successful write.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("unexpected leftover tmp file %q after atomic write", e.Name())
		}
	}
}

func TestWriteContentMatchMemoConcurrentWritersPublishWholePair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memo.json")
	const writers = 32
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			pattern := fmt.Sprintf("pattern-%02d", index)
			writeContentMatchMemo(path, pattern, []string{"/abs/" + pattern})
		}(i)
	}
	wg.Wait()

	memo, ok := readContentMatchMemo(path)
	if !ok {
		t.Fatal("concurrent writers did not publish a complete memo")
	}
	if len(memo.AbsPaths) != 1 || memo.AbsPaths[0] != "/abs/"+memo.Pattern {
		t.Fatalf("pattern and paths came from different writers: %+v", memo)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("concurrent writer left temporary file %q", entry.Name())
		}
	}
}

func TestRunDirectContentMatchMemoRehydratesRelOnlyCheckpointEntries(t *testing.T) {
	if _, ok := search.RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	project := setupTestProject(t, map[string]string{
		"src/hit.go":  "package hit\n// TODO\n",
		"src/miss.go": "package miss\n",
	})
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	cfg := prediscoveredCommandConfig{
		CheckpointPath: checkpointPath,
		Invocation: command.Invocation{
			WorkingDir: project,
		},
	}
	scope := command.ExecutionScope{Targets: []string{"src"}, Contains: "TODO"}
	allowed := []discovery.Entry{
		{RelPath: "src/hit.go"},
		{RelPath: "src/miss.go"},
	}
	var stdout bytes.Buffer
	if err := runDirectContentMatch(cfg, scope, allowed, false, &stdout); err != nil {
		t.Fatal(err)
	}
	memo, ok := readContentMatchMemo(contentMatchMemoPath(checkpointPath))
	if !ok {
		t.Fatal("direct match did not publish a memo")
	}
	if memo.Pattern != "TODO" || len(memo.AbsPaths) != 1 || memo.AbsPaths[0] != filepath.Join(project, "src", "hit.go") {
		t.Fatalf("direct memo = %+v", memo)
	}
}

func TestRunInternalNotContainsPublishesFinalSurvivorMemo(t *testing.T) {
	if _, ok := search.RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	project := setupTestProject(t, map[string]string{
		"src/hit.go":  "package hit\n// TODO\n",
		"src/miss.go": "package miss\n",
	})
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	entries := []discovery.Entry{
		{RelPath: "src/hit.go"},
		{RelPath: "src/miss.go"},
	}
	if err := discovery.WriteCheckpoint(checkpointPath, project, discovery.CheckpointData{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	scope := command.ExecutionScope{
		Targets:     []string{"src"},
		NotContains: []string{"TODO"},
		Stages: []command.Stage{{
			Kind:   command.StageNotContains,
			Values: []string{"TODO"},
		}},
	}
	cfg := prediscoveredCommandConfig{
		CheckpointPath: checkpointPath,
		Invocation:     command.Invocation{WorkingDir: project},
		Scopes:         []command.ExecutionScope{scope},
	}
	var stdout bytes.Buffer
	if err := RunInternalPrediscoveredContentMatchList(cfg, &stdout); err != nil {
		t.Fatal(err)
	}
	memo, ok := readContentMatchMemo(contentMatchMemoPath(checkpointPath))
	if !ok {
		t.Fatal("negative match did not publish a memo")
	}
	if memo.Pattern != "TODO" || len(memo.AbsPaths) != 1 || memo.AbsPaths[0] != filepath.Join(project, "src", "miss.go") {
		t.Fatalf("negative memo = %+v", memo)
	}
}
