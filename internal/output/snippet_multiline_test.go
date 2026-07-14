package output

import (
	"reflect"
	"testing"
)

// TestSnippetMultilineDecoratorIncluded pins the fix for adornments whose
// argument list wraps across lines: the whole decorated function (decorator
// included) is returned, not just the def and body.
func TestSnippetMultilineDecoratorIncluded(t *testing.T) {
	lines := []string{
		"@app.route(",           // 1
		`    "/admin",`,         // 2
		`    methods=["POST"])`, // 3
		"def admin():",          // 4
		"    return secret()",   // 5
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".py"))
	want := []SnippetRange{{Start: 1, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multiline decorator: got %v, want whole decorated function %v", got, want)
	}
}

// TestSnippetMultilineDecoratorLeavesPrecedingStatement guards against the
// bracket-balancing walk over-reaching: a wrapped statement above the def whose
// balancing line is not an adornment must NOT be absorbed.
func TestSnippetMultilineDecoratorLeavesPrecedingStatement(t *testing.T) {
	lines := []string{
		"logger = get_logger(", // 1  unrelated wrapped statement
		"    __name__,",        // 2
		")",                    // 3
		"def handler():",       // 4
		"    return 1",         // 5
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".py"))
	want := []SnippetRange{{Start: 4, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preceding statement: got %v, want just the function %v", got, want)
	}
}

// TestSnippetMultilineOpenTag pins the fix for an opening tag whose '>' lands on
// a later line: the enclosing <dependency> is returned, not the outer root.
func TestSnippetMultilineOpenTag(t *testing.T) {
	lines := []string{
		"<project>",                      // 1
		"  <dependency",                  // 2  open tag spans lines
		`    scope="compile">`,           // 3  '>' here
		"    <artifactId>x</artifactId>", // 4  match here
		"  </dependency>",                // 5
		"</project>",                     // 6
	}
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".xml"))
	want := []SnippetRange{{Start: 2, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multiline open tag: got %v, want <dependency> %v", got, want)
	}
}
