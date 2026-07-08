package discovery

import (
	"os"
	"runtime"
	"strconv"
	"sync"
)

// StatFunc selects between os.Stat (follows symlinks) and os.Lstat
// (does not). Callers pass their existing choice — parallelStat is
// policy-neutral.
type StatFunc func(path string) (os.FileInfo, error)

// parallelStat fans out per-path stat calls across a bounded worker
// pool. Returns per-index (FileInfo, error) so each caller can
// implement the failure policy that matches its contract:
//
//   - EnsureEntrySizes / EnsureEntryModTimes are fail-fast: on any
//     error, return early with the first non-nil error.
//   - FillEntrySizes is continue-on-error (documented "the checkpoint
//     is never failed"): iterate errors, mark failing indices
//     unpopulated, return the (partial) slice unchanged.
//
// Empty paths yield (nil, nil) for that index — the caller decides
// whether that's a skip or an error. This matches the sequential
// loops' behavior where an empty AbsPath is filled in from workingDir
// before statting; callers pass already-resolved absolute paths.
//
// Worker cap is min(runtime.NumCPU(), 8) by default, overridable via
// CATCLIP_STAT_WORKERS. 8 was validated as the point of diminishing
// returns on warm POSIX (see 2026-07-03 stat_bench measurements —
// 1.7-1.9× speedup at 8 workers, 16 barely better). On Windows the
// bottleneck is MsMpEng intercept latency rather than scheduler
// contention, so raising the cap may help — the env var makes it
// testable without recompiling.
func parallelStat(paths []string, stat StatFunc) ([]os.FileInfo, []error) {
	n := len(paths)
	infos := make([]os.FileInfo, n)
	errs := make([]error, n)
	if n == 0 {
		return infos, errs
	}

	workers := statWorkerCount()
	if workers > n {
		workers = n
	}

	type job struct {
		index int
		path  string
	}
	jobs := make(chan job, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if j.path == "" {
					continue
				}
				infos[j.index], errs[j.index] = stat(j.path)
			}
		}()
	}
	for i, p := range paths {
		jobs <- job{index: i, path: p}
	}
	close(jobs)
	wg.Wait()
	return infos, errs
}

// statWorkerCount returns the worker cap. CATCLIP_STAT_WORKERS
// overrides the min(NumCPU, 8) default when set to a positive
// integer. Non-positive or unparseable values fall through to the
// default — invalid env values shouldn't disable parallelism.
func statWorkerCount() int {
	def := runtime.NumCPU()
	if def > 8 {
		def = 8
	}
	if def < 1 {
		def = 1
	}
	env := os.Getenv("CATCLIP_STAT_WORKERS")
	if env == "" {
		return def
	}
	n, err := strconv.Atoi(env)
	if err != nil || n < 1 {
		return def
	}
	return n
}
