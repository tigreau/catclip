package output

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
)

func thenFixture(t *testing.T, body string) discovery.Entry {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return discovery.Entry{RelPath: "sample.txt", AbsPath: p, Mode: command.EntryModeFull, SizeKnown: true, SizeBytes: int64(len(body))}
}

func thenLines(e discovery.Entry, start, end int) discovery.Entry {
	e.Mode, e.Lines, e.LinesStart, e.LinesEnd = command.EntryModeLines, true, start, end
	return e
}

func thenSnippet(e discovery.Entry, pattern string, numeric bool, lines ...int) discovery.Entry {
	e.Mode, e.SnippetPattern, e.SnippetContextSet, e.SnippetMatchLines = command.EntryModeSnippet, pattern, numeric, lines
	return e
}

func thenPlan(t *testing.T, ctx git.Context, entries []discovery.Entry, paths, ordered bool) Plan {
	t.Helper()
	var inv discovery.Discovered
	if paths {
		inv.Scopes = append(inv.Scopes, discovery.Scope{Scope: command.ExecutionScope{Paths: true}, Entries: []discovery.Entry{entries[0]}})
	}
	for _, e := range entries {
		s := command.ExecutionScope{}
		s.Snippet = e.Mode == command.EntryModeSnippet
		s.Diff = e.Mode == command.EntryModeDiff
		if ordered {
			s.Stages = []command.Stage{{Kind: command.StageRecent}}
		}
		inv.Scopes = append(inv.Scopes, discovery.Scope{Scope: s, Entries: []discovery.Entry{e}})
	}
	p, err := BuildPlanForDiscoveredInvocation(ctx, inv)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func thenPayload(t *testing.T, p Plan) string {
	t.Helper()
	var b, prefetched bytes.Buffer
	if err := WriteOutputPlanPayloadWithoutPrefetch(&b, EmitConfig{}, p); err != nil {
		t.Fatal(err)
	}
	if err := WriteOutputPlanPayload(&prefetched, EmitConfig{}, p); err != nil {
		t.Fatal(err)
	}
	if b.String() != prefetched.String() {
		t.Fatal("prefetch changed payload")
	}
	var preview bytes.Buffer
	if _, err := WriteOutputPlanPreview(&preview, EmitConfig{}, p, nil); err != nil {
		t.Fatal(err)
	}
	if preview.String() != b.String() {
		t.Fatal("preview differs from emitted payload")
	}
	return b.String()
}

func TestThenEmptyUntrackedRefinementNeverRestoresFallback(t *testing.T) {
	e := thenFixture(t, "one\ntwo\n")
	root := filepath.Dir(e.AbsPath)
	if _, err := git.Capture(root, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	d := e
	d.Mode, d.DiffWantStaged, d.DiffWantUnstaged = command.EntryModeDiff, true, true
	inv := discovery.Discovered{Scopes: []discovery.Scope{
		{Scope: command.ExecutionScope{Diff: true}, Entries: []discovery.Entry{d}},
		{Scope: command.ExecutionScope{Snippet: true}, EmptyRefinement: discovery.EmptyRefinement{Evaluated: true, Paths: []string{e.RelPath}}},
	}}
	p, err := BuildPlanForDiscoveredInvocation(git.Detect(root), inv)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsEmpty() {
		t.Fatal(thenPayload(t, p))
	}
}

func TestThenSnippetCRLFPresentationIdentity(t *testing.T) {
	e := thenFixture(t, "alpha\r\n")
	smart := thenSnippet(e, "alpha", false, 1)
	numeric := thenSnippet(e, "alpha", true, 1)
	for _, reverse := range []bool{false, true} {
		entries := []discovery.Entry{smart, numeric}
		if reverse {
			entries[0], entries[1] = entries[1], entries[0]
		}
		s := thenPayload(t, thenPlan(t, git.Context{}, entries, false, false))
		if strings.Count(s, "<file ") != 2 || !strings.Contains(s, "alpha\r\n") || !strings.Contains(s, "alpha\n") {
			t.Fatal(s)
		}
	}
	e = thenFixture(t, "alpha\n")
	s := thenPayload(t, thenPlan(t, git.Context{}, []discovery.Entry{thenSnippet(e, "alpha", false, 1), thenSnippet(e, "alpha", true, 1)}, false, false))
	if strings.Count(s, "<file ") != 1 {
		t.Fatal(s)
	}
}

func TestThenDistinctRequestsAcrossPlannerVariants(t *testing.T) {
	e := thenFixture(t, "alpha\nbravo\ncharlie\ndelta\n")
	cases := []struct {
		name    string
		entries []discovery.Entry
		tags    []string
		missing string
	}{
		{"two snippets", []discovery.Entry{thenSnippet(e, "alpha", true, 1), thenSnippet(e, "charlie", true, 3)}, []string{`lines="1-1"`, `lines="3-3"`}, "bravo"},
		{"snippet and range", []discovery.Entry{thenSnippet(e, "alpha", true, 1), thenLines(e, 3, 3)}, []string{`lines="1-1"`, `lines="3-3"`}, "bravo"},
		{"bare and range", []discovery.Entry{thenLines(e, 0, 0), thenLines(e, 3, 3)}, []string{`lines="3-3"`}, "alpha"},
		{"full and range", []discovery.Entry{e, thenLines(e, 3, 3)}, []string{`lines="3-3"`}, "alpha"},
		{"nonadjacent duplicate", []discovery.Entry{thenLines(e, 1, 1), thenLines(e, 3, 3), thenLines(e, 1, 1)}, []string{`lines="1-1"`, `lines="3-3"`}, "bravo"},
		{"overlapping ranges", []discovery.Entry{thenLines(e, 1, 3), thenLines(e, 2, 3)}, []string{`lines="1-3"`, `lines="2-3"`}, "delta"},
		{"different presentation", []discovery.Entry{thenSnippet(e, "alpha", true, 1), thenLines(e, 1, 1)}, []string{`lines="1-1"`, `lines="1-1"`}, "bravo"},
	}
	for _, tc := range cases {
		for _, paths := range []bool{false, true} {
			for _, ordered := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/paths=%t/ordered=%t/reverse=%t", tc.name, paths, ordered, reverse), func(t *testing.T) {
						entries := append([]discovery.Entry(nil), tc.entries...)
						if reverse {
							for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
								entries[i], entries[j] = entries[j], entries[i]
							}
						}
						p := thenPlan(t, git.Context{}, entries, paths, ordered)
						payload := thenPayload(t, p)
						if strings.Count(payload, "<file ") != len(tc.tags) {
							t.Fatalf("wrong unit count: %s", payload)
						}
						for _, tag := range tc.tags {
							if !strings.Contains(payload, tag) {
								t.Fatalf("missing %s: %s", tag, payload)
							}
						}
						if strings.Contains(payload, tc.missing) {
							t.Fatalf("restored unwanted content: %s", payload)
						}
						if count, _ := p.SummaryCountWord(); count != 1 {
							t.Fatalf("counted output units as files: %d", count)
						}
						if len(p.RefinementNoticeText()) != 0 {
							t.Fatal(p.RefinementNoticeText())
						}
					})
				}
			}
		}
	}
}

