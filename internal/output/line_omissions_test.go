package output

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

func TestLineOmissionSummaryBounded(t *testing.T) {
	for _, count := range []int{1, 3, 10, 11, 1000, 100000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var omissions lineOmissions
			for i := 0; i < count; i++ {
				omissions.add(discovery.Entry{RelPath: fmt.Sprintf("src/%d.go", i), LinesStart: 100, LinesEnd: 150}, 42)
			}
			var diagnostic bytes.Buffer
			plan := Plan{lineOmissions: omissions}
			if err := plan.WriteLineOmissions(&diagnostic, platform.Palette{}); err != nil {
				t.Fatal(err)
			}
			var colored bytes.Buffer
			palette := platform.ANSIPalette()
			if err := plan.WriteLineOmissions(&colored, palette); err != nil {
				t.Fatal(err)
			}
			var expectedColor strings.Builder
			for _, line := range strings.Split(strings.TrimSuffix(diagnostic.String(), "\n\n"), "\n") {
				expectedColor.WriteString(palette.Warn + line + palette.Reset + "\n")
			}
			expectedColor.WriteString("\n")
			if colored.String() != expectedColor.String() {
				t.Fatal("omission diagnostics must use the warning palette on every line")
			}
			if count <= 10 {
				if strings.Count(diagnostic.String(), "file has 42 lines.") != count {
					t.Fatal(diagnostic.String())
				}
			} else {
				want := fmt.Sprintf("Omitted %d files: requested lines 100-150 were unavailable.\nOmitted files average 42 lines; longest is 42 lines.\n\n", count)
				if diagnostic.String() != want {
					t.Fatal(diagnostic.String())
				}
				if len(omissions.groups[0].samples) != 0 {
					t.Fatal("retained broad paths")
				}
			}
		})
	}
}

func TestLineOmissionExplicitSurvivesEquivalentBroadSelection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "body.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var entries []discovery.Entry
	for i := 0; i < 12; i++ {
		entries = append(entries, discovery.Entry{AbsPath: path, RelPath: fmt.Sprintf("src/%02d.txt", i), TargetRoot: "src", Mode: command.EntryModeLines, Lines: true, LinesStart: 10, LinesEnd: 20})
	}
	explicit := entries[0]
	explicit.TargetRoot = explicit.RelPath
	entries = append(entries, explicit)
	plan, err := BuildPlanForResolvedScopes(git.Context{}, nil, nil, entries)
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic bytes.Buffer
	if err := plan.WriteLineOmissions(&diagnostic, platform.Palette{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostic.String(), "Omitted src/00.txt:") || !strings.Contains(diagnostic.String(), "Omitted 11 files:") || strings.Contains(diagnostic.String(), "src/01.txt") {
		t.Fatal(diagnostic.String())
	}
}

func TestLineOmissionScanAndPayload(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "body.txt")
	// CRLF and a final unterminated empty-looking line exercise scanner counts.
	if err := os.WriteFile(path, []byte("a\r\nb\r\nlast"), 0600); err != nil {
		t.Fatal(err)
	}
	entry := discovery.Entry{AbsPath: path, RelPath: "body.txt", Lines: true, Mode: command.EntryModeLines, LinesStart: 2, LinesEnd: 100}
	plan, err := PreparePlan(git.Context{}, []discovery.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	_, size := plan.PayloadSizes()
	if size != 7 || plan.HasLineOmissions() {
		t.Fatalf("size=%d omissions=%v", size, plan.HasLineOmissions())
	}
	var out bytes.Buffer
	if err := WriteOutputPlanPayload(&out, EmitConfig{}, plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `lines="2-3"`) {
		t.Fatal(out.String())
	}
	// Diagnostics are retained without another read, including after file removal.
	entry.LinesStart = 50
	plan, err = PreparePlan(git.Context{}, []discovery.Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := plan.WriteLineOmissions(&out, platform.Palette{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "file has 3 lines.") || !plan.IsEmpty() {
		t.Fatal(out.String())
	}
}

func TestLinesPreviewOmissionsAndActualBounds(t *testing.T) {
	root := t.TempDir()
	var entries []discovery.Entry
	for i, body := range []string{"short\n", "a\nb\nc\nd\n", ""} {
		name := fmt.Sprintf("%d.txt", i)
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, discovery.Entry{AbsPath: path, RelPath: name, Lines: true, Mode: command.EntryModeLines, LinesStart: 3, LinesEnd: 10})
	}
	for _, raw := range []bool{false, true} {
		var out bytes.Buffer
		cap := NewPreviewCapWriter(&out, context.Background(), PreviewByteLimit)
		if err := WriteLinesPreview(cap, EmitConfig{Raw: raw}, buildLinesPreviewPlan(entries)); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if strings.Contains(got, "Omitted") || strings.Contains(got, "0.txt") || strings.Contains(got, "2.txt") || !strings.Contains(got, "d\n") {
			t.Fatal(got)
		}
		if !raw && !strings.Contains(got, `lines="3-4"`) {
			t.Fatal(got)
		}
		if raw && strings.Contains(got, "<file") {
			t.Fatal(got)
		}
	}
}

func TestLinesPreviewSkipsOmissionsAndCapsSurvivingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.txt")
	if err := os.WriteFile(path, []byte("a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	largePath := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(largePath, []byte(strings.Repeat(strings.Repeat("x", 2048)+"\n", 20)), 0600); err != nil {
		t.Fatal(err)
	}
	entries := make([]discovery.Entry, 100000)
	for i := range entries {
		entries[i] = discovery.Entry{AbsPath: path, RelPath: "short.txt", Lines: true, Mode: command.EntryModeLines, LinesStart: 10, LinesEnd: 20}
		if i == 50 {
			entries[i].AbsPath = largePath
		}
		// Only surviving content exhausts the cap; skipped entries add no text.
		if i > 50 {
			entries[i].AbsPath = filepath.Join(t.TempDir(), "missing.txt")
			break
		}
	}
	plan := buildLinesPreviewPlan(entries)
	cap := NewPreviewCapWriter(io.Discard, context.Background(), 1024)
	if err := WriteLinesPreview(cap, EmitConfig{}, plan); !errors.Is(err, ErrPreviewLimitReached) || !cap.Truncated() {
		t.Fatalf("%v; truncated=%v", err, cap.Truncated())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cap = NewPreviewCapWriter(io.Discard, ctx, PreviewByteLimit)
	if err := WriteLinesPreview(cap, EmitConfig{}, plan); !errors.Is(err, ErrPreviewLimitReached) || !cap.Cancelled() || cap.BytesWritten() != 0 {
		t.Fatalf("cancel: %v", err)
	}
}

func BenchmarkLineOmissions100K(b *testing.B) {
	entries := make([]discovery.Entry, 100000)
	for i := range entries {
		entries[i] = discovery.Entry{RelPath: fmt.Sprintf("src/%d.txt", i), TargetRoot: "src", Mode: command.EntryModeLines, Lines: true, LinesStart: 100, LinesEnd: 150}
	}
	for _, count := range []int{0, 10, 1000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var omissions lineOmissions
				omissions.noteExplicit(entries)
				for _, entry := range entries[:count] {
					omissions.add(entry, 42)
				}
				var diagnostic bytes.Buffer
				if err := (Plan{lineOmissions: omissions}).WriteLineOmissions(&diagnostic, platform.Palette{}); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(diagnostic.Len()), "diagnostic-bytes")
			}
		})
	}
}

