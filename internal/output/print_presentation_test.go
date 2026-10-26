package output

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
)

var printTestSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func testPrintHighlight(_ string, s string, _ bool) string { return "\x1b[32m" + s + "\x1b[0m" }

func TestPrintPresentationPolicy(t *testing.T) {
	for _, stdout := range []bool{false, true} {
		for _, raw := range []bool{false, true} {
			for _, headless := range []bool{false, true} {
				for _, terminal := range []bool{false, true} {
					for _, term := range []string{"", "xterm-256color", "dumb"} {
						mode := command.OutputModeClipboard
						if stdout {
							mode = command.OutputModeStdout
						}
						got := printColorPolicy(EmitConfig{OutputMode: mode, Raw: raw}, headless, terminal, term)
						want := stdout && !raw && !headless && terminal && term != "dumb"
						if got != want {
							t.Fatalf("policy got %v want %v", got, want)
						}
					}
				}
			}
		}
	}
}

func TestPrintPresentationPipeNeverCallsHighlighter(t *testing.T) {
	unit := PreparedFileUnit{Entry: discovery.Entry{RelPath: "a.go"}}
	unit.Payload, unit.BodyBytes = BuildWrappedPayload("a.go", "", []byte("package main\n"))
	for _, noColor := range []string{"", "1"} {
		t.Setenv("NO_COLOR", noColor)
		for _, headless := range []bool{false, true} {
			var out bytes.Buffer
			stats, err := EmitOutputPlan(EmitConfig{OutputMode: command.OutputModeStdout}, RuntimeEnvironment{}, BuildPlan([]PreparedFileUnit{unit}), &out, platform.ANSIPalette(), PrintPresentation{Headless: headless, Highlight: func(string, string, bool) string { t.Fatal("pipe invoked lexer"); return "" }})
			if err != nil {
				t.Fatal(err)
			}
			if out.String() != string(unit.Payload) || stats.PayloadBytes != int64(len(unit.Payload)) {
				t.Fatal("plain sink changed")
			}
		}
	}
}

func TestPrintPresentationSerializedParity(t *testing.T) {
	root := t.TempDir()
	body := "package main\r\n// </file>\r\nfunc main() {}"
	abs := filepath.Join(root, "a.go")
	if err := os.WriteFile(abs, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	full := PreparedFileUnit{Entry: discovery.Entry{RelPath: "a.go", AbsPath: abs}, BodyBytes: int64(len(body))}
	lines := full
	lines.Entry.Lines, lines.Entry.LinesStart, lines.Entry.LinesEnd = true, 2, 3
	numeric := full
	numeric.Entry.Mode = command.EntryModeSnippet
	numeric.SnippetRanges = []SnippetRange{{Start: 1, End: 1}, {Start: 3, End: 3}}
	smart := numeric
	smart.Payload = []byte("<file path=\"a.go\" lines=\"1-1\">\npackage main\n</file>\n\n<file path=\"a.go\" lines=\"3-3\">\nfunc main() {}\n</file>\n\n")
	diff := full
	diff.Entry.Mode = command.EntryModeDiff
	diff.Payload, diff.BodyBytes = BuildWrappedPayload("a.go", "staged-diff", []byte("@@ -1 +1 @@\n-old\n+new\n"))
	untracked := diff
	untracked.Payload, untracked.BodyBytes = BuildWrappedPayload("a.go", "untracked", []byte(body))
	for _, unit := range []PreparedFileUnit{full, lines, numeric, smart, diff, untracked} {
		var plain, colored bytes.Buffer
		plan := BuildPlan([]PreparedFileUnit{unit})
		if err := WriteOutputPlanPayloadWithoutPrefetch(&plain, EmitConfig{}, plan); err != nil {
			t.Fatal(err)
		}
		calls := 0
		p := &printPresentation{limit: printHighlightLimit, highlight: func(path, s string, isDiff bool) string {
			calls++
			if path != "a.go" {
				t.Fatalf("path=%q", path)
			}
			wantDiff := bytes.Contains(unit.Payload, []byte(`type="staged-diff"`))
			if isDiff != wantDiff {
				t.Fatalf("diff=%v want %v", isDiff, wantDiff)
			}
			if unit.Entry.Lines && strings.HasPrefix(s, "     2\t") {
				t.Fatal("lexer got line numbers")
			}
			return testPrintHighlight(path, s, isDiff)
		}}
		if err := writeOutputPlanPayloadPresented(&colored, EmitConfig{}, plan, false, p); err != nil {
			t.Fatal(err)
		}
		if calls == 0 {
			t.Fatal("no coloring")
		}
		if got := printTestSGR.ReplaceAllString(colored.String(), ""); got != plain.String() {
			t.Fatalf("parity failed mode=%v\n%q\n%q", unit.Entry.Mode, got, plain.String())
		}
		if int64(colored.Len()-plain.Len()) != p.addedBytes {
			t.Fatal("ANSI accounting wrong")
		}
	}
}

func TestPrintPresentationLargeUnitStreamsOnce(t *testing.T) {
	var dest bytes.Buffer
	w := printUnitCapture{dest: &dest, limit: 8}
	for _, chunk := range []string{"1234", "5678", "90123", "456"} {
		if _, err := io.WriteString(&w, chunk); err != nil {
			t.Fatal(err)
		}
		if w.buf.Len() > 8 {
			t.Fatal("capture grew past cap")
		}
	}
	if !w.streaming || dest.String() != "1234567890123456" {
		t.Fatalf("stream=%q", dest.String())
	}
	unit := PreparedFileUnit{Entry: discovery.Entry{RelPath: "a.go"}, Payload: []byte(strings.Repeat("x", 64))}
	var out bytes.Buffer
	p := printPresentation{limit: 32, highlight: func(string, string, bool) string { t.Fatal("oversize unit invoked lexer"); return "" }}
	if err := p.emitEntry(&out, unit, 0, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(unit.Payload) || p.addedBytes != 0 {
		t.Fatal("large fallback changed")
	}
}

type failedPrintWriter struct{}

func (failedPrintWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestPrintPresentationPropagatesWriteError(t *testing.T) {
	w := printUnitCapture{dest: failedPrintWriter{}, limit: 1}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("y")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("err=%v", err)
	}
}
