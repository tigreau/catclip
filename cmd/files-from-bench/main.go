// Standalone benchmark comparing path-list delivery / scanning strategies
// for catclip's content-match stage. Not built into catclip; run from the
// repo root as `go run ./cmd/files-from-bench --root ~/Desktop/catclip-test-data/vscode-main`.
//
// The benchmark fixes the candidate file set once (rg --files), then runs
// each strategy against that same set and reports wall time + match count.
// Each strategy's job is identical: "find files containing PATTERN under
// the given candidate paths."
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	root := flag.String("root", os.ExpandEnv("$HOME/Desktop/catclip-test-data/vscode-main"), "corpus root")
	pattern := flag.String("pattern", "TODO", "search pattern")
	repeats := flag.Int("repeats", 3, "runs per strategy (best wall time wins)")
	chunkSize := flag.Int("chunk-size", 256, "paths per chunk for chunked-rg baseline")
	chunkBytes := flag.Int("chunk-bytes", 60*1024, "argv-byte cap per chunk for chunked-rg baseline")
	skipSymlink := flag.Bool("skip-symlink", false, "skip the symlink-tree strategy")
	flag.Parse()

	rgBin, err := findRg()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rg not found:", err)
		os.Exit(1)
	}

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "abs root:", err)
		os.Exit(1)
	}
	if info, err := os.Stat(absRoot); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "corpus root not a directory: %s\n", absRoot)
		os.Exit(1)
	}

	fmt.Printf("rg binary  : %s\n", rgBin)
	fmt.Printf("corpus root: %s\n", absRoot)
	fmt.Printf("pattern    : %q\n", *pattern)
	fmt.Printf("repeats    : %d (reporting best wall time)\n", *repeats)
	fmt.Println()

	t0 := time.Now()
	paths, err := rgListFiles(rgBin, absRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rg --files:", err)
		os.Exit(1)
	}
	fmt.Printf("discovered : %d files (rg --files in %s)\n", len(paths), time.Since(t0).Round(time.Millisecond))

	t0 = time.Now()
	textPaths, err := rgTextFiles(rgBin, absRoot, paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rg text filter:", err)
		os.Exit(1)
	}
	fmt.Printf("text-only  : %d files (rg text filter in %s)\n", len(textPaths), time.Since(t0).Round(time.Millisecond))
	fmt.Println()

	type strategy struct {
		name string
		run  func() (int, error)
	}
	strategies := []strategy{
		{
			name: "A. chunked-rg (today's catclip)",
			run: func() (int, error) {
				return strategyChunkedRg(rgBin, *pattern, textPaths, *chunkSize, *chunkBytes)
			},
		},
		{
			name: "B. single-rg argv (no chunk)",
			run: func() (int, error) {
				return strategySingleArgvRg(rgBin, *pattern, textPaths)
			},
		},
		{
			name: "C. direct rg PATTERN root/ (rg walks)",
			run: func() (int, error) {
				return strategyDirectRg(rgBin, *pattern, absRoot)
			},
		},
		{
			name: "D. Go RE2 in-process scanner",
			run: func() (int, error) {
				return strategyGoRE2(*pattern, textPaths)
			},
		},
	}
	if !*skipSymlink {
		strategies = append(strategies, strategy{
			name: "E. symlink-tree + single rg",
			run: func() (int, error) {
				return strategySymlinkTree(rgBin, *pattern, textPaths)
			},
		})
	}

	type result struct {
		name    string
		best    time.Duration
		matches int
		err     error
	}
	results := make([]result, 0, len(strategies))

	for _, s := range strategies {
		best := time.Duration(0)
		var bestMatches int
		var lastErr error
		for i := 0; i < *repeats; i++ {
			start := time.Now()
			m, err := s.run()
			elapsed := time.Since(start)
			if err != nil {
				lastErr = err
				continue
			}
			if best == 0 || elapsed < best {
				best = elapsed
				bestMatches = m
			}
		}
		results = append(results, result{name: s.name, best: best, matches: bestMatches, err: lastErr})
		if lastErr != nil {
			fmt.Printf("  %-44s  ERROR: %v\n", s.name, lastErr)
		} else {
			fmt.Printf("  %-44s  best=%-9s  matches=%d\n", s.name, best.Round(time.Millisecond), bestMatches)
		}
	}

	fmt.Println()
	fmt.Println("Summary (best run per strategy):")
	fmt.Println()
	fmt.Printf("  %-44s  %-12s  %s\n", "strategy", "wall", "matches")
	for _, r := range results {
		if r.err != nil {
			fmt.Printf("  %-44s  ERROR\n", r.name)
			continue
		}
		fmt.Printf("  %-44s  %-12s  %d\n", r.name, r.best.Round(time.Millisecond), r.matches)
	}
}

func findRg() (string, error) {
	local := "./bin/rg"
	if _, err := os.Stat(local); err == nil {
		abs, _ := filepath.Abs(local)
		return abs, nil
	}
	return exec.LookPath("rg")
}

