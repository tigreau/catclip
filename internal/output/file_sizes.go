package output

import (
	"context"
	"runtime"
	"sync"

	"github.com/tigreau/catclip/internal/discovery"
)

// CollectFileBodySizes resolves ordinary full-file sizes with the same
// SizeKnown-or-Lstat contract as FileBodySize. Work is bounded and results are
// checked in entry order so parallel collection does not change which error is
// returned first.
func CollectFileBodySizes(ctx context.Context, entries []discovery.Entry) (map[string]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(entries))
	missing := make([]int, 0)
	for index, entry := range entries {
		if entry.SizeKnown {
			result[entry.RelPath] = entry.SizeBytes
			continue
		}
		missing = append(missing, index)
	}
	if len(missing) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result, nil
	}

	sizes := make([]int64, len(entries))
	errs := make([]error, len(entries))

	// Lstat is I/O-bound, but a fixed cap prevents large ignored dependency
	// trees from turning one preview into unbounded filesystem pressure.
	workers := runtime.GOMAXPROCS(0)
	if workers > 16 {
		workers = 16
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(missing) {
		workers = len(missing)
	}

	jobs := make(chan int, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				sizes[index], errs[index] = FileBodySize(entries[index])
			}
		}()
	}

sendLoop:
	for _, index := range missing {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, index := range missing {
		if errs[index] != nil {
			return nil, errs[index]
		}
		result[entries[index].RelPath] = sizes[index]
	}
	return result, nil
}
