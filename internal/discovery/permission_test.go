package discovery

import (
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/search"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPermissionPickerUsesOnlyRequiredMetadata(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix permissions")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	bad := filepath.Join(root, "unreadable.txt")
	if err := os.WriteFile(bad, []byte("private"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0600) })
	r := Resolver{Cfg: command.Invocation{WorkingDir: root}, CaptureTargetPreviewSizes: true}
	if err := r.BuildVisibleFileList(); err != nil {
		t.Fatal(err)
	}
	<-r.targetPreviewSizes.Done()
	if len(search.AccessFailures(root)) != 0 {
		t.Fatal("picker probed contents it did not need to read")
	}
	// The picker already collects metadata; use its actual failure observations
	// when committing, without issuing a readability preflight.
	matches := []TargetMatch{{Path: "unreadable.txt", Kind: "file"}, {Path: "gone.txt", Kind: "file"}}
	metadata := r.targetPreviewSizes.MetadataSnapshot()
	metadata["gone.txt"] = search.FileMetadata{State: search.FileMetadataVanished}
	entries := TargetPreviewEntries(root, matches, metadata)
	if len(entries) != 1 || entries[0].RelPath != "unreadable.txt" || !entries[0].SizeKnown {
		t.Fatalf("metadata projection: %+v", entries)
	}
}
