package search

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/tigreau/catclip/internal/platform"
)

const (
	// Classification may enqueue accepted paths immediately, but metadata I/O
	// does not begin until boostWorkers. Even one concurrent Lstat worker was
	// measurable on the 196k-file reference corpus, so list readiness wins over
	// speculative overlap here.
	textSizeCaptureInitialWorkers = 0
	// Once classification finishes, metadata collection can use the same
	// bounded parallelism as the preview's former exact-size fallback.
	textSizeCaptureMaxWorkers = 16
)

// TextSizeCapture collects opportunistic Lstat sizes for files already known
// to be text. It is owned by one interactive target picker and never makes
// classification fail: unreadable or disappearing files simply remain absent
// so the preview's existing exact lookup can handle them if selected.
type TextSizeCapture struct {
	workingDir  string
	ctx         context.Context
	cancel      context.CancelFunc
	batches     chan []string
	boost       chan struct{}
	done        chan struct{}
	closeOnce   sync.Once
	boostOnce   sync.Once
	interrupted atomic.Bool

	mu    sync.RWMutex
	sizes map[string]int64
}

func newTextSizeCapture(workingDir string) *TextSizeCapture {
	ctx, cancel := context.WithCancel(context.Background())
	capture := &TextSizeCapture{
		workingDir: workingDir,
		ctx:        ctx,
		cancel:     cancel,
		batches:    make(chan []string, 4),
		boost:      make(chan struct{}),
		done:       make(chan struct{}),
		sizes:      make(map[string]int64),
	}
	go capture.run()
	return capture
}

// StartTextSizeCapture starts a standalone capture for paths whose text-file
// eligibility is already known. The returned capture is closed for input;
// callers may take snapshots while it continues in the background.
func StartTextSizeCapture(workingDir string, relPaths []string) *TextSizeCapture {
	capture := newTextSizeCapture(workingDir)
	capture.boostWorkers()
	capture.add(relPaths)
	capture.closeInput()
	return capture
}

func (c *TextSizeCapture) run() {
	defer close(c.done)

	finishBench := platform.InternalBenchSpan("search.text_size_capture",
		"initial_workers", platform.InternalBenchInt(textSizeCaptureInitialWorkers),
		"max_workers", platform.InternalBenchInt(textSizeCaptureMaxWorkers),
	)
	defer func() {
		c.mu.RLock()
		captured := len(c.sizes)
		c.mu.RUnlock()
		finishBench(
			"captured", platform.InternalBenchInt(captured),
			"cancelled", platform.InternalBenchBool(c.ctx.Err() != nil),
		)
	}()
	jobs := make(chan string, textSizeCaptureMaxWorkers*2)
	var wg sync.WaitGroup
	workers := 0
	startWorkers := func(count int) {
		for range count {
			wg.Add(1)
			workers++
			go func() {
				defer wg.Done()
				for rel := range jobs {
					if c.ctx.Err() != nil {
						return
					}
					abs := filepath.Join(c.workingDir, filepath.FromSlash(rel))
					info, err := os.Lstat(abs)
					if err != nil {
						continue
					}
					c.record(rel, info.Size())
				}
			}()
		}
	}
	startWorkers(textSizeCaptureInitialWorkers)

	boost := c.boost
	sendBatch := func(batch []string) bool {
		for _, rel := range batch {
			for {
				select {
				case jobs <- rel:
					goto sent
				case <-boost:
					startWorkers(textSizeCaptureMaxWorkers - workers)
					boost = nil
				case <-c.ctx.Done():
					return false
				}
			}
		sent:
		}
		return true
	}

sendBatches:
	for {
		select {
		case batch, ok := <-c.batches:
			if !ok {
				// Normal callers boost before closing input. Both closed channels
				// may be selectable here, so ensure the pool exists before shutting
				// down; otherwise a small batch can fit in jobs and be discarded
				// with no worker ever started.
				if workers == 0 && c.ctx.Err() == nil {
					startWorkers(textSizeCaptureMaxWorkers)
					boost = nil
				}
				break sendBatches
			}
			if !sendBatch(batch) {
				break sendBatches
			}
		case <-boost:
			startWorkers(textSizeCaptureMaxWorkers - workers)
			boost = nil
		case <-c.ctx.Done():
			break sendBatches
		}
	}
	close(jobs)
	wg.Wait()
}

// boostWorkers expands the pool after text classification is complete. It is
// deliberately separate from closeInput: callers with already-classified
// paths boost immediately, while the hybrid classifier defers all metadata I/O
// until its residue scan and empty-file admission have finished.
func (c *TextSizeCapture) boostWorkers() {
	if c == nil {
		return
	}
	c.boostOnce.Do(func() { close(c.boost) })
}

func (c *TextSizeCapture) add(relPaths []string) {
	if c == nil || len(relPaths) == 0 {
		return
	}
	select {
	case c.batches <- relPaths:
	case <-c.ctx.Done():
	}
}

func (c *TextSizeCapture) record(rel string, size int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.sizes[rel] = size
	c.mu.Unlock()
}

func (c *TextSizeCapture) closeInput() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() { close(c.batches) })
}

// Snapshot returns the sizes completed so far. The returned map is detached
// from the workers and can be serialized while capture continues.
func (c *TextSizeCapture) Snapshot() map[string]int64 {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	out := make(map[string]int64, len(c.sizes))
	for rel, size := range c.sizes {
		out[rel] = size
	}
	c.mu.RUnlock()
	return out
}

// Done is closed after every queued lookup has finished or cancellation has
// stopped the capture.
func (c *TextSizeCapture) Done() <-chan struct{} {
	if c == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return c.done
}

func (c *TextSizeCapture) Complete() bool {
	if c == nil {
		return true
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Cancelled reports whether Stop had to interrupt unfinished metadata work.
// A naturally completed capture remains reusable by a later target picker in
// the same startup flow.
func (c *TextSizeCapture) Cancelled() bool {
	if c == nil {
		return false
	}
	return c.interrupted.Load()
}

// Stop cancels outstanding work and waits for every worker to exit.
func (c *TextSizeCapture) Stop() {
	if c == nil {
		return
	}
	if !c.Complete() {
		c.interrupted.Store(true)
	}
	c.cancel()
	c.closeInput()
	<-c.done
}
