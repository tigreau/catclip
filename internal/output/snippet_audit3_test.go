package output

import (
	"reflect"
	"testing"
)

// TestSnippetAnnotationBlockCommentBracket pins the block-comment-aware balancer:
// a ')' inside an inline /* */ block comment within a wrapped Java annotation
// must not skew attachment, so the whole annotated class is returned.
func TestSnippetAnnotationBlockCommentBracket(t *testing.T) {
	lines := []string{
		"@Entity(",                 // 1
		"    name = \"x\" /* ) */", // 2  ')' lives in a block comment
		")",                        // 3
		"public class Foo {",       // 4
		"    int id;",              // 5
		"}",                        // 6
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".java"))
	want := []SnippetRange{{Start: 1, End: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("annotation w/ block-comment paren: got %v, want whole annotated class %v", got, want)
	}
}

// TestSnippetHTMLImpliedLiClose pins HTML optional end tags: a match in the
// first <li> (which has no explicit </li>) returns that item, not the whole <ul>.
func TestSnippetHTMLImpliedLiClose(t *testing.T) {
	lines := []string{
		"<ul>",         // 1
		"  <li>",       // 2
		"    Item one", // 3  match
		"  <li>",       // 4
		"    Item two", // 5
		"</ul>",        // 6
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".html"))
	want := []SnippetRange{{Start: 2, End: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html implied <li> close: got %v, want first item {2,3}", got)
	}
}

// TestSnippetHTMLImpliedLiCloseByParent pins that the LAST optional-end child is
// closed by its parent's close tag, so a match there returns the item.
func TestSnippetHTMLImpliedLiCloseByParent(t *testing.T) {
	lines := []string{
		"<ul>",         // 1
		"  <li>",       // 2
		"    Item one", // 3
		"  <li>",       // 4
		"    Item two", // 5  match (last item)
		"</ul>",        // 6
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".html"))
	want := []SnippetRange{{Start: 4, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html implied last <li> close: got %v, want last item {4,5}", got)
	}
}

// TestSnippetXMLNoImpliedClose guards that XML does NOT apply HTML implied-close
// rules: without an explicit </li>, the match resolves to the outer element.
func TestSnippetXMLNoImpliedClose(t *testing.T) {
	lines := []string{
		"<ul>",         // 1
		"  <li>",       // 2
		"    Item one", // 3  match
		"  <li>",       // 4
		"    Item two", // 5
		"</ul>",        // 6
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".xml"))
	// No implied close in XML; <li> never closes, so <ul> is the enclosing unit.
	want := []SnippetRange{{Start: 1, End: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xml no implied close: got %v, want outer <ul> %v", got, want)
	}
}
