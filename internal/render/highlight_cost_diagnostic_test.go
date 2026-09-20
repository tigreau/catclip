package render

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
)

// Reads the exact bounded payload captured by TestSinkHighlightCorpusProfile.
// Cached tokens are an experiment to isolate formatter cost, not a proposed
// production content cache. No production formatter is changed.
func TestHighlightCostDiagnostic(t *testing.T) {
	dir := os.Getenv("CATCLIP_BENCH_ARTIFACT_DIR")
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" || dir == "" {
		t.Skip("opt-in captured payload diagnostic")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "payload-raw.txt"))
	if err != nil {
		t.Fatal(err)
	}
	type block struct {
		path, body string
		lexer      chroma.Lexer
		tokens     []chroma.Token
	}
	var blocks []block
	for rest := raw; len(rest) > 0; {
		idx := bytes.Index(rest, []byte("<file path=\""))
		if idx < 0 {
			break
		}
		rest = rest[idx+len("<file path=\""):]
		end := bytes.IndexByte(rest, '"')
		if end < 0 {
			break
		}
		path := string(rest[:end])
		tagEnd := bytes.IndexByte(rest, '>')
		if tagEnd < 0 {
			break
		}
		rest = rest[tagEnd+1:]
		close := bytes.Index(rest, []byte("</file>"))
		if close < 0 {
			break
		}
		body := string(rest[:close])
		rest = rest[close+len("</file>"):]
		lexer := lexerForPreview(path, body)
		if lexer == nil {
			continue
		}
		it, err := lexer.Tokenise(nil, body)
		if err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, block{path, body, lexer, it.Tokens()})
	}
	style := previewChromaStyle(RenderOptions{PreviewTheme: "fzf-dark"})
	tokens, nbytes := 0, 0
	for _, b := range blocks {
		tokens += len(b.tokens)
		nbytes += len(b.body)
	}
	t.Logf("highlighted_blocks=%d body_bytes=%d tokens=%d", len(blocks), nbytes, tokens)
	for _, b := range blocks {
		var samples []float64
		for i := 0; i < 3; i++ {
			start := time.Now()
			it, err := b.lexer.Tokenise(nil, b.body)
			if err != nil {
				t.Fatal(err)
			}
			for tok := it(); tok != chroma.EOF; tok = it() {
			}
			samples = append(samples, float64(time.Since(start))/float64(time.Millisecond))
		}
		slices.Sort(samples)
		t.Logf("file=%q lexer=%q filename_selected=%t bytes=%d tokens=%d tokenize_ms=%.3f", b.path, b.lexer.Config().Name, lexerForPath(b.path) != nil, len(b.body), len(b.tokens), samples[1])
	}
	type variant struct {
		name string
		run  func()
	}
	variants := []variant{
		{"filename_only_highlight", func() {
			for _, b := range blocks {
				if lexerForPath(b.path) != nil {
					_ = highlightFilePreview(b.path, b.body, RenderOptions{PreviewTheme: "fzf-dark"})
				}
			}
		}},
		{"explicit_config_highlight", func() {
			for _, b := range blocks {
				path := b.path
				if filepath.Base(path) == ".clang-format" {
					path = "config.yaml"
				}
				if lexerForPath(path) != nil {
					_ = highlightFilePreview(path, b.body, RenderOptions{PreviewTheme: "fzf-dark"})
				}
			}
		}},
		{"style_only", func() {
			for range blocks {
				_ = previewChromaStyle(RenderOptions{PreviewTheme: "fzf-dark"})
			}
		}},
		{"tokenize_only", func() {
			for _, b := range blocks {
				it, err := b.lexer.Tokenise(nil, b.body)
				if err != nil {
					t.Fatal(err)
				}
				for tok := it(); tok != chroma.EOF; tok = it() {
				}
			}
		}},
		{"tty256_empty_per_block", func() {
			for range blocks {
				if err := formatters.TTY256.Format(io.Discard, style, chroma.Literator()); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"tty256_cached_tokens", func() {
			for _, b := range blocks {
				if err := formatters.TTY256.Format(io.Discard, style, chroma.Literator(b.tokens...)); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"tty256_one_call_same_tokens", func() {
			// A timing lower bound: production must keep per-file lexer state and
			// wrappers. Combining these token streams is not an implementation.
			var all []chroma.Token
			for _, b := range blocks {
				all = append(all, b.tokens...)
			}
			if err := formatters.TTY256.Format(io.Discard, style, chroma.Literator(all...)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, v := range variants {
		var samples []float64
		for round := 0; round < 5; round++ {
			start := time.Now()
			v.run()
			samples = append(samples, float64(time.Since(start))/float64(time.Millisecond))
		}
		slices.Sort(samples)
		t.Logf("%s median_ms=%.3f range=%.3f..%.3f", v.name, samples[2], samples[0], samples[4])
	}
}
