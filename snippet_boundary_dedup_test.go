package catclip

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

// TestSnippetBoundaryMatchSetEquivalence is the data-integrity gate for Fix 2
// (deduping the boundary-flow content scans). It proves the cheap, reusable
// match set — filterEntriesByContent over the already-resolved view entries —
// yields exactly the same path set as contentMatchPathsForArgs, which the
// committed --only coverage check (startupStageSelectionCoversAll) relies on.
// If these ever diverge, deduping would change which files get copied, so Fix 2
// must NOT merge the scans.
func TestSnippetBoundaryMatchSetEquivalence(t *testing.T) {
	project := t.TempDir()
	files := map[string]string{
		"a/match1.go":   "package a\n\n// TODO one\nfunc A() {}\n",
		"a/nomatch.go":  "package a\n\nfunc B() {}\n",
		"b/match2.go":   "package b\n\nvar x = 1 // TODO two\n",
		"b/c/match3.go": "package c\n\n// TODO three\n",
		"b/c/plain.txt": "nothing here\n",
		"d/match4.md":   "# doc\n\nTODO four\n",
	}
	for rel, content := range files {
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)

	currentArgs := []string{"."}
	const pattern = "TODO"

	view, err := resolvedCurrentScopeViewForArgs(currentArgs)
	if err != nil {
		t.Fatalf("resolve view: %v", err)
	}
	matched, err := filterEntriesByContent(ensureEntryAbsPaths(view.Entries, view.Invocation.WorkingDir), pattern)
	if err != nil {
		t.Fatalf("filterEntriesByContent: %v", err)
	}
	aPaths := make([]string, 0, len(matched))
	for _, e := range matched {
		aPaths = append(aPaths, normalizeRelPath(e.RelPath))
	}
	sort.Strings(aPaths)

	bPaths, err := contentMatchPathsForArgs(currentArgs, "--snippet", pattern)
	if err != nil {
		t.Fatalf("contentMatchPathsForArgs: %v", err)
	}
	for i := range bPaths {
		bPaths[i] = normalizeRelPath(bPaths[i])
	}
	sort.Strings(bPaths)

	if len(aPaths) == 0 {
		t.Fatal("expected some matches")
	}
	if len(aPaths) != len(bPaths) {
		t.Fatalf("match-set size differs: filterEntriesByContent=%d (%v) contentMatchPathsForArgs=%d (%v)", len(aPaths), aPaths, len(bPaths), bPaths)
	}
	for i := range aPaths {
		if aPaths[i] != bPaths[i] {
			t.Errorf("match-set differs at %d: %q vs %q\n full A=%v\n full B=%v", i, aPaths[i], bPaths[i], aPaths, bPaths)
		}
	}
}

// benchDedupProject builds a synthetic project under cwd with n files, ~40% of
// which contain the pattern, for measuring the boundary-flow content scans.
func benchDedupProject(tb testing.TB, n int) (project string, restore func()) {
	tb.Helper()
	project = tb.TempDir()
	for i := 0; i < n; i++ {
		rel := filepath.Join("src", "pkg"+strconv.Itoa(i%20), "f"+strconv.Itoa(i)+".go")
		body := "package p\n\nfunc F() {}\n"
		if i%5 < 2 { // ~40% match
			body = "package p\n\n// TODO mark " + strconv.Itoa(i) + "\nfunc F() {}\n"
		}
		abs := filepath.Join(project, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	orig, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		tb.Fatal(err)
	}
	return project, func() { os.Chdir(orig) }
}

// BenchmarkSnippetBoundaryFlowScans compares the boundary flow's content-scan
// cost for a specific (non-"all") selection: the current path runs the content
// scan 3x (onlyValues coverage + preview matched entries + final coverage); the
// deduped path runs it once and reuses the result.
func BenchmarkSnippetBoundaryFlowScans(b *testing.B) {
	for _, n := range []int{1000, 4000} {
		_, restore := benchDedupProject(b, n)
		currentArgs := []string{"."}
		const pattern = "TODO"

		b.Run("n="+strconv.Itoa(n)+"/before_3scans", func(b *testing.B) {
			for range b.N {
				if _, err := contentMatchPathsForArgs(currentArgs, "--snippet", pattern); err != nil {
					b.Fatal(err)
				}
				view, err := resolvedCurrentScopeViewForArgs(currentArgs)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := filterEntriesByContent(ensureEntryAbsPaths(view.Entries, view.Invocation.WorkingDir), pattern); err != nil {
					b.Fatal(err)
				}
				if _, err := contentMatchPathsForArgs(currentArgs, "--snippet", pattern); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("n="+strconv.Itoa(n)+"/after_1scan", func(b *testing.B) {
			for range b.N {
				view, err := resolvedCurrentScopeViewForArgs(currentArgs)
				if err != nil {
					b.Fatal(err)
				}
				matched, err := filterEntriesByContent(ensureEntryAbsPaths(view.Entries, view.Invocation.WorkingDir), pattern)
				if err != nil {
					b.Fatal(err)
				}
				_ = matched // relpaths derived from this single scan feed both coverage and preview
			}
		})
		restore()
	}
}

// TestSnippetBoundaryLazyRoundTripsSource proves the JSON round-trip the real
// flow performs (picker open serializes the source to a tmpdir; the per-focus
// handler reads it back) does not alter the streamed output: streaming a boundary
// from the round-tripped source must be byte-identical to streaming from the
// in-memory source, for every boundary.
func TestSnippetBoundaryLazyRoundTripsSource(t *testing.T) {
	view := benchSnippetBoundaryView(t, 40)
	matched, err := snippetBoundaryPreviewMatchedEntries(view, "TODO", nil)
	if err != nil {
		t.Fatalf("matched: %v", err)
	}
	if len(matched) == 0 {
		t.Fatal("no matches")
	}

	source, err := buildSnippetBoundarySource(view, "TODO", matched, nil)
	if err != nil {
		t.Fatalf("build source: %v", err)
	}
	if len(source.Entries) == 0 {
		t.Fatal("no source entries")
	}

	tmp := filepath.Join(t.TempDir(), "source.json")
	if err := writeSnippetBoundarySource(tmp, source); err != nil {
		t.Fatal(err)
	}
	loaded, err := readSnippetBoundarySource(tmp)
	if err != nil {
		t.Fatal(err)
	}

	for _, choice := range startupSnippetBoundaryChoices {
		var inMem, roundTrip bytes.Buffer
		if err := streamSnippetBoundaryPreview(source, choice, &inMem, false); err != nil {
			t.Fatalf("stream in-memory %s: %v", choice.Key, err)
		}
		if err := streamSnippetBoundaryPreview(loaded, choice, &roundTrip, false); err != nil {
			t.Fatalf("stream round-trip %s: %v", choice.Key, err)
		}
		if !bytes.Equal(inMem.Bytes(), roundTrip.Bytes()) {
			t.Errorf("boundary %q: round-tripped source streamed different bytes than in-memory source", choice.Key)
		}
	}
}
