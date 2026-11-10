package output

import (
	"bytes"
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

func TestEmitSkipsUnavailableBeforeWritingFile(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, lines := range []bool{false, true} {
			for _, size := range []int{64, 2 * 1024 * 1024} {
				t.Run(fmt.Sprintf("raw=%t/lines=%t/bytes=%d", raw, lines, size), func(t *testing.T) {
					root := t.TempDir()
					var entries []discovery.Entry
					for _, name := range []string{"bad.txt", "good.txt"} {
						abs := filepath.Join(root, name)
						if err := os.WriteFile(abs, []byte(strings.Repeat("a\n", size/2)), 0600); err != nil {
							t.Fatal(err)
						}
						e := discovery.Entry{AbsPath: abs, RelPath: name, Mode: command.EntryModeFull}
						if lines {
							e.Mode, e.Lines, e.LinesStart, e.LinesEnd = command.EntryModeLines, true, 1, 2
						}
						entries = append(entries, e)
					}
					plan, err := BuildPlanForResolvedScopes(git.Context{}, nil, nil, entries, raw)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(entries[0].AbsPath); err != nil {
						t.Fatal(err)
					}
					var out bytes.Buffer
					cfg := EmitConfig{OutputMode: command.OutputModeStdout, Raw: raw}
					stats, err := EmitOutputPlan(cfg, RuntimeEnvironment{}, plan, &out, platform.Palette{})
					if err != nil {
						t.Fatal(err)
					}
					if len(stats.SkippedPaths) != 1 || stats.SkippedPaths[0] != "bad.txt" || strings.Contains(out.String(), "bad.txt") || out.Len() == 0 {
						t.Fatalf("incorrect partial export: stats=%+v bytes=%d", stats, out.Len())
					}
					kept := plan.WithoutFiles(stats.SkippedPaths).EntriesInEmissionOrder()
					if len(kept) != 1 || kept[0].RelPath != "good.txt" {
						t.Fatalf("incorrect success accounting: %+v", kept)
					}
					if err := os.Remove(entries[1].AbsPath); err != nil {
						t.Fatal(err)
					}
					out.Reset()
					_, err = EmitOutputPlan(cfg, RuntimeEnvironment{}, plan, &out, platform.Palette{})
					if !errors.Is(err, ErrNoAccessibleOutput) || out.Len() != 0 {
						t.Fatalf("empty export: %v bytes=%d", err, out.Len())
					}
				})
			}
		}
	}
}

func TestAccessSkipPreservesPartialWritesAndOtherErrors(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "source.txt")
	for _, tc := range []struct {
		name    string
		err     error
		written int64
	}{
		{"partial-source", &os.PathError{Op: "read", Path: abs, Err: os.ErrPermission}, 1},
		{"destination-same-path", &os.PathError{Op: "write", Path: abs, Err: os.ErrPermission}, 0},
		{"destination", &os.PathError{Op: "write", Path: abs + ".out", Err: os.ErrPermission}, 0},
		{"other-io", &os.PathError{Op: "read", Path: abs, Err: io.ErrUnexpectedEOF}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &payloadWriter{w: io.Discard, n: tc.written}
			if w.skip(tc.err, abs, "source.txt", 0) {
				t.Fatal("silently suppressed a non-skippable failure")
			}
		})
	}
}
