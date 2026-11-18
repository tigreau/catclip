package render

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestScopeGroupsPayloadAndRendering(t *testing.T) {
	doc := Document{Mode: DocumentModeTree, Groups: []DocumentGroup{
		{Label: "Scope 1: src", Entries: []DocumentEntry{{Path: "src/a.go", ModeTag: "1-3"}}},
		{Label: "Scope 2: docs", Empty: "No output.", Notices: []string{"No files found."}},
	}, Summary: &DocumentSummary{Count: 1, HumanSize: "12B", Tokens: 3, FileWord: "file"}}
	var wire, before, after bytes.Buffer
	opts := DefaultRenderOptions()
	if err := EncodePayload(&wire, doc); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePayload(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderDocument(&before, doc, opts, Palette{}); err != nil {
		t.Fatal(err)
	}
	if err := RenderDocument(&after, decoded, opts, Palette{}); err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatalf("%s != %s", &before, &after)
	}
	text := after.String()
	if strings.Count(text, "Count:") != 1 || strings.Index(text, "No files found.") < strings.Index(text, "Scope 2:") {
		t.Fatal(text)
	}
}

func TestRangeWrappingKeepsEveryRange(t *testing.T) {
	var ranges []string
	for i := 0; i < 37; i++ {
		ranges = append(ranges, fmt.Sprintf("%d-%d", i*10+1, i*10+3))
	}
	for _, color := range []bool{false, true} {
		palette := Palette{}
		if color {
			palette = Palette{Git: "\x1b[35m", Tree: "\x1b[90m", Reset: "\x1b[0m"}
		}
		line := "│   ├── 日本.go (35KB) [M] " + palette.Git + "[" + strings.Join(ranges, ", ") + "]" + palette.Reset + "\n"
		got := WrapTreeRanges(line, 64, palette)
		if !reflect.DeepEqual(regexp.MustCompile(`[0-9]+-[0-9]+`).FindAllString(got, -1), ranges) {
			t.Fatalf("ranges lost: %s", got)
		}
		for _, row := range strings.Split(strings.TrimSpace(got), "\n") {
			if visibleTreeWidth(row) > 64 {
				t.Fatalf("too wide: %d %q", visibleTreeWidth(row), row)
			}
		}
		if !strings.Contains(got, "│   │ ") || strings.Contains(got, "...") {
			t.Fatal(got)
		}
		if WrapTreeRanges(line, 0, palette) != line {
			t.Fatal("pipe text changed")
		}
	}
}

func BenchmarkTreeRangeWrapping(b *testing.B) {
	for _, count := range []int{0, 37, 500} {
		ranges := make([]string, count)
		for i := range ranges {
			ranges[i] = fmt.Sprintf("%d-%d", i*10+1, i*10+3)
		}
		text := "├── sample.go (35KB) [M] [" + strings.Join(ranges, ", ") + "]\n"
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = WrapTreeRanges(text, 80, Palette{})
			}
		})
	}
}

func BenchmarkPreparedScopeTree(b *testing.B) {
	for _, rangeCount := range []int{0, 37} {
		var ranges []string
		for i := 0; i < rangeCount; i++ {
			ranges = append(ranges, fmt.Sprintf("%d-%d", i*10+1, i*10+3))
		}
		rows := make([]DocumentEntry, 1000)
		for i := range rows {
			rows[i] = DocumentEntry{Path: fmt.Sprintf("src/%04d.go", i), ModeTag: strings.Join(ranges, ", ")}
		}
		tree := PrepareTree(rows)
		b.Run(fmt.Sprint(rangeCount), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := tree.Render(io.Discard, RenderOptions{Width: 80, ShowModeTags: true}, Palette{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
