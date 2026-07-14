package output

import (
	"reflect"
	"testing"
)

// TestSnippetJSONReturnsEnclosingObject pins the brace-unit strategy: a match on
// a nested key returns its smallest enclosing object, not the whole document.
func TestSnippetJSONReturnsEnclosingObject(t *testing.T) {
	lines := []string{
		"{",                        // 1
		`  "server": {`,            // 2
		`    "host": "localhost",`, // 3  match
		`    "port": 8080`,         // 4
		"  },",                     // 5
		`  "name": "app"`,          // 6
		"}",                        // 7
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".json"))
	want := []SnippetRange{{Start: 2, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("json nested key: got %v, want server object {2,5}", got)
	}
}

// TestSnippetJSONIgnoresBraceInString proves the scanner is string-aware: a
// brace inside a string value must not skew nesting for an array match.
func TestSnippetJSONIgnoresBraceInString(t *testing.T) {
	lines := []string{
		"{",            // 1
		`  "items": [`, // 2
		`    "a{b}c",`, // 3  brace inside string must not count
		`    "d"`,      // 4  match
		"  ]",          // 5
		"}",            // 6
	}
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".json"))
	want := []SnippetRange{{Start: 2, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("json brace-in-string: got %v, want items array {2,5}", got)
	}
}
