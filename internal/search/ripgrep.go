package search

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/tigreau/catclip/internal/platform"
)

var (
	benchRgFilesTotal   atomic.Int64
	benchRgTextTotal    atomic.Int64
	benchRgMatchesTotal atomic.Int64
	benchRgVisibleTotal atomic.Int64
	benchRgFilesCalls   atomic.Int64
	benchRgTextCalls    atomic.Int64
	benchRgMatchesCalls atomic.Int64
	benchRgVisibleCalls atomic.Int64
)

func benchEnabled() bool {
	return os.Getenv("CATCLIP_BENCH_RG") != ""
}

func BenchReport() {
	if !benchEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "  rg --files (%dx):         %s\n", benchRgFilesCalls.Load(), time.Duration(benchRgFilesTotal.Load()))
	fmt.Fprintf(os.Stderr, "  rg --files-without-match: %s (%dx)\n", time.Duration(benchRgTextTotal.Load()), benchRgTextCalls.Load())
	fmt.Fprintf(os.Stderr, "  rg --files-with-matches:  %s (%dx)\n", time.Duration(benchRgMatchesTotal.Load()), benchRgMatchesCalls.Load())
	fmt.Fprintf(os.Stderr, "  rg --files (visible):     %s (%dx)\n", time.Duration(benchRgVisibleTotal.Load()), benchRgVisibleCalls.Load())
}

var errRipgrepUnavailable = errors.New("Error: this catclip install is missing bundled ripgrep.\n  Reinstall catclip with its packaged tools; runtime does not fall back to arbitrary PATH copies.")

// ErrRipgrepBadPattern is returned by rg helpers when the pattern fails to
// compile under PCRE2. Callers serving interactive previews (where the
// user types characters one at a time, so the pattern is invalid most of
// the time) can use errors.Is to swallow this silently; non-interactive
// callers let it propagate so the user sees the rg error.
var ErrRipgrepBadPattern = errors.New("Error: --contains/--snippet pattern failed to compile in ripgrep PCRE2 engine.")

type RipgrepFileOptions struct {
	NoIgnore  bool
	Basenames []string
	Paths     []string
	HissPath  string
	// Timeout caps the rg call as a hung-process guard for error-path probes
	// like the ignored-ancestor lookup (see ACTIVE_PLAN_surface_ignored_ancestor.md).
	// Zero means use the default reloadCancelCtx behavior (no extra timeout).
	Timeout time.Duration
}

func RipgrepBinary() (string, bool) {
	return platform.BundledToolBinary("CATCLIP_RG", "rg")
}

func RunRipgrepFiles(workingDir string, opts RipgrepFileOptions) ([]string, error) {
	finishBench := platform.InternalBenchSpan("search.rg.files",
		"paths", platform.InternalBenchInt(len(opts.Paths)),
		"basenames", platform.InternalBenchInt(len(opts.Basenames)),
		"no_ignore", strconv.FormatBool(opts.NoIgnore),
		"timeout", strconv.FormatBool(opts.Timeout > 0),
	)
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return nil, errRipgrepUnavailable
	}

	// Symlinks are intentionally excluded for now, so keep rg on its default
	// non-following behavior and avoid pulling link paths into candidate lists.
	args := []string{"--files", "--hidden", "--no-ignore-dot", "--no-require-git", "-0"}
	if opts.NoIgnore {
		args = append(args, "--no-ignore")
	}
	if !opts.NoIgnore && opts.HissPath != "" {
		args = append(args, "--ignore-file", opts.HissPath)
	}
	for _, base := range opts.Basenames {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		args = append(args, "-g", base)
	}
	pathArgs := make([]string, 0, len(opts.Paths))
	for _, rel := range opts.Paths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." {
			continue
		}
		pathArgs = append(pathArgs, rel)
	}
	if len(pathArgs) > 0 {
		args = append(args, "--")
		args = append(args, pathArgs...)
	}

	ctx := reloadCancelCtx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(reloadCancelCtx, opts.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workingDir
	t0 := time.Now()
	out, err := cmd.Output()
	if benchEnabled() {
		benchRgFilesTotal.Add(int64(time.Since(t0)))
		benchRgFilesCalls.Add(1)
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			finishBench("err", "true", "deadline", "true")
			return nil, ctx.Err()
		}
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			finishBench("err", "false", "results", "0")
			return nil, nil
		}
		finishBench("err", "true")
		return nil, err
	}

	paths := splitNullSeparated(out)
	for i, rel := range paths {
		paths[i] = normalizeRelPath(rel)
	}
	sort.Strings(paths)
	paths = dedupeSortedStrings(paths)
	finishBench("err", "false", "results", platform.InternalBenchInt(len(paths)))
	return paths, nil
}

