package search

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/platform"
)

// commandArgUnits is a conservative bound, not shell quoting. Direct exec
// receives raw arguments. UTF-8 bytes bound UTF-16 units; allowing an extra
// character per backslash/quote also bounds Windows argv escaping.
func commandArgUnits(arg, goos string) int {
	if goos == "windows" {
		return len(arg) + strings.Count(arg, `\`) + strings.Count(arg, `"`) + 3
	}
	// Include the argv pointer as well as its terminating NUL on Unix. This
	// also bounds many very short roots without relying on a path-count cap.
	return len(arg) + 1 + 8
}

func commandArgBudgetUnits(bin string, args []string, goos string) int {
	units := commandArgUnits(bin, goos)
	for _, arg := range args {
		units += commandArgUnits(arg, goos)
	}
	return units
}

// ripgrepFileArgBatches partitions only literal roots. Every batch keeps the
// complete ordered glob/ignore policy. Never split glob flags into independent
// unions: negative globs and later overrides make that transformation unsound.
func ripgrepFileArgBatches(bin string, opts RipgrepFileOptions, debug bool, goos string) ([][]string, error) {
	roots := opts.Paths
	opts.Paths = nil
	base := ripgrepFileArgs(opts, debug)
	baseUnits := commandArgBudgetUnits(bin, base, goos)
	limit := execPathChunkByteLimit(goos)
	if baseUnits > limit {
		return nil, fmt.Errorf("rg file enumeration options exceed the %s command budget (%d units)", goos, limit)
	}
	var batches [][]string
	var batch []string
	units := baseUnits + commandArgUnits("--", goos)
	for _, root := range roots {
		root = normalizeRelPath(root)
		// Match ripgrepFileArgs normalization before batching: an empty batch
		// must never accidentally become an unrestricted working-directory walk.
		if root == "" || root == "." {
			continue
		}
		n := commandArgUnits(root, goos)
		if baseUnits+commandArgUnits("--", goos)+n > limit {
			return nil, fmt.Errorf("one rg file enumeration target exceeds the %s command budget (%d units)", goos, limit)
		}
		if len(batch) > 0 && units+n > limit {
			batches = append(batches, batch)
			batch = nil
			units = baseUnits + commandArgUnits("--", goos)
		}
		if batch == nil {
			batch = append(append([]string(nil), base...), "--")
		}
		batch = append(batch, root)
		units += n
	}
	if batch != nil {
		batches = append(batches, batch)
	}
	if len(batches) == 0 {
		batches = [][]string{base}
	}
	return batches, nil
}

func runRipgrepFileBatches(ctx context.Context, bin, workingDir string, opts RipgrepFileOptions, kind MembershipEnumerationKind) ([]string, error) {
	batches, err := ripgrepFileArgBatches(bin, opts, false, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	policy := MembershipVisible
	if opts.NoIgnore {
		policy = MembershipNoIgnore
	}
	var paths []string
	for _, args := range batches {
		span := beginMembershipEnumeration(kind, policy, opts.Enumeration)
		finish := platform.InternalBenchSpan("search.rg.file_batch",
			"argc", platform.InternalBenchInt(len(args)),
			"arg_units_upper_bound", platform.InternalBenchInt(commandArgBudgetUnits(bin, args, runtime.GOOS)),
		)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = workingDir
		start := time.Now()
		out, err := cmd.Output()
		if benchEnabled() {
			if kind == MembershipEnumerationVisibleSet {
				benchRgVisibleTotal.Add(int64(time.Since(start)))
				benchRgVisibleCalls.Add(1)
			} else {
				benchRgFilesTotal.Add(int64(time.Since(start)))
				benchRgFilesCalls.Add(1)
			}
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			err = nil
		}
		batchPaths := splitNullSeparated(out)
		span.finish(len(batchPaths), scanWasCancelled(ctx, err), err)
		finish("err", platform.InternalBenchError(err))
		if err != nil {
			// A failed later batch must not turn an incomplete union into success.
			return nil, err
		}
		for i, rel := range batchPaths {
			batchPaths[i] = normalizeRelPath(rel)
		}
		if paths == nil {
			// Keep the usual single-batch path allocation-equivalent to the
			// old walk. Map consumers also avoid an unnecessary global sort.
			paths = batchPaths
		} else {
			paths = append(paths, batchPaths...)
		}
	}
	return paths, nil
}
