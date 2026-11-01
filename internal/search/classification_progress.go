package search

import (
	"sync/atomic"
	"time"

	"github.com/tigreau/catclip/internal/platform"
)

// Progress is opt-in and process-local, like the invocation classification
// cache. Only the foreground discovery owner observes it; preview helpers and
// headless runs do not enable per-file progress accounting.
var classificationObserver atomic.Pointer[classificationProgress]

type classificationProgress struct {
	batch atomic.Pointer[classificationBatch]
}

type classificationBatch struct {
	completed atomic.Int64
	total     int
	started   time.Time
}

// ObserveClassificationProgress supplies a polling reader. Workers never
// format messages, write to the terminal, or send a notification per file.
func ObserveClassificationProgress() (func() platform.FileScanProgress, func()) {
	observer := &classificationProgress{}
	classificationObserver.Store(observer)
	return func() platform.FileScanProgress {
		batch := observer.batch.Load()
		if batch == nil {
			return platform.FileScanProgress{}
		}
		return platform.FileScanProgress{
			Completed: int(batch.completed.Load()), Total: batch.total, Started: batch.started,
		}
	}, func() { classificationObserver.CompareAndSwap(observer, nil) }
}

func beginClassificationProgress(total int) (*classificationBatch, func()) {
	observer := classificationObserver.Load()
	if observer == nil || total == 0 {
		return nil, func() {}
	}
	batch := &classificationBatch{total: total, started: time.Now()}
	observer.batch.Store(batch)
	return batch, func() { observer.batch.CompareAndSwap(batch, nil) }
}