// textFileSetCache memoizes runRipgrepTextFiles by canonicalized working
// directory. catclip is short-lived; multiple resolvers within one invocation
// hit the same working dir, so paying the rg-text scan once amortizes across
// the whole run instead of once per resolver.
var (
	textFileSetCacheMu sync.Mutex
	textFileSetCache   = map[string]map[string]struct{}{}
)

// scopedCacheTargets returns a normalized, sorted, deduped list of target
// paths suitable for use as both an rg cache key component and rg positional
// arguments. Returns nil when the text-set must fall back to project-wide.
//
// Scope-narrowing is only safe when every target resolves to a real literal
// path. If any target is a fuzzy query, a glob, or absent from disk, the
// downstream discovery pipeline may land paths anywhere in the project, so
// the text-set has to cover the whole working dir.
func scopedCacheTargets(workingDir string, targets []string) []string {
	if len(targets) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(targets))
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		if hasGlobChars(t) {
			return nil
		}
		rel := normalizeRelPath(t)
		if rel == "" || rel == "." {
			return nil
		}
		if _, err := os.Stat(filepath.Join(workingDir, rel)); err != nil {
			// Fuzzy / missing target — fall back to project-wide so the
			// resolved file set (wherever it lands) is still covered.
			return nil
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// joinScopedCacheTargets renders a normalized target list into a stable
// cache key component. Uses NUL as a separator so no path containing
// whitespace or slashes can collide with another.
func joinScopedCacheTargets(targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	return strings.Join(targets, "\x00")
}

// ResolveTextFileSet returns the cached rg-derived set of NUL-free files for
// workingDir restricted to the given scope targets. Pass nil/empty targets to
// get the project-wide set (the pre-scope-aware behavior); pass a non-empty
// list to scope the enumeration to those paths only. Multiple scopes in one
// process get independent cache entries. Safe to call from multiple resolvers;
// the underlying rg invocation runs at most once per distinct cache key.
func ResolveTextFileSet(workingDir string, targets []string) (map[string]struct{}, error) {
	dirKey, err := filepath.Abs(workingDir)
	if err != nil {
		dirKey = workingDir
	}
	dirKey = filepath.Clean(dirKey)
	normTargets := scopedCacheTargets(workingDir, targets)
	cacheKey := dirKey + "\x00" + joinScopedCacheTargets(normTargets)

	textFileSetCacheMu.Lock()
	if cached, ok := textFileSetCache[cacheKey]; ok {
		textFileSetCacheMu.Unlock()
		return cached, nil
	}
	textFileSetCacheMu.Unlock()

	set, err := runRipgrepTextFiles(workingDir, normTargets)
	if err != nil {
		return nil, err
	}

	textFileSetCacheMu.Lock()
	textFileSetCache[cacheKey] = set
	textFileSetCacheMu.Unlock()
	return set, nil
}

// runRipgrepTextFiles returns the set of files whose content contains no NUL
// byte. rg scans every file once in parallel; binary files (which contain
// NULs) are excluded. The query mirrors the working-directory scope of
// RunRipgrepFiles with NoIgnore=true and Hidden=true so the returned set
// covers every candidate the discovery pipeline can see, not just the
// gitignored-aware view.
//
// When targets is non-empty, the scan is restricted to those paths as rg
// positional arguments — files outside the targets are never enumerated.
// Pass nil/empty for the project-wide scan.
func runRipgrepTextFiles(workingDir string, targets []string) (map[string]struct{}, error) {
	finishBench := platform.InternalBenchSpan("search.rg.text_files",
		"targets", platform.InternalBenchInt(len(targets)),
	)
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return nil, errRipgrepUnavailable
	}

	args := []string{
		"--files-without-match",
		"--text",
		"--no-ignore",
		"--hidden",
		"--no-messages",
		"-0",
		"-e", `\x00`,
	}
	if len(targets) > 0 {
		args = append(args, "--")
		args = append(args, targets...)
	}

	cmd := exec.CommandContext(reloadCancelCtx, bin, args...)
	cmd.Dir = workingDir
	t0 := time.Now()
	out, err := cmd.Output()
	if benchEnabled() {
		benchRgTextTotal.Add(int64(time.Since(t0)))
		benchRgTextCalls.Add(1)
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			finishBench("err", "false", "results", "0")
			return map[string]struct{}{}, nil
		}
		finishBench("err", "true")
		return nil, err
	}

	paths := splitNullSeparated(out)
	set := make(map[string]struct{}, len(paths))
	for _, rel := range paths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." {
			continue
		}
		set[rel] = struct{}{}
	}
	finishBench("err", "false", "results", platform.InternalBenchInt(len(set)))
	return set, nil
}

