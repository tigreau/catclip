package output

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
)

// Only the final partial unit uses this reader. Complete units retain their
// ordinary emit/prefetch path; line counting never touches preceding files.
func emitTokenCutFile(w io.Writer, unit PreparedFileUnit, raw bool, cutBytes int64) (limitPosition, error) {
	c := &sourceCutWriter{w: w, remaining: cutBytes, line: 1, lastLine: 1, partial: true}
	if len(unit.Payload) > 0 {
		return emitPreparedTokenCut(c, unit)
	}
	f, err := os.Open(unit.Entry.AbsPath)
	if err != nil {
		return c.position(), err
	}
	defer f.Close()
	if unit.Entry.Lines || len(unit.SnippetRanges) > 0 {
		return emitRangedTokenCut(c, unit, f, raw)
	}
	if !raw {
		if err := writePartialOpen(w, unit, ""); err != nil {
			return c.position(), err
		}
	}
	// Four bytes of lookahead suffice to avoid splitting a UTF-8 character.
	// LimitReader also bounds speculative reads on a very large final file.
	readLimit := c.remaining
	if readLimit <= math.MaxInt64-4 {
		readLimit += 4
	}
	r := bufio.NewReaderSize(io.LimitReader(f, readLimit), 32*1024)
	for c.remaining > 0 {
		want := r.Size()
		if c.remaining < int64(want)-4 {
			want = int(c.remaining) + 4
		}
		p, readErr := r.Peek(want)
		if len(p) == 0 {
			if readErr == io.EOF {
				break
			}
			return c.position(), readErr
		}
		// Keep lookahead at every buffer boundary, not only at the budget's
		// end: an earlier chunk must not emit half of the final character.
		n := utf8CutLength(p, min(len(p), r.Size()-4, int(min(c.remaining, int64(len(p))))))
		if n == 0 {
			break
		}
		if err := c.write(p[:n]); err != nil {
			return c.position(), err
		}
		_, _ = r.Discard(n)
	}
	if !raw {
		err = c.close()
	}
	return c.position(), err
}

type sourceCutWriter struct {
	w         io.Writer
	remaining int64
	line      int
	lastLine  int
	partial   bool
}

func (c *sourceCutWriter) position() limitPosition {
	return limitPosition{line: c.lastLine, partial: c.partial}
}

func (c *sourceCutWriter) write(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	n := utf8CutLength(p, int(min(c.remaining, int64(len(p)))))
	if n == 0 {
		c.remaining = 0
		return nil
	}
	p = p[:n]
	if _, err := c.w.Write(p); err != nil {
		return err
	}
	c.remaining -= int64(n)
	c.line += bytes.Count(p, []byte{'\n'})
	c.partial = p[len(p)-1] != '\n'
	c.lastLine = c.line
	if !c.partial {
		c.lastLine--
	}
	return nil
}

func (c *sourceCutWriter) close() error {
	tag := fileCloseTag
	if c.partial {
		tag = fileCloseTagWithNewline
	}
	_, err := c.w.Write(tag)
	return err
}

func writePartialOpen(w io.Writer, unit PreparedFileUnit, lines string) error {
	tag := buildFileOpenTagWithLines(unit.Entry.RelPath, unit.sourceType, lines)
	tag = append(tag[:len(tag)-2], []byte(" partial=\"true\">\n")...)
	_, err := w.Write(tag)
	return err
}

