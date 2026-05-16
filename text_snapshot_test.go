package catclip

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadTextSnapshotPreservesRawBytesAndCanonicalPreviewText(t *testing.T) {
	project := setupTestProject(t, nil)
	raw := []byte("alpha\nbad:\xffz\nomega")
	absPath := filepath.Join(project, "raw.txt")
	if err := os.WriteFile(absPath, raw, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	snapshot, err := loadTextSnapshot(absPath, "raw.txt")
	if err != nil {
		t.Fatalf("loadTextSnapshot returned error: %v", err)
	}
	if !snapshot.IsText {
		t.Fatal("expected raw.txt to be treated as text")
	}
	if !bytes.Equal(snapshot.RawBytes, raw) {
		t.Fatalf("RawBytes mismatch:\nwant: %v\ngot:  %v", raw, snapshot.RawBytes)
	}

	wantText := string(bytes.ToValidUTF8(raw, []byte{}))
	if got := snapshot.PreviewText(); got != wantText {
		t.Fatalf("PreviewText = %q, want %q", got, wantText)
	}

	wantLines := splitLogicalLines(raw)
	if got := snapshot.SnippetLines(); !reflect.DeepEqual(got, wantLines) {
		t.Fatalf("SnippetLines = %#v, want %#v", got, wantLines)
	}
}

func TestSnippetResolutionMatchesPreviewAndPreparedPayload(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": "alpha\nTODO one\nTODO two\nomega\n\nbeta\nTODO three\ngamma",
	})
	relPath := "src/app.ts"
	absPath := filepath.Join(project, filepath.FromSlash(relPath))

	snapshot, err := loadTextSnapshot(absPath, relPath)
	if err != nil {
		t.Fatalf("loadTextSnapshot returned error: %v", err)
	}

	matchedLines, err := runRipgrepMatchLines("TODO", []string{absPath})
	if err != nil {
		t.Fatalf("runRipgrepMatchLines returned error: %v", err)
	}
	snippet, err := resolveSnippetFromSnapshot(snapshot, matchedLines[absPath])
	if err != nil {
		t.Fatalf("resolveSnippetFromSnapshot returned error: %v", err)
	}
	wantRanges := []snippetRange{{Start: 1, End: 4}, {Start: 6, End: 8}}
	if got := snippet.Ranges; !reflect.DeepEqual(got, wantRanges) {
		t.Fatalf("Ranges = %#v, want %#v", got, wantRanges)
	}

	doc, ok := buildInternalSnippetPreviewDocument(relPath, absPath, "TODO")
	if !ok {
		t.Fatal("expected snippet preview document")
	}
	if doc.File == nil {
		t.Fatal("expected file preview payload")
	}
	if !strings.Contains(doc.File.Content, "[lines 1-4]") || !strings.Contains(doc.File.Content, "[lines 6-8]") {
		t.Fatalf("expected preview to use shared snippet ranges, got %q", doc.File.Content)
	}

	payload, _, err := buildPreparedSnippetPayload(fileEntry{
		AbsPath:        absPath,
		RelPath:        relPath,
		Mode:           entryModeSnippet,
		SnippetPattern: "TODO",
	}, matchedLines[absPath])
	if err != nil {
		t.Fatalf("buildPreparedSnippetPayload returned error: %v", err)
	}
	if !strings.Contains(string(payload), `<file path="src/app.ts" lines="1-4">`) ||
		!strings.Contains(string(payload), `<file path="src/app.ts" lines="6-8">`) {
		t.Fatalf("expected payload to use shared snippet ranges, got:\n%s", string(payload))
	}
}
