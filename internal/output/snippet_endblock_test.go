package output

import (
	"reflect"
	"testing"
)

// TestSnippetRubyEndDelimited pins end-delimited extent: a method-body match
// returns the enclosing def (to its `end`), and a class-level match returns the
// whole class (to its `end`).
func TestSnippetRubyEndDelimited(t *testing.T) {
	lines := []string{
		"class Foo",    // 1
		"  ATTR = 1",   // 2  class-level match
		"  def bar",    // 3
		"    do_thing", // 4  method-body match
		"  end",        // 5
		"end",          // 6
	}
	rb := profileForExt(".rb")

	if got := buildSnippetRanges(lines, []int{4}, rb); !reflect.DeepEqual(got, []SnippetRange{{Start: 3, End: 5}}) {
		t.Fatalf("ruby method: got %v, want def bar {3,5}", got)
	}
	if got := buildSnippetRanges(lines, []int{2}, rb); !reflect.DeepEqual(got, []SnippetRange{{Start: 1, End: 6}}) {
		t.Fatalf("ruby class-level: got %v, want class {1,6}", got)
	}
}

func TestSnippetElixirEndDelimited(t *testing.T) {
	lines := []string{
		"defmodule Math do",  // 1
		"  def add(a, b) do", // 2
		"    a + b",          // 3  match
		"  end",              // 4
		"end",                // 5
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".ex"))
	if !reflect.DeepEqual(got, []SnippetRange{{Start: 2, End: 4}}) {
		t.Fatalf("elixir def: got %v, want add/2 {2,4}", got)
	}
}
