package search

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestExecPathChunkByteLimitReservesWindowsCommandLineSpace(t *testing.T) {
	if got, want := execPathChunkByteLimit("windows"), 24*1024; got != want {
		t.Fatalf("Windows path-argument budget = %d, want %d", got, want)
	}
	if got, want := execPathChunkByteLimit("linux"), 60*1024; got != want {
		t.Fatalf("Unix path-argument budget = %d, want %d", got, want)
	}
}

func TestWindowsExecPathBudgetSplitsLong256FileBatch(t *testing.T) {
	paths := make([]string, 256)
	for i := range paths {
		paths[i] = fmt.Sprintf("nested/%03d/%s.unknown", i, strings.Repeat("long-path-segment/", 8))
	}

	windowsChunks := chunkExecArgs(paths, execPathChunkMaxCount, execPathChunkByteLimit("windows"))
	if len(windowsChunks) < 2 {
		t.Fatalf("Windows budget kept all 256 long paths in one command: %d chunk(s)", len(windowsChunks))
	}
	assertExecPathChunksWithinLimits(t, windowsChunks, execPathChunkMaxCount, windowsExecPathChunkBytes)
	if got := flattenExecPathChunks(windowsChunks); !reflect.DeepEqual(got, paths) {
		t.Fatal("Windows chunking did not preserve every path in input order")
	}

	unixChunks := chunkExecArgs(paths, execPathChunkMaxCount, execPathChunkByteLimit("linux"))
	if len(unixChunks) != 1 {
		t.Fatalf("Unix budget unexpectedly split the same batch into %d chunks", len(unixChunks))
	}
}

func flattenExecPathChunks(chunks [][]string) []string {
	var paths []string
	for _, chunk := range chunks {
		paths = append(paths, chunk...)
	}
	return paths
}

func TestResidueChunkingPreservesCountAndPlatformByteBoundaries(t *testing.T) {
	for _, goos := range []string{"windows", "linux", "darwin"} {
		for _, longPaths := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/long=%v", goos, longPaths), func(t *testing.T) {
				paths := make([]string, 1025)
				for i := range paths {
					paths[i] = fmt.Sprintf("%04d.xyz", i)
					if longPaths {
						paths[i] = strings.Repeat("nested path/", 12) + paths[i]
					}
				}
				budget := execPathChunkByteLimit(goos)
				chunks := chunkExecArgs(paths, residuePathChunkMaxCount, budget)
				assertExecPathChunksWithinLimits(t, chunks, residuePathChunkMaxCount, budget)
				if !reflect.DeepEqual(flattenExecPathChunks(chunks), paths) {
					t.Fatal("batching lost or reordered paths")
				}
				if !longPaths && (len(chunks) != 2 || len(chunks[0]) != 1024 || len(chunks[1]) != 1) {
					t.Fatalf("short paths should split 1024+1; got %d chunks", len(chunks))
				}
				if longPaths && len(chunks[0]) >= residuePathChunkMaxCount {
					t.Fatal("long paths did not split at the byte budget")
				}
			})
		}
	}
}

func assertExecPathChunksWithinLimits(t *testing.T, chunks [][]string, maxCount, maxBytes int) {
	t.Helper()
	for i, chunk := range chunks {
		if len(chunk) > maxCount {
			t.Fatalf("chunk %d contains %d paths, limit %d", i, len(chunk), maxCount)
		}
		bytes := 0
		for _, path := range chunk {
			bytes += len(path) + 1
		}
		if bytes > maxBytes {
			t.Fatalf("chunk %d uses %d path bytes, limit %d", i, bytes, maxBytes)
		}
	}
}
