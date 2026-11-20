package unpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func edit(start, end int, body string) LineEdit {
	return LineEdit{Start: start, End: end, Body: []byte(body)}
}

func TestApplyLineEdits(t *testing.T) {
	threeFuncs := "func A() {\n\ta()\n}\n\nfunc B() {\n\tb()\n}\n\nfunc C() {\n\tc()\n}\n"
	for _, tt := range []struct {
		name  string
		local string
		edits []LineEdit
		want  string
	}{
		{
			// The worked example from the plan: B grows from 3 to 9 lines;
			// C moves down unchanged.
			name:  "middle function grows",
			local: threeFuncs,
			edits: []LineEdit{edit(5, 7, "func B() {\n\tb1()\n\tb2()\n\tb3()\n\tb4()\n\tb5()\n\tb6()\n\tb7()\n}\n")},
			want:  "func A() {\n\ta()\n}\n\nfunc B() {\n\tb1()\n\tb2()\n\tb3()\n\tb4()\n\tb5()\n\tb6()\n\tb7()\n}\n\nfunc C() {\n\tc()\n}\n",
		},
		{
			// All three edited in one reply with their original numbers:
			// applied bottom-up, so each keeps pointing at its own code.
			name:  "three edits with original numbers",
			local: threeFuncs,
			edits: []LineEdit{edit(1, 3, "func A() {\n\ta2()\n}\n"), edit(5, 7, "func B() {\n\tb()\n\tb()\n}\n"), edit(9, 11, "func C() {}\n")},
			want:  "func A() {\n\ta2()\n}\n\nfunc B() {\n\tb()\n\tb()\n}\n\nfunc C() {}\n",
		},
		{"shorter body", "a\nb\nc\nd\n", []LineEdit{edit(2, 3, "x\n")}, "a\nx\nd\n"},
		{"empty body deletes the range", "a\nb\nc\n", []LineEdit{edit(2, 2, "")}, "a\nc\n"},
		{"append after the last line", "a\nb\n", []LineEdit{edit(3, 3, "\nfunc New() {}\n")}, "a\nb\n\nfunc New() {}\n"},
		{"append to an unterminated last line", "a\nb", []LineEdit{edit(3, 3, "c\n")}, "a\nb\nc\n"},
		{"range starting far past the end appends without padding", "a\n", []LineEdit{edit(10, 12, "z\n")}, "a\nz\n"},
		{"range running past the end replaces to the end", "a\nb\nc\n", []LineEdit{edit(2, 9, "x\n")}, "a\nx\n"},
		{"replacing an unterminated last line keeps it unterminated", "a\nb", []LineEdit{edit(2, 2, "B\n")}, "a\nB"},
		{"CRLF file keeps CRLF", "a\r\nb\r\nc\r\n", []LineEdit{edit(2, 2, "x\ny\n")}, "a\r\nx\r\ny\r\nc\r\n"},
		{"LF file stays LF for a CRLF body", "a\nb\n", []LineEdit{edit(1, 1, "x\r\n")}, "x\nb\n"},
		{"mixed file follows the majority", "a\r\nb\r\nc\n", []LineEdit{edit(3, 3, "z\n")}, "a\r\nb\r\nz\r\n"},
		{"new file from edits is LF", "", []LineEdit{edit(1, 1, "x\r\ny\r\n")}, "x\ny\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(applyLineEdits([]byte(tt.local), tt.edits)); got != tt.want {
				t.Fatalf("got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestStripLineNumbers(t *testing.T) {
	for _, tt := range []struct{ name, in, want string }{
		{"catclip --lines output", "    12\tfunc A() {\n    13\t\treturn\n    14\t}\n", "func A() {\n\treturn\n}\n"},
		{"blank line with and without its tab", "    12\ta\n    13\t\n    14\n    15\tb\n", "a\n\n\nb\n"},
		// A chat app turned the tab into 4 spaces and the tab indent into
		// 4 more: the narrowest gap (4) is the separator, indent is kept.
		{"tab turned into spaces", "12    func A() {\n13        return\n14    }\n", "func A() {\n    return\n}\n"},
		{"numbers not counting up are code", "12\tx\n14\ty\n", "12\tx\n14\ty\n"},
		{"not starting at the range start", "1\tx\n2\ty\n", "1\tx\n2\ty\n"},
		{"ordinary code is untouched", "func A() {}\n", "func A() {}\n"},
	} {
		first := 12
		if tt.name == "not starting at the range start" {
			first = 5
		}
		if got := string(stripLineNumbers([]byte(tt.in), first)); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestLineCount(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "\n": 1, "a\r\nb\r\n": 2} {
		if got := LineCount([]byte(in)); got != want {
			t.Errorf("LineCount(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseRefusesInconsistentReplies(t *testing.T) {
	for _, tt := range []struct{ name, bundle, want string }{
		{"overlapping ranges",
			"<file path=\"a.go\" lines=\"1-5\">\nx\n</file>\n<file path=\"a.go\" lines=\"5-9\">\ny\n</file>\n",
			"overlapping line ranges"},
		{"whole file plus edits",
			"<file path=\"a.go\">\nx\n</file>\n<file path=\"a.go\" lines=\"1-2\">\ny\n</file>\n",
			"both as a whole file and as line edits"},
		{"edits plus whole file",
			"<file path=\"a.go\" lines=\"1-2\">\ny\n</file>\n<file path=\"a.go\">\nx\n</file>\n",
			"both as a whole file and as line edits"},
		{"two different whole files", "<file path=\"a.go\">\nx\n</file>\n<file path=\"a.go\">\ny\n</file>\n", "twice with different contents"},
		{"unreadable range", "<file path=\"a.go\" lines=\"9-2\">\nx\n</file>\n", "unreadable line range"},
		{"unreadable out-of", "<file path=\"a.go\" lines=\"1-2\" out-of=\"many\">\nx\n</file>\n", "unreadable out-of"},
	} {
		if _, err := Parse([]byte(tt.bundle)); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want %q", tt.name, err, tt.want)
		}
	}
	// An identical repeat of the same edit is harmless and applied once.
	b, err := Parse([]byte("<file path=\"a.go\" lines=\"1-1\">\nx\n</file>\n<file path=\"a.go\" lines=\"1-1\">\nx\n</file>\n"))
	if err != nil || len(b.Entries) != 1 || len(b.Entries[0].Edits) != 1 {
		t.Fatalf("identical repeat: %+v %v", b, err)
	}
}

func TestParseReadsBackslashPathsAsFolders(t *testing.T) {
	b, err := Parse([]byte("<file path=\"src\\components\\Button.tsx\">\nx\n</file>\n"))
	if err != nil || b.Entries[0].Path != "src/components/Button.tsx" {
		t.Fatalf("got %+v %v", b, err)
	}
}

func TestBuildPlanLineEditsAndLineEndings(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.go"), "package main\n\nfunc A() {}\n")
	mustWrite(t, filepath.Join(root, "win.go"), "package win\r\n\r\nfunc W() {}\r\n")
	mustWrite(t, filepath.Join(root, "moved.go"), "// a line added after copying\npackage m\n\nfunc M() {}\n")
	bundle := "<file path=\"main.go\" lines=\"3-3\" out-of=\"3\">\nfunc A() { return }\n</file>\n" +
		"<file path=\"win.go\">\npackage win\n\nfunc W() { return }\n</file>\n" +
		"<file path=\"moved.go\" lines=\"3-3\" out-of=\"3\">\nfunc M() { return }\n</file>\n" +
		"<file path=\"fresh.go\">\r\npackage fresh\r\n</file>\r\n"
	b, err := Parse([]byte(bundle))
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(root, b)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Change{}
	for _, c := range p.Changes {
		got[c.Path] = c
	}
	if c := got["main.go"]; string(c.Body) != "package main\n\nfunc A() { return }\n" || !c.LineEdits {
		t.Errorf("main.go: %+v", c)
	}
	// A whole-file reply arrives as LF; the existing CRLF file stays CRLF.
	if c := got["win.go"]; string(c.Body) != "package win\r\n\r\nfunc W() { return }\r\n" || c.LineEdits {
		t.Errorf("win.go: %q", c.Body)
	}
	// New files are LF, whatever the bundle used.
	if c := got["fresh.go"]; c.Kind != KindNew || string(c.Body) != "package fresh\n" {
		t.Errorf("fresh.go: %+v", c)
	}
	// moved.go has 4 lines now, 3 when the excerpt was copied: skipped.
	if _, ok := got["moved.go"]; ok || len(p.Skipped) != 1 || p.Skipped[0].Path != "moved.go" ||
		p.Skipped[0].Problem != ProblemChanged || !strings.HasSuffix(p.Skipped[0].Detail, `out-of="3", but the file has 4 lines now`) {
		t.Errorf("moved.go should be skipped: changes %v skipped %+v", got["moved.go"], p.Skipped)
	}
	if _, err := os.Stat(filepath.Join(root, "fresh.go")); err == nil {
		t.Fatal("planning must not write")
	}
}

func TestParseLineRange(t *testing.T) {
	for in, want := range map[string][2]int{"12-14": {12, 14}, "7": {7, 7}, " 3 - 9 ": {3, 9}, "40-": {40, openRangeEnd}} {
		start, end, ok := parseLineRange(in)
		if !ok || start != want[0] || end != want[1] {
			t.Errorf("parseLineRange(%q) = %d, %d, %v", in, start, end, ok)
		}
	}
	for _, in := range []string{"", "0-3", "9-2", "a-b", "1,3"} {
		if _, _, ok := parseLineRange(in); ok {
			t.Errorf("parseLineRange(%q) should fail", in)
		}
	}
	// lines="40-" replaces from line 40 to the end.
	if got := string(applyLineEdits([]byte("a\nb\nc\n"), []LineEdit{{Start: 2, End: openRangeEnd, Body: []byte("x\n")}})); got != "a\nx\n" {
		t.Fatalf("open range: %q", got)
	}
}

// Seen from Haiku: a new file written as an excerpt with out-of="0". The
// agent states the file has no lines yet, so the missing file is created; an
// excerpt claiming lines of a missing file still blocks (wrong folder), and
// out-of="0" for a file that has lines is stale.
func TestBuildPlanTotalZeroCreatesAMissingFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "full.go"), "package full\n")
	b, err := Parse([]byte("<file path=\"new_test.go\" lines=\"1-1\" out-of=\"0\">\npackage handler\n</file>\n" +
		"<file path=\"gone.go\" lines=\"3-4\" out-of=\"84\">\nx\n</file>\n" +
		"<file path=\"full.go\" lines=\"1-1\" out-of=\"0\">\npackage other\n</file>\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].Path != "new_test.go" || p.Changes[0].Kind != KindNew || string(p.Changes[0].Body) != "package handler\n" {
		t.Fatalf("changes %+v", p.Changes)
	}
	blocked := map[string]Skipped{}
	for _, s := range p.Blocked() {
		blocked[s.Path] = s
	}
	if blocked["gone.go"].Problem != ProblemMissing || blocked["full.go"].Problem != ProblemChanged ||
		!strings.HasSuffix(blocked["full.go"].Detail, `out-of="0", but the file has 1 line now`) {
		t.Fatalf("blocked %+v", blocked)
	}
}
