package output

import (
	"reflect"
	"testing"
)

// TestSnippetDecoratorCommentBracket pins the comment-aware bracket balancer: a
// paren inside a `#` comment within a wrapped decorator must not skew the walk,
// so the whole decorated function (decorator included) is returned.
func TestSnippetDecoratorCommentBracket(t *testing.T) {
	lines := []string{
		"@route(",             // 1
		`    "/x",  # note )`, // 2  ')' lives in a comment
		")",                   // 3
		"def f():",            // 4
		"    return 1",        // 5
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".py"))
	want := []SnippetRange{{Start: 1, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decorator w/ comment paren: got %v, want whole decorated function %v", got, want)
	}
}

// TestSnippetMultilineTagUnindentedContinuation pins the line-join fix: an
// opening tag whose attributes wrap onto an UNINDENTED next line must still be
// recognized (the joined buffer needs a separating space).
func TestSnippetMultilineTagUnindentedContinuation(t *testing.T) {
	lines := []string{
		"<project>",                  // 1
		"<dependency",                // 2  open tag spans lines
		`scope="compile">`,           // 3  unindented continuation, '>' here
		"<artifactId>x</artifactId>", // 4  match
		"</dependency>",              // 5
		"</project>",                 // 6
	}
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".xml"))
	want := []SnippetRange{{Start: 2, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unindented multiline tag: got %v, want <dependency> %v", got, want)
	}
}

// TestSnippetHTMLScriptRawText pins raw-text handling: tag-like text inside a
// <script> string must not pop the element, so a match in the script returns
// the coherent <script> region rather than widening to the document.
func TestSnippetHTMLScriptRawText(t *testing.T) {
	lines := []string{
		"<body>",                    // 1
		"  <script>",                // 2
		`    var s = "</section>";`, // 3  fake close tag in a string
		"    doWork()",              // 4  match
		"  </script>",               // 5
		"</body>",                   // 6
	}
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".html"))
	want := []SnippetRange{{Start: 2, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html script raw text: got %v, want <script> region %v", got, want)
	}
}

// TestSnippetHTMLCaseInsensitive pins case-insensitive HTML tag matching:
// <DIV>...</div> is a valid element and must not fall back to the outer node.
func TestSnippetHTMLCaseInsensitive(t *testing.T) {
	lines := []string{
		"<section>",          // 1
		"  <DIV>",            // 2
		"    <span>x</span>", // 3  match
		"  </div>",           // 4
		"</section>",         // 5
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".html"))
	want := []SnippetRange{{Start: 2, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("html case-insensitive: got %v, want <DIV> block %v", got, want)
	}
}

// TestSnippetXMLStaysCaseSensitive guards that XML does NOT fold case: a
// mismatched-case close must not pair, so the match resolves to the outer node.
func TestSnippetXMLStaysCaseSensitive(t *testing.T) {
	lines := []string{
		"<Outer>",      // 1
		"  <Item>",     // 2  <Item> ... </item> mismatch under XML rules
		"    <v>1</v>", // 3  match
		"  </item>",    // 4
		"</Outer>",     // 5
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".xml"))
	// <Item> never closes (case-sensitive), so the nearest real element is <Outer>.
	want := []SnippetRange{{Start: 1, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xml case-sensitive: got %v, want outer element %v", got, want)
	}
}
