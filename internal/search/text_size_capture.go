package search

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tigreau/catclip/internal/platform"
)

const (
	// Start one worker while classification is still running. Target
	// confirmation is the completion boundary, but this small speculative lane
	// lets already-classified files make progress while keeping the initial list
	// responsive.
	textSizeCaptureInitialWorkers = 1
	// Once classification finishes, metadata collection can use the same
	// bounded parallelism as the preview's former exact-size fallback.
	textSizeCaptureMaxWorkers = 16
)

// FileMetadataState is the terminal result of the primary Lstat owned by an
// interactive target generation. Ready is zero so existing successful record
// literals remain source-compatible.
type FileMetadataState uint8

const (
	FileMetadataReady FileMetadataState = iota
	FileMetadataVanished
	FileMetadataUnreadable
)

// FileMetadata is the reusable part of one successful Lstat. Keeping the full
// record costs no additional filesystem work and prevents later interactive
// stages from asking the operating system for the same facts again.
type FileMetadata struct {
	SizeBytes int64
	ModTime   time.Time
	Mode      fs.FileMode
	State     FileMetadataState
	Error     string
}

// TextSizeCapture collects opportunistic Lstat metadata for files already
// known to be text. It is owned by one interactive target picker and never
// makes classification fail. Failures are retained as terminal observations
// so downstream screens do not repeat the same primary lookup.
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

	mu       sync.RWMutex
	metadata map[string]FileMetadata
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
		metadata:   make(map[string]FileMetadata),
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
		captured := len(c.metadata)
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
					c.recordResult(rel, info, err)
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

func (c *TextSizeCapture) record(rel string, info os.FileInfo) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.metadata[rel] = FileMetadata{
		SizeBytes: info.Size(),
		ModTime:   info.ModTime(),
		Mode:      info.Mode(),
	}
	c.mu.Unlock()
}

func (c *TextSizeCapture) recordResult(rel string, info os.FileInfo, err error) {
	if c == nil {
		return
	}
	if err == nil && info != nil && info.Mode().IsRegular() {
		c.record(rel, info)
		return
	}
	record := FileMetadata{State: FileMetadataUnreadable}
	if err != nil {
		record.Error = err.Error()
		if os.IsNotExist(err) {
			record.State = FileMetadataVanished
		}
	} else {
		record.Error = "path is not a regular file"
	}
	c.mu.Lock()
	c.metadata[rel] = record
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
	out := make(map[string]int64, len(c.metadata))
	for rel, metadata := range c.metadata {
		if metadata.State == FileMetadataReady {
			out[rel] = metadata.SizeBytes
		}
	}
	c.mu.RUnlock()
	return out
}

// MetadataSnapshot returns every completed Lstat record. The returned map is
// detached from the workers and becomes the interactive session's immutable
// filesystem metadata seed after the target picker closes.
func (c *TextSizeCapture) MetadataSnapshot() map[string]FileMetadata {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	out := make(map[string]FileMetadata, len(c.metadata))
	for rel, metadata := range c.metadata {
		out[rel] = metadata
	}
	c.mu.RUnlock()
	return out
}

// FinalizeSelection stops speculative work, reuses every completed selected
// record, and fills only selected gaps. It returns only after every requested
// path has a terminal ready/vanished/unreadable record.
func (c *TextSizeCapture) FinalizeSelection(relPaths []string) map[string]FileMetadata {
	if c == nil {
		return nil
	}
	c.Stop()

	selected := make([]string, 0, len(relPaths))
	seen := make(map[string]struct{}, len(relPaths))
	for _, rel := range relPaths {
		rel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
		if rel == "" || rel == "." {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		selected = append(selected, rel)
	}

	c.mu.RLock()
	missing := make([]string, 0, len(selected))
	for _, rel := range selected {
		if _, ok := c.metadata[rel]; !ok {
			missing = append(missing, rel)
		}
	}
	c.mu.RUnlock()

	jobs := make(chan string)
	var wg sync.WaitGroup
	workers := min(textSizeCaptureMaxWorkers, len(missing))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range jobs {
				abs := filepath.Join(c.workingDir, filepath.FromSlash(rel))
				info, err := os.Lstat(abs)
				c.recordResult(rel, info, err)
			}
		}()
	}
	for _, rel := range missing {
		jobs <- rel
	}
	close(jobs)
	wg.Wait()

	c.mu.RLock()
	out := make(map[string]FileMetadata, len(selected))
	for _, rel := range selected {
		if record, ok := c.metadata[rel]; ok {
			out[rel] = record
		}
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
