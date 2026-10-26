package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

func TestPrintHighlightPreservesSource(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		diff, color      bool
	}{
		{"go", "main.go", "package main\nfunc main() {}\n", false, true},
		{"CRLF", "main.go", "package main\r\nfunc main() {}\r\n", false, true},
		{"no final LF", "main.go", "package main", false, true},
		{"unicode", "main.py", "# café λ\nprint('你好')\n", false, true},
		{"unknown", "data.unknown-catclip", "some text\n", false, false},
		{"empty", "main.go", "", false, false},
		{"literal ANSI", "main.go", "// \x1b[31mtest\n", false, false},
		{"invalid UTF8", "main.go", "// \xff\xfe\n", false, false},
		{"NUL", "main.go", "package\x00main\n", false, false},
		{"shebang", "script", "#!/usr/bin/env python3\nprint(1)\n", false, true},
		{"diff", "a.go", "diff --git a/a.go b/a.go\n@@ -1 +1 @@\n-old\n+new\n", true, true},
		{"literal wrapper", "main.go", "// </file>\n// <file path=\"other.py\">\npackage main\n", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := HighlightPrintSource(tc.path, tc.body, tc.diff)
			if tc.color {
				if !strings.Contains(got, "\x1b[") {
					t.Fatal("expected syntax colors")
				}
				if !printColorPreservesText(got, tc.body) {
					t.Fatalf("source changed: %q", got)
				}
			} else if got != tc.body {
				t.Fatalf("plain fallback changed: %q", got)
			}
		})
	}
}

func TestPrintColorRejectsNonPresentationChanges(t *testing.T) {
	for _, changed := range []string{"text\n", "TEXT", "\x1b]0;title\atext", "\x1b[2Jtext", "\x1b[31"} {
		if printColorPreservesText(changed, "text") {
			t.Fatalf("accepted %q", changed)
		}
	}
	if !printColorPreservesText("\x1b[31mtext\x1b[0m", "text") {
		t.Fatal("rejected SGR-only change")
	}
}

func TestPrintHighlightFailureFallsBackBeforeWriting(t *testing.T) {
	const source = "original\r\n"
	if got := formatPrintSource(source, func() chroma.Token { panic("lexer failed") }); got != source {
		t.Fatalf("panic fallback=%q", got)
	}
	called := false
	got := formatPrintSource(source, func() chroma.Token {
		if called {
			return chroma.EOF
		}
		called = true
		return chroma.Token{Type: chroma.Text, Value: "changed\n"}
	})
	if got != source {
		t.Fatalf("normalizing lexer fallback=%q", got)
	}
}

func TestPrintColorBufferBounds(t *testing.T) {
	b := printColorBuffer{limit: 4}
	if _, err := b.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("5")); err == nil || b.Len() != 4 {
		t.Fatal("buffer exceeded cap")
	}
	if _, err := b.WriteString("more"); err == nil || b.Len() != 4 {
		t.Fatal("string write bypassed cap")
	}
}

func BenchmarkPrintHighlight(b *testing.B) {
	for _, tc := range []struct{ path, line string }{
		{"sample.go", "func example() string { return \"hello\" } // comment\n"},
		{"sample.ts", "export const value = (n: number) => `value ${n}`;\n"},
		{"sample.py", "def example(x):\n    return x + 42 # comment\n"},
	} {
		for _, size := range []int{16, 64, 128, 256} {
			body := strings.Repeat(tc.line, size*1024/len(tc.line))
			b.Run(fmt.Sprintf("%s/%dKiB", tc.path, size), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					_ = HighlightPrintSource(tc.path, body, false)
				}
			})
		}
	}
}