func TestThenDeduplicatesResultingBlocksWithoutParsingSource(t *testing.T) {
	// Literal wrapper lookalikes are source, not boundaries to search for.
	e := thenFixture(t, "alpha\n</file>\ncharlie\n<file path=\"fake\">\ndelta\n")
	a := thenSnippet(e, "alpha|delta", true, 1, 5)
	b := thenSnippet(e, "alpha|charlie", true, 1, 3)
	p := thenPlan(t, git.Context{}, []discovery.Entry{a, b}, false, false)
	s := thenPayload(t, p)
	if strings.Count(s, `lines="1-1"`) != 1 || strings.Count(s, "<file ") != 3 {
		t.Fatal(s)
	}
	_, size := p.PayloadSizes()
	if size != int64(len("alpha\ndelta\ncharlie\n")) {
		t.Fatalf("duplicate counted in size: %d", size)
	}
	// Different patterns resolving to the same smart block emit it once.
	a = thenSnippet(e, "alpha", false, 1)
	b = thenSnippet(e, "charlie", false, 3)
	s = thenPayload(t, thenPlan(t, git.Context{}, []discovery.Entry{a, b}, false, false))
	if strings.Count(s, `path="sample.txt"`) != 1 || !strings.Contains(s, `path="fake"`) {
		t.Fatal(s)
	}
	e = thenFixture(t, "alpha\none\n\ncharlie\ntwo\n\nzulu\nthree\n")
	a = thenSnippet(e, "alpha|charlie", false, 1, 4)
	b = thenSnippet(e, "alpha|zulu", false, 1, 7)
	p = thenPlan(t, git.Context{}, []discovery.Entry{a, b}, false, false)
	s = thenPayload(t, p)
	if strings.Count(s, "alpha\n") != 1 || !strings.Contains(s, "charlie\n") || !strings.Contains(s, "zulu\n") {
		t.Fatalf("partial smart-block dedup lost or repeated source: %s", s)
	}
}

