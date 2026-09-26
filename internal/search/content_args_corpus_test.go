package search

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Full-corpus argument-planning comparison, not a content-I/O benchmark or
// native Windows timing. Real scan parity is covered by the native fixture.
func TestContentBudgetFullCorpusPlanning(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in complete corpus")
	}
	root := os.Getenv("CATCLIP_TEST_CORPUS")
	if root == "" {
		t.Fatal("CATCLIP_TEST_CORPUS required")
	}
	bin, ok := RipgrepBinary()
	if !ok {
		t.Fatal("rg required")
	}
	fixed := []string{"--color=never", "--no-messages", "--files-with-matches", "--pcre2", "-0", "-m", "1", "--ignore-case", "-e", "TODO", "--"}
	for _, noIgnore := range []bool{false, true} {
		paths, err := RunRipgrepFiles(root, RipgrepFileOptions{NoIgnore: noIgnore, HissPath: os.Getenv("CATCLIP_TEST_HISS")})
		if err != nil {
			t.Fatal(err)
		}
		for i, path := range paths {
			paths[i] = filepath.Join(root, path)
		}
		for _, goos := range []string{"darwin", "windows"} {
			for round := 0; round < 3; round++ {
				start := time.Now()
				old := chunkExecArgs(paths, execPathChunkMaxCount, execPathChunkByteLimit(goos))
				oldTime := time.Since(start)
				start = time.Now()
				next, err := contentPathChunks(bin, fixed, paths, execPathChunkMaxCount, goos)
				nextTime := time.Since(start)
				if err != nil || !reflect.DeepEqual(flattenExecPathChunks(next), paths) {
					t.Fatalf("lost corpus paths: %v", err)
				}
				for _, chunk := range next {
					args := append(append([]string(nil), fixed...), chunk...)
					if commandArgBudgetUnits(bin, args, goos) > execPathChunkByteLimit(goos) {
						t.Fatal("unsafe batch")
					}
				}
				t.Logf("no_ignore=%t budget_os=%s round=%d paths=%d old_batches=%d new_batches=%d old_ms=%.3f new_ms=%.3f", noIgnore, goos, round, len(paths), len(old), len(next), float64(oldTime)/float64(time.Millisecond), float64(nextTime)/float64(time.Millisecond))
			}
		}
	}
}
