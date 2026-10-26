package ui

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/platform"
)

func TestMetadataSizeAndRecencyColors(t *testing.T) {
	colors := platform.ANSIPalette()
	now := time.Date(2026, 10, 24, 0, 5, 0, 0, time.FixedZone("local", 3*60*60))
	for _, tc := range []struct {
		bytes                int64
		modified             time.Time
		sizeColor, dateColor string
	}{
		{39999, now, colors.Dim, colors.Bold + colors.Prompt},
		{40000, now.Add(-10 * time.Minute), colors.Warn, colors.Prompt},
		{199999, now.AddDate(0, 0, -2), colors.Warn, colors.Dim},
		{200000, time.Time{}, colors.Err, colors.Dim},
	} {
		label := formatFinderModifiedSpec(now, tc.modified)
		row := MetadataRow{Path: "src/main.go", Bytes: tc.bytes, Size: "SIZE", Tokens: "~50,000", Git: "S", Modified: label}
		var plain, colored bytes.Buffer
		style := &metadataStyle{colors: colors}
		if err := writeMetadataRows(&plain, []MetadataRow{row}); err != nil {
			t.Fatal(err)
		}
		if err := writeMetadataRows(&colored, []MetadataRow{row}, style); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			tc.sizeColor + "SIZE" + colors.Reset,
			tc.dateColor + label + colors.Reset,
			colors.Dim + "src/" + colors.Reset + colors.Bold + "main.go" + colors.Reset,
			colors.Reset + "  ~50,000  " + colors.OK,
		} {
			if !strings.Contains(colored.String(), want) {
				t.Errorf("bytes=%d missing %q in %q", tc.bytes, want, colored.String())
			}
		}
		if got := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(colored.String(), ""); got != plain.String() {
			t.Fatal("changed plain layout")
		}
	}
}

func coloredMetadataFixture() *MetadataReport {
	return &MetadataReport{
		Root: "project", Generated: "2026-10-24T12:00:00Z",
		Git: &MetadataGitSummary{Branch: "main", Commit: "abc123", Modified: 1, Staged: 1, Untracked: 1},
		Scopes: []MetadataScope{
			{Summary: "src --only '*.go'", Selected: 2, Visible: 3, VisibleKnown: true, Ignored: MetadataIgnoredSummary{Total: 1, Rows: []MetadataIgnoredPath{{Path: "src/cache", Kind: "directory", Source: ".gitignore", Pattern: "cache/"}}}},
			{Summary: "assets --no-ignore", Selected: 1, NoIgnore: true},
		},
		Composition:     []MetadataAggregate{{Label: ".go", Count: 2, Bytes: 80, Tokens: 20}},
		DirectoryGroups: []MetadataAggregate{{Label: "src/", Count: 2, Bytes: 80, Tokens: 20}},
		LargestFiles:    []MetadataLargestFile{{Path: "src/main.go", Bytes: 60, Tokens: 15}},
		Rows: []MetadataRow{
			{Path: "src/main.go", Size: "60B", Tokens: "~15", Git: "M", Modified: "Today"},
			{Path: "src/long name.go", Size: "20B", Tokens: "~5", Git: "A", Modified: "Yesterday"},
			{Path: "assets/image.png", Size: "1KB", Tokens: "—", Git: "?", Modified: "Yesterday", Flag: "binary"},
		}, TotalBytes: 1104, TextTokens: 20, BinaryCount: 1,
	}
}

func TestMetadataColorsPreserveLayoutAndPlainAccounting(t *testing.T) {
	strip := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for _, flags := range []bool{false, true} {
		report := coloredMetadataFixture()
		if !flags {
			report.Rows = report.Rows[:2]
			report.BinaryCount = 0
			report.Scopes = report.Scopes[:1]
		}
		var plain, colored, disabled bytes.Buffer
		if err := WriteMetadataReport(&plain, report); err != nil {
			t.Fatal(err)
		}
		added, err := WriteMetadataReportWithPalette(&colored, report, platform.ANSIPalette())
		if err != nil {
			t.Fatal(err)
		}
		if added <= 0 || !strings.Contains(colored.String(), "\x1b[") {
			t.Fatal("missing colors")
		}
		if got := strip.ReplaceAllString(colored.String(), ""); got != plain.String() {
			t.Fatalf("layout changed\nwant %q\ngot  %q", plain.String(), got)
		}
		if int64(colored.Len()-plain.Len()) != added {
			t.Fatal("ANSI byte accounting wrong")
		}
		size, err := report.EncodedSize()
		if err != nil || size != int64(plain.Len()) {
			t.Fatalf("plain size=%d err=%v", size, err)
		}
		added, err = WriteMetadataReportWithPalette(&disabled, report, platform.Palette{})
		if err != nil || added != 0 || disabled.String() != plain.String() {
			t.Fatal("disabled colors changed payload")
		}
	}
}

func TestMetadataColorCountDoesNotStripSourceEscapes(t *testing.T) {
	report := coloredMetadataFixture()
	report.Root = "project\x1b[0m"
	var plain, colored bytes.Buffer
	if err := WriteMetadataReport(&plain, report); err != nil {
		t.Fatal(err)
	}
	added, err := WriteMetadataReportWithPalette(&colored, report, platform.ANSIPalette())
	if err != nil || int64(colored.Len())-added != int64(plain.Len()) {
		t.Fatal("counted source escape as presentation")
	}
	if !strings.Contains(colored.String(), report.Root) {
		t.Fatal("source escaped bytes changed")
	}
}

type metadataFailedWriter struct{}

func (metadataFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestMetadataColorsPreserveFailures(t *testing.T) {
	if _, err := WriteMetadataReportWithPalette(io.Discard, nil, platform.ANSIPalette()); err == nil {
		t.Fatal("nil report succeeded")
	}
	if _, err := WriteMetadataReportWithPalette(metadataFailedWriter{}, coloredMetadataFixture(), platform.ANSIPalette()); err == nil {
		t.Fatal("sink error lost")
	}
}

func BenchmarkMetadataPresentation(b *testing.B) {
	for _, count := range []int{10, 100000} {
		report := coloredMetadataFixture()
		report.Rows = make([]MetadataRow, count)
		for i := range report.Rows {
			report.Rows[i] = MetadataRow{Path: fmt.Sprintf("src/package/file-%06d.go", i), Size: "1KB", Tokens: "~256", Git: "M", Modified: "Today"}
		}
		for _, colored := range []bool{false, true} {
			b.Run(fmt.Sprintf("rows%d/color%v", count, colored), func(b *testing.B) {
				palette := platform.Palette{}
				if colored {
					palette = platform.ANSIPalette()
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := WriteMetadataReportWithPalette(io.Discard, report, palette); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
