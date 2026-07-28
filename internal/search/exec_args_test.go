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
