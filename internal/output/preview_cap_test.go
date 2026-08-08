package output

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestPreviewCapWriterPassesThroughUnderLimit(t *testing.T) {
	var buf bytes.Buffer
	w := NewPreviewCapWriter(&buf, context.Background(), 100)
	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("want n=5, got %d", n)
	}
	if w.Truncated() || w.Cancelled() {
		t.Fatalf("flags should be false: truncated=%v cancelled=%v", w.Truncated(), w.Cancelled())
	}
	if w.BytesWritten() != 5 {
		t.Fatalf("BytesWritten want 5, got %d", w.BytesWritten())
	}
	if buf.String() != "hello" {
		t.Fatalf("buf want hello, got %q", buf.String())
	}
}

func TestPreviewCapWriterTruncatesMidWrite(t *testing.T) {
	var buf bytes.Buffer
	w := NewPreviewCapWriter(&buf, context.Background(), 4)
	n, err := w.Write([]byte("abcdef"))
	if !errors.Is(err, ErrPreviewLimitReached) {
		t.Fatalf("want ErrPreviewLimitReached, got %v", err)
	}
	if n != 4 {
		t.Fatalf("want n=4 (partial flush), got %d", n)
	}
	if !w.Truncated() {
		t.Fatalf("Truncated() should be true")
	}
	if w.Cancelled() {
		t.Fatalf("Cancelled() should be false")
	}
	if buf.String() != "abcd" {
		t.Fatalf("buf want abcd, got %q", buf.String())
	}
}

func TestPreviewCapWriterTruncatesOnSubsequentWriteOnceFull(t *testing.T) {
	var buf bytes.Buffer
	w := NewPreviewCapWriter(&buf, context.Background(), 5)
	if _, err := w.Write([]byte("abcde")); err != nil {
		t.Fatalf("first write should succeed, got %v", err)
	}
	// Now cap-full but not yet observed as truncated until next write.
	n, err := w.Write([]byte("more"))
	if !errors.Is(err, ErrPreviewLimitReached) {
		t.Fatalf("want ErrPreviewLimitReached, got %v", err)
	}
	if n != 0 {
		t.Fatalf("want n=0, got %d", n)
	}
	if !w.Truncated() {
		t.Fatalf("Truncated() should be true")
	}
}

func TestPreviewCapWriterShortCircuitsOnCancelledContext(t *testing.T) {
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	w := NewPreviewCapWriter(&buf, ctx, 1024)

	// First write succeeds — ctx still alive.
	if _, err := w.Write([]byte("alive")); err != nil {
		t.Fatalf("pre-cancel write should succeed, got %v", err)
	}

	cancel()

	n, err := w.Write([]byte("ignored"))
	if !errors.Is(err, ErrPreviewLimitReached) {
		t.Fatalf("post-cancel write should return ErrPreviewLimitReached, got %v", err)
	}
	if n != 0 {
		t.Fatalf("post-cancel n should be 0, got %d", n)
	}
	if !w.Cancelled() {
		t.Fatalf("Cancelled() should be true")
	}
	if w.Truncated() {
		t.Fatalf("Truncated() should be false")
	}
	if buf.String() != "alive" {
		t.Fatalf("buf should hold only pre-cancel bytes, got %q", buf.String())
	}
}

func TestPreviewCapWriterCancellationOutranksCap(t *testing.T) {
	// If both conditions could fire on the same Write, cancellation takes
	// precedence: it bails before touching the underlying writer.
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()                               // pre-cancelled
	w := NewPreviewCapWriter(&buf, ctx, 1) // tiny cap too
	n, err := w.Write([]byte("x"))
	if !errors.Is(err, ErrPreviewLimitReached) {
		t.Fatalf("want ErrPreviewLimitReached, got %v", err)
	}
	if n != 0 || buf.Len() != 0 {
		t.Fatalf("nothing should have been written; n=%d buf=%q", n, buf.String())
	}
	if !w.Cancelled() {
		t.Fatalf("Cancelled() should be true")
	}
	if w.Truncated() {
		t.Fatalf("Truncated() should be false (cancellation came first)")
	}
}

func TestPreviewCapWriterEmptyWriteIsNoop(t *testing.T) {
	var buf bytes.Buffer
	w := NewPreviewCapWriter(&buf, context.Background(), 10)
	n, err := w.Write(nil)
	if err != nil || n != 0 {
		t.Fatalf("nil write want (0,nil), got (%d,%v)", n, err)
	}
}

func TestPreviewCapWriterZeroLimitFailsImmediately(t *testing.T) {
	var buf bytes.Buffer
	w := NewPreviewCapWriter(&buf, context.Background(), 0)
	n, err := w.Write([]byte("x"))
	if !errors.Is(err, ErrPreviewLimitReached) {
		t.Fatalf("want ErrPreviewLimitReached, got %v", err)
	}
	if n != 0 || buf.Len() != 0 {
		t.Fatalf("nothing should have been written; n=%d buf=%q", n, buf.String())
	}
	if !w.Truncated() {
		t.Fatalf("Truncated() should be true")
	}
}

func TestPreviewCapWriterNilContextIsBackground(t *testing.T) {
	var buf bytes.Buffer
	w := NewPreviewCapWriter(&buf, nil, 10)
	if _, err := w.Write([]byte("ok")); err != nil {
		t.Fatalf("nil ctx should behave as Background, got %v", err)
	}
}

func TestPreviewByteLimitRemains128KiB(t *testing.T) {
	if PreviewByteLimit != 128*1024 {
		t.Fatalf("PreviewByteLimit should be 128 KiB, got %d", PreviewByteLimit)
	}
}
