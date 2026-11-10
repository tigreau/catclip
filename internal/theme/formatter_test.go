package theme

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
)

func formatterTokens() []chroma.Token {
	return []chroma.Token{
		{Type: chroma.Comment, Value: "// comment\r\n\nsecond line\r\n"},
		{Type: chroma.KeywordDeclaration, Value: "func"},
		{Type: chroma.NameFunction, Value: " example"},
		{Type: chroma.Punctuation, Value: "() {\n"},
		{Type: chroma.LiteralStringDouble, Value: "\"value\""},
		{Type: chroma.LiteralStringEscape, Value: "\\n"},
		{Type: chroma.LiteralNumberInteger, Value: "42"},
		{Type: chroma.Name, Value: "\tidentifier\r"},
		{Type: chroma.TokenType(99999), Value: "unknown\n"},
		{Type: chroma.GenericStrong, Value: "bold"},
		{Type: chroma.Error, Value: "!"},
		{Type: chroma.Comment, Value: ""},
		{Type: chroma.Text, Value: "\n\x00no-final-newline"},
	}
}

func formatTokens(t *testing.T, formatter chroma.Formatter, style *chroma.Style, tokens []chroma.Token) string {
	t.Helper()
	var out strings.Builder
	if err := formatter.Format(&out, style, chroma.Literator(tokens...)); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestPreparedIndexedFormatterMatchesChroma(t *testing.T) {
	// Exact ANSI colours have no equidistant nearest-colour ambiguity.
	style := chroma.MustNewStyle("parity", chroma.StyleEntries{
		chroma.Background: "#ansiteal bg:#ansiblack",
		chroma.Keyword:    "bold #ansipurple", chroma.Comment: "italic #ansidarkgreen",
		chroma.LiteralString: "#ansidarkred", chroma.Name: "noinherit",
		chroma.GenericStrong: "bold", chroma.Error: "underline #ansidarkred bg:#ansiblack",
	})
	for _, upstream := range []chroma.Formatter{formatters.TTY16, formatters.TTY256} {
		formatter := &preparedIndexedFormatter{upstream: upstream}
		for _, s := range []*chroma.Style{style, chroma.MustNewStyle("empty", nil)} {
			want := formatTokens(t, upstream, s, formatterTokens())
			for range 3 {
				if got := formatTokens(t, formatter, s, formatterTokens()); got != want {
					t.Fatalf("output changed:\n got %q\nwant %q", got, want)
				}
			}
		}
	}
}

func TestPreparedIndexedPalettesKeepTextAndEmphasis(t *testing.T) {
	// Chroma's map iteration randomly breaks equal-distance 256-colour ties.
	// Ignore the index only here; exact colours are covered above and by the
	// Terminal/Solarized role tests. All other controls and source bytes match.
	indexedColor := regexp.MustCompile(`\x1b\[(38|48);5;[0-9]+m`)
	for _, choice := range Choices() {
		selected, _ := Lookup(choice.ID)
		upstream := formatters.TTY256
		if choice.ID == TerminalID {
			upstream = formatters.TTY16
		}
		formatter := &preparedIndexedFormatter{upstream: upstream}
		for _, style := range []*chroma.Style{selected.Style, selected.Syntax} {
			tokens := formatterTokens()
			for _, kind := range chroma.TokenTypeValues() {
				tokens = append(tokens, chroma.Token{Type: kind, Value: "token\n"})
			}
			got := indexedColor.ReplaceAllString(formatTokens(t, formatter, style, tokens), "<$1>")
			want := indexedColor.ReplaceAllString(formatTokens(t, upstream, style, tokens), "<$1>")
			if got != want {
				t.Fatalf("%s changed text or emphasis", choice.ID)
			}
		}
	}
}

func TestPreparedIndexedStyleOnceUnderConcurrentUse(t *testing.T) {
	var calls atomic.Int32
	formatter := &preparedIndexedFormatter{upstream: chroma.FormatterFunc(func(w io.Writer, s *chroma.Style, it chroma.Iterator) error {
		calls.Add(1)
		return formatters.TTY16.Format(w, s, it)
	})}
	selected, _ := Lookup(TerminalID)
	want := formatTokens(t, formatters.TTY16, selected.Syntax, formatterTokens())
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if got := formatTokens(t, formatter, selected.Syntax, formatterTokens()); got != want {
				t.Error("concurrent formatting changed output")
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("prepared %d times", calls.Load())
	}
}

type failingFormatWriter struct{ err error }

func (w failingFormatWriter) Write([]byte) (int, error) { return 0, w.err }

func TestPreparedIndexedFormatterStopsOnWriteError(t *testing.T) {
	formatter := &preparedIndexedFormatter{upstream: formatters.TTY16}
	selected, _ := Lookup(TerminalID)
	want := errors.New("sink failed")
	reads := 0
	it := func() chroma.Token {
		reads++
		return chroma.Token{Type: chroma.Keyword, Value: "keyword"}
	}
	if err := formatter.Format(failingFormatWriter{want}, selected.Syntax, it); !errors.Is(err, want) || reads != 1 {
		t.Fatalf("error=%v tokens consumed=%d", err, reads)
	}
}

func BenchmarkIndexedFormatter(b *testing.B) {
	for _, id := range []string{TerminalID, "xcode-dark", "catppuccin-mocha"} {
		selected, _ := Lookup(id)
		upstream := formatters.TTY256
		if id == TerminalID {
			upstream = formatters.TTY16
		}
		tokens := formatterTokens()
		for _, mode := range []string{"upstream", "prepared", "first-file"} {
			b.Run(id+"/"+mode, func(b *testing.B) {
				prepared := &preparedIndexedFormatter{upstream: upstream}
				if mode == "prepared" {
					_ = prepared.Format(io.Discard, selected.Syntax, chroma.Literator(tokens...))
				}
				b.ReportAllocs()
				for b.Loop() {
					formatter := chroma.Formatter(prepared)
					if mode == "upstream" {
						formatter = upstream
					} else if mode == "first-file" {
						formatter = &preparedIndexedFormatter{upstream: upstream}
					}
					if err := formatter.Format(io.Discard, selected.Syntax, chroma.Literator(tokens...)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
