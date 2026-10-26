package output

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/tigreau/catclip/internal/discovery"
)

// WriteLinesPreview shows only available excerpts, without omission diagnostics.
// A bounded per-file buffer allows actual range headers without a sizing pass.
func WriteLinesPreview(w *PreviewCapWriter, cfg EmitConfig, plan Plan) error {
	for _, item := range plan.items {
		if _, err := w.Write(nil); err != nil {
			return err
		}
		if w.written >= w.limit {
			w.truncated = true
			return ErrPreviewLimitReached
		}
		if err := writeLinesPreviewFile(w, cfg, item.unit.Entry); err != nil {
			return err
		}
	}
	return nil
}

func writeLinesPreviewFile(w *PreviewCapWriter, cfg EmitConfig, entry discovery.Entry) error {
	f, err := os.Open(entry.AbsPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var body bytes.Buffer
	bounded := NewPreviewCapWriter(&body, w.ctx, w.limit-w.written)
	start := max(1, entry.LinesStart)
	var bodyWriter io.Writer = bounded
	if !cfg.Raw {
		bodyWriter = &numberedWriter{w: bounded, line: start, atLineStart: true}
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, readBufferSize()), 10*1024*1024)
	lineNum, lastSelected := 0, 0
	for scanner.Scan() {
		// Cancellation must also work while skipping to a distant start line.
		if _, err := w.Write(nil); err != nil {
			return err
		}
		lineNum++
		if lineNum < start {
			continue
		}
		lastSelected = lineNum
		_, err = bodyWriter.Write(scanner.Bytes())
		if err == nil {
			_, err = bodyWriter.Write([]byte{'\n'})
		}
		if errors.Is(err, ErrPreviewLimitReached) {
			if bounded.Cancelled() {
				w.cancelled = true
				return err
			}
			break
		}
		if err != nil {
			return err
		}
		if entry.LinesEnd > 0 && lineNum == entry.LinesEnd {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if lastSelected == 0 && (entry.LinesStart > 0 || entry.LinesEnd > 0) {
		return nil
	}
	if !cfg.Raw {
		attr := ""
		if entry.LinesStart > 0 {
			attr = fmt.Sprintf("%d-%d", start, lastSelected)
		}
		if _, err := w.Write(buildFileOpenTagWithLines(entry.RelPath, "", attr)); err != nil {
			return err
		}
	}
	if _, err := w.Write(body.Bytes()); err != nil {
		return err
	}
	if bounded.Truncated() {
		w.truncated = true
		return ErrPreviewLimitReached
	}
	if !cfg.Raw {
		_, err = w.Write(fileCloseTag)
	}
	return err
}