// visibleFileSetCache memoizes runRipgrepVisibleFiles by
// (canonicalized workingDir, hissPath). The two-call attribution scheme
// (one without --ignore-file, one with) hits this cache twice per
// resolver invocation, so memoizing keeps each rg call to once per
// catclip process.
var (
	visibleFileSetCacheMu sync.Mutex
	visibleFileSetCache   = map[string]map[string]struct{}{}
)

// ResolveVisibleFileSet returns the rg-derived gitignore-aware visible
// file set for workingDir, optionally also filtered by an additional
// gitignore-syntax file at hissPath. Pass hissPath="" to get the
// gitignore-only view.
//
// This cache is intentionally **not** scope-narrowed: rg's positional-path
// walk does not respect ancestor ignore patterns, so passing targets as
// positionals would silently change ignore semantics. The text-file set
// (ResolveTextFileSet) uses --no-ignore and can safely be scope-narrowed;
// the visible set cannot, without a Go-side filter step that does not yet
// exist. See ACTIVE_PLAN_scope_aware_rg_caches.md for follow-up.
func ResolveVisibleFileSet(workingDir, hissPath string) (map[string]struct{}, error) {
	dirKey, err := filepath.Abs(workingDir)
	if err != nil {
		dirKey = workingDir
	}
	dirKey = filepath.Clean(dirKey)

	hissKey := hissPath
	if hissKey != "" {
		if abs, err := filepath.Abs(hissKey); err == nil {
			hissKey = filepath.Clean(abs)
		}
	}
	cacheKey := dirKey + "\x00" + hissKey

	visibleFileSetCacheMu.Lock()
	if cached, ok := visibleFileSetCache[cacheKey]; ok {
		visibleFileSetCacheMu.Unlock()
		return cached, nil
	}
	visibleFileSetCacheMu.Unlock()

	set, err := runRipgrepVisibleFiles(workingDir, hissPath)
	if err != nil {
		return nil, err
	}

	visibleFileSetCacheMu.Lock()
	visibleFileSetCache[cacheKey] = set
	visibleFileSetCacheMu.Unlock()
	return set, nil
}

// runRipgrepVisibleFiles enumerates files under workingDir that survive
// rg's ignore filtering. Default rg behavior respects .gitignore,
// .git/info/exclude, and core.excludesFile. We pass --no-ignore-dot to
// suppress .ignore/.rgignore (so attribution stays clean), and
// --no-require-git so the gitignore engine activates outside git repos
// (catclip's previous Go matcher applied root .gitignore unconditionally).
// When hissPath is non-empty, rg also applies it as a gitignore-syntax
// overlay.
func runRipgrepVisibleFiles(workingDir, hissPath string) (map[string]struct{}, error) {
	finishBench := platform.InternalBenchSpan("search.rg.visible_files",
		"has_hiss", strconv.FormatBool(strings.TrimSpace(hissPath) != ""),
	)
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return nil, errRipgrepUnavailable
	}

	args := []string{
		"--files",
		"--hidden",
		"--no-ignore-dot",
		"--no-require-git",
		"-0",
	}
	if hissPath != "" {
		args = append(args, "--ignore-file", hissPath)
	}

	cmd := exec.CommandContext(reloadCancelCtx, bin, args...)
	cmd.Dir = workingDir
	t0 := time.Now()
	out, err := cmd.Output()
	if benchEnabled() {
		benchRgVisibleTotal.Add(int64(time.Since(t0)))
		benchRgVisibleCalls.Add(1)
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			finishBench("err", "false", "results", "0")
			return map[string]struct{}{}, nil
		}
		finishBench("err", "true")
		return nil, err
	}

	paths := splitNullSeparated(out)
	set := make(map[string]struct{}, len(paths))
	for _, rel := range paths {
		rel = normalizeRelPath(rel)
		if rel == "" || rel == "." {
			continue
		}
		set[rel] = struct{}{}
	}
	finishBench("err", "false", "results", platform.InternalBenchInt(len(set)))
	return set, nil
}

