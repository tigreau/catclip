package output

import (
	"reflect"
	"testing"
)

// A <version> match inside a pom.xml returns the enclosing multi-line
// <dependency> element, not the single-line leaf.
func TestSnippetTagReturnsEnclosingDependency(t *testing.T) {
	lines := []string{
		"<dependencies>",
		"  <dependency>",
		"    <groupId>org.springframework</groupId>",
		"    <artifactId>spring-core</artifactId>",
		"    <version>5.3.0</version>",
		"  </dependency>",
		"</dependencies>",
	}
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".xml"))
	want := []SnippetRange{{Start: 2, End: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tag: got %v, want the <dependency> block %v; elements=%v", got, want, xmlElementCandidates(lines, false, false))
	}
}

func TestSnippetSectionReturnsEnclosingSection(t *testing.T) {
	lines := []string{
		"[database]",
		"host = localhost",
		"port = 5432",
		"[cache]",
		"ttl = 60",
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".ini"))
	want := []SnippetRange{{Start: 1, End: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("section: got %v, want [database] section %v", got, want)
	}
}

func TestSnippetFlatReturnsSmallContext(t *testing.T) {
	lines := []string{"a=1", "b=2", "c=3", "d=4", "e=5", "f=6", "g=7"}
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".properties"))
	want := []SnippetRange{{Start: 2, End: 6}} // match +/- 2 lines
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flat: got %v, want small context %v", got, want)
	}
}

// A code-shaped line in a prose file must NOT be recognized as a declaration;
// the paragraph strategy returns the blank-line block.
func TestSnippetParagraphSkipsCodeRecognition(t *testing.T) {
	lines := []string{
		"Some text",
		"def compute():",
		"    return 1",
		"More text",
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".md"))
	want := []SnippetRange{{Start: 1, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paragraph: got %v, want blank-line block %v (no code recognition)", got, want)
	}
	// Sanity: the SAME lines under the .py profile DO recognize the function.
	if code := buildSnippetRanges(lines, []int{3}, profileForExt(".py")); reflect.DeepEqual(code, want) {
		t.Fatalf(".py profile should recognize def compute(), not return the paragraph; got %v", code)
	}
}
