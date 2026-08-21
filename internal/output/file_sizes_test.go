package output

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestCollectFileBodySizesUsesKnownSizesWithoutFilesystemLookup(t *testing.T) {
	entries := []discovery.Entry{
		{RelPath: "gone.txt", AbsPath: filepath.Join(t.TempDir(), "gone.txt"), SizeBytes: 37, SizeKnown: true},
	}
	sizes, err := CollectFileBodySizes(context.Background(), entries)
	if err != nil {
		t.Fatal(err)
	}
	if got := sizes["gone.txt"]; got != 37 {
		t.Fatalf("size = %d, want 37", got)
	}
}

func TestCollectFileBodySizesLooksUpOnlyMissingSizes(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(dir, "missing.txt")
	if err := os.WriteFile(missingPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []discovery.Entry{
		{RelPath: "known.txt", AbsPath: filepath.Join(dir, "does-not-exist.txt"), SizeBytes: 11, SizeKnown: true},
		{RelPath: "missing.txt", AbsPath: missingPath},
	}
	sizes, err := CollectFileBodySizes(context.Background(), entries)
	if err != nil {
		t.Fatal(err)
	}
	if sizes["known.txt"] != 11 || sizes["missing.txt"] != 5 {
		t.Fatalf("sizes = %v", sizes)
	}
}

func TestCollectFileBodySizesHonorsCancellationWithKnownSizes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CollectFileBodySizes(ctx, []discovery.Entry{{RelPath: "known.txt", SizeKnown: true}})
	if err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