// dirsContainingFiles returns the set of ancestor directories that have
// at least one descendant in the provided file set. The root "." and
// the empty string are excluded.
func DirsContainingFiles(files map[string]struct{}) map[string]struct{} {
	dirs := make(map[string]struct{}, len(files)/2)
	for f := range files {
		for d := normalizeRelPath(filepath.ToSlash(filepath.Dir(f))); d != "" && d != "."; d = normalizeRelPath(filepath.ToSlash(filepath.Dir(d))) {
			dirs[d] = struct{}{}
		}
	}
	return dirs
}

// RunRipgrepMatchLines is the line-number-aware sibling of
// RunRipgrepMatches. It returns the matched line numbers per file rather
// than just a "has any match" boolean, and runs the pattern under PCRE2
// so users can write backreferences/lookaround/atomic groups that Go's
// RE2-only regexp engine doesn't accept. See
// ACTIVE_PLAN_ripgrep_migration_tail.md for the migration rationale and
// the documented regex-flavor change.
//
// Output is parsed from rg's --null-delimited line-number stream:
//
//	{path}\0{lineno}:{matched line content}\n
//
// Splitting on \x00 first (then on the first ':' of the remainder) keeps
// the parser robust against paths containing colons (Windows drive
// letters) and matched lines starting with digits-and-colons.
//
// Returns a map keyed by the absolute path rg emitted, with line numbers
// in the order rg produced them. Empty input → empty map.
func RunRipgrepMatchLines(pattern string, absPaths []string) (map[string][]int, error) {
	finishBench := platform.InternalBenchSpan("search.rg.match_lines",
		"paths", platform.InternalBenchInt(len(absPaths)),
	)
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return nil, errRipgrepUnavailable
	}
	if len(absPaths) == 0 {
		finishBench("err", "false", "chunks", "0", "matches", "0")
		return map[string][]int{}, nil
	}

	matches := make(map[string][]int, len(absPaths))
	chunks := 0
	for _, chunk := range chunkExecArgs(absPaths, 256, 60*1024) {
		chunks++
		args := []string{
			"--color=never",
			"--no-messages",
			"--no-heading",
			"--line-number",
			"--null",
			"--pcre2",
			"-H",
		}
		if isSmartCaseInsensitive(pattern) {
			args = append(args, "--ignore-case")
		}
		args = append(args, "-e", pattern, "--")
		args = append(args, chunk...)

		cmd := exec.CommandContext(reloadCancelCtx, bin, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		t0 := time.Now()
		out, err := cmd.Output()
		if benchEnabled() {
			benchRgMatchesTotal.Add(int64(time.Since(t0)))
			benchRgMatchesCalls.Add(1)
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				switch exitErr.ExitCode() {
				case 1:
					continue
				case 2:
					if isRipgrepBadPatternStderr(stderr.Bytes()) {
						finishBench(
							"err", "true",
							"bad_pattern", "true",
							"chunks", platform.InternalBenchInt(chunks),
							"matches", platform.InternalBenchInt(len(matches)),
						)
						return nil, ErrRipgrepBadPattern
					}
				}
			}
			finishBench(
				"err", "true",
				"chunks", platform.InternalBenchInt(chunks),
				"matches", platform.InternalBenchInt(len(matches)),
			)
			return nil, err
		}

		for _, rec := range bytes.Split(out, []byte{'\n'}) {
			if len(rec) == 0 {
				continue
			}
			nul := bytes.IndexByte(rec, 0)
			if nul < 0 {
				continue
			}
			path := string(rec[:nul])
			rest := rec[nul+1:]
			colon := bytes.IndexByte(rest, ':')
			if colon < 0 {
				continue
			}
			line, err := strconv.Atoi(string(rest[:colon]))
			if err != nil || line < 1 {
				continue
			}
			matches[path] = append(matches[path], line)
		}
	}
	finishBench(
		"err", "false",
		"chunks", platform.InternalBenchInt(chunks),
		"matches", platform.InternalBenchInt(len(matches)),
	)
	return matches, nil
}

