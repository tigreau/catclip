package ui

import (
	"bytes"
	"context"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/render"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBinarySinkPreviewPreservesActualPayload(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, name := range []string{"known.jar", "unknown.fixture"} {
			body := []byte("BINARY_PAYLOAD\x00data")
			abs := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(abs, body, 0600); err != nil {
				t.Fatal(err)
			}
			plan := output.BuildPlan([]output.PreparedFileUnit{{Entry: discovery.Entry{
				AbsPath: abs, RelPath: name, Mode: command.EntryModeFull,
				SizeKnown: true, SizeBytes: int64(len(body)),
			}, BodyBytes: int64(len(body))}})
			cfg := output.EmitConfig{Raw: raw}
			preview, err := readSinkOutputTextPreview(context.Background(), plan, cfg, 4096, false)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(preview.Body, []byte("[binary]")) || bytes.Contains(preview.Body, []byte("BINARY_PAYLOAD")) || preview.FullBytesKnown {
				t.Fatalf("preview %+v", preview)
			}
			measurement := measureOutputForSinkMenu(plan, cfg)
			var actual bytes.Buffer
			if err := output.WriteOutputPlanPayloadWithoutPrefetch(&actual, cfg, plan); err != nil {
				t.Fatal(err)
			}
			if measurement.Err != nil || measurement.RawPreview != nil || measurement.Bytes != int64(actual.Len()) || !bytes.Contains(actual.Bytes(), body) {
				t.Fatalf("measurement %+v actual %q", measurement, actual.Bytes())
			}
		}
	}
}

func TestBinarySinkPreviewDoesNotMislabelUTF16(t *testing.T) {
	for _, body := range [][]byte{{0xff, 0xfe, 'a', 0}, {0xfe, 0xff, 0, 'a'}} {
		abs := filepath.Join(t.TempDir(), "text.unknown")
		if err := os.WriteFile(abs, body, 0600); err != nil {
			t.Fatal(err)
		}
		plan := output.BuildPlan([]output.PreparedFileUnit{{Entry: discovery.Entry{AbsPath: abs, RelPath: "text.unknown", Mode: command.EntryModeFull}, BodyBytes: int64(len(body))}})
		preview, err := readSinkOutputTextPreview(context.Background(), plan, output.EmitConfig{}, 4096, false)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(preview.Body, []byte("UTF-16 text")) || bytes.Contains(preview.Body, []byte("[binary]")) {
			t.Fatalf("%q", preview.Body)
		}
	}
}

func TestBinaryPreviewMetadataAndTreeColors(t *testing.T) {
	for _, name := range []string{"library.jar", "unknown.fixture"} {
		abs := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(abs, []byte("PRIVATE_BINARY_BYTES\x00"), 0600); err != nil {
			t.Fatal(err)
		}
		doc, ok := buildInternalFullFilePreviewDocument(name, abs, "")
		if !ok || !strings.Contains(doc.File.Content, "[binary]") || strings.Contains(doc.File.Content, "PRIVATE_BINARY_BYTES") {
			t.Fatalf("preview: %+v", doc)
		}
	}
	size := int64(42)
	doc := treeDocument{Mode: treeDocumentModeTree, Entries: []treeDocumentEntry{{Path: "lib.jar", Binary: true, Size: &size}, {Path: "unknown.fixture"}}}
	var payload bytes.Buffer
	if err := encodeTreePayload(&payload, doc); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeTreePayload(&payload)
	if err != nil {
		t.Fatal(err)
	}
	var colored, plain bytes.Buffer
	colors := platform.ANSIPalette()
	if err := renderTreeDocument(&colored, decoded, render.DefaultRenderOptions(), colors); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(colored.String(), colors.Prompt+"lib.jar"+colors.Reset) || !strings.Contains(colored.String(), "[binary]") {
		t.Fatal(colored.String())
	}
	if err := renderTreeDocument(&plain, decoded, render.DefaultRenderOptions(), platform.Palette{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b") || strings.Count(plain.String(), "[binary]") != 1 {
		t.Fatal(plain.String())
	}
	// Focused target previews use captured metadata even after deletion.
	focused := buildFreshTargetTreeDocument(RenderConfig{TreeKind: TreeTargetKindFile}, []discovery.Entry{{RelPath: "lib.jar", SizeKnown: true, SizeBytes: size}}, nil, nil)
	if focused.File == nil || !strings.Contains(focused.File.Content, "42 bytes") {
		t.Fatalf("focused: %+v", focused)
	}
}
