package search

// Opt-in diagnostic for a live home directory. This keeps the production name
// shortcuts and empty-file admission, and measures classification only. It is
// a comparison of the production classifier and independent diagnostic controls.
import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

func homeClassifyGo(root string, paths []string, workers int) (map[string]struct{}, int) {
	text := make(map[string]struct{}, len(paths))
	var residue []string
	for _, p := range paths {
		switch classifyPathByName(p) {
		case nameClassText:
			text[p] = struct{}{}
		case nameClassUnknown:
			residue = append(residue, p)
		}
	}
	type observation struct {
		text bool
		info os.FileInfo
		err  error
	}
	observed := make([]observation, len(residue))
	jobs := make(chan int, workers*2)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 32*1024)
			for i := range jobs {
				o := &observed[i]
				o.text, o.err = scaleClassifyFile(context.Background(), filepath.Join(root, filepath.FromSlash(residue[i])), buf, &o.info)
			}
		}()
	}
	for i := range residue {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	stats := make(map[string]os.FileInfo, len(residue))
	binary := make(map[string]struct{})
	failures := 0
	for i, o := range observed {
		p := residue[i]
		stats[p] = o.info
		if o.err != nil {
			failures++
			continue
		}
		if o.text {
			text[p] = struct{}{}
		} else {
			binary[p] = struct{}{}
		}
	}
	admitEmptyFilesToTextSet(root, paths, text, nil, stats, binary)
	return text, failures
}

func homeClassifyRgControl(root, bin string, paths []string, extra ...string) (map[string]struct{}, int, error) {
	text := make(map[string]struct{}, len(paths))
	var residue []string
	for _, p := range paths {
		switch classifyPathByName(p) {
		case nameClassText:
			text[p] = struct{}{}
		case nameClassUnknown:
			residue = append(residue, p)
		}
	}
	fixed := []string{"--files-without-match", "--text", "--no-messages", "-0", "-e", `\x00`}
	fixed = append(fixed, extra...)
	fixed = append(fixed, "--")
	chunks, err := contentPathChunks(bin, fixed, residue, residuePathChunkMaxCount, runtime.GOOS)
	if err != nil {
		return nil, 0, err
	}
	binary := make(map[string]struct{})
	failures := 0
	for _, chunk := range chunks {
		cmd := exec.Command(bin, append(slices.Clone(fixed), chunk...)...)
		cmd.Dir = root
		data, err := cmd.Output()
		clean := err == nil
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				return nil, failures, err
			}
			if exit.ExitCode() == 1 {
				clean = true
			} else {
				failures++
			}
		}
		for _, p := range splitNullSeparated(data) {
			text[normalizeRelPath(p)] = struct{}{}
		}
		if clean {
			for _, p := range chunk {
				if _, ok := text[p]; !ok {
					binary[p] = struct{}{}
				}
			}
		}
	}
	admitEmptyFilesToTextSet(root, paths, text, nil, nil, binary)
	return text, failures, nil
}

func TestHomeClassifierTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_HOME_CLASSIFIER_BENCH") != "1" {
		t.Skip("opt-in home classification benchmark")
	}
	root := os.Getenv("CATCLIP_TEST_CORPUS")
	if root == "" {
		t.Fatal("CATCLIP_TEST_CORPUS required")
	}
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", "")
	bin, ok := RipgrepBinary()
	if !ok {
		t.Fatal("rg required")
	}
	rounds := 3
	if s := os.Getenv("CATCLIP_HOME_BENCH_ROUNDS"); s != "" {
		var err error
		rounds, err = strconv.Atoi(s)
		if err != nil || rounds < 1 {
			t.Fatal("positive CATCLIP_HOME_BENCH_ROUNDS required")
		}
	}
	t.Logf("environment go=%s os=%s arch=%s cpus=%d gomaxprocs=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0))
	for _, expanded := range []bool{false, true} {
		name := "visible"
		if expanded {
			name = "no-ignore"
		}
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			paths, err := RunRipgrepFiles(root, RipgrepFileOptions{NoIgnore: expanded, HissPath: os.Getenv("CATCLIP_TEST_HISS")})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("enumeration workload=%s files=%d ms=%.3f", name, len(paths), float64(time.Since(start))/float64(time.Millisecond))
			var residue []string
			initialInfo := make(map[string]os.FileInfo, len(paths))
			var totalBytes, residueBytes, largest int64
			var knownText, knownBinary, statErrors, nonRegular int
			for _, p := range paths {
				class := classifyPathByName(p)
				switch class {
				case nameClassText:
					knownText++
				case nameClassBinary:
					knownBinary++
				default:
					residue = append(residue, p)
				}
				info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
				initialInfo[p] = info
				if err != nil {
					statErrors++
					continue
				}
				if !info.Mode().IsRegular() {
					nonRegular++
					continue
				}
				totalBytes += info.Size()
				if class == nameClassUnknown {
					residueBytes += info.Size()
					largest = max(largest, info.Size())
				}
			}
			fixed := []string{"--files-without-match", "--text", "--no-messages", "-0", "-e", `\x00`, "--"}
			chunks, err := contentPathChunks(bin, fixed, residue, residuePathChunkMaxCount, runtime.GOOS)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("workload=%s files=%d name_text=%d name_binary=%d residue=%d logical_bytes=%d residue_bytes=%d largest_residue_bytes=%d rg_batches=%d stat_errors=%d non_regular=%d", name, len(paths), knownText, knownBinary, len(residue), totalBytes, residueBytes, largest, len(chunks), statErrors, nonRegular)
			start = time.Now()
			want, _, err := homeClassifyRgControl(root, bin, paths)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("oracle workload=%s ms=%.3f text=%d", name, float64(time.Since(start))/float64(time.Millisecond), len(want))
			variants := []int{0, -1, 4, 8}
			if os.Getenv("CATCLIP_HOME_BENCH_INTEGRATED") == "1" {
				variants = []int{0}
			}
			for round := 0; round < rounds; round++ {
				for offset := range variants {
					workers := variants[(round+offset)%len(variants)]
					runtime.GC()
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					start = time.Now()
					var got map[string]struct{}
					var failures int
					variant := fmt.Sprintf("go-%d", workers)
					if workers == 0 {
						variant = "go-production"
						got, _, err = classifyEnumeratedTextPaths(root, paths)
					} else if workers == -1 {
						variant = "rg-max-count-1"
						got, failures, err = homeClassifyRgControl(root, bin, paths, "--max-count", "1")
					} else {
						got, failures = homeClassifyGo(root, paths, workers)
						err = nil
					}
					elapsed := time.Since(start)
					runtime.ReadMemStats(&after)
					if err != nil {
						t.Fatal(err)
					}
					missing, extra, changed, unexplained := 0, 0, 0, 0
					checkMismatch := func(p string) {
						before := initialInfo[p]
						after, _ := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
						mutated := (before == nil) != (after == nil)
						if before != nil && after != nil {
							mutated = before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, after)
						}
						if mutated {
							changed++
						} else {
							unexplained++
						}
					}
					for p := range want {
						if _, ok := got[p]; !ok {
							missing++
							checkMismatch(p)
							if missing <= 10 {
								t.Logf("mismatch variant=%s missing=%q", variant, p)
							}
						}
					}
					for p := range got {
						if _, ok := want[p]; !ok {
							extra++
							checkMismatch(p)
							if extra <= 10 {
								t.Logf("mismatch variant=%s extra=%q", variant, p)
							}
						}
					}
					t.Logf("sample workload=%s round=%d variant=%s ms=%.3f parent_alloc_bytes=%d text=%d missing=%d extra=%d changed_mismatches=%d unexplained_mismatches=%d observed_errors=%d parity=%t", name, round, variant, float64(elapsed)/float64(time.Millisecond), after.TotalAlloc-before.TotalAlloc, len(got), missing, extra, changed, unexplained, failures, missing == 0 && extra == 0)
					if unexplained != 0 {
						t.Error("unchanged-file membership disagrees; classifier differences need investigation")
					}
				}
			}
		})
	}
}
