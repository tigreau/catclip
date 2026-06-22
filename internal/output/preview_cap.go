package output

import (
	"context"
	"errors"
	"io"
)

// PreviewByteLimit caps the per-focus bytes any multi-file body preview
// emits to fzf's preview pane. fzf renders a constant number of lines
// regardless of input size, so emitting more is pure waste; on Windows
// every per-file os.Open is intercepted by Defender, so the cap also
// bounds spawn-time linear-in-file-count cost.
const PreviewByteLimit int64 = 128 * 1024

// ErrPreviewLimitReached is returned by PreviewCapWriter.Write once the
// byte cap is reached OR the supplied cancellation context fires. The
// same sentinel covers both because callers handle them identically:
// stop the per-entry write loop, no more output is wanted.
var ErrPreviewLimitReached = errors.New("preview limit reached")

// PreviewCapWriter wraps an io.Writer with two short-circuit conditions:
//
//  1. Byte cap: once cumulative bytes written reach `limit`, further
//     writes return (0, ErrPreviewLimitReached). The write that hits
//     the cap returns the partial byte count actually flushed plus the
//     sentinel.
//  2. Cancellation: if `ctx` fires (e.g. fzf SIGTERMed the preview
//     child because the user moved focus), Write returns
//     (0, ErrPreviewLimitReached) on the next call without touching
//     the underlying writer.
//
// Callers must treat errors.Is(err, ErrPreviewLimitReached) as
// success-with-truncation/cancellation, not failure. Truncated() and
// Cancelled() distinguish which condition fired, so callers can choose
// whether to render a "truncated" footer or stay silent.
//
// The writer is cheap to construct (no goroutines, no channels) and is
// instantiated per preview focus. Pass context.Background() (or simply
// the result of search.ReloadCancelContext outside an internal preview
// process, which is also a Background context) when there is no
// supersession signal to honor.
type PreviewCapWriter struct {
	w         io.Writer
	ctx       context.Context
	limit     int64
	written   int64
	truncated bool
	cancelled bool
}

// NewPreviewCapWriter returns a PreviewCapWriter that wraps w with the
// given byte cap and cancellation context. A nil ctx is treated as
// context.Background(). A non-positive limit means "cap on first write."
func NewPreviewCapWriter(w io.Writer, ctx context.Context, limit int64) *PreviewCapWriter {
	if ctx == nil {
		ctx = context.Background()
	}
	return &PreviewCapWriter{w: w, ctx: ctx, limit: limit}
}

func (p *PreviewCapWriter) Write(b []byte) (int, error) {
	if p.truncated || p.cancelled {
		return 0, ErrPreviewLimitReached
	}
	if err := p.ctx.Err(); err != nil {
		p.cancelled = true
		return 0, ErrPreviewLimitReached
	}
	if len(b) == 0 {
		return 0, nil
	}
	if p.limit <= 0 {
		p.truncated = true
		return 0, ErrPreviewLimitReached
	}
	remaining := p.limit - p.written
	if remaining <= 0 {
		p.truncated = true
		return 0, ErrPreviewLimitReached
	}
	if int64(len(b)) > remaining {
		n, err := p.w.Write(b[:int(remaining)])
		p.written += int64(n)
		p.truncated = true
		if err != nil {
			return n, err
		}
		return n, ErrPreviewLimitReached
	}
	n, err := p.w.Write(b)
	p.written += int64(n)
	return n, err
}

// Truncated reports whether the byte cap was reached.
func (p *PreviewCapWriter) Truncated() bool { return p.truncated }

// Cancelled reports whether the cancellation context fired.
func (p *PreviewCapWriter) Cancelled() bool { return p.cancelled }

// BytesWritten returns the cumulative byte count successfully flushed
// to the underlying writer.
func (p *PreviewCapWriter) BytesWritten() int64 { return p.written }
