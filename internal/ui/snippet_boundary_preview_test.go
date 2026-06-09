package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
)

// ansiEscape matches SGR color sequences chroma's TTY formatter emits.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// snippetBoundaryCommittedOutput returns the exact `<file lines>` output that
// `--snippet PATTERN <choice>` copies for the matched entries — the oracle the
// streamed boundary preview must reproduce byte-for-byte (preview == what gets
// copied). It is uncapped, like the streaming handler: unlike the sink picker
// (whose output can be many whole files at once), snippet output is small per
// file, so there is nothing to cap.
func snippetBoundaryCommittedOutput(view resolvedScopeView, pattern string, choice startupSnippetBoundaryChoice, matched []discovery.Entry) (string, error) {
	scope := snippetBoundaryPreviewScope(view.Scope, pattern, choice)
	entries := append([]discovery.Entry(nil), matched...)
	discovery.StampEntriesWithScopeOutputMode(entries, command.EntryModeSnippet, scope)
	entries = discovery.EnsureEntryAbsPaths(entries, view.Invocation.WorkingDir)
	if len(entries) == 0 {
		return "", nil
	}
	evaluatedScopes := []output.EvaluatedScope{{
		Paths:   scope.Paths,
		Entries: append([]discovery.Entry(nil), entries...),
	}}
	plan, err := output.BuildPlanForResolvedScopes(view.GitContext, []command.ExecutionScope{scope}, evaluatedScopes, entries)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := output.WriteOutputPlanPayloadWithoutPrefetch(&buf, output.EmitConfig{}, plan); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// streamSnippetBoundaryToString builds the source the picker would serialize and
// streams one boundary width to a string, the way RunInternalSnippetBoundaryPreview
// does per focus (but reading the whole stream, with no fzf backpressure to stop it).
func streamSnippetBoundaryToString(tb testing.TB, view resolvedScopeView, pattern string, choice startupSnippetBoundaryChoice, matched []discovery.Entry) string {
	tb.Helper()
	source, err := buildSnippetBoundarySource(view, pattern, matched, nil)
	if err != nil {
		tb.Fatalf("build boundary source: %v", err)
	}
	var buf bytes.Buffer
	if err := streamSnippetBoundaryPreview(source, choice, &buf, false); err != nil {
		tb.Fatalf("stream boundary %q: %v", choice.Key, err)
	}
	return buf.String()
}

// TestSnippetBoundaryStreamMatchesCommit is the correctness + data-integrity net:
// for every boundary choice (block and each numeric width), the streamed preview
// must be byte-identical to what `--snippet PATTERN <choice>` actually copies. That
// equality simultaneously proves the preview shows exactly what gets copied and
// that no matched file is dropped, reordered, or altered by the streaming path.
func TestSnippetBoundaryStreamMatchesCommit(t *testing.T) {
	view := benchSnippetBoundaryView(t, 40)
	matched, err := snippetBoundaryPreviewMatchedEntries(view, "TODO", nil)
	if err != nil {
		t.Fatalf("matched entries: %v", err)
	}
	if len(matched) == 0 {
		t.Fatal("no matched entries")
	}

	for _, choice := range startupSnippetBoundaryChoices {
		got := streamSnippetBoundaryToString(t, view, "TODO", choice, matched)
		want, err := snippetBoundaryCommittedOutput(view, "TODO", choice, matched)
		if err != nil {
			t.Fatalf("committed output %q: %v", choice.Key, err)
		}
		if got != want {
			t.Errorf("boundary %q: streamed preview != committed output\n streamed:\n%s\n committed:\n%s",
				choice.Key, truncateForLog([]byte(got)), truncateForLog([]byte(want)))
		}
	}
}

// TestSnippetBoundaryHighlightIsDisplayOnly proves the syntax highlighting the UI
// applies is a pure display layer: with the ANSI color sequences stripped, the
// highlighted stream is byte-identical to the raw stream for every boundary. So
// the colorized context menu shows exactly the files and lines that get copied —
// highlighting cannot drop, add, or alter content.
func TestSnippetBoundaryHighlightIsDisplayOnly(t *testing.T) {
	view := benchSnippetBoundaryView(t, 40)
	matched, err := snippetBoundaryPreviewMatchedEntries(view, "TODO", nil)
	if err != nil || len(matched) == 0 {
		t.Fatalf("matched: %v", err)
	}
	source, err := buildSnippetBoundarySource(view, "TODO", matched, nil)
	if err != nil {
		t.Fatalf("build source: %v", err)
	}

	for _, choice := range startupSnippetBoundaryChoices {
		var raw, colored bytes.Buffer
		if err := streamSnippetBoundaryPreview(source, choice, &raw, false); err != nil {
			t.Fatalf("raw stream %q: %v", choice.Key, err)
		}
		if err := streamSnippetBoundaryPreview(source, choice, &colored, true); err != nil {
			t.Fatalf("highlighted stream %q: %v", choice.Key, err)
		}
		if !bytes.Contains(colored.Bytes(), []byte("\x1b[")) && raw.Len() > 0 {
			t.Logf("boundary %q: highlighted stream carried no ANSI (lexer may be absent for this type)", choice.Key)
		}
		stripped := ansiEscape.ReplaceAll(colored.Bytes(), nil)
		if !bytes.Equal(stripped, raw.Bytes()) {
			t.Errorf("boundary %q: ANSI-stripped highlight != raw stream — highlighting altered content", choice.Key)
		}
	}
}

// TestSnippetBoundaryStreamNoDropCorpus is the data-integrity proof on real data:
// over the full ~/Desktop/catclip-test-data corpus, the streamed boundary preview
// must be byte-identical to the committed output for every boundary — guaranteeing
// no matched file is dropped, reordered, or altered at scale. Both paths are
// uncapped, so this is an exact comparison. Skips when the corpus is absent.
func TestSnippetBoundaryStreamNoDropCorpus(t *testing.T) {
	corpus := filepath.Join(os.Getenv("HOME"), "Desktop", "catclip-test-data")
	if _, err := os.Stat(corpus); err != nil {
		t.Skipf("corpus not present: %s", corpus)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(corpus); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)

	const pattern = "TODO"
	view, err := resolvedCurrentScopeViewForArgs([]string{"."})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	matched, err := snippetBoundaryPreviewMatchedEntries(view, pattern, nil)
	if err != nil {
		t.Fatalf("matched: %v", err)
	}
	if len(matched) == 0 {
		t.Fatal("no matched entries on corpus")
	}
	t.Logf("comparing streamed preview vs committed output over %d matched files x %d boundaries", len(matched), len(startupSnippetBoundaryChoices))

	for _, choice := range startupSnippetBoundaryChoices {
		got := streamSnippetBoundaryToString(t, view, pattern, choice, matched)
		want, err := snippetBoundaryCommittedOutput(view, pattern, choice, matched)
		if err != nil {
			t.Fatalf("committed %q: %v", choice.Key, err)
		}
		if got != want {
			t.Errorf("boundary %q: streamed != committed on corpus (len got=%d want=%d) — possible dropped/reordered/altered entry",
				choice.Key, len(got), len(want))
		}
	}
}

// TestSnippetBoundaryStreamStopsOnPipeClose proves the streaming handler stops
// (without error) when the consumer closes the pipe mid-stream — the mechanism
// that bounds per-focus I/O to a screenful under fzf. We feed a writer that fails
// after the first file's payload and assert the handler returns nil and never
// reads the remaining bodies into the (capped) output.
func TestSnippetBoundaryStreamStopsOnPipeClose(t *testing.T) {
	view := benchSnippetBoundaryView(t, 40)
	matched, err := snippetBoundaryPreviewMatchedEntries(view, "TODO", nil)
	if err != nil || len(matched) == 0 {
		t.Fatalf("matched: %v", err)
	}
	source, err := buildSnippetBoundarySource(view, "TODO", matched, nil)
	if err != nil {
		t.Fatalf("build source: %v", err)
	}
	choice, _ := snippetBoundaryChoiceByKey("3")
	const failAfter = 1
	w := &failAfterNWriter{failAfter: failAfter}
	if err := streamSnippetBoundaryPreview(source, choice, w, false); err != nil {
		t.Fatalf("stream should swallow pipe-close, got %v", err)
	}
	if w.writes == 0 {
		t.Fatal("expected at least one write before the simulated pipe close")
	}
	// The handler can only learn the pipe is closed by attempting the write that
	// fails, so it stops at failAfter+1 writes. The point is that it stops there —
	// it must NOT stream all matched entries once the consumer has gone away.
	if len(source.Entries) <= failAfter+1 {
		t.Fatalf("need more source entries (%d) than failAfter+1 to prove early stop", len(source.Entries))
	}
	if w.writes > failAfter+1 {
		t.Fatalf("handler kept writing after pipe close: %d writes over %d entries — backpressure not honored",
			w.writes, len(source.Entries))
	}
}

// failAfterNWriter returns a pipe-closed style error once it has been written to
// more than failAfter times. The streaming handler flushes one file's payload per
// Flush, so this simulates fzf closing the preview pipe after failAfter files.
type failAfterNWriter struct {
	failAfter int
	writes    int
}

func (w *failAfterNWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > w.failAfter {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func truncateForLog(b []byte) string {
	const max = 600
	if len(b) > max {
		return string(b[:max]) + "...[truncated]"
	}
	return string(b)
}

// benchSnippetBoundaryView builds a synthetic project where every file carries
// two TODO matches separated by a few lines, then returns a resolved scope view
// over it. Two matches per file means context windows of different widths do
// real, distinct slicing work.
func benchSnippetBoundaryView(tb testing.TB, n int) resolvedScopeView {
	tb.Helper()
	project := tb.TempDir()
	entries := make([]discovery.Entry, 0, n)
	for i := range n {
		rel := fmt.Sprintf("src/pkg%02d/file%05d.go", i%25, i)
		content := fmt.Sprintf(
			"package pkg%02d\n\nfunc File%d() {\n\t// TODO one %d\n\tx := %d\n\ty := x + %d\n\t// TODO two %d\n\t_ = y\n}\n\nvar marker%d = %d\n",
			i%25, i, i, i, i, i, i, i,
		)
		abs := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			tb.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			tb.Fatalf("write %s: %v", rel, err)
		}
		entries = append(entries, discovery.Entry{
			AbsPath:    abs,
			RelPath:    rel,
			SizeBytes:  int64(len(content)),
			SizeKnown:  true,
			GitVisible: true,
			Mode:       command.EntryModeFull,
		})
	}
	return resolvedScopeView{
		Invocation: command.Invocation{WorkingDir: project},
		Scope:      command.ExecutionScope{Targets: []string{"."}},
		Entries:    entries,
	}
}

// TestSnippetBoundaryCorpusTiming measures the streaming boundary-preview path on
// the real ~/Desktop/catclip-test-data corpus (195k files): the picker-open cost
// (one width-independent rg pass + serialize, no body reads) versus the per-focus
// cost (streaming one width's snippet output). It also samples peak heap during a
// full one-width stream to confirm the one-body-at-a-time bound. Skips when the
// corpus is absent. Run:
//
//	go test -run=TestSnippetBoundaryCorpusTiming -v -timeout=300s ./
func TestSnippetBoundaryCorpusTiming(t *testing.T) {
	corpus := filepath.Join(os.Getenv("HOME"), "Desktop", "catclip-test-data")
	if _, err := os.Stat(corpus); err != nil {
		t.Skipf("corpus not present: %s", corpus)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(corpus); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)

	const pattern = "TODO"
	ms := func(d time.Duration) string { return d.Round(time.Millisecond).String() }

	t0 := time.Now()
	view, err := resolvedCurrentScopeViewForArgs([]string{"."})
	if err != nil {
		t.Fatalf("resolve #1: %v", err)
	}
	t.Logf("resolvedCurrentScopeViewForArgs (discovery): %s (%d entries)", ms(time.Since(t0)), len(view.Entries))

	t0 = time.Now()
	matched, err := snippetBoundaryPreviewMatchedEntries(view, pattern, nil)
	if err != nil {
		t.Fatalf("matched: %v", err)
	}
	t.Logf("snippetBoundaryPreviewMatchedEntries (rg):    %s (%d matched)", ms(time.Since(t0)), len(matched))

	// Picker-open cost: the only blocking work before fzf paints. No body reads —
	// just the rg match-line pass and building the in-memory source. (The prior
	// eager path rendered all 8 boundary widths here, ~1.3 s on this corpus.)
	t0 = time.Now()
	source, err := buildSnippetBoundarySource(view, pattern, matched, nil)
	if err != nil {
		t.Fatalf("build source: %v", err)
	}
	t.Logf("buildSnippetBoundarySource (OPEN, no body reads): %s (%d source entries)", ms(time.Since(t0)), len(source.Entries))

	// Per-focus cost: stream one width's output to a discard sink (no fzf
	// backpressure, so this reads every matched body — the upper bound for a
	// focus; in the UI fzf stops reading after a screenful).
	choice, _ := snippetBoundaryChoiceByKey("3")
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	stop := make(chan struct{})
	peak := make(chan uint64, 1)
	go func() {
		var max uint64
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				peak <- max
				return
			default:
				runtime.ReadMemStats(&m)
				if m.HeapInuse > max {
					max = m.HeapInuse
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
	t0 = time.Now()
	if err := streamSnippetBoundaryPreview(source, choice, io.Discard, true); err != nil {
		close(stop)
		t.Fatalf("stream: %v", err)
	}
	focus := time.Since(t0)
	close(stop)
	maxHeap := <-peak
	mib := func(b uint64) uint64 { return b / (1024 * 1024) }
	t.Logf("streamSnippetBoundaryPreview one width (FOCUS, full read): %s", ms(focus))
	t.Logf("  heap-inuse baseline (ambient, before stream): %d MB", mib(base.HeapInuse))
	t.Logf("  heap-inuse peak during stream:                %d MB", mib(maxHeap))
	t.Logf("  marginal heap (one body at a time):           %d MB", (int64(maxHeap)-int64(base.HeapInuse))/(1024*1024))
}

// BenchmarkSnippetBoundaryOpenVsFocus splits the cost the way the UI experiences
// it: open is the blocking work before fzf paints (rg + serialize the source);
// focus is streaming one boundary width. The win over the prior design is that
// open no longer renders all 8 widths' bodies — it does no body reads at all.
//
//	go test -run=^$ -bench=BenchmarkSnippetBoundaryOpenVsFocus -benchmem ./
func BenchmarkSnippetBoundaryOpenVsFocus(b *testing.B) {
	for _, n := range []int{1000, 4000} {
		view := benchSnippetBoundaryView(b, n)
		matched, err := snippetBoundaryPreviewMatchedEntries(view, "TODO", nil)
		if err != nil || len(matched) == 0 {
			b.Fatalf("matched: %v", err)
		}

		b.Run(fmt.Sprintf("n=%d/open_scan_serialize", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				src, err := buildSnippetBoundarySource(view, "TODO", matched, nil)
				if err != nil {
					b.Fatal(err)
				}
				if len(src.Entries) == 0 {
					b.Fatal("no source entries — stamping is wrong")
				}
			}
		})

		source, err := buildSnippetBoundarySource(view, "TODO", matched, nil)
		if err != nil || len(source.Entries) == 0 {
			b.Fatalf("source: %v", err)
		}
		choice, _ := snippetBoundaryChoiceByKey("3")
		b.Run(fmt.Sprintf("n=%d/focus_stream_1_width", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if err := streamSnippetBoundaryPreview(source, choice, io.Discard, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