func rgListFiles(rgBin, root string) ([]string, error) {
	cmd := exec.Command(rgBin, "--files", "--hidden", "--no-ignore-dot", "--no-require-git", "-0")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	rels := splitNul(out)
	abs := make([]string, 0, len(rels))
	for _, r := range rels {
		abs = append(abs, filepath.Join(root, r))
	}
	return abs, nil
}

func rgTextFiles(rgBin, root string, paths []string) ([]string, error) {
	set, err := runTextFilter(rgBin, root)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			continue
		}
		if _, ok := set[filepath.ToSlash(rel)]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func runTextFilter(rgBin, root string) (map[string]struct{}, error) {
	cmd := exec.Command(rgBin,
		"--files-without-match",
		"--text",
		"--no-ignore",
		"--hidden",
		"--no-messages",
		"-0",
		"-e", `\x00`,
	)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	parts := splitNul(out)
	set := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		set[filepath.ToSlash(p)] = struct{}{}
	}
	return set, nil
}

func splitNul(b []byte) []string {
	parts := bytes.Split(b, []byte{0})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) > 0 {
			out = append(out, string(p))
		}
	}
	return out
}

// Strategy A — today's catclip: chunk path list, run rg per chunk.
func strategyChunkedRg(rgBin, pattern string, paths []string, chunkCount, chunkBytes int) (int, error) {
	chunks := chunkArgs(paths, chunkCount, chunkBytes)
	seen := make(map[string]struct{})
	for _, c := range chunks {
		args := []string{"--color=never", "--no-messages", "--files-with-matches", "--pcre2", "-0", "-m", "1", "-e", pattern, "--"}
		args = append(args, c...)
		cmd := exec.Command(rgBin, args...)
		out, err := cmd.Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
				continue
			}
			return 0, err
		}
		for _, m := range splitNul(out) {
			seen[m] = struct{}{}
		}
	}
	return len(seen), nil
}

// Strategy B — one rg, all paths on argv (only fits if ARG_MAX is generous).
func strategySingleArgvRg(rgBin, pattern string, paths []string) (int, error) {
	args := []string{"--color=never", "--no-messages", "--files-with-matches", "--pcre2", "-0", "-m", "1", "-e", pattern, "--"}
	args = append(args, paths...)
	cmd := exec.Command(rgBin, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return 0, nil
		}
		return 0, err
	}
	return len(splitNul(out)), nil
}

// Strategy C — let rg walk the tree itself in one call.
func strategyDirectRg(rgBin, pattern, root string) (int, error) {
	cmd := exec.Command(rgBin, "--color=never", "--no-messages", "--files-with-matches", "--pcre2", "-0", "-m", "1", "-e", pattern, root)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return 0, nil
		}
		return 0, err
	}
	return len(splitNul(out)), nil
}

// Strategy D — in-process Go scanner using stdlib regexp (RE2).
func strategyGoRE2(pattern string, paths []string) (int, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0, err
	}
	workers := runtime.NumCPU()
	jobs := make(chan string, workers*2)
	var matched atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				f, err := os.Open(p)
				if err != nil {
					continue
				}
				if re.MatchReader(bufio.NewReader(f)) {
					matched.Add(1)
				}
				f.Close()
			}
		}()
	}
	for _, p := range paths {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	return int(matched.Load()), nil
}

// Strategy E — symlink each candidate into a tmpdir, rg PATTERN tmpdir/ once.
func strategySymlinkTree(rgBin, pattern string, paths []string) (int, error) {
	tmp, err := os.MkdirTemp("", "catclip-symlink-bench-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	for i, p := range paths {
		link := filepath.Join(tmp, "f"+strconv.Itoa(i))
		if err := os.Symlink(p, link); err != nil {
			return 0, err
		}
	}
	cmd := exec.Command(rgBin, "--color=never", "--no-messages", "--files-with-matches", "--pcre2", "-0", "-m", "1", "-L", "-e", pattern, tmp)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return 0, nil
		}
		return 0, err
	}
	return len(splitNul(out)), nil
}

func chunkArgs(paths []string, maxCount, maxBytes int) [][]string {
	if len(paths) == 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(paths)+maxCount-1)/maxCount)
	current := make([]string, 0, maxCount)
	currentBytes := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		cp := make([]string, len(current))
		copy(cp, current)
		chunks = append(chunks, cp)
		current = current[:0]
		currentBytes = 0
	}
	for _, p := range paths {
		size := len(p) + 1
		if len(current) > 0 && (len(current) >= maxCount || currentBytes+size > maxBytes) {
			flush()
		}
		current = append(current, p)
		currentBytes += size
	}
	flush()
	return chunks
}

// keep imports linked
var (
	_ = io.Discard
	_ = strings.TrimSpace
	_ = sort.Strings
	_ = bufio.NewScanner
)
