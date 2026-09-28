package discovery

// Compare production indexing with the frozen pre-shortcut implementation.
import (
	"os"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
)

func TestKnownAncestorIndexParity(t *testing.T) {
	for _, paths := range [][]string{
		nil, {"root.go"}, {"src/a/x.go", "src/a/y.go", "src/b/z.go", "src/z.go"},
		{"a/b/c", "a/b/c", "a/file", "x/y/z", "a/bb/file"}, {"a/../b/file", "./root"},
	} {
		for _, reverse := range []bool{false, true} {
			entries := make([]Entry, len(paths))
			for i, p := range paths {
				entries[i] = Entry{RelPath: p}
			}
			if reverse {
				slices.Reverse(entries)
			}
			current := &Resolver{VisibleFileList: entries, visibleFileListReady: true}
			candidate := &Resolver{VisibleFileList: entries, visibleFileListReady: true}
			if err := legacyKnownAncestorIndex(current); err != nil {
				t.Fatal(err)
			}
			if err := candidate.BuildVisibleDirIndex(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(current.VisibleDirs, candidate.VisibleDirs) {
				t.Fatalf("different index: %v", paths)
			}
		}
	}
}

func TestKnownAncestorIndexCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in complete corpus")
	}
	root := os.Getenv("CATCLIP_TEST_CORPUS")
	if root == "" {
		root = "/Users/chris/Desktop/catclip-test-data"
	}
	resolver := &Resolver{Cfg: command.Invocation{WorkingDir: root}}
	if err := resolver.BuildVisibleFileList(); err != nil {
		t.Fatal(err)
	}
	entries := resolver.VisibleFileList
	var timings [2][]float64
	var allocations [2][]uint64
	for round := 0; round < 7; round++ {
		for offset := 0; offset < 2; offset++ {
			variant := (round + offset) % 2
			r := &Resolver{VisibleFileList: entries, visibleFileListReady: true}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			var err error
			if variant == 0 {
				err = legacyKnownAncestorIndex(r)
			} else {
				err = r.BuildVisibleDirIndex()
			}
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			if err != nil {
				t.Fatal(err)
			}
			if round == 0 && variant == 0 {
				resolver.VisibleDirs = r.VisibleDirs
			}
			if !reflect.DeepEqual(resolver.VisibleDirs, r.VisibleDirs) {
				t.Fatal("full corpus index differs")
			}
			timings[variant] = append(timings[variant], float64(elapsed)/float64(time.Millisecond))
			allocations[variant] = append(allocations[variant], after.TotalAlloc-before.TotalAlloc)
		}
	}
	for variant := range timings {
		slices.Sort(timings[variant])
		slices.Sort(allocations[variant])
		t.Logf("files=%d dirs=%d variant=%d median_ms=%.3f range=%.3f..%.3f alloc_MiB=%.3f",
			len(entries), len(resolver.VisibleDirs.Dirs), variant, timings[variant][3], timings[variant][0], timings[variant][6], float64(allocations[variant][3])/(1<<20))
	}
}
