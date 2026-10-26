package search

import (
	"context"
	"sync"
)

// One CLI process is one stable-filesystem invocation, including every --then
// scope. Store content evidence, never scope membership or ignore decisions.
// Helper processes have their own cache; nothing is persisted across runs.
var invocationClassifications pathClassificationCache

type pathClassificationCache struct {
	mu       sync.Mutex
	complete map[string]fileClassification
	pending  map[string]chan struct{}
}

// classify coalesces concurrent reads of the same absolute, cleaned path. A
// cancelled waiter can leave without interrupting the owning scanner. Failed
// or cancelled scans are not cached; another caller can retry. Independently
// completed file observations remain usable even if their enclosing batch is
// subsequently cancelled (the batch itself still returns no selection).
func (c *pathClassificationCache) classify(ctx context.Context, abs string, buf []byte, scan func(context.Context, string, []byte) fileClassification) fileClassification {
	for {
		if ctx.Err() != nil {
			return fileClassification{}
		}
		c.mu.Lock()
		if result, ok := c.complete[abs]; ok {
			c.mu.Unlock()
			result.reused = true
			return result
		}
		if ready, ok := c.pending[abs]; ok {
			// Most paths have no concurrent waiter. Allocate a notification
			// channel only when another caller actually needs to wait.
			if ready == nil {
				ready = make(chan struct{})
				c.pending[abs] = ready
			}
			c.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return fileClassification{}
			}
		}
		if c.pending == nil {
			c.pending = make(map[string]chan struct{})
		}
		c.pending[abs] = nil
		c.mu.Unlock()

		result := scan(ctx, abs, buf)
		c.mu.Lock()
		if ctx.Err() == nil && (result.class == contentText || result.class == contentBinary) {
			if c.complete == nil {
				c.complete = make(map[string]fileClassification)
			}
			c.complete[abs] = result
		}
		if ready := c.pending[abs]; ready != nil {
			close(ready)
		}
		delete(c.pending, abs)
		c.mu.Unlock()
		return result
	}
}
