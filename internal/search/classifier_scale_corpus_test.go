package search

// Diagnostic prototype only. Production classification remains unchanged.
// These tests compare exact decoded-NUL membership, not filename heuristics.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

// UTF-16 decoding can produce U+0000 only from an aligned 0000 code unit.
// Invalid/truncated sequences decode to replacement characters, not NULs.
// This prototype deliberately does not implement configured rg encodings.
func scaleClassifyFile(ctx context.Context, filename string, buf []byte, observation ...*os.FileInfo) (bool, error) {
	info, err := os.Lstat(filename)
	if len(observation) > 0 {
		*observation[0] = info
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("non-regular benchmark input: %s", filename)
	}
	f, err := os.Open(filename)
	if err != nil {
		return false, err
	}
	defer f.Close()
	n, err := io.ReadFull(f, buf[:2])
	utf16 := n == 2 && ((buf[0] == 0xff && buf[1] == 0xfe) || (buf[0] == 0xfe && buf[1] == 0xff))
	if !utf16 && bytes.IndexByte(buf[:n], 0) >= 0 {
		return false, nil
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	pending := -1
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, err = f.Read(buf)
		if utf16 {
			for _, b := range buf[:n] {
				if pending < 0 {
					pending = int(b)
				} else {
					if pending == 0 && b == 0 {
						return false, nil
					}
					pending = -1
				}
			}
		} else if bytes.IndexByte(buf[:n], 0) >= 0 {
			return false, nil
		}
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
}

func scaleClassifyGo(ctx context.Context, root string, paths []string, workers int) (map[string]struct{}, error) {
	accepted := make([]bool, len(paths))
	jobs := make(chan int, workers*2)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 32*1024)
			for i := range jobs {
				ok, err := scaleClassifyFile(ctx, filepath.Join(root, filepath.FromSlash(paths[i])), buf)
				if err != nil {
					once.Do(func() { firstErr = err })
				}
				accepted[i] = ok
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	out := make(map[string]struct{}, len(paths))
	for i, ok := range accepted {
		if ok {
			out[paths[i]] = struct{}{}
		}
	}
	return out, nil
}

func scaleClassifyRg(root, bin string, paths []string, extra ...string) (map[string]struct{}, int, error) {
	fixed := []string{"--files-without-match", "--text", "--no-messages", "-0", "-e", `\x00`}
	fixed = append(fixed, extra...)
	fixed = append(fixed, "--")
	chunks, err := contentPathChunks(bin, fixed, paths, residuePathChunkMaxCount, runtime.GOOS)
	if err != nil {
		return nil, 0, err
	}
	out := make(map[string]struct{}, len(paths))
	for _, chunk := range chunks {
		cmd := exec.Command(bin, append(slices.Clone(fixed), chunk...)...)
		cmd.Dir = root
		data, err := cmd.Output()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
				return nil, len(chunks), err
			}
		}
		for _, p := range splitNullSeparated(data) {
			out[normalizeRelPath(p)] = struct{}{}
		}
	}
	return out, len(chunks), nil
}

func assertScaleMembership(t *testing.T, got, want map[string]struct{}) {
	t.Helper()
	for p := range want {
		if _, ok := got[p]; !ok {
			t.Fatalf("Go/rg classification mismatch: missing %q", p)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Fatalf("Go/rg classification mismatch: extra %q", p)
		}
	}
}

