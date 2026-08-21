package search

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyTextPathsWithSizeCaptureStoresOnlyTextFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"known.go":      []byte("package example\n"),
		"binary.png":    []byte{0x89, 'P', 'N', 'G', 0},
		"empty.png":     nil,
		"residue.datax": []byte("residue text\n"),
		"nul.datax":     []byte{'x', 0, 'y'},
	}
	paths := make([]string, 0, len(files))
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), body, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, rel)
	}

	set, capture, err := ClassifyTextPathsWithSizeCapture(dir, paths)
	if err != nil {
		t.Fatal(err)
	}
	<-capture.Done()
	sizes := capture.Snapshot()

	for _, rel := range []string{"known.go", "empty.png", "residue.datax"} {
		if _, ok := set[rel]; !ok {
			t.Fatalf("%s did not classify as text: %v", rel, set)
		}
		if got, ok := sizes[rel]; !ok || got != int64(len(files[rel])) {
			t.Fatalf("size[%q] = %d, %t; want %d, true", rel, got, ok, len(files[rel]))
		}
	}
	for _, rel := range []string{"binary.png", "nul.datax"} {
		if _, ok := set[rel]; ok {
			t.Fatalf("%s unexpectedly classified as text: %v", rel, set)
		}
		if _, ok := sizes[rel]; ok {
			t.Fatalf("binary path %q was passed to size capture: %v", rel, sizes)
		}
	}
}

func TestClassifyTextPathsWithoutSizeCaptureAdmitsEmptyBinaryName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.png"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	set, err := ClassifyTextPaths(dir, []string{"empty.png"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set["empty.png"]; !ok {
		t.Fatalf("empty file did not classify as text: %v", set)
	}
}

func TestTextSizeCaptureStopCancelsOutstandingWork(t *testing.T) {
	capture := newTextSizeCapture(t.TempDir())
	capture.add([]string{"not-started.txt"})
	capture.Stop()
	if !capture.Complete() {
		t.Fatal("capture did not complete after Stop")
	}
	if !capture.Cancelled() {
		t.Fatal("unfinished capture was not marked cancelled")
	}
}

func TestTextSizeCaptureCompletedSnapshotRemainsReusableAfterStop(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ready.txt"), []byte("ready"), 0o644); err != nil {
		t.Fatal(err)
	}
	capture := StartTextSizeCapture(dir, []string{"ready.txt"})
	<-capture.Done()
	capture.Stop()
	if capture.Cancelled() {
		t.Fatal("completed capture was marked cancelled")
	}
	if got := capture.Snapshot()["ready.txt"]; got != 5 {
		t.Fatalf("captured size = %d, want 5", got)
	}
}
