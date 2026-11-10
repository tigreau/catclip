package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

func TestPreviewSkipUsesActualWriteBoundary(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "bad.go")
	plan := BuildPlan([]PreparedFileUnit{{Entry: discovery.Entry{AbsPath: abs, RelPath: "bad.go", Mode: command.EntryModeFull}}})
	for _, tc := range []struct {
		name, op, path, prefix string
		cause                  error
		skip                   bool
	}{
		{"permission", "open", abs, "", os.ErrPermission, true},
		{"vanished", "open", abs, "", os.ErrNotExist, true},
		{"partial-input", "read", abs, "partial source", os.ErrPermission, false},
		{"destination", "write", abs, "", os.ErrPermission, false},
		{"other-path", "open", abs + ".other", "", os.ErrPermission, false},
		{"other-io", "read", abs, "", io.ErrUnexpectedEOF, false},
		{"cancelled", "read", abs, "", context.Canceled, false},
		{"preview-cap", "read", abs, "", ErrPreviewLimitReached, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := &os.PathError{Op: tc.op, Path: tc.path, Err: tc.cause}
			var out bytes.Buffer
			skipped, err := WriteOutputPlanPreview(&out, EmitConfig{}, plan, func(w io.Writer, _ PreparedFileUnit) (bool, error) {
				if _, err := io.WriteString(w, tc.prefix); err != nil {
					t.Fatal(err)
				}
				return true, want
			})
			if tc.skip {
				if err != nil || len(skipped) != 1 || skipped[0].Vanished != errors.Is(tc.cause, os.ErrNotExist) {
					t.Fatalf("did not report skippable preview input: %v %+v", err, skipped)
				}
			} else if !errors.Is(err, want) || len(skipped) != 0 {
				t.Fatalf("suppressed a non-skippable preview error: %v %+v", err, skipped)
			}
		})
	}
}