func emitRangedTokenCut(c *sourceCutWriter, unit PreparedFileUnit, r io.Reader, raw bool) (limitPosition, error) {
	ranges := unit.SnippetRanges
	if len(ranges) == 0 {
		end := unit.Entry.LinesEnd
		if end <= 0 {
			end = int(^uint(0) >> 1)
		}
		ranges = []SnippetRange{{Start: max(1, unit.Entry.LinesStart), End: end}}
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, readBufferSize()), 10*1024*1024)
	line, index := 0, 0
	hardCut := false
	// A tiny budget must not read a multi-megabyte selected line just to
	// truncate it. Only the cutoff reader uses this bounded final token.
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		ri := index
		for ri < len(ranges) && line+1 > ranges[ri].End {
			ri++
		}
		if ri < len(ranges) && line+1 >= ranges[ri].Start && int64(len(data)) > c.remaining &&
			(atEOF || int64(len(data))-c.remaining >= 4) {
			newline := bytes.IndexByte(data, '\n')
			lineBytes := newline + 1
			if newline > 0 && data[newline-1] == '\r' {
				// ScanLines removes CR; selected ranges emit one LF. A whole
				// normalized line may fit even when its on-disk CRLF does not.
				lineBytes--
			}
			if newline < 0 || int64(lineBytes) > c.remaining {
				n := utf8CutLength(data, int(c.remaining))
				hardCut = true
				return n, data[:n], bufio.ErrFinalToken
			}
		}
		return bufio.ScanLines(data, atEOF)
	})
	opened := false
	dest := c.w
	for c.remaining > 0 && scanner.Scan() {
		line++
		for index < len(ranges) && line > ranges[index].End {
			if opened && !raw {
				c.w = dest
				if err := c.close(); err != nil {
					return c.position(), err
				}
			}
			opened = false
			index++
		}
		if index == len(ranges) {
			break
		}
		if line < ranges[index].Start {
			continue
		}
		if !opened {
			if !raw {
				// The requested end may be omitted by the hard cut. A start-only
				// range and partial attribute do not promise nonexistent lines.
				if err := writePartialOpen(dest, unit, fmt.Sprintf("%d-", line)); err != nil {
					return c.position(), err
				}
			}
			c.w = dest
			if unit.Entry.Lines && !raw {
				c.w = &numberedWriter{w: dest, line: line, atLineStart: true}
			}
			opened = true
		}
		c.line, c.lastLine, c.partial = line, line, true
		data := scanner.Bytes()
		before := c.remaining
		if err := c.write(data); err != nil {
			return c.position(), err
		}
		if hardCut || before < int64(len(data)) {
			break
		}
		if c.remaining > 0 {
			if err := c.write([]byte{'\n'}); err != nil {
				return c.position(), err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return c.position(), err
	}
	c.w = dest
	if opened && !raw {
		if err := c.close(); err != nil {
			return c.position(), err
		}
	}
	return c.position(), nil
}

func emitPreparedTokenCut(c *sourceCutWriter, unit PreparedFileUnit) (limitPosition, error) {
	payload := unit.Payload
	ranges := unit.SnippetRanges
	for index := 0; len(payload) > 0 && c.remaining > 0; index++ {
		// Headers are generated by Catclip. Body boundaries come from retained
		// range lengths, never from looking for wrapper-like text in source.
		headerEnd := bytes.IndexByte(payload, '\n') + 1
		if headerEnd == 0 {
			return c.position(), fmt.Errorf("invalid prepared file header")
		}
		header := payload[:headerEnd]
		body := payload[headerEnd:]
		if len(ranges) > 0 {
			if index >= len(ranges) {
				return c.position(), fmt.Errorf("invalid prepared snippet ranges")
			}
			c.line, c.lastLine, c.partial = ranges[index].Start, ranges[index].Start, true
			end := 0
			for line := ranges[index].Start; line <= ranges[index].End; line++ {
				n := bytes.IndexByte(body[end:], '\n')
				if n < 0 {
					return c.position(), fmt.Errorf("invalid prepared snippet body")
				}
				end += n + 1
			}
			body = body[:end]
		} else {
			body = body[:len(body)-len(fileCloseTag)]
		}
		partial := int64(len(body)) > c.remaining
		if partial {
			tag := append(append([]byte(nil), header[:len(header)-2]...), []byte(" partial=\"true\">\n")...)
			if _, err := c.w.Write(tag); err != nil {
				return c.position(), err
			}
		} else if _, err := c.w.Write(header); err != nil {
			return c.position(), err
		}
		if err := c.write(body); err != nil {
			return c.position(), err
		}
		if err := c.close(); err != nil {
			return c.position(), err
		}
		if partial {
			break
		}
		payload = payload[headerEnd+len(body)+len(fileCloseTag):]
	}
	pos := c.position()
	pos.outputLine = len(ranges) == 0
	return pos, nil
}