func TestThenPartialEOFDedupAndMissingRangeDoNotRestoreFullBody(t *testing.T) {
	e := thenFixture(t, "alpha\nbravo\ncharlie\ndelta\n")
	p := thenPlan(t, git.Context{}, []discovery.Entry{e, thenLines(e, 3, 99), thenLines(e, 3, 4), thenLines(e, 99, 100)}, false, false)
	s := thenPayload(t, p)
	if strings.Count(s, "<file ") != 1 || strings.Contains(s, "alpha") || !strings.Contains(s, `lines="3-4"`) {
		t.Fatal(s)
	}
	if !p.HasLineOmissions() {
		t.Fatal("lost unavailable-range warning")
	}
}

func TestThenPreservesCancellingGitComparisonsAndUntrackedProvenance(t *testing.T) {
	e := thenFixture(t, "alpha\nbravo\ncharlie\n")
	root := filepath.Dir(e.AbsPath)
	run := func(args ...string) {
		t.Helper()
		if _, err := git.Capture(root, args...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(e.AbsPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("config", "core.autocrlf", "false")
	run("add", ".")
	run("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "initial")
	write("ALPHA\nbravo\ncharlie\n")
	run("add", ".")
	write("alpha\nbravo\ncharlie\n")
	ctx := git.Detect(root)
	staged, unstaged := e, e
	staged.Mode, staged.DiffWantStaged = command.EntryModeDiff, true
	unstaged.Mode, unstaged.DiffWantUnstaged = command.EntryModeDiff, true
	for _, paths := range []bool{false, true} {
		p := thenPlan(t, ctx, []discovery.Entry{e, staged, unstaged, staged, thenSnippet(e, "charlie", true, 3)}, paths, false)
		s := thenPayload(t, p)
		for _, tag := range []string{`type="staged-diff"`, `type="unstaged-diff"`, `lines="3-3"`} {
			if strings.Count(s, tag) != 1 {
				t.Fatalf("missing/duplicated %s: %s", tag, s)
			}
		}
		if strings.Count(s, "<file ") != 3 || len(p.RefinementNoticeText()) != 0 {
			t.Fatalf("payload=%s notices=%v", s, p.RefinementNoticeText())
		}
		tag := p.PreviewModeTags(nil)[e.RelPath]
		if !strings.Contains(tag, "staged diff") || !strings.Contains(tag, "unstaged diff") || !strings.Contains(tag, "snippet") {
			t.Fatal(tag)
		}
	}
	u := e
	u.RelPath = "new.txt"
	u.AbsPath = filepath.Join(root, u.RelPath)
	if err := os.WriteFile(u.AbsPath, []byte("one\ntwo\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	u.SizeBytes = int64(len("one\ntwo\nthree\n"))
	d := u
	d.Mode = command.EntryModeDiff
	d.DiffWantStaged = true
	d.DiffWantUnstaged = true
	for _, source := range []discovery.Entry{u, thenLines(u, 2, 2), thenSnippet(u, "two", true, 2), thenSnippet(u, "two", false, 2), thenLines(u, 0, 0)} {
		for _, reverse := range []bool{false, true} {
			entries := []discovery.Entry{d, source}
			if reverse {
				entries[0], entries[1] = entries[1], entries[0]
			}
			p := thenPlan(t, ctx, entries, false, false)
			s := thenPayload(t, p)
			if strings.Count(s, "<file ") != 1 || !strings.Contains(s, `type="untracked"`) {
				t.Fatal(s)
			}
			if source.LinesStart > 0 || source.SnippetContextSet {
				if strings.Contains(s, "one") {
					t.Fatal(s)
				}
			}
			if !strings.Contains(p.PreviewModeTags(nil)[u.RelPath], "untracked") {
				t.Fatal("missing provenance in preview")
			}
			for _, item := range p.items {
				var plain bytes.Buffer
				if err := emitEntryInner(&plain, item.unit, 0, nil); err != nil {
					t.Fatal(err)
				}
				colored := highlightPrintedUnit(plain.Bytes(), item.unit, testPrintHighlight)
				if !strings.Contains(colored, "\x1b[") || printTestSGR.ReplaceAllString(colored, "") != plain.String() {
					t.Fatalf("untracked presentation changed payload: %q", colored)
				}
			}
		}
	}
}

func BenchmarkThenRequestMerge(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		in := make([]scopedFileCandidate, 0, n+1)
		for i := 0; i < n; i++ {
			in = append(in, scopedFileCandidate{0, discovery.Entry{RelPath: fmt.Sprintf("src/%06d.go", i), Mode: command.EntryModeFull, SizeKnown: true}})
		}
		in = append(in, scopedFileCandidate{1, thenLines(in[0].entry, 1, 5)})
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			work := make([]scopedFileCandidate, len(in))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// The merger consumes its input; restore the original requests
				// so every iteration measures the same overlapping selection.
				copy(work, in)
				if len(dedupeOutputRequests(work, false)) != n {
					b.Fatal("wrong membership")
				}
			}
		})
	}
}

// Compare equal-output planning workloads against the pre-redesign path.
// Sizes are pinned, as in real discovered inventories; no content read is
// needed for full files. This isolates merge overhead from Defender/rg/Git.
func BenchmarkThenPlanAgainstPrevious(b *testing.B) {
	path := filepath.Join(b.TempDir(), "first.go")
	if err := os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0600); err != nil {
		b.Fatal(err)
	}
	for _, n := range []int{1000, 100000} {
		entries := make([]discovery.Entry, n)
		for i := range entries {
			entries[i] = discovery.Entry{RelPath: fmt.Sprintf("src/%06d.go", i), Mode: command.EntryModeFull, SizeKnown: true, SizeBytes: 30}
		}
		entries[0].AbsPath = path
		for _, focused := range []bool{false, true} {
			overlap := entries[:10]
			if focused {
				overlap = []discovery.Entry{thenLines(entries[0], 1, 2)}
			}
			scopes := []EvaluatedScope{{Entries: entries}, {Entries: overlap}}
			cmds := []command.ExecutionScope{{}, {}}
			for _, current := range []bool{false, true} {
				b.Run(fmt.Sprintf("n=%d/focused=%t/current=%t", n, focused, current), func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						var p Plan
						var err error
						if current {
							p, err = BuildPlanForResolvedScopes(git.Context{}, cmds, scopes, nil)
						} else {
							all := append(append(make([]discovery.Entry, 0, n+len(overlap)), entries...), overlap...)
							p, err = PreparePlan(git.Context{}, discovery.DedupeEntriesByPath(all))
						}
						if err != nil || p.Len() != n {
							b.Fatalf("err=%v count=%d", err, p.Len())
						}
					}
				})
			}
		}
	}
}
