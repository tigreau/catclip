package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
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
