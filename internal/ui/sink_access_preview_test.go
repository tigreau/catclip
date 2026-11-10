package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/search"
)

func TestSinkOutputPreviewKeepsAccessibleFiles(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, raw := range []bool{false, true} {
			for _, lines := range []bool{false, true} {
				for _, badPosition := range []int{0, 1, 2} {
					t.Run(fmt.Sprintf("missing=%t/raw=%t/lines=%t/bad=%d", missing, raw, lines, badPosition), func(t *testing.T) {
						root := t.TempDir()
						var units []output.PreparedFileUnit
						var bad string
						for i := range 3 {
							name := fmt.Sprintf("file-%d.go", i)
							abs := filepath.Join(root, name)
							body := fmt.Sprintf("// READABLE_%d\n", i)
							if err := os.WriteFile(abs, []byte(body), 0600); err != nil {
								t.Fatal(err)
							}
							entry := discovery.Entry{AbsPath: abs, RelPath: name, Mode: command.EntryModeFull, SizeKnown: true, SizeBytes: int64(len(body))}
							if lines {
								entry.Mode, entry.Lines, entry.LinesStart, entry.LinesEnd = command.EntryModeLines, true, 1, 1
							}
							units = append(units, output.PreparedFileUnit{Entry: entry, BodyBytes: int64(len(body))})
							if i == badPosition {
								bad = abs
							}
						}
						if missing {
							if err := os.Remove(bad); err != nil {
								t.Fatal(err)
							}
						} else {
							if runtime.GOOS == "windows" || os.Geteuid() == 0 {
								t.Skip("requires unprivileged Unix permissions")
							}
							if err := os.Chmod(bad, 0); err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { _ = os.Chmod(bad, 0600) })
						}
						plan := output.BuildPlan(units)
						cfg := output.EmitConfig{Raw: raw}
						preview, err := renderSinkOutputTextPreview(plan, cfg, output.PreviewByteLimit)
						if err != nil {
							t.Fatal(err)
						}
						plain := string(ansiEscape.ReplaceAll(formatSinkPreview(preview), nil))
						for i := range 3 {
							if strings.Contains(plain, fmt.Sprintf("READABLE_%d", i)) != (i != badPosition) {
								t.Fatalf("preview lost a readable sibling or included inaccessible source: %s", plain)
							}
						}
						if len(preview.SkippedPaths) != 1 || preview.SkippedPaths[0].Vanished != missing || !strings.Contains(plain, "Skipped 1 unavailable path(s) in preview:") {
							t.Fatalf("preview lacks omission details: %+v\n%s", preview.SkippedPaths, plain)
						}
						// Reusing the small bundle-decision buffer must retain the
						// same omission notice, without rereading the good files.
						measured := measureOutputForSinkMenu(plan, cfg)
						if measured.Err != nil || measured.RawPreview == nil {
							t.Fatalf("measurement failed or cannot reuse small output: %+v", measured)
						}
						cached, err := renderSinkPreviewWithModeContext(context.Background(), StartupSinkPickerContext{Plan: plan, Emit: cfg, rawOutputPreview: measured.RawPreview}, sinkPreviewModeOutputText, output.PreviewByteLimit)
						if err != nil || len(cached.SkippedPaths) != 1 {
							t.Fatalf("measurement handoff lost access diagnostics: %v %+v", err, cached.SkippedPaths)
						}
						var final bytes.Buffer
						if err := output.WriteOutputPlanPayloadWithoutPrefetch(&final, cfg, plan); err != nil {
							t.Fatal(err)
						}
						if measured.Bytes != int64(final.Len()) || strings.Contains(final.String(), "Skipped") {
							t.Fatal("preview diagnostics contaminated real output or payload measurement")
						}
						artifact := filepath.Join(t.TempDir(), "output-preview.txt")
						writeAsyncSinkPreview(context.Background(), StartupSinkPickerContext{Plan: plan, Emit: cfg}, sinkPreviewModeOutputText, artifact, renderSinkPreviewWithModeContext)
						body, err := os.ReadFile(artifact)
						if err != nil || bytes.Contains(body, []byte("Preview unavailable:")) || !bytes.Contains(body, []byte("Skipped 1 unavailable")) {
							t.Fatalf("async output preview replaced accessible output: %v %s", err, body)
						}
					})
				}
			}
		}
	}
}

func TestSinkPreviewAccessNoticeIsBoundedAndEscapesPaths(t *testing.T) {
	preview := sinkPreview{Body: []byte("readable output\n")}
	for i := range 12 {
		preview.SkippedPaths = append(preview.SkippedPaths, search.AccessFailure{Path: fmt.Sprintf("file-%d.go", i), Vanished: i == 1})
	}
	preview.SkippedPaths[0].Path = "bad\n\x1b[31m.go"
	got := string(formatSinkPreview(preview))
	for _, want := range []string{"readable output", `"bad\n\x1b[31m.go"`, "[permission error]", "[no longer exists]", "…and 2 more unavailable paths"} {
		if !strings.Contains(got, want) {
			t.Fatalf("notice missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "file-10.go") || strings.ContainsRune(got, '\x1b') {
		t.Fatal("notice is unbounded or contains source-controlled terminal escapes")
	}
}