// FirstMatchLinePerFile returns the line number of the first match in each
// file under absPaths, given the regex pattern. Files without a match are
// absent from the returned map. Used by the content-match picker to set
// fzf's --preview-window offset so the per-file preview opens centered on
// the first hit.
//
// Why a dedicated helper rather than reusing RunRipgrepMatchLines: this
// runs with --max-count 1 so rg stops scanning each file as soon as the
// first match is found, which matters for the picker's per-keystroke
// refresh cadence on large files.
//
// Output parsing uses the same NUL-delimited format as runRipgrepMatchLines
// (`{path}\0{lineno}:{matched}\n`) — robust against Windows drive-letter
// colons in paths and digit-prefixed match content.
func FirstMatchLinePerFile(pattern string, absPaths []string) (map[string]int, error) {
	finishBench := platform.InternalBenchSpan("search.rg.first_match_line_per_file",
		"paths", platform.InternalBenchInt(len(absPaths)),
	)
	if len(absPaths) == 0 {
		finishBench("err", "false", "chunks", "0", "matches", "0")
		return map[string]int{}, nil
	}
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return nil, errRipgrepUnavailable
	}
	out := make(map[string]int, len(absPaths))
	chunks := 0
	for _, chunk := range chunkExecArgs(absPaths, 256, 60*1024) {
		chunks++
		args := []string{
			"--color=never",
			"--no-messages",
			"--no-heading",
			"--line-number",
			"--null",
			"--pcre2",
			"-H",
			"--max-count", "1",
			"--only-matching",
			"--replace", "",
		}
		if isSmartCaseInsensitive(pattern) {
			args = append(args, "--ignore-case")
		}
		args = append(args, "-e", pattern, "--")
		args = append(args, chunk...)

		cmd := exec.CommandContext(reloadCancelCtx, bin, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		result, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				switch exitErr.ExitCode() {
				case 1:
					continue
				case 2:
					if isRipgrepBadPatternStderr(stderr.Bytes()) {
						finishBench(
							"err", "true",
							"bad_pattern", "true",
							"chunks", platform.InternalBenchInt(chunks),
							"matches", platform.InternalBenchInt(len(out)),
						)
						return nil, ErrRipgrepBadPattern
					}
				}
			}
			finishBench(
				"err", "true",
				"chunks", platform.InternalBenchInt(chunks),
				"matches", platform.InternalBenchInt(len(out)),
			)
			return nil, err
		}
		for _, rec := range bytes.Split(result, []byte{'\n'}) {
			if len(rec) == 0 {
				continue
			}
			nul := bytes.IndexByte(rec, 0)
			if nul < 0 {
				continue
			}
			path := string(rec[:nul])
			rest := rec[nul+1:]
			colon := bytes.IndexByte(rest, ':')
			if colon < 0 {
				continue
			}
			line, err := strconv.Atoi(string(rest[:colon]))
			if err != nil || line < 1 {
				continue
			}
			if _, dup := out[path]; dup {
				continue
			}
			out[path] = line
		}
	}
	finishBench(
		"err", "false",
		"chunks", platform.InternalBenchInt(chunks),
		"matches", platform.InternalBenchInt(len(out)),
	)
	return out, nil
}

