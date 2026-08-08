package ui

import (
	"strconv"
	"testing"
)

// TestSnippetBoundaryChoicesListEveryContextValue pins the boundary picker's
// contract: the smart block leads, and every legal fixed context 0..200 (the
// CLI's SnippetContextMax) is a real row, so each value is selectable and
// previewable without any typed-value escape hatch.
func TestSnippetBoundaryChoicesListEveryContextValue(t *testing.T) {
	choices := startupSnippetBoundaryChoices
	if len(choices) != 202 {
		t.Fatalf("expected 202 choices (smart block + 0..200), got %d", len(choices))
	}
	if choices[0].Key != "block" || choices[0].SnippetContextSet {
		t.Fatalf("first choice must be the smart block, got %+v", choices[0])
	}
	for n := 0; n <= 200; n++ {
		c := choices[n+1]
		if c.Key != strconv.Itoa(n) || !c.SnippetContextSet || c.SnippetContextLines != n {
			t.Fatalf("choice %d: got %+v, want context %d", n+1, c, n)
		}
		if c.Description == "" {
			t.Fatalf("choice %d has empty description", n+1)
		}
	}
}
