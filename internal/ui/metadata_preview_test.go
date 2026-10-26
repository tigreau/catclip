package ui

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func TestMetadataOutputPreviewUsesPresentationColors(t *testing.T) {
	report := coloredMetadataFixture()
	report.Rows[0].Bytes, report.Rows[0].Size = 40000, "40KB"
	var plain, styled bytes.Buffer
	if err := WriteMetadataReport(&plain, report); err != nil {
		t.Fatal(err)
	}
	// Colors alone must not push a plain payload across the bundle threshold.
	report.Root += strings.Repeat("x", output.BundleThreshold-1-plain.Len())
	plain.Reset()
	if err := WriteMetadataReport(&plain, report); err != nil {
		t.Fatal(err)
	}
	colors := platform.ANSIPalette()
	if _, err := WriteMetadataReportWithPalette(&styled, report, colors); err != nil {
		t.Fatal(err)
	}
	ctx := StartupSinkPickerContext{Config: command.Parsed{PayloadKind: command.PayloadMetadata}, Metadata: report}
	before := measureStartupSinkPayload(ctx)
	preview, err := renderSinkPreviewWithModeContext(context.Background(), ctx, sinkPreviewModeOutputText, output.PreviewByteLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preview.Body, styled.Bytes()) || !bytes.Contains(preview.Body, []byte(colors.Warn+"40KB")) {
		t.Fatalf("preview did not use existing metadata styling: %q", preview.Body)
	}
	if preview.Truncated || !preview.FullBytesKnown || preview.FullBytes != int64(plain.Len()) {
		t.Fatalf("incorrect raw size: %+v", preview)
	}
	if styled.Len() <= output.BundleThreshold || before.WouldBundle || before.Bytes != int64(plain.Len()) {
		t.Fatalf("bad threshold fixture/measurement: %+v", before)
	}
	after := measureStartupSinkPayload(ctx)
	if before.Bytes != after.Bytes || before.WouldBundle != after.WouldBundle {
		t.Fatal("preview changed transport decision")
	}
	var payload bytes.Buffer
	if err := WriteMetadataReport(&payload, report); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload.Bytes(), plain.Bytes()) || bytes.Contains(payload.Bytes(), []byte("\x1b[")) {
		t.Fatal("preview styling leaked into payload")
	}
}

func TestMetadataOutputPreviewCapAndCancellation(t *testing.T) {
	report := coloredMetadataFixture()
	size, err := report.EncodedSize()
	if err != nil {
		t.Fatal(err)
	}
	strip := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for _, limit := range []int64{0, 1, 7, 9, 12, 24, 100, 500} {
		preview, err := renderSinkMetadataPreviewContext(context.Background(), report, limit)
		if err != nil {
			t.Fatal(err)
		}
		if !preview.Truncated || !preview.FullBytesKnown || preview.FullBytes != size {
			t.Fatalf("limit=%d incorrect accounting: %+v", limit, preview)
		}
		if int64(len(preview.Body)) > limit+int64(len(platform.ANSIPalette().Reset)) {
			t.Fatal("unbounded presentation")
		}
		if bytes.ContainsRune(strip.ReplaceAll(preview.Body, nil), '\x1b') {
			t.Fatalf("incomplete ANSI sequence: %q", preview.Body)
		}
		if !bytes.HasSuffix(preview.Body, []byte(platform.ANSIPalette().Reset)) {
			t.Fatal("truncation footer can inherit color")
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderSinkMetadataPreviewContext(cancelled, report, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if _, err := renderSinkMetadataPreviewContext(context.Background(), nil, 100); err == nil {
		t.Fatal("nil report succeeded")
	}
}
