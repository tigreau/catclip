package render

import (
	"bytes"
	"testing"
)

func TestEncodeDecodePayloadRoundTrip(t *testing.T) {
	sizeA := int64(73216)
	sizeB := int64(3276)
	original := Document{
		Mode: DocumentModeTree,
		Root: "src/components",
		Target: &DocumentTarget{
			Path:  "src/components",
			Kind:  TargetKindDir,
			State: TargetStateOK,
		},
		Entries: []DocumentEntry{
			{
				Path:      "src/components/Editor/Editor.tsx",
				Size:      &sizeA,
				GitStatus: "M",
				ModeTag:   "diff only",
			},
			{
				Path: "src/components/Editor/Toolbar.tsx",
				Size: &sizeB,
			},
		},
		Summary: &DocumentSummary{
			Count:     2,
			Bytes:     sizeA + sizeB,
			HumanSize: "74.74KB",
			Tokens:    19123,
			FileWord:  "files",
		},
	}

	var payload bytes.Buffer
	if err := EncodePayload(&payload, original); err != nil {
		t.Fatalf("EncodePayload returned error: %v", err)
	}

	decoded, err := DecodePayload(&payload)
	if err != nil {
		t.Fatalf("DecodePayload returned error: %v", err)
	}

	if decoded.Mode != DocumentModeTree {
		t.Fatalf("decoded mode = %q, want tree", decoded.Mode)
	}
	if decoded.Root != "src/components" {
		t.Fatalf("decoded root = %q, want src/components", decoded.Root)
	}
	if decoded.Target == nil || decoded.Target.Path != "src/components" || decoded.Target.Kind != TargetKindDir || decoded.Target.State != TargetStateOK {
		t.Fatalf("decoded target = %#v, want src/components dir ok", decoded.Target)
	}
	if len(decoded.Entries) != 2 {
		t.Fatalf("decoded %d entries, want 2", len(decoded.Entries))
	}
	if decoded.Entries[0].Path != "src/components/Editor/Editor.tsx" {
		t.Fatalf("decoded first path = %q", decoded.Entries[0].Path)
	}
	if decoded.Entries[0].Size == nil || *decoded.Entries[0].Size != sizeA {
		t.Fatalf("decoded first size = %#v, want %d", decoded.Entries[0].Size, sizeA)
	}
	if decoded.Summary == nil || decoded.Summary.Tokens != 19123 {
		t.Fatalf("decoded summary = %#v, want tokens 19123", decoded.Summary)
	}
}

func TestEncodeDecodeFilePreviewPayloadRoundTrip(t *testing.T) {
	original := Document{
		Mode: DocumentModeFile,
		File: &FilePreview{
			Path:          "src/main.ts",
			HighlightPath: "diff",
			FocusLines:    []int{2, 3},
			Content:       "diff --git a/src/main.ts b/src/main.ts\n@@ -1 +1 @@\n-old\n+new\n",
			MatchPattern:  "TODO",
			Truncated:     true,
		},
	}

	var payload bytes.Buffer
	if err := EncodePayload(&payload, original); err != nil {
		t.Fatalf("EncodePayload returned error: %v", err)
	}

	decoded, err := DecodePayload(&payload)
	if err != nil {
		t.Fatalf("DecodePayload returned error: %v", err)
	}

	if decoded.Mode != DocumentModeFile {
		t.Fatalf("decoded mode = %q, want file", decoded.Mode)
	}
	if decoded.File == nil {
		t.Fatal("expected decoded file preview payload")
	}
	if decoded.File.HighlightPath != "diff" {
		t.Fatalf("decoded highlight path = %q, want diff", decoded.File.HighlightPath)
	}
	if len(decoded.File.FocusLines) != 2 || decoded.File.FocusLines[0] != 2 || decoded.File.FocusLines[1] != 3 {
		t.Fatalf("decoded focus lines = %v, want [2 3]", decoded.File.FocusLines)
	}
	if !decoded.File.Truncated {
		t.Fatal("expected truncated file preview flag")
	}
}
