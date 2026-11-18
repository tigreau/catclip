package output

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

func tokenTestUnit(t *testing.T, path, body string) PreparedFileUnit {
	t.Helper()
	abs := filepath.Join(t.TempDir(), path)
	if err := os.WriteFile(abs, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return PreparedFileUnit{Entry: discovery.Entry{AbsPath: abs, RelPath: path, Mode: command.EntryModeFull}, BodyBytes: int64(len(body))}
}

func tokenTestOutput(t *testing.T, plan Plan, limit int64) string {
	t.Helper()
	var out bytes.Buffer
	if err := WriteOutputPlanPayload(&out, EmitConfig{TokenLimit: limit}, plan); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestTokenLimitSourceOnlyAndExactFit(t *testing.T) {
	a := tokenTestUnit(t, "a.go", "one\ntwo\n")
	b := tokenTestUnit(t, "b.go", "ABCD\nEFGH\n")
	plan := BuildPlan([]PreparedFileUnit{a, b})
	out := tokenTestOutput(t, plan, 3)
	for _, want := range []string{"one\ntwo\n</file>", "ABCD\n</file>", `partial="true"`, "Stopped in b.go at line 1 (partial line)", "output truncated"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	limited := plan.WithTokenLimit(3)
	report, err := BuildReportForPlan(git.Context{}, limited, ReportOptions{IncludeTreeMetadata: true})
	if err != nil || report.Tokens != 3 || report.ModeTags["b.go"] != "partial" {
		t.Fatalf("report %#v, %v", report, err)
	}
	if plan.items[1].bodyBytes != b.BodyBytes {
		t.Fatal("mutated original plan")
	}
	fit := BuildPlan([]PreparedFileUnit{a})
	if !fit.SameInstance(fit.WithTokenLimit(2)) || strings.Contains(tokenTestOutput(t, fit, 2), "truncated") {
		t.Fatal("exact fit truncated")
	}
	if !plan.SameInstance(plan.WithTokenLimit(0)) {
		t.Fatal("unlimited path allocated")
	}
	boundary := tokenTestOutput(t, plan, 2)
	if strings.Contains(boundary, `path="b.go"`) || !strings.Contains(boundary, "Stopped before: b.go") {
		t.Fatal(boundary)
	}
}

func TestTokenLimitUTF8AndRaw(t *testing.T) {
	for _, body := range []string{"abc😀rest", "ab€rest", "abcérest", "abcdefgh\nrest"} {
		u := tokenTestUnit(t, "a.txt", body)
		var out bytes.Buffer
		stats, err := EmitOutputPlan(EmitConfig{TokenLimit: 1, Raw: true, OutputMode: command.OutputModeStdout}, RuntimeEnvironment{}, BuildPlan([]PreparedFileUnit{u}), &out, platform.Palette{})
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.Valid(out.Bytes()) || out.Len() > 4 || !strings.HasPrefix(body, out.String()) || stats.TokenLimitNotice == "" {
			t.Fatalf("raw %q, stats %#v", out.String(), stats)
		}
	}
}

func TestTokenLimitUTF8AcrossReadBufferBoundary(t *testing.T) {
	const boundary = 32 * 1024
	for _, char := range []string{"a", "é", "€", "😀"} {
		for offset := -7; offset <= 1; offset++ {
			body := strings.Repeat("a", boundary+offset) + char + "trailing content"
			u := tokenTestUnit(t, "unicode.txt", body)
			for budget := int64(boundary - 8); budget <= boundary+5; budget++ {
				var out bytes.Buffer
				if _, err := emitTokenCutFile(&out, u, true, budget); err != nil {
					t.Fatal(err)
				}
				end := int(budget)
				charStart := boundary + offset
				if end > charStart && end < charStart+len(char) {
					end = charStart
				}
				want := body[:end]
				if !utf8.Valid(out.Bytes()) || out.String() != want {
					t.Fatalf("character %q offset %d budget %d: got %d bytes (valid=%v), want %d", char, offset, budget, out.Len(), utf8.Valid(out.Bytes()), len(want))
				}
			}
		}
	}
}

func TestTokenLimitRangedCRLFBoundary(t *testing.T) {
	for _, lines := range []bool{false, true} {
		for _, raw := range []bool{false, true} {
			u := tokenTestUnit(t, "windows.go", "skip\r\nABC\r\nDEF\r\n")
			u.Entry.Lines, u.Entry.LinesStart, u.Entry.LinesEnd = lines, 2, 3
			if lines {
				u.Entry.Mode = command.EntryModeLines
			} else {
				u.Entry.Mode = command.EntryModeSnippet
				u.SnippetRanges = []SnippetRange{{Start: 2, End: 3}}
			}
			for _, tc := range []struct {
				budget  int64
				body    string
				line    int
				partial bool
			}{
				{3, "ABC", 2, true},
				{4, "ABC\n", 2, false},
				{5, "ABC\nD", 3, true},
			} {
				var out bytes.Buffer
				pos, err := emitTokenCutFile(&out, u, raw, tc.budget)
				if err != nil {
					t.Fatal(err)
				}
				want := tc.body
				if !raw {
					if lines {
						want = "     2\t" + strings.ReplaceAll(want, "\nD", "\n     3\tD")
					}
					if tc.partial {
						want += "\n"
					}
					want = "<file path=\"windows.go\" lines=\"2-\" partial=\"true\">\n" + want + "</file>\n\n"
				}
				if out.String() != want || pos.line != tc.line || pos.partial != tc.partial {
					t.Fatalf("lines=%v raw=%v budget=%d: output %q, position %+v; want %q, line %d, partial=%v", lines, raw, tc.budget, out.String(), pos, want, tc.line, tc.partial)
				}
			}
		}
	}
}

func TestTokenLimitRangesAndPreparedSnippets(t *testing.T) {
	body := "skip\nABCD\nEFGH\nskip\nIJKL\nMNOP\n"
	for _, kind := range []string{"lines", "numeric", "smart", "diff"} {
		t.Run(kind, func(t *testing.T) {
			u := tokenTestUnit(t, "a.go", body)
			u.BodyBytes = 20
			switch kind {
			case "lines":
				u.Entry.Lines = true
				u.Entry.LinesStart = 2
				u.Entry.LinesEnd = 6
				u.Entry.Mode = command.EntryModeLines
				u.BodyBytes = 25
			case "numeric", "smart":
				u.Entry.Mode = command.EntryModeSnippet
				u.SnippetRanges = []SnippetRange{{Start: 2, End: 3}, {Start: 5, End: 6}}
				if kind == "smart" {
					u.Payload = []byte("<file path=\"a.go\" lines=\"2-3\">\nABCD\nEFGH\n</file>\n\n<file path=\"a.go\" lines=\"5-6\">\nIJKL\nMNOP\n</file>\n\n")
				}
			case "diff":
				u.Entry.Mode = command.EntryModeDiff
				u.Payload, u.BodyBytes = BuildWrappedPayload("a.go", "staged-diff", []byte(body))
			}
			out := tokenTestOutput(t, BuildPlan([]PreparedFileUnit{u}), 3)
			want := "line 5 (partial line)"
			if kind == "lines" {
				want = "line 4 (partial line)"
			}
			if kind == "diff" {
				want = "output line 3 (partial line)"
			}
			if !strings.Contains(out, want) || strings.Count(out, "<file ") != strings.Count(out, "</file>") {
				t.Fatal(out)
			}
			if kind == "lines" && !strings.Contains(out, "     2\tABCD") {
				t.Fatal(out)
			}
		})
	}
}

func TestTokenLimitPreparedSourceWrapperLookalikes(t *testing.T) {
	u := PreparedFileUnit{Entry: discovery.Entry{RelPath: "a.go", Mode: command.EntryModeSnippet}, SnippetRanges: []SnippetRange{{Start: 12, End: 15}}, BodyBytes: 40}
	u.Payload = []byte("<file path=\"a.go\" lines=\"12-15\">\nfirst\n</file>\n<file fake>\nlast\n</file>\n\n")
	out := tokenTestOutput(t, BuildPlan([]PreparedFileUnit{u}), 6)
	if !strings.Contains(out, "Stopped in a.go at line 14") || !strings.Contains(out, "first\n</file>\n<file fake") {
		t.Fatal(out)
	}
}

func TestTokenLimitPreviewSkipsHighlightForPartialUnit(t *testing.T) {
	a := tokenTestUnit(t, "a.go", "abc\n")
	b := tokenTestUnit(t, "b.go", "defghijk\n")
	plan := BuildPlan([]PreparedFileUnit{a, b})
	var out bytes.Buffer
	var paths []string
	_, err := WriteOutputPlanPreview(&out, EmitConfig{TokenLimit: 2}, plan, func(w io.Writer, u PreparedFileUnit) (bool, error) {
		paths = append(paths, u.Entry.RelPath)
		return false, nil
	})
	if err != nil || strings.Join(paths, ",") != "a.go" || out.String() != tokenTestOutput(t, plan, 2) {
		t.Fatalf("paths %v, err %v, out %q", paths, err, out.String())
	}
}

type tokenMetadata string

func (m tokenMetadata) EncodedSize() (int64, error) { return int64(len(m)), nil }
func (m tokenMetadata) WriteMetadata(w io.Writer, _ platform.Palette) (int64, error) {
	_, err := io.WriteString(w, string(m))
	return 0, err
}

func TestTokenLimitAcrossMetadataPathsAndFiles(t *testing.T) {
	a := tokenTestUnit(t, "a.go", "ABC\n")
	plan := BuildPlan([]PreparedFileUnit{a})
	path := newPathOutputPlanItem(discovery.Entry{RelPath: "path"})
	plan.items = append(plan.items, path)
	plan.sections = append(plan.sections, PlanSection{kind: SectionKindPaths, items: []PlanItem{path}}, PlanSection{kind: SectionKindMetadata, metadata: tokenMetadata("0123456789"), metadataBytes: 10})
	out := tokenTestOutput(t, plan, 3)
	if !strings.Contains(out, "path\n") || !strings.Contains(out, "012\n[catclip:") || !strings.Contains(out, "metadata report is incomplete") {
		t.Fatal(out)
	}
}

func TestTokenLimitUnavailableDoesNotReportSuccess(t *testing.T) {
	u := tokenTestUnit(t, "gone.go", "abcdefgh\n")
	if err := os.Remove(u.Entry.AbsPath); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err := EmitOutputPlan(EmitConfig{TokenLimit: 1, OutputMode: command.OutputModeStdout}, RuntimeEnvironment{}, BuildPlan([]PreparedFileUnit{u}), &out, platform.Palette{})
	if err != ErrNoAccessibleOutput || out.Len() != 0 {
		t.Fatalf("err %v, output %q", err, out.String())
	}
}

func BenchmarkTokenLimitProjection(b *testing.B) {
	units := make([]PreparedFileUnit, 206000)
	for i := range units {
		units[i] = PreparedFileUnit{Entry: discovery.Entry{RelPath: fmt.Sprintf("source/%d.go", i)}, BodyBytes: 16384}
	}
	plan := BuildPlan(units)
	for _, limit := range []int64{0, 100000, 10000000000} {
		b.Run(fmt.Sprint(limit), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = plan.WithTokenLimit(limit)
			}
		})
	}
}

func TestTokenLimitRangedBlankLines(t *testing.T) {
	u := tokenTestUnit(t, "a.go", "a\n\n\nZ\nmore\n")
	u.Entry.Lines = true
	u.Entry.Mode = command.EntryModeLines
	out := tokenTestOutput(t, BuildPlan([]PreparedFileUnit{u}), 1)
	if !strings.Contains(out, "line 3.") || !strings.Contains(out, "     3\t\n</file>") {
		t.Fatal(out)
	}
}

func TestTokenLimitPrintDoesNotHighlightPartialSource(t *testing.T) {
	a := tokenTestUnit(t, "a.go", "one\n")
	b := tokenTestUnit(t, "b.go", "abcdefgh\n")
	plan := BuildPlan([]PreparedFileUnit{a, b})
	var out bytes.Buffer
	calls := 0
	print := &printPresentation{limit: printHighlightLimit, highlight: func(path, source string, _ bool) string {
		calls++
		if path != "a.go" {
			t.Fatal("highlighted partial source")
		}
		return testPrintHighlight(path, source, false)
	}}
	if err := writeOutputPlanPayloadPresented(&out, EmitConfig{TokenLimit: 2}, plan, false, print); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || printTestSGR.ReplaceAllString(out.String(), "") != tokenTestOutput(t, plan, 2) {
		t.Fatal(out.String())
	}
}

func TestTokenLimitPreviewKeepsVanishedClassification(t *testing.T) {
	a := tokenTestUnit(t, "gone.go", "x\n")
	b := tokenTestUnit(t, "b.go", "abcdefgh\n")
	if err := os.Remove(a.Entry.AbsPath); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	skipped, err := WriteOutputPlanPreview(&out, EmitConfig{TokenLimit: 1}, BuildPlan([]PreparedFileUnit{a, b}), nil)
	if err != nil || len(skipped) != 1 || !skipped[0].Vanished {
		t.Fatalf("%+v, %v", skipped, err)
	}
}

func TestTokenLimitDoesNotScanEntireCutLine(t *testing.T) {
	u := tokenTestUnit(t, "large.go", strings.Repeat("a", 12*1024*1024))
	u.Entry.Lines = true
	u.Entry.Mode = command.EntryModeLines
	out := tokenTestOutput(t, BuildPlan([]PreparedFileUnit{u}), 1)
	if !strings.Contains(out, "     1\taaaa\n</file>") || !strings.Contains(out, "line 1 (partial line)") {
		t.Fatal(out)
	}
}

func BenchmarkTokenLimitCutFile(b *testing.B) {
	path := filepath.Join(b.TempDir(), "large.go")
	body := strings.Repeat(strings.Repeat("a", 79)+"\n", 100000)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		b.Fatal(err)
	}
	unit := PreparedFileUnit{Entry: discovery.Entry{AbsPath: path, RelPath: "large.go", Mode: command.EntryModeFull}, BodyBytes: int64(len(body))}
	b.ReportAllocs()
	b.SetBytes(400000)
	for b.Loop() {
		if _, err := emitTokenCutFile(io.Discard, unit, false, 400000); err != nil {
			b.Fatal(err)
		}
	}
}
