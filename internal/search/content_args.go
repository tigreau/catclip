package search

import "fmt"

// Budget the complete native command, not just its variable path tail. Fixed
// regexes are indivisible: if they cannot fit, fail before starting any batch.
// Returned slices borrow the caller's paths and are never modified by runners.
func contentPathChunks(bin string, fixed, paths []string, maxCount int, goos string) ([][]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	limit := execPathChunkByteLimit(goos)
	base := commandArgBudgetUnits(bin, fixed, goos)
	if base >= limit {
		return nil, fmt.Errorf("rg content options exceed the %s command budget (%d units)", goos, limit)
	}
	var chunks [][]string
	start, units := 0, base
	for i, path := range paths {
		cost := commandArgUnits(path, goos)
		if base+cost > limit {
			return nil, fmt.Errorf("one rg content target exceeds the remaining %s command budget (%d units)", goos, limit-base)
		}
		if i > start && (i-start >= maxCount || units+cost > limit) {
			chunks = append(chunks, paths[start:i:i])
			start, units = i, base
		}
		units += cost
	}
	return append(chunks, paths[start:len(paths):len(paths)]), nil
}
