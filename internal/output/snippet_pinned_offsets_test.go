package output

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
)

// The filter-attribute-persistence data-integrity gate: prepared units
// built from PINNED offsets (Entry.SnippetMatchLines, produced by the
// snippet stage's single rg pass) must be identical to units built via
// the BatchSnippetMatches fallback (pins stripped → second rg pass).
// Pinned offsets feed the sink and change what bytes get copied, so
// equivalence here is the shipping condition, not a nicety.
func TestPreparedSnippetPinnedOffsetsMatchBatchPath(t *testing.T) {
	if _, ok := search.RipgrepBinary(); !ok {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	content := "alpha\nTODO one\nbeta\ngamma\nTODO two\ndelta\n"
	abs := filepath.Join(dir, "match.go")
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	makeEntry := func(pinned []int) discovery.Entry {
		return discovery.Entry{
			AbsPath:             abs,
			RelPath:             "match.go",
			Mode:                command.EntryModeSnippet,
			SnippetPattern:      "TODO",
			SnippetContextSet:   true,
			SnippetContextLines: 1,
			SnippetMatchLines:   pinned,
		}
	}

	// Batch path: no pins → BatchSnippetMatches runs rg.
	batchUnits, err := PrepareFileUnits(git.Context{}, []discovery.Entry{makeEntry(nil)})
	if err != nil {
		t.Fatalf("batch path: %v", err)
	}
	if len(batchUnits) != 1 {
		t.Fatalf("batch path: expected 1 unit, got %d", len(batchUnits))
	}

	// Pinned path: offsets carried on the entry, batch skipped.
	pinnedUnits, err := PrepareFileUnits(git.Context{}, []discovery.Entry{makeEntry([]int{2, 5})})
	if err != nil {
		t.Fatalf("pinned path: %v", err)
	}
	if len(pinnedUnits) != 1 {
		t.Fatalf("pinned path: expected 1 unit, got %d", len(pinnedUnits))
	}

	b, p := batchUnits[0], pinnedUnits[0]
	if !reflect.DeepEqual(b.SnippetRanges, p.SnippetRanges) {
		t.Fatalf("range divergence: batch=%v pinned=%v", b.SnippetRanges, p.SnippetRanges)
	}
	if b.BodyBytes != p.BodyBytes {
		t.Fatalf("body-bytes divergence: batch=%d pinned=%d", b.BodyBytes, p.BodyBytes)
	}
}
