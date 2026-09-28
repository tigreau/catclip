package search

import (
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"
)

// Phase timing and exact membership checks, not an end-to-end picker latency
// benchmark. The legacy oracle is the previous single-process file walk.
func TestRipgrepFileBatchCorpusParityAndTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in complete corpus")
	}
	root := os.Getenv("CATCLIP_TEST_CORPUS")
	if root == "" {
		t.Fatal("set CATCLIP_TEST_CORPUS to the complete corpus directory")
	}
	bin, ok := RipgrepBinary()
	if !ok {
		t.Fatal("rg required")
	}
	for _, noIgnore := range []bool{false, true} {
		opts := RipgrepFileOptions{NoIgnore: noIgnore, HissPath: os.Getenv("CATCLIP_TEST_HISS")}
		var want []string
		for round := 0; round < 5; round++ {
			for offset := 0; offset < 2; offset++ {
				variant := (round + offset) % 2
				start := time.Now()
				var got []string
				var err error
				if variant == 0 {
					cmd := exec.Command(bin, ripgrepFileArgs(opts, false)...)
					cmd.Dir = root
					var out []byte
					out, err = cmd.Output()
					got = splitNullSeparated(out)
					for i := range got {
						got[i] = normalizeRelPath(got[i])
					}
					sort.Strings(got)
					got = dedupeSortedStrings(got)
				} else {
					got, err = RunRipgrepFiles(root, opts)
				}
				elapsed := time.Since(start)
				if err != nil {
					t.Fatal(err)
				}
				if want == nil {
					want = got
				} else if !reflect.DeepEqual(got, want) {
					t.Fatalf("corpus membership changed: got %d, want %d", len(got), len(want))
				}
				t.Logf("no_ignore=%t round=%d variant=%d paths=%d ms=%.3f", noIgnore, round, variant, len(got), float64(elapsed)/float64(time.Millisecond))
			}
		}
		// Every individual path, rather than a substitute '.' target. This
		// deliberately exceeds argv limits and must not widen or drop files.
		opts.Paths = want
		batches, err := ripgrepFileArgBatches(bin, opts, false, runtime.GOOS)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		got, err := RunRipgrepFiles(root, opts)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("individual corpus paths changed: got %d, want %d, err=%v", len(got), len(want), err)
		}
		t.Logf("individual_paths no_ignore=%t paths=%d batches=%d ms=%.3f", noIgnore, len(got), len(batches), float64(time.Since(start))/float64(time.Millisecond))
	}
}
