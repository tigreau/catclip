package output

import (
	"reflect"
	"testing"
)

// TestSnippetCRLFParagraphBoundary pins CRLF handling: SplitLogicalLines keeps
// the trailing '\r', so a blank line in a CRLF file is "\r". The paragraph
// fallback must treat it as a boundary; before the fix it returned the whole
// file for any CRLF match outside a recognized declaration.
func TestSnippetCRLFParagraphBoundary(t *testing.T) {
	lines := SplitLogicalLines([]byte(
		"intro one\r\nintro two\r\n\r\nneedle paragraph\r\nsecond line\r\n\r\ntrailer\r\n"))
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".txt"))
	want := []SnippetRange{{Start: 4, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("crlf paragraph: got %v, want the needle paragraph %v", got, want)
	}
}

// TestSnippetLFWhitespaceLineStaysNonBoundary guards the deliberate limit of
// the CRLF fix: a whitespace-only line is still NOT a paragraph boundary,
// matching historical LF behavior.
func TestSnippetLFWhitespaceLineStaysNonBoundary(t *testing.T) {
	lines := []string{
		"para one", // 1
		"  ",       // 2  whitespace-only: not a boundary
		"needle",   // 3  match
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".txt"))
	want := []SnippetRange{{Start: 1, End: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("whitespace line: got %v, want joined paragraph %v", got, want)
	}
}