func RunRipgrepMatches(pattern string, absPaths []string) (map[string]struct{}, error) {
	finishBench := platform.InternalBenchSpan("search.rg.matches",
		"paths", platform.InternalBenchInt(len(absPaths)),
	)
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return nil, errRipgrepUnavailable
	}
	if len(absPaths) == 0 {
		finishBench("err", "false", "chunks", "0", "matches", "0")
		return map[string]struct{}{}, nil
	}

	matches := make(map[string]struct{}, len(absPaths))
	chunks := 0
	for _, chunk := range chunkExecArgs(absPaths, 256, 60*1024) {
		chunks++
		args := []string{
			"--color=never",
			"--no-messages",
			"--files-with-matches",
			"--pcre2",
			"-0",
			"-m", "1",
		}
		if isSmartCaseInsensitive(pattern) {
			args = append(args, "--ignore-case")
		}
		args = append(args, "-e", pattern, "--")
		args = append(args, chunk...)

		cmd := exec.CommandContext(reloadCancelCtx, bin, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		t0 := time.Now()
		out, err := cmd.Output()
		if benchEnabled() {
			benchRgMatchesTotal.Add(int64(time.Since(t0)))
			benchRgMatchesCalls.Add(1)
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				switch exitErr.ExitCode() {
				case 1:
					continue
				case 2:
					if isRipgrepBadPatternStderr(stderr.Bytes()) {
						finishBench(
							"err", "true",
							"bad_pattern", "true",
							"chunks", platform.InternalBenchInt(chunks),
							"matches", platform.InternalBenchInt(len(matches)),
						)
						return nil, ErrRipgrepBadPattern
					}
				}
			}
			finishBench(
				"err", "true",
				"chunks", platform.InternalBenchInt(chunks),
				"matches", platform.InternalBenchInt(len(matches)),
			)
			return nil, err
		}

		for _, match := range splitNullSeparated(out) {
			match = filepath.Clean(match)
			if match != "" {
				matches[match] = struct{}{}
			}
		}
	}
	finishBench(
		"err", "false",
		"chunks", platform.InternalBenchInt(chunks),
		"matches", platform.InternalBenchInt(len(matches)),
	)
	return matches, nil
}

// isRipgrepBadPatternStderr returns true when rg's stderr indicates the
// pattern (not file enumeration / IO) was the failure. rg emits messages
// like "regex parse error" or "PCRE2: pattern could not be compiled".
// Checking by substring is good enough: the only other exit-2 cause we
// hit in practice is missing files, which rg suppresses under
// --no-messages anyway.
func isRipgrepBadPatternStderr(stderr []byte) bool {
	if len(stderr) == 0 {
		return false
	}
	lower := bytes.ToLower(stderr)
	for _, marker := range [][]byte{
		[]byte("regex parse error"),
		[]byte("pcre2"),
		[]byte("error parsing regex"),
	} {
		if bytes.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func splitNullSeparated(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	parts := bytes.Split(data, []byte{0})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		out = append(out, string(part))
	}
	return out
}

func chunkExecArgs(paths []string, maxCount, maxBytes int) [][]string {
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
		chunk := make([]string, len(current))
		copy(chunk, current)
		chunks = append(chunks, chunk)
		current = current[:0]
		currentBytes = 0
	}

	for _, path := range paths {
		size := len(path) + 1
		if len(current) > 0 && (len(current) >= maxCount || currentBytes+size > maxBytes) {
			flush()
		}
		current = append(current, path)
		currentBytes += size
	}
	flush()
	return chunks
}

