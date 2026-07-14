package output

import (
	"reflect"
	"testing"
)

// TestSnippetRustImplRecognized pins the Phase 2 augmentation: the .rs profile
// recognizes `impl` (a shape the agnostic recognizer misses), so an impl-level
// match returns the whole impl block. The agnostic default does not know
// `impl`, so it falls back to the wider paragraph.
func TestSnippetRustImplRecognized(t *testing.T) {
	lines := []string{
		"impl Driver for Foo {",
		"    const NAME: &str = \"x\";", // match here (impl-level, not in a fn)
		"    fn probe(&self) {",
		"        do_it();",
		"    }",
		"}",
		"struct Other;",
	}
	want := []SnippetRange{{Start: 1, End: 6}}
	if got := buildSnippetRanges(lines, []int{2}, profileForExt(".rs")); !reflect.DeepEqual(got, want) {
		t.Fatalf("rust impl: got %v, want the impl block %v", got, want)
	}
	// Default recognizer misses impl; the match falls to the paragraph, which
	// (no blank lines) runs past the impl into the trailing struct line.
	if got := buildSnippetRanges(lines, []int{2}, defaultProfile); reflect.DeepEqual(got, want) {
		t.Fatalf("default profile should not recognize impl; got %v", got)
	}
}
