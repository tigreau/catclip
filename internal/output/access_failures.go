package output

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/tigreau/catclip/internal/search"
)

var ErrNoAccessibleOutput = errors.New("No accessible files remain; no output was exported.")

type lazyWriter struct {
	start func() (io.Writer, error)
	w     io.Writer
}

func (w *lazyWriter) Write(p []byte) (int, error) {
	if w.w == nil {
		var err error
		w.w, err = w.start()
		if err != nil {
			return 0, err
		}
	}
	return w.w.Write(p)
}

// payloadWriter observes actual writes, never probes source files. An input
// access error is skippable only before this entry has written any bytes.
type payloadWriter struct {
	w       io.Writer
	n       int64
	skipped []string
}

func (w *payloadWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

func (w *payloadWriter) skip(err error, abs, rel string, before int64) bool {
	if err == nil || before != w.n {
		return false
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || filepath.Clean(pathErr.Path) != filepath.Clean(abs) {
		return false
	}
	switch pathErr.Op {
	case "open", "read", "readat", "stat", "lstat":
	default:
		return false
	}
	if !search.RecordAccessFailure(err, false) {
		return false
	}
	w.skipped = append(w.skipped, rel)
	return true
}

// WithoutFiles adjusts success accounting after real export reads skipped
// files. Metadata/path sections remain valid independently of source reads.
func (p Plan) WithoutFiles(paths []string) Plan {
	if len(paths) == 0 {
		return p
	}
	missing := make(map[string]bool, len(paths))
	for _, path := range paths {
		missing[path] = true
	}
	filter := func(items []PlanItem) []PlanItem {
		out := make([]PlanItem, 0, len(items))
		for _, item := range items {
			if item.kind != SectionKindFiles || !missing[item.relPath] {
				out = append(out, item)
			}
		}
		return out
	}
	p.items = filter(p.items)
	p.sections = append([]PlanSection(nil), p.sections...)
	for i := range p.sections {
		p.sections[i].items = filter(p.sections[i].items)
	}
	return p
}
