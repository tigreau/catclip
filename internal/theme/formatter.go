package theme

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
)

// Indexed Chroma formatters rebuild their style/colour-distance tables on every
// Format call. Reuse that preparation across files, without sharing lexers or
// token iterators. These zero-value maps do no work until highlighting starts.
var preparedTTY16 = preparedIndexedFormatter{upstream: formatters.TTY16}
var preparedTTY256 = preparedIndexedFormatter{upstream: formatters.TTY256}

type preparedIndexedFormatter struct {
	upstream chroma.Formatter
	styles   sync.Map // immutable *chroma.Style -> *preparedIndexedStyle
}

type preparedIndexedStyle struct {
	once    sync.Once
	escapes map[chroma.TokenType]string
	err     error
}

func (f *preparedIndexedFormatter) Format(w io.Writer, style *chroma.Style, it chroma.Iterator) error {
	value, ok := f.styles.Load(style)
	if !ok {
		value, _ = f.styles.LoadOrStore(style, &preparedIndexedStyle{})
	}
	prepared := value.(*preparedIndexedStyle)
	prepared.once.Do(func() {
		prepared.escapes, prepared.err = prepareIndexedStyle(f.upstream, style)
	})
	if prepared.err != nil {
		return prepared.err
	}
	for token := it(); token != chroma.EOF; token = it() {
		// Match Chroma's indexed fallback order, including explicitly uncoloured
		// entries: an empty escape is still a found entry.
		formatting, ok := prepared.escapes[token.Type]
		if !ok {
			formatting, ok = prepared.escapes[token.Type.SubCategory()]
			if !ok {
				formatting, ok = prepared.escapes[token.Type.Category()]
				if !ok {
					formatting, ok = prepared.escapes[chroma.Text]
					if !ok {
						formatting = prepared.escapes[chroma.Background]
					}
				}
			}
		}
		if err := writeIndexedToken(w, formatting, token.Value); err != nil {
			return err
		}
	}
	return nil
}

func prepareIndexedStyle(upstream chroma.Formatter, style *chroma.Style) (map[chroma.TokenType]string, error) {
	types := style.Types()
	// Chroma's clearBackground adds this entry even when absent from the style.
	types = append(types, chroma.Background)
	tokens := make([]chroma.Token, len(types))
	for i, kind := range types {
		tokens[i] = chroma.Token{Type: kind, Value: "\x00\n"}
	}
	// Let the pinned upstream formatter choose the actual ANSI sequences once.
	// This preserves its colour-distance metric, inheritance and emphasis without
	// duplicating its colour tables. The marker is private, never source/output.
	var sample strings.Builder
	if err := upstream.Format(&sample, style, chroma.Literator(tokens...)); err != nil {
		return nil, err
	}
	escapes := make(map[chroma.TokenType]string, len(types))
	remaining := sample.String()
	for _, kind := range types {
		line, rest, found := strings.Cut(remaining, "\n")
		prefix, suffix, marker := strings.Cut(line, "\x00")
		if !found || !marker || (suffix != "" && suffix != "\x1b[0m") {
			return nil, fmt.Errorf("unexpected Chroma indexed token format")
		}
		escapes[kind] = prefix
		remaining = rest
	}
	if remaining != "" {
		return nil, fmt.Errorf("unexpected Chroma indexed format suffix")
	}
	return escapes, nil
}

// Preserve Chroma's per-line reset/resume behaviour, including CRLF. Scan for
// newlines directly instead of allocating a regexp match list for each token.
// Unlike upstream writeToken, propagate sink errors and stop consuming tokens.
func writeIndexedToken(w io.Writer, formatting, text string) error {
	if formatting == "" {
		_, err := io.WriteString(w, text)
		return err
	}
	for len(text) > 0 {
		end := strings.IndexByte(text, '\n')
		body, newline := text, ""
		if end >= 0 {
			body, newline, text = text[:end], "\n", text[end+1:]
			if strings.HasSuffix(body, "\r") {
				body, newline = body[:len(body)-1], "\r\n"
			}
		} else {
			text = ""
		}
		for _, part := range []string{formatting, body, "\x1b[0m", newline} {
			if _, err := io.WriteString(w, part); err != nil {
				return err
			}
		}
	}
	return nil
}
