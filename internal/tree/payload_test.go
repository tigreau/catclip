package tree

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