// HasScopedIgnoredTargetsStreaming reports whether any path within
// scopeTargets is ignored (by .gitignore or the .hiss overlay), at
// any nesting depth.
//
// Implementation: take the cached visible-with-hiss set from
// ResolveVisibleFileSet (process-cached, free on warm cache), then
// stream `rg --files --no-ignore` over the scope targets and compare
// each emitted path against the visible set. The first path missing
// from visible short-circuits to (true, nil) and the rg subprocess
// is cancelled.
//
// Hard rg failures return (false, err); the caller may log under -v
// and treat the modifier as unavailable. There is no fallback path.
//
// A previous version of this helper used a `--max-depth 3` cap on
// both rg invocations as a perf optimization. The cap was removed
// because it produced false negatives (deep ignored entries hid
// `--include` from the modifier menu). See
// docs/versions/v0.5.0/reports/ACTIVE_PLAN_modifier_menu_performance.md.
func HasScopedIgnoredTargetsStreaming(ctx context.Context, workingDir string, scopeTargets []string, hissPath string) (bool, error) {
	bin, ok := RipgrepBinary()
	if !ok {
		return false, errRipgrepUnavailable
	}

	relTargets := make([]string, 0, len(scopeTargets))
	for _, t := range scopeTargets {
		t = normalizeRelPath(t)
		if t == "" {
			continue
		}
		relTargets = append(relTargets, t)
	}
	if len(relTargets) == 0 {
		return false, nil
	}

	visible, err := ResolveVisibleFileSet(workingDir, hissPath)
	if err != nil {
		return false, err
	}

	withIgnoredArgs := []string{
		"--files",
		"--hidden",
		"--no-require-git",
		"-0",
		"-g", "!.git",
		"--no-ignore",
		"--",
	}
	withIgnoredArgs = append(withIgnoredArgs, relTargets...)

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	cmd := exec.CommandContext(streamCtx, bin, withIgnoredArgs...)
	cmd.Dir = workingDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}

	found := false
	reader := bufio.NewReader(stdout)
	for {
		chunk, readErr := reader.ReadBytes(0)
		if len(chunk) > 0 && chunk[len(chunk)-1] == 0 {
			chunk = chunk[:len(chunk)-1]
		}
		if len(chunk) > 0 {
			p := normalizeRelPath(string(chunk))
			if p != "" && p != "." {
				if _, ok := visible[p]; !ok {
					found = true
					break
				}
			}
		}
		if readErr != nil {
			break
		}
	}

	if found {
		cancelStream()
	}
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if !found && waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
			return false, waitErr
		}
	}
	return found, nil
}

// MaxLinesForFiles returns the largest line count across absPaths, using
// bundled rg as the line-counting engine (`rg -c '^'`). The Go side does
// no file reading; rg owns the scan.
//
// Returns (0, nil) on empty input. Returns errRipgrepUnavailable if the
// bundled binary cannot be located. Per-chunk rg exit code 1 (no matches)
// is tolerated — that just means the chunk's files are empty/binary;
// continue with the next chunk.
//
// Used by the interactive lines picker to bound its numeric row set.
// Callers must pass absolute paths; the function does not resolve relative
// paths against a working directory.
func MaxLinesForFiles(absPaths []string) (int, error) {
	finishBench := platform.InternalBenchSpan("search.rg.max_lines",
		"paths", platform.InternalBenchInt(len(absPaths)),
	)
	if len(absPaths) == 0 {
		finishBench("err", "false", "chunks", "0", "max_lines", "0")
		return 0, nil
	}
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return 0, errRipgrepUnavailable
	}
	maxLines := 0
	chunks := 0
	for _, chunk := range chunkExecArgs(absPaths, 256, 60*1024) {
		chunks++
		chunkMax, err := maxLinesForFileChunk(bin, chunk)
		if err != nil {
			finishBench(
				"err", "true",
				"chunks", platform.InternalBenchInt(chunks),
				"max_lines", platform.InternalBenchInt(maxLines),
			)
			return 0, err
		}
		if chunkMax > maxLines {
			maxLines = chunkMax
		}
	}
	finishBench(
		"err", "false",
		"chunks", platform.InternalBenchInt(chunks),
		"max_lines", platform.InternalBenchInt(maxLines),
	)
	return maxLines, nil
}

// SizedFile is the leaf POD shape MaxLinesForSizedFiles accepts. Root maps
// its domain fileEntry to []SizedFile so the optimization below can run
// without dragging fileEntry across the search boundary.
type SizedFile struct {
	AbsPath   string
	SizeBytes int64
	SizeKnown bool
}