func TestScaleClassifierEncodingParity(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	bin, ok := RipgrepBinary()
	if !ok {
		t.Skip("rg required")
	}
	root := t.TempDir()
	fixtures := [][]byte{nil, {'a'}, {0}, {0xff, 'a', 0x80}, {0xef, 0xbb, 0xbf, 'a'},
		{0xff, 0xfe, 'a', 0}, {0xfe, 0xff, 0, 'a'}, {0xff, 0xfe, 'a', 0, 0, 0},
		{0xff, 0xfe, 0x00, 0xd8, 0}, {0xfe, 0xff, 0xd8, 0, 0, 0},
		append(bytes.Repeat([]byte{'a'}, 65537), 0),
		append([]byte{0xff, 0xfe}, bytes.Repeat([]byte{'a', 0}, 32769)...)}
	var paths []string
	for i, data := range fixtures {
		p := fmt.Sprintf("fixture-%d.unknown", i)
		if err := os.WriteFile(filepath.Join(root, p), data, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	want, _, err := scaleClassifyRg(root, bin, paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 4, 8} {
		got, err := scaleClassifyGo(context.Background(), root, paths, workers)
		if err != nil {
			t.Fatal(err)
		}
		assertScaleMembership(t, got, want)
	}
}

func TestScaleClassifierCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in large classification benchmark")
	}
	root := os.Getenv("CATCLIP_TEST_CORPUS")
	if root == "" {
		t.Fatal("CATCLIP_TEST_CORPUS required")
	}
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	bin, ok := RipgrepBinary()
	if !ok {
		t.Fatal("rg required")
	}
	all, err := RunRipgrepFiles(root, RipgrepFileOptions{NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := RunRipgrepFiles(root, RipgrepFileOptions{HissPath: os.Getenv("CATCLIP_TEST_HISS")})
	if err != nil {
		t.Fatal(err)
	}
	sizes := make(map[string]int64, len(all))
	var large []string
	for _, p := range all {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("invalid corpus file %q: %v", p, err)
		}
		sizes[p] = info.Size()
		if info.Size() >= 1<<20 {
			large = append(large, p)
		}
	}
	unknown := func(paths []string) []string {
		var out []string
		for _, p := range paths {
			if classifyPathByName(p) == nameClassUnknown {
				out = append(out, p)
			}
		}
		return out
	}
	var largeSource []string
	for _, p := range visible {
		if sizes[p] >= 1<<20 {
			largeSource = append(largeSource, p)
		}
	}
	for _, work := range []struct {
		name  string
		paths []string
	}{{"visible-residue", unknown(visible)}, {"no-ignore-residue", unknown(all)}, {"large-files", large}, {"all-files", all}, {"large-source-files", largeSource}, {"visible-all-files", visible}} {
		t.Run(work.name, func(t *testing.T) {
			var size, maximum int64
			for _, p := range work.paths {
				size += sizes[p]
				maximum = max(maximum, sizes[p])
			}
			// Untimed oracle exercises the selected workload first. This is a
			// repeated-run benchmark, not a controlled warm/cold-cache experiment:
			// a large workload may evict earlier files from the OS cache.
			want, batches, err := scaleClassifyRg(root, bin, work.paths)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("workload=%s files=%d logical_bytes=%d largest_bytes=%d text=%d rg_batches=%d", work.name, len(work.paths), size, maximum, len(want), batches)
			variants := []int{0, 1, 4, 8}
			if os.Getenv("CATCLIP_CLASSIFIER_RG_CONTROL") == "1" {
				// Is a smaller rg option change sufficient instead of a rewrite?
				variants = []int{0, -1, 8}
			}
			for round := 0; round < 3; round++ {
				for offset := range variants {
					workers := variants[(round+offset)%len(variants)]
					runtime.GC()
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					start := time.Now()
					var got map[string]struct{}
					name := fmt.Sprintf("go-%d", workers)
					if workers == 0 {
						name = "rg-batched"
						got, _, err = scaleClassifyRg(root, bin, work.paths)
					} else if workers == -1 {
						name = "rg-max-count-1"
						got, _, err = scaleClassifyRg(root, bin, work.paths, "--max-count", "1")
					} else {
						got, err = scaleClassifyGo(context.Background(), root, work.paths, workers)
					}
					elapsed := time.Since(start)
					runtime.ReadMemStats(&after)
					if err != nil {
						t.Fatal(err)
					}
					assertScaleMembership(t, got, want)
					t.Logf("sample workload=%s round=%d variant=%s ms=%.3f parent_alloc_bytes=%d parity=true", work.name, round, name, float64(elapsed)/float64(time.Millisecond), after.TotalAlloc-before.TotalAlloc)
				}
			}
		})
	}
}
