package output

import (
	"reflect"
	"testing"
)

// TestSnippetHTMLSelfClosingVoidClosesParagraph pins that self-closing syntax on
// a void block element (<hr/>) still closes an open <p>, exactly as <hr> does.
func TestSnippetHTMLSelfClosingVoidClosesParagraph(t *testing.T) {
	lines := []string{
		"<main>",     // 1
		"  <p>",      // 2
		"    needle", // 3  match
		"  <hr/>",    // 4  self-closing void: closes the <p>
		"  body",     // 5
		"</main>",    // 6
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".html"))
	want := []SnippetRange{{Start: 2, End: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html <hr/> implied close: got %v, want paragraph {2,3}", got)
	}
}

// TestSnippetHTMLTableMultiLevelImpliedClose pins that a new <tbody> composes
// td -> tr -> tbody implied closes, so a match in the first cell returns the
// cell, not the cell plus the next table body.
func TestSnippetHTMLTableMultiLevelImpliedClose(t *testing.T) {
	lines := []string{
		"<table>",     // 1
		"  <tbody>",   // 2
		"    <tr>",    // 3
		"      <td>",  // 4
		"        one", // 5  match (first cell)
		"  <tbody>",   // 6  new body: closes td -> tr -> tbody
		"    <tr>",    // 7
		"      <td>",  // 8
		"        two", // 9
		"</table>",    // 10
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".html"))
	want := []SnippetRange{{Start: 4, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html table multi-level implied close: got %v, want first cell {4,5}", got)
	}
}

// TestSnippetXMLSelfClosingUnchanged guards that XML self-closing keeps its
// no-nesting semantics (the '/' still ends the element with no push).
func TestSnippetXMLSelfClosingUnchanged(t *testing.T) {
	lines := []string{
		"<root>",             // 1
		"  <group>",          // 2
		"    <leaf/>",        // 3  self-closing, no nesting
		"    <item>x</item>", // 4  match
		"  </group>",         // 5
		"</root>",            // 6
	}
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".xml"))
	want := []SnippetRange{{Start: 2, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xml self-closing: got %v, want <group> {2,5}", got)
	}
}
