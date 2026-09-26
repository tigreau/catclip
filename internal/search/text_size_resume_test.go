package search

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResumeTextSizeCaptureReusesCompletedObservationsWithoutMembershipLeaks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := newTextSizeCapture(root)
	previous.Stop()
	// These absent paths carry completed observations. A restat would replace
	// them with vanished records, exposing accidental repetition deterministically.
	previous.metadata["kept.txt"] = FileMetadata{SizeBytes: 17}
	previous.metadata["ignored.txt"] = FileMetadata{SizeBytes: 99}
	previous.metadata["failed.txt"] = FileMetadata{State: FileMetadataUnreadable, Error: "original failure"}
	resumed := ResumeTextSizeCapture(root, []string{"kept.txt", "new.txt", "failed.txt"}, previous)
	defer resumed.Stop()
	<-resumed.Done()
	selected := resumed.FinalizeSelection([]string{"kept.txt", "new.txt", "failed.txt"})
	if len(selected) != 3 || selected["kept.txt"].SizeBytes != 17 || selected["new.txt"].SizeBytes != 3 || selected["failed.txt"].Error != "original failure" {
		t.Fatalf("wrong resumed observations: %#v", selected)
	}
	if _, ok := selected["ignored.txt"]; ok {
		t.Fatal("metadata seed leaked ignored membership")
	}
	resumed.metadata["kept.txt"] = FileMetadata{SizeBytes: 888}
	if previous.MetadataSnapshot()["kept.txt"].SizeBytes != 17 {
		t.Fatal("resumed capture mutated earlier branch")
	}
}

func TestResumeTextSizeCaptureRejectsDifferentWorkingDirectory(t *testing.T) {
	previous := newTextSizeCapture(t.TempDir())
	previous.Stop()
	previous.metadata["same.txt"] = FileMetadata{SizeBytes: 99}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "same.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	resumed := ResumeTextSizeCapture(root, []string{"same.txt"}, previous)
	defer resumed.Stop()
	<-resumed.Done()
	if resumed.MetadataSnapshot()["same.txt"].SizeBytes != 3 {
		t.Fatal("metadata crossed working directories")
	}
}
