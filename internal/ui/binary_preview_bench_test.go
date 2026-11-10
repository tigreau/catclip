package ui

import (
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
	"github.com/tigreau/catclip/internal/output"
)

func BenchmarkBinarySafeSinkPreview(b *testing.B) {
	for _, count := range []int{1, 100} {
		b.Run(fmt.Sprintf("text-files-%d", count), func(b *testing.B) {
			dir := b.TempDir()
			var units []output.PreparedFileUnit
			body := []byte(strings.Repeat("ordinary source text\n", 50))
			for i := 0; i < count; i++ {
				rel := fmt.Sprintf("file-%d.txt", i)
				abs := filepath.Join(dir, rel)
				if err := os.WriteFile(abs, body, 0600); err != nil {
					b.Fatal(err)
				}
				units = append(units, output.PreparedFileUnit{Entry: discovery.Entry{AbsPath: abs, RelPath: rel, Mode: command.EntryModeFull}, BodyBytes: int64(len(body))})
			}
			plan := output.BuildPlan(units)
			for _, safe := range []bool{false, true} {
				b.Run(fmt.Sprintf("metadata-preview-%v", safe), func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						w := output.NewPreviewCapWriter(io.Discard, context.Background(), output.PreviewByteLimit)
						var err error
						if safe {
							replaced := false
							_, err = output.WriteOutputPlanPreview(w, output.EmitConfig{}, plan, func(w io.Writer, unit output.PreparedFileUnit) (bool, error) {
								return presentSinkFileUnit(context.Background(), w, unit, false, output.PreviewByteLimit, &replaced)
							})
						} else {
							err = output.WriteOutputPlanPayloadWithoutPrefetch(w, output.EmitConfig{}, plan)
						}
						if err != nil && !errors.Is(err, output.ErrPreviewLimitReached) {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