type previewCancelDuringScan struct {
	context.Context
	checks int
}

func (c *previewCancelDuringScan) Err() error {
	c.checks++
	if c.checks >= 5 {
		return context.Canceled
	}
	return nil
}

func TestLinesPreviewCancellationWhileSkipping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 1000)), 0600); err != nil {
		t.Fatal(err)
	}
	plan := buildLinesPreviewPlan([]discovery.Entry{{AbsPath: path, RelPath: "body.txt", Lines: true, Mode: command.EntryModeLines, LinesStart: 2000}})
	ctx := &previewCancelDuringScan{Context: context.Background()}
	cap := NewPreviewCapWriter(io.Discard, ctx, PreviewByteLimit)
	if err := WriteLinesPreview(cap, EmitConfig{}, plan); !errors.Is(err, ErrPreviewLimitReached) || !cap.Cancelled() || cap.BytesWritten() != 0 {
		t.Fatalf("skip cancellation: err=%v cancelled=%v bytes=%d", err, cap.Cancelled(), cap.BytesWritten())
	}
}

func TestLineOmissionGroupsKeepTheirOwnLengths(t *testing.T) {
	var omissions lineOmissions
	for i := 0; i < 12; i++ {
		entry := discovery.Entry{RelPath: fmt.Sprint(i), LinesStart: 100, LinesEnd: 150}
		if i >= 6 {
			entry.LinesStart, entry.LinesEnd = 200, 0
		}
		omissions.add(entry, i)
	}
	var out bytes.Buffer
	if err := (Plan{lineOmissions: omissions}).WriteLineOmissions(&out, platform.Palette{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Omitted 6 files: requested lines 100-150", "average 3 lines; longest is 5 lines", "requested lines 200-", "average 9 lines; longest is 11 lines"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(out.String())
		}
	}
}

// Warm-file control isolates the added diagnostics from the existing slice read.
// Both arms perform the identical preparation scan; neither measures cold boot.
func BenchmarkLineOmissionPreparation(b *testing.B) {
	root := b.TempDir()
	entries := make([]discovery.Entry, 1000)
	for i := range entries {
		name := fmt.Sprintf("%d.txt", i)
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("first\nsecond\n"), 0600); err != nil {
			b.Fatal(err)
		}
		entries[i] = discovery.Entry{AbsPath: path, RelPath: name, Mode: command.EntryModeLines, Lines: true, LinesStart: 10, LinesEnd: 20}
	}
	for _, enabled := range []bool{false, true} {
		// Access fresh fixture files before timing either arm; first access on
		// Windows can dominate this bookkeeping comparison.
		if _, err := prepareFileUnits(git.Context{}, entries, nil); err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("diagnostics=%v", enabled), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var collector *lineOmissions
				if enabled {
					collector = &lineOmissions{}
				}
				if _, err := prepareFileUnits(git.Context{}, entries, collector); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLinesPreview(b *testing.B) {
	for _, body := range []struct{ name, text string }{
		{"small", strings.Repeat("line\n", 40)},
		{"large", strings.Repeat(strings.Repeat("x", 1023)+"\n", 260)},
	} {
		path := filepath.Join(b.TempDir(), "body.txt")
		if err := os.WriteFile(path, []byte(body.text), 0600); err != nil {
			b.Fatal(err)
		}
		plan := buildLinesPreviewPlan([]discovery.Entry{{AbsPath: path, RelPath: "body.txt", Lines: true, Mode: command.EntryModeLines, LinesStart: 1, LinesEnd: 999999}})
		for _, updated := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/updated=%v", body.name, updated), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					cap := NewPreviewCapWriter(io.Discard, context.Background(), PreviewByteLimit)
					var err error
					if updated {
						err = WriteLinesPreview(cap, EmitConfig{}, plan)
					} else {
						err = WriteOutputPlanPayloadWithoutPrefetch(cap, EmitConfig{}, plan)
					}
					if err != nil && !errors.Is(err, ErrPreviewLimitReached) {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
