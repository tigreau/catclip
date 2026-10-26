package render

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
)

// HighlightPrintSource shares preview lexer selection and colors, but never
// clips, numbers, normalizes or guesses wrapper boundaries. Its caller bounds
// the source block. Any failure returns the original bytes before output starts.
func HighlightPrintSource(path, content string, diff bool) (result string) {
	result = content
	defer func() {
		if recover() != nil {
			result = content
		}
	}()
	if content == "" || !utf8.ValidString(content) || strings.ContainsAny(content, "\x00\x1b") {
		return content
	}
	if diff {
		return highlightUnifiedDiffPreview(content)
	}
	lexer := lexerForPreview(path, content)
	if lexer == nil {
		return content
	}
	iterator, err := lexer.Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: false}, content)
	if err != nil {
		return content
	}
	return formatPrintSource(content, iterator)
}

func formatPrintSource(content string, iterator chroma.Iterator) (result string) {
	result = content
	defer func() {
		if recover() != nil {
			result = content
		}
	}()
	// Stop pathological ANSI expansion without allocating a whole colored bundle.
	buf := printColorBuffer{limit: len(content)*16 + 4096}
	if err := formatters.TTY256.Format(&buf, previewChromaStyle(RenderOptions{PreviewTheme: previewThemeFzfDark}), iterator); err != nil || buf.err != nil {
		return content
	}
	colored := buf.String()
	if !printColorPreservesText(colored, content) {
		return content
	}
	return colored
}

type printColorBuffer struct {
	bytes.Buffer
	limit int
	err   error
}

func (b *printColorBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.err = errors.New("print color expansion limit")
		return 0, b.err
	}
	if b.err != nil {
		return 0, b.err
	}
	return b.Buffer.Write(p)
}

func (b *printColorBuffer) WriteString(s string) (int, error) {
	if len(s) > b.limit-b.Len() {
		b.err = errors.New("print color expansion limit")
	}
	if b.err != nil {
		return 0, b.err
	}
	return b.Buffer.WriteString(s)
}

// Source containing ESC is excluded above, so every escape here must be a
// generated SGR. Compare without allocating a second uncolored copy.
func printColorPreservesText(colored, plain string) bool {
	j := 0
	for i := 0; i < len(colored); {
		if colored[i] == '\x1b' {
			if i+1 >= len(colored) || colored[i+1] != '[' {
				return false
			}
			i += 2
			for i < len(colored) && (colored[i] >= '0' && colored[i] <= '9' || colored[i] == ';') {
				i++
			}
			if i >= len(colored) || colored[i] != 'm' {
				return false
			}
			i++
			continue
		}
		if j >= len(plain) || colored[i] != plain[j] {
			return false
		}
		i++
		j++
	}
	return j == len(plain)
}