// MaxLinesForSizedFiles is the size-sorted early-stop variant of
// MaxLinesForFiles: sorts candidates by descending size, runs rg
// chunk-by-chunk, and short-circuits as soon as the largest-remaining file
// can't possibly exceed the running max. Falls back to the unsized loop
// when any input lacks a known size.
func MaxLinesForSizedFiles(files []SizedFile) (int, error) {
	finishBench := platform.InternalBenchSpan("search.rg.max_lines_sized",
		"files", platform.InternalBenchInt(len(files)),
	)
	if len(files) == 0 {
		finishBench("err", "false", "chunks", "0", "max_lines", "0")
		return 0, nil
	}

	absPaths := make([]string, 0, len(files))
	candidates := make([]sizedLineCountCandidate, 0, len(files))
	for _, f := range files {
		if strings.TrimSpace(f.AbsPath) == "" {
			continue
		}
		absPaths = append(absPaths, f.AbsPath)
		if !f.SizeKnown || f.SizeBytes < 0 {
			continue
		}
		candidates = append(candidates, sizedLineCountCandidate{
			absPath:   f.AbsPath,
			sizeBytes: f.SizeBytes,
		})
	}
	if len(absPaths) == 0 {
		finishBench("err", "false", "chunks", "0", "max_lines", "0")
		return 0, nil
	}
	if len(candidates) != len(absPaths) {
		maxLines, err := MaxLinesForFiles(absPaths)
		finishBench(
			"err", platform.InternalBenchError(err),
			"fallback", "true",
			"max_lines", platform.InternalBenchInt(maxLines),
		)
		return maxLines, err
	}

	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return 0, errRipgrepUnavailable
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].sizeBytes > candidates[j].sizeBytes
	})

	maxLines := 0
	chunks := 0
	for start := 0; start < len(candidates); {
		if candidates[start].sizeBytes <= int64(maxLines) {
			finishBench(
				"err", "false",
				"chunks", platform.InternalBenchInt(chunks),
				"max_lines", platform.InternalBenchInt(maxLines),
				"short_circuit", "true",
			)
			return maxLines, nil
		}
		chunk, next := sizedLineCountCandidateChunk(candidates, start, 256, 60*1024)
		chunks++
		chunkMax, err := maxLinesForFileChunk(bin, chunk)
		if err != nil {
			finishBench(
				"err", "true",
				"chunks", platform.InternalBenchInt(chunks),
				"max_lines", platform.InternalBenchInt(maxLines),
			)
			return 0, err
		}
		if chunkMax > maxLines {
			maxLines = chunkMax
		}
		start = next
	}
	finishBench(
		"err", "false",
		"chunks", platform.InternalBenchInt(chunks),
		"max_lines", platform.InternalBenchInt(maxLines),
		"short_circuit", "false",
	)
	return maxLines, nil
}

type sizedLineCountCandidate struct {
	absPath   string
	sizeBytes int64
}

func sizedLineCountCandidateChunk(candidates []sizedLineCountCandidate, start, maxCount, maxBytes int) ([]string, int) {
	chunk := make([]string, 0, maxCount)
	currentBytes := 0
	i := start
	for i < len(candidates) {
		path := candidates[i].absPath
		size := len(path) + 1
		if len(chunk) > 0 && (len(chunk) >= maxCount || currentBytes+size > maxBytes) {
			break
		}
		chunk = append(chunk, path)
		currentBytes += size
		i++
	}
	return chunk, i
}

func maxLinesForFileChunk(bin string, absPaths []string) (int, error) {
	if len(absPaths) == 0 {
		return 0, nil
	}
	args := []string{"-c", "--no-messages", "--color=never", "-e", "^", "--"}
	args = append(args, absPaths...)
	cmd := exec.CommandContext(reloadCancelCtx, bin, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return 0, nil
		}
		return 0, err
	}

	maxLines := 0
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		// rg prints `path:count` when there are multiple positionals
		// but just `count` for a single positional. Handle both: prefer
		// LastIndexByte for the colon (also right for Windows drives),
		// fall back to the bare-count form.
		tail := line
		if colon := bytes.LastIndexByte(line, ':'); colon >= 0 {
			tail = line[colon+1:]
		}
		n, err := strconv.Atoi(string(bytes.TrimSpace(tail)))
		if err != nil {
			continue
		}
		if n > maxLines {
			maxLines = n
		}
	}
	return maxLines, nil
}

func isSmartCaseInsensitive(pattern string) bool {
	for _, r := range pattern {
		if unicode.IsUpper(r) {
			return false
		}
	}
	return true
}
