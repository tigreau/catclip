package discovery

import (
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureEntryModTimesDoesNotRestatCompleteEntries(t *testing.T) {
	root := t.TempDir()
	missingPath := filepath.Join(root, "removed.txt")
	wantTime := time.Unix(1_700_000_000, 0)
	entries, err := EnsureEntryModTimes([]Entry{{
		RelPath:   "removed.txt",
		ModTime:   wantTime,
		SizeBytes: 42,
		SizeKnown: true,
	}}, root)
	if err != nil {
		t.Fatalf("complete retained metadata triggered a restat: %v", err)
	}
	if len(entries) != 1 || entries[0].AbsPath != missingPath || !entries[0].ModTime.Equal(wantTime) || entries[0].SizeBytes != 42 {
		t.Fatalf("completed entry changed: %#v", entries)
	}
}
