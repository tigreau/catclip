package output

import (
	"reflect"
	"testing"
)

// TestSnippetHTMLVoidElementNotPushed pins that a void element (<br>) does not
// block the implied close: the following block <div> still closes the open <p>,
// so a match in the paragraph returns the paragraph, not the outer container.
func TestSnippetHTMLVoidElementNotPushed(t *testing.T) {
	lines := []string{
		"<main>",        // 1
		"  <p>",         // 2
		"    needle",    // 3  match
		"  <br>",        // 4  void: must not be pushed
		"  <div></div>", // 5  block: closes the <p>
		"</main>",       // 6
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".html"))
	want := []SnippetRange{{Start: 2, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html void element: got %v, want enclosing <p> {2,4}", got)
	}
}

// TestSnippetHTMLVoidInputThenParagraph pins the second void-element case: an
// <input> before a <p> is not pushed, so the paragraph is closed by </form> and
// resolves correctly.
func TestSnippetHTMLVoidInputThenParagraph(t *testing.T) {
	lines := []string{
		"<form>",     // 1
		"  <input>",  // 2  void
		"  <p>",      // 3
		"    needle", // 4  match
		"</form>",    // 5
	}
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".html"))
	want := []SnippetRange{{Start: 3, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html void <input>: got %v, want enclosing <p> {3,4}", got)
	}
}

// TestSnippetHTMLParagraphClosedByParent pins the <p> ancestor-close rule: a
// <section><p>needle</section> with no explicit </p> returns the paragraph.
func TestSnippetHTMLParagraphClosedByParent(t *testing.T) {
	lines := []string{
		"<section>",  // 1
		"  <p>",      // 2
		"    needle", // 3  match
		"</section>", // 4
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".html"))
	want := []SnippetRange{{Start: 2, End: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html <p> closed by parent: got %v, want paragraph {2,3}", got)
	}
}

// TestSnippetXMLVoidNameStillNests guards that XML does not treat HTML void
// names specially: a self-closed-by-convention name like <br> without a close
// is just an unclosed element, and normal XML nesting is unaffected.
func TestSnippetXMLVoidNameStillNests(t *testing.T) {
	lines := []string{
		"<root>",             // 1
		"  <group>",          // 2
		"    <item>x</item>", // 3  match
		"  </group>",         // 4
		"</root>",            // 5
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".xml"))
	want := []SnippetRange{{Start: 2, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xml nesting: got %v, want <group> {2,4}", got)
	}
}
