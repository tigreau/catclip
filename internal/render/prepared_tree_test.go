package render

import (
	"bytes"
	"testing"
)

func TestPreparedTreeDetachesRowsAndPreservesRendering(t *testing.T) {
	size := int64(100)
	rows := []DocumentEntry{{Path: "z/src/a.go", Size: &size, GitStatus: "M", ModeTag: "lines 1-2", TargetRoot: "z", IgnoreBypassed: true}, {Path: "a/src/b.go", Size: &size}}
	p := PrepareTree(rows)
	for _, opts := range []RenderOptions{{ShowSizes: true, ShowModeTags: true, ShowGitStatus: true}, {Bare: true}, {}} {
		var want, got bytes.Buffer
		colors := Palette{Dir: "\x1b[34m", Reset: "\x1b[0m"}
		if err := renderEntries(&want, rows, false, opts, colors); err != nil {
			t.Fatal(err)
		}
		if err := p.Render(&got, opts, colors); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want.Bytes(), got.Bytes()) {
			t.Fatalf("render mismatch opts=%+v", opts)
		}
	}
	var want, got bytes.Buffer
	if err := p.Render(&want, RenderOptions{ShowSizes: true}, Palette{}); err != nil {
		t.Fatal(err)
	}
	size = 999
	rows[0].Path = "corrupt"
	if err := p.Render(&got, RenderOptions{ShowSizes: true}, Palette{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want.Bytes(), got.Bytes()) {
		t.Fatal("prepared rows retained mutable inputs")
	}
}
