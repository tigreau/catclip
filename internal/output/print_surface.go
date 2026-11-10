package output

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/theme"
)

const surfaceReset = "\x1b[0m"
const surfaceClearLine = "\x1b[K"

// WithTerminalPrintSurface surrounds the physical terminal sink, outside the
// payload byte counter and its buffer. That keeps accounting plain and lets
// cleanup restore terminal defaults even when emission abandons its buffer on
// an input error. Resolve colors using the original stdout before calling it.
func WithTerminalPrintSurface(w io.Writer, colors platform.Palette, emit func(io.Writer) (EmitStats, error)) (EmitStats, error) {
	if colors.Reset == "" {
		return emit(w)
	}
	selected := theme.Current()
	surface := &terminalPrintSurface{dest: w, lineStart: true, fg: selected.FG, bg: selected.BG}
	stats, err := emit(surface)
	finalizeStarted := time.Now()
	cleanupErr := surface.finish()
	stats.SinkFinalizeDuration += time.Since(finalizeStarted)
	if cleanupErr != nil {
		err = errors.Join(err, cleanupErr)
	}
	return stats, err
}

type terminalPrintSurface struct {
	dest   io.Writer
	fg, bg string
	buffer *bufio.Writer
	// Escape sequences may cross writes. Bound retained state even for malformed
	// source-provided escapes; the payload itself is never buffered here.
	escape    [64]byte
	escapeLen int
	lineStart bool
}

func (s *terminalPrintSurface) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s.buffer == nil {
		s.buffer = bufio.NewWriterSize(s.dest, 32*1024)
		if _, err := s.buffer.WriteString(s.fg + s.bg); err != nil {
			return 0, err
		}
	}
	consumed := 0
	for len(p) > 0 {
		if s.escapeLen > 0 {
			b := p[0]
			s.escape[s.escapeLen] = b
			s.escapeLen++
			p, consumed = p[1:], consumed+1
			// Only SGR needs interpretation. All other input bytes pass through.
			complete := s.escapeLen == 2 && b != '[' || s.escapeLen > 2 && b >= 0x40 && b <= 0x7e
			if complete || s.escapeLen == len(s.escape) {
				seq := s.escape[:s.escapeLen]
				if _, err := s.buffer.Write(seq); err != nil {
					return consumed, err
				}
				if complete && b == 'm' && s.escape[1] == '[' {
					fg, bg := surfaceDefaultsReset(seq[2 : len(seq)-1])
					if fg {
						if _, err := s.buffer.WriteString(s.fg); err != nil {
							return consumed, err
						}
					}
					if bg {
						if _, err := s.buffer.WriteString(s.bg); err != nil {
							return consumed, err
						}
					}
				}
				s.escapeLen = 0
			}
			continue
		}
		if p[0] == '\x1b' {
			s.escape[0], s.escapeLen = p[0], 1
			p, consumed = p[1:], consumed+1
			continue
		}
		if s.lineStart {
			// Clear before writing the line, never after it: clearing at the
			// right margin can erase the last character of a full-width line.
			// This paints blank lines without adding spaces to copied text.
			if _, err := s.buffer.WriteString(surfaceClearLine); err != nil {
				return consumed, err
			}
			s.lineStart = false
		}
		n := bytes.IndexAny(p, "\x1b\n")
		if n < 0 {
			n = len(p)
		} else if p[n] == '\n' {
			n++
		}
		written, err := s.buffer.Write(p[:n])
		consumed += written
		if err != nil {
			return consumed, err
		}
		s.lineStart = p[n-1] == '\n'
		p = p[n:]
	}
	return consumed, nil
}

func (s *terminalPrintSurface) finish() error {
	if s.buffer == nil {
		return nil
	}
	if s.escapeLen > 0 {
		_, _ = s.buffer.Write(s.escape[:s.escapeLen])
	}
	_, err := s.buffer.WriteString(surfaceReset)
	return errors.Join(err, s.buffer.Flush())
}

// Identify defaults restored by an SGR, while respecting later parameters and
// skipping indexed/RGB color operands (a color component of 0 is not a reset).
func surfaceDefaultsReset(params []byte) (fg, bg bool) {
	for {
		part, rest, more := bytes.Cut(params, []byte{';'})
		value := 0
		if len(part) > 0 {
			var err error
			value, err = strconv.Atoi(string(part))
			if err != nil {
				value = -1
			}
		}
		switch {
		case value == 0:
			fg, bg = true, true
		case value == 39:
			fg = true
		case value == 49:
			bg = true
		case value >= 30 && value <= 37 || value >= 90 && value <= 97:
			fg = false
		case value >= 40 && value <= 47 || value >= 100 && value <= 107:
			bg = false
		case value == 38 || value == 48:
			if value == 38 {
				fg = false
			} else {
				bg = false
			}
			mode, tail, found := bytes.Cut(rest, []byte{';'})
			if found {
				count := 0
				if bytes.Equal(mode, []byte{'5'}) {
					count = 1
				}
				if bytes.Equal(mode, []byte{'2'}) {
					count = 3
				}
				if count > 0 {
					rest = tail
					for range count {
						_, rest, more = bytes.Cut(rest, []byte{';'})
					}
				}
			}
		}
		if !more {
			break
		}
		params = rest
	}
	return fg, bg
}
