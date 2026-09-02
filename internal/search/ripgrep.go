package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

const (
	execPathChunkMaxCount     = 256
	execPathChunkMaxBytes     = 60 * 1024
	windowsExecPathChunkBytes = 24 * 1024
)

// execPathChunkByteLimit keeps explicit path arguments below the platform's
// process-spawn limit. Windows CreateProcess caps the complete command line at
// 32,767 UTF-16 code units, so reserve about 8 KiB for the executable, fixed
// rg flags, quoting, and the terminating NUL. Unix keeps the existing 60 KiB
// budget, which is comfortably below its usual ARG_MAX.
func execPathChunkByteLimit(goos string) int {
	if goos == "windows" {
		return windowsExecPathChunkBytes
	}
	return execPathChunkMaxBytes
}

func currentExecPathChunkByteLimit() int {
	return execPathChunkByteLimit(runtime.GOOS)
}

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
var ErrRipgrepBadPattern = errors.New("Error: --contains/--not-contains/--snippet pattern failed to compile in ripgrep PCRE2 engine.")

type RipgrepFileOptions struct {
	NoIgnore    bool
	Basenames   []string
	Paths       []string
	HissPath    string
	Enumeration MembershipEnumerationContext
	// Timeout caps the rg call as a hung-process guard for error-path probes
	// like the ignored-ancestor lookup (see ACTIVE_PLAN_surface_ignored_ancestor.md).
	// Zero means use the default reloadCancelCtx behavior (no extra timeout).
	Timeout time.Duration
}

func RipgrepBinary() (string, bool) {
	return platform.BundledToolBinary("CATCLIP_RG", "rg")
}

func RunRipgrepFiles(workingDir string, opts RipgrepFileOptions) (paths []string, retErr error) {
	policy := MembershipVisible
	if opts.NoIgnore {
		policy = MembershipNoIgnore
	}
	ctx := reloadCancelCtx
	var membershipSpan *membershipEnumerationSpan
	defer func() { membershipSpan.finish(len(paths), scanWasCancelled(ctx, retErr), retErr) }()
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
	membershipSpan = beginMembershipEnumeration(MembershipEnumerationFiles, policy, opts.Enumeration)

	// Symlinks are intentionally excluded for now, so keep rg on its default
	// non-following behavior and avoid pulling link paths into candidate lists.
	args := ripgrepFileArgs(opts, false)

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

	paths = splitNullSeparated(out)
	for i, rel := range paths {
		paths[i] = normalizeRelPath(rel)
	}
	sort.Strings(paths)
	paths = dedupeSortedStrings(paths)
	finishBench("err", "false", "results", platform.InternalBenchInt(len(paths)))
	return paths, nil
}

// ripgrepFileArgs is the single command-shape owner for ordinary file
// enumeration and metadata's diagnostic --debug pass. Keeping the flags here
// prevents the diagnostic inventory from silently observing a different
// ignore universe than normal Catclip discovery.
func ripgrepFileArgs(opts RipgrepFileOptions, debug bool) []string {
	args := []string{"--files", "--hidden", "--no-ignore-dot", "--no-require-git", "-0"}
	if debug {
		args = append(args, "--debug")
	}
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
	return args
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
func ResolveTextFileSet(workingDir string, targets []string, enumeration ...MembershipEnumerationContext) (map[string]struct{}, error) {
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

	context := membershipContextOrDefault(enumeration, MembershipReasonTextSetFallback)
	set, err := runRipgrepTextFiles(workingDir, normTargets, context)
	if err != nil {
		return nil, err
	}

	textFileSetCacheMu.Lock()
	textFileSetCache[cacheKey] = set
	textFileSetCacheMu.Unlock()
	return set, nil
}

// runRipgrepTextFiles returns the set of text files under workingDir.
//
// Hybrid classifier (2026-07-04, RESOLVED_PLAN_binary_detection_replacement.md
// "Option C revisited"): THE DEFINITION of binary is "contains a NUL byte
// anywhere" — what the pre-v0.6.5 full-file scan implemented. The hybrid
// preserves that definition while avoiding corpus content reads:
//
//  1. Enumerate the --no-ignore universe (path list only; Defender does
//     not toll directory enumeration the way it tolls ReadFile).
//  2. Classify by name (known_files.go): known-text and known-binary
//     extensions/basenames are never opened.
//  3. The residue (names the lists cannot decide) pays the definitional
//     full NUL scan with explicit path args — exact, and mode-independent
//     because --text bypasses rg's own binary heuristics.
//  4. Empty files contain no NUL → text by definition regardless of name
//     class (an empty .png is text under the rule, matching the prior
//     full-scan behavior); an Lstat-only pass re-admits 0-byte regular
//     files the name pass called binary. Residue empties are already
//     admitted by the scan itself (--files-without-match reports them).
//
// Known divergence from the definition (accepted; pinned by
// TestHybridKnownTextNameDivergence): a NUL-bearing file with a
// known-TEXT name (binary bytes in a *.md) classifies text without being
// read. The plan's sink-gate decision is the backstop for that class.
//
// When targets is non-empty, the universe is restricted to those paths as
// rg positional arguments — files outside the targets are never
// enumerated. Pass nil/empty for the project-wide universe.
func runRipgrepTextFiles(workingDir string, targets []string, enumeration MembershipEnumerationContext) (map[string]struct{}, error) {
	finishBench := platform.InternalBenchSpan("search.rg.text_files",
		"targets", platform.InternalBenchInt(len(targets)),
		"classifier", "hybrid",
	)

	allPaths, err := RunRipgrepFiles(workingDir, RipgrepFileOptions{
		NoIgnore:    true,
		Paths:       targets,
		Enumeration: enumeration.WithReason(MembershipReasonTextSetFallback),
	})
	if err != nil {
		finishBench("err", "true")
		return nil, fmt.Errorf("text classification: no-ignore enumeration under %q failed: %w", workingDir, err)
	}

	set, stats, err := classifyEnumeratedTextPaths(workingDir, allPaths)
	if err != nil {
		finishBench("err", "true", "residue_err", "true")
		return nil, err
	}

	finishBench("err", "false",
		"results", platform.InternalBenchInt(len(set)),
		"name_text", platform.InternalBenchInt(stats.nameText),
		"name_binary", platform.InternalBenchInt(stats.nameBinary),
		"residue_scan_count", platform.InternalBenchInt(stats.residueCount),
		"residue_text_count", platform.InternalBenchInt(stats.residueText),
		"residue_stat_count", platform.InternalBenchInt(stats.statCount),
		"residue_admitted_count", platform.InternalBenchInt(stats.admitted),
	)
	return set, nil
}

// ClassifyTextPaths applies Catclip's hybrid NUL classifier to an already
// enumerated path set. Discovery uses this after a visibility-aware rg walk so
// a small visible project does not pay to enumerate and classify a large
// ignored dependency tree merely to build an interactive picker.
func ClassifyTextPaths(workingDir string, relPaths []string) (map[string]struct{}, error) {
	finishBench := platform.InternalBenchSpan("search.rg.text_paths",
		"paths", platform.InternalBenchInt(len(relPaths)),
		"classifier", "hybrid",
	)
	set, stats, err := classifyEnumeratedTextPaths(workingDir, relPaths)
	if err != nil {
		finishBench("err", "true", "residue_err", "true")
		return nil, err
	}
	finishBench("err", "false",
		"results", platform.InternalBenchInt(len(set)),
		"name_text", platform.InternalBenchInt(stats.nameText),
		"name_binary", platform.InternalBenchInt(stats.nameBinary),
		"residue_scan_count", platform.InternalBenchInt(stats.residueCount),
		"residue_text_count", platform.InternalBenchInt(stats.residueText),
		"residue_stat_count", platform.InternalBenchInt(stats.statCount),
		"residue_admitted_count", platform.InternalBenchInt(stats.admitted),
	)
	return set, nil
}

// ClassifyTextPathsWithSizeCapture applies the same classifier as
// ClassifyTextPaths while opportunistically collecting Lstat sizes for files
// that classify as text. Known-text names enter the bounded size queue before
// the residue NUL scan; one metadata worker overlaps classification and the
// bounded pool expands after classification.
// Known-binary files are never queued; when the
// existing empty-file admission pass proves one is an empty text file, its
// already-known zero size is recorded directly.
func ClassifyTextPathsWithSizeCapture(workingDir string, relPaths []string) (map[string]struct{}, *TextSizeCapture, error) {
	finishBench := platform.InternalBenchSpan("search.rg.text_paths",
		"paths", platform.InternalBenchInt(len(relPaths)),
		"classifier", "hybrid+sizes",
	)
	set, stats, capture, err := classifyEnumeratedTextPathsWithSizeCapture(workingDir, relPaths)
	if err != nil {
		finishBench("err", "true", "residue_err", "true")
		return nil, nil, err
	}
	finishBench("err", "false",
		"results", platform.InternalBenchInt(len(set)),
		"name_text", platform.InternalBenchInt(stats.nameText),
		"name_binary", platform.InternalBenchInt(stats.nameBinary),
		"residue_scan_count", platform.InternalBenchInt(stats.residueCount),
		"residue_text_count", platform.InternalBenchInt(stats.residueText),
		"residue_stat_count", platform.InternalBenchInt(stats.statCount),
		"residue_admitted_count", platform.InternalBenchInt(stats.admitted),
	)
	return set, capture, nil
}

type textClassificationStats struct {
	nameText     int
	nameBinary   int
	residueCount int
	residueText  int
	statCount    int
	admitted     int
}

func classifyEnumeratedTextPaths(workingDir string, allPaths []string) (map[string]struct{}, textClassificationStats, error) {
	set, stats, _, err := classifyEnumeratedTextPathsInternal(workingDir, allPaths, false)
	return set, stats, err
}

func classifyEnumeratedTextPathsWithSizeCapture(workingDir string, allPaths []string) (map[string]struct{}, textClassificationStats, *TextSizeCapture, error) {
	return classifyEnumeratedTextPathsInternal(workingDir, allPaths, true)
}

func classifyEnumeratedTextPathsInternal(workingDir string, allPaths []string, captureSizes bool) (map[string]struct{}, textClassificationStats, *TextSizeCapture, error) {
	stats := textClassificationStats{}
	var capture *TextSizeCapture
	if captureSizes {
		capture = newTextSizeCapture(workingDir)
	}

	set := make(map[string]struct{}, len(allPaths))
	residue := make([]string, 0, 32)
	knownText := make([]string, 0, len(allPaths))
	for _, rel := range allPaths {
		switch classifyPathByName(rel) {
		case nameClassText:
			set[rel] = struct{}{}
			if capture != nil {
				knownText = append(knownText, rel)
			}
		case nameClassBinary:
			stats.nameBinary++
		default:
			residue = append(residue, rel)
		}
	}
	if capture != nil {
		capture.add(knownText)
	}

	stats.residueCount = len(residue)
	if len(residue) > 0 {
		scanned, scanErr := runRipgrepNulScanFiles(workingDir, residue)
		if scanErr != nil {
			if capture != nil {
				capture.Stop()
			}
			return nil, stats, nil, scanErr
		}
		stats.residueText = len(scanned)
		acceptedResidue := make([]string, 0, len(scanned))
		for rel := range scanned {
			set[rel] = struct{}{}
			acceptedResidue = append(acceptedResidue, rel)
		}
		if capture != nil {
			capture.add(acceptedResidue)
		}
	}

	stats.statCount, stats.admitted = admitEmptyFilesToTextSet(workingDir, allPaths, set, capture)
	stats.nameText = len(set) - stats.residueText - stats.admitted
	recordTextClassificationResidue(residue, stats.residueText)
	if capture != nil {
		capture.boostWorkers()
		capture.closeInput()
	}
	return set, stats, capture, nil
}

// runRipgrepNulScanFiles runs the definitional full-file NUL scan
// (`--files-without-match --text -e '\x00'`) over explicit relative paths,
// chunked for command-line limits, and returns the subset containing no
// NUL byte. --text forces rg's mode 3 so its own binary heuristics never
// preempt the pattern; with explicit file args the scan is
// mode-independent, exactly like the pre-v0.6.5 Stage 2.
//
// Error tolerance (matching the direct and chunked content-match
// helpers): with explicit file args, rg exits 2 when ANY listed file
// cannot be opened (locked, permission-denied, cloud placeholder) — even
// under --no-messages — while still printing the rows it could classify.
// That must not fail the scan: an unreadable file is simply absent from
// the without-match output and classifies binary, the definitionally
// correct answer ("cannot prove NUL-free"). Only spawn-level failures
// are fatal, with context so nothing surfaces as a bare "exit status 2"
// (live failure 2026-07-04: one unreadable Desktop file killed the run).
func runRipgrepNulScanFiles(workingDir string, relPaths []string) (map[string]struct{}, error) {
	bin, ok := RipgrepBinary()
	if !ok {
		return nil, errRipgrepUnavailable
	}
	out := make(map[string]struct{}, len(relPaths))
	for _, chunk := range chunkExecArgs(relPaths, execPathChunkMaxCount, currentExecPathChunkByteLimit()) {
		args := []string{
			"--files-without-match",
			"--text",
			"--no-messages",
			"-0",
			"-e", `\x00`,
			"--",
		}
		args = append(args, chunk...)
		cmd := exec.CommandContext(reloadCancelCtx, bin, args...)
		cmd.Dir = workingDir
		t0 := time.Now()
		o, err := cmd.Output()
		if benchEnabled() {
			benchRgTextTotal.Add(int64(time.Since(t0)))
			benchRgTextCalls.Add(1)
		}
		if err != nil {
			if _, isExit := err.(*exec.ExitError); !isExit {
				return nil, fmt.Errorf("text classification: NUL scan of %d residue file(s) under %q failed to run: %w", len(chunk), workingDir, err)
			}
			// Exit 1: every file in the chunk matched \x00 (all binary) —
			// output is empty. Exit 2: some listed file was unreadable —
			// output still carries the rows rg could classify; absent
			// rows classify binary. Fall through and parse whatever
			// stdout was produced.
		}
		for _, rel := range splitNullSeparated(o) {
			rel = normalizeRelPath(rel)
			if rel == "" || rel == "." {
				continue
			}
			out[rel] = struct{}{}
		}
	}
	return out, nil
}

// textResidue accumulates the residue (name-undecidable, content-scanned)
// paths across this process's text-set builds, for --verbose reporting.
// This is also the list-growth feedback loop: an extension recurring here
// is a candidate for known_files.go, with evidence attached.
var (
	textResidueMu    sync.Mutex
	textResiduePaths []string
	textResidueSeen  map[string]struct{}
	textResidueText  int
)

func recordTextClassificationResidue(paths []string, textCount int) {
	if len(paths) == 0 {
		return
	}
	textResidueMu.Lock()
	defer textResidueMu.Unlock()
	if textResidueSeen == nil {
		textResidueSeen = make(map[string]struct{}, len(paths))
	}
	for _, p := range paths {
		if _, dup := textResidueSeen[p]; dup {
			continue
		}
		textResidueSeen[p] = struct{}{}
		textResiduePaths = append(textResiduePaths, p)
	}
	textResidueText += textCount
}

// TextClassificationResidue returns the residue paths content-scanned by
// this process's text-set builds (cache misses only — cached sets record
// nothing) and how many of them classified text. Root prints this under
// --verbose.
func TextClassificationResidue() (paths []string, textCount int) {
	textResidueMu.Lock()
	defer textResidueMu.Unlock()
	return append([]string(nil), textResiduePaths...), textResidueText
}

// admitEmptyFilesToTextSet re-admits 0-byte regular files still absent
// from the text set after the name pass and residue scan — i.e. empties
// whose NAME said binary (an empty .png). Empty files contain no NUL
// byte, so they are text by the rule-11 definition regardless of name,
// matching the prior full-scan behavior. Lstat-only: metadata, not
// content classification, so rg remains the sole content classifier.
// Symlinks are excluded by policy (discovery doesn't emit symlink
// entries). Individual Lstat failures skip the file (it stays binary).
//
// allPaths is the already-enumerated --no-ignore universe, so empty files in
// blocked subtrees are considered too without a second rg walk.
func admitEmptyFilesToTextSet(workingDir string, allPaths []string, set map[string]struct{}, capture *TextSizeCapture) (statCount, admittedCount int) {
	for _, rel := range allPaths {
		if _, ok := set[rel]; ok {
			continue
		}
		statCount++
		abs := filepath.Join(workingDir, filepath.FromSlash(rel))
		info, statErr := os.Lstat(abs)
		if statErr != nil {
			continue
		}
		if info.Size() == 0 && info.Mode().IsRegular() {
			set[rel] = struct{}{}
			if capture != nil {
				capture.record(rel, info)
			}
			admittedCount++
		}
	}
	return statCount, admittedCount
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
// Pass nil/empty targets for the project-wide set; pass a non-empty list to
// scope the enumeration to those paths only (Slice 1 narrowing). Pin #1/#2
// (v0.6.7) empirically confirmed that rg, run from workingDir with the target
// as a positional path and catclip's flag mix, still applies ancestor
// .gitignore rules (via its add_parents traversal) and anchors --ignore-file
// (.hiss) patterns at workingDir, so positional narrowing yields the same
// visible set for the target subtree with no Go-side filter. The one flag
// catclip must never add here is --no-ignore-parent, which disables that
// ancestor traversal. Mirrors ResolveTextFileSet's narrowing; globs, "."/root,
// and missing targets fall back to project-wide via scopedCacheTargets.
func ResolveVisibleFileSet(workingDir, hissPath string, targets []string, enumeration ...MembershipEnumerationContext) (map[string]struct{}, error) {
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
	normTargets := scopedCacheTargets(workingDir, targets)
	cacheKey := dirKey + "\x00" + hissKey + "\x00" + joinScopedCacheTargets(normTargets)

	visibleFileSetCacheMu.Lock()
	if cached, ok := visibleFileSetCache[cacheKey]; ok {
		visibleFileSetCacheMu.Unlock()
		return cached, nil
	}
	visibleFileSetCacheMu.Unlock()

	context := membershipContextOrDefault(enumeration, MembershipReasonIgnoreAttribution)
	set, err := runRipgrepVisibleFiles(workingDir, hissPath, normTargets, context)
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
func runRipgrepVisibleFiles(workingDir, hissPath string, targets []string, enumeration MembershipEnumerationContext) (set map[string]struct{}, retErr error) {
	ctx := reloadCancelCtx
	var membershipSpan *membershipEnumerationSpan
	defer func() { membershipSpan.finish(len(set), scanWasCancelled(ctx, retErr), retErr) }()
	finishBench := platform.InternalBenchSpan("search.rg.visible_files",
		"has_hiss", strconv.FormatBool(strings.TrimSpace(hissPath) != ""),
		"targets", platform.InternalBenchInt(len(targets)),
	)
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return nil, errRipgrepUnavailable
	}
	membershipSpan = beginMembershipEnumeration(MembershipEnumerationVisibleSet, MembershipVisible, enumeration)

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
	// Positional narrowing: rg still applies ancestor .gitignore and .hiss
	// anchored at workingDir (Pin #1/#2). Never add --no-ignore-parent.
	if len(targets) > 0 {
		args = append(args, "--")
		args = append(args, targets...)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
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
	set = make(map[string]struct{}, len(paths))
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
	for _, chunk := range chunkExecArgs(absPaths, execPathChunkMaxCount, currentExecPathChunkByteLimit()) {
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
			exitErr, isExit := err.(*exec.ExitError)
			if !isExit {
				finishBench(
					"err", "true",
					"chunks", platform.InternalBenchInt(chunks),
					"matches", platform.InternalBenchInt(len(matches)),
				)
				return nil, err
			}
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
				// Exit 2 without a pattern error: unreadable/vanished
				// listed file; parse the chunk's partial output instead
				// of failing (2026-07-04 tolerance).
			default:
				// Includes -1 (reload cancellation kill) — must keep
				// propagating so ReloadWasCancelled handling works.
				finishBench(
					"err", "true",
					"chunks", platform.InternalBenchInt(chunks),
					"matches", platform.InternalBenchInt(len(matches)),
				)
				return nil, err
			}
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
	for _, chunk := range chunkExecArgs(absPaths, execPathChunkMaxCount, currentExecPathChunkByteLimit()) {
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
			exitErr, isExit := err.(*exec.ExitError)
			if !isExit {
				finishBench(
					"err", "true",
					"chunks", platform.InternalBenchInt(chunks),
					"matches", platform.InternalBenchInt(len(out)),
				)
				return nil, err
			}
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
				// Exit 2 without a pattern error: unreadable/vanished
				// listed file; parse the chunk's partial output instead
				// of failing (2026-07-04 tolerance).
			default:
				// Includes -1 (reload cancellation kill) — must keep
				// propagating so ReloadWasCancelled handling works.
				finishBench(
					"err", "true",
					"chunks", platform.InternalBenchInt(chunks),
					"matches", platform.InternalBenchInt(len(out)),
				)
				return nil, err
			}
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

func RunRipgrepMatches(pattern string, absPaths []string, invert ...bool) (map[string]struct{}, error) {
	inv := len(invert) > 0 && invert[0]
	spanName := "search.rg.matches"
	if inv {
		spanName = "search.rg.not_matches"
	}
	finishBench := platform.InternalBenchSpan(spanName,
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
	for _, chunk := range chunkExecArgs(absPaths, execPathChunkMaxCount, currentExecPathChunkByteLimit()) {
		chunks++
		args := []string{
			"--color=never",
			"--no-messages",
		}
		if inv {
			args = append(args, "--files-without-match")
		} else {
			args = append(args, "--files-with-matches")
		}
		args = append(args,
			"--pcre2",
			"-0",
		)
		if !inv {
			args = append(args, "-m", "1")
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
			exitErr, isExit := err.(*exec.ExitError)
			if !isExit {
				finishBench(
					"err", "true",
					"chunks", platform.InternalBenchInt(chunks),
					"matches", platform.InternalBenchInt(len(matches)),
				)
				return nil, err
			}
			switch exitErr.ExitCode() {
			case 1:
				// invert=false: no files matched in chunk → skip.
				// invert=true:  every file matched → no "without-match"
				//   rows to emit → skip.
				// Both cases: continue to next chunk.
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
				// Exit 2 without a pattern error: an explicitly listed
				// file was unreadable or vanished mid-session; rg still
				// printed rows for the rest of the chunk. Fall through
				// and parse the partial output — unreadable files are
				// simply absent (2026-07-04 tolerance, matching the
				// direct helpers).
			default:
				// Includes -1 (reload cancellation kill) — must keep
				// propagating so ReloadWasCancelled handling works.
				finishBench(
					"err", "true",
					"chunks", platform.InternalBenchInt(chunks),
					"matches", platform.InternalBenchInt(len(matches)),
				)
				return nil, err
			}
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

// DirectOption configures one of the boolean knobs on the direct rg
// helpers (RunRipgrepDirect, RunRipgrepDirectMatchLines). Use
// DirectInvert() for --files-without-match (--not-contains direct
// mode) and DirectNoIgnore() to mirror a parent --no-ignore scope.
type DirectOption func(*directOptions)

type directOptions struct {
	invert   bool
	noIgnore bool
}

func scanWasCancelled(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// DirectInvert switches from --files-with-matches to --files-without-match.
func DirectInvert() DirectOption { return func(o *directOptions) { o.invert = true } }

// DirectNoIgnore appends --no-ignore so rg walks gitignored paths.
// --ignore-file (.hiss) still applies.
func DirectNoIgnore() DirectOption { return func(o *directOptions) { o.noIgnore = true } }

func directOptionsFrom(opts []DirectOption) directOptions {
	var cfg directOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// RunRipgrepDirect is the scope-rooted sibling of RunRipgrepMatches:
// one rg call that walks `target` (relative to workingDir) and applies
// the regex in one pass. Returns the set of matched files (relative
// paths) using the same map[string]struct{} shape as RunRipgrepMatches.
//
// Options: DirectInvert switches to --files-without-match (drops -m 1).
// DirectNoIgnore appends --no-ignore for scopes that disabled ignore rules.
//
// Eligibility: callers must verify the scope qualifies via
// command.IsDirectModeEligible. This helper does not check eligibility
// itself; passing a scope with filters rg can't natively express
// silently produces a wrong (broader) set.
//
// Smart-case: catclip's content match is smart-case by default
// (all-lowercase pattern → case-insensitive). This helper passes
// --ignore-case when isSmartCaseInsensitive(pattern) is true, matching
// RunRipgrepMatches.
//
// Path normalization: rg emits "./path" when the scope target is "."
// and "src/path" when the target is "src". This helper passes results
// through normalizeRelPath so callers always see catclip-canonical
// relative paths.
//
// Bad pattern: rg exit 2 + stderr matching PCRE2 markers →
// ErrRipgrepBadPattern. rg "no matches" exit 1 → empty map, no error.
//
// Production status (v0.6.5 dead-code sweep): no production caller yet —
// the shipped direct paths use RunRipgrepDirectMatchLines only. This
// function and DirectInvert are retained deliberately as the executable
// spec for direct-mode file-set semantics (parity/invert/smart-case
// contracts in ripgrep_direct_test.go), pending the --contains /
// --not-contains direct-mode wiring. If that wiring is abandoned, delete
// this together with DirectInvert and re-anchor the shared contract
// tests on RunRipgrepDirectMatchLines.
func RunRipgrepDirect(workingDir, target, pattern, hissPath string, opts ...DirectOption) (map[string]struct{}, error) {
	cfg := directOptionsFrom(opts)
	spanName := "search.rg.direct"
	if cfg.invert {
		spanName = "search.rg.direct_not_matches"
	}
	finishBench := platform.InternalBenchSpan(spanName,
		"scan_class", "content-root",
		"membership_authority", "false",
		"target", target,
		"pattern_len", platform.InternalBenchInt(len(pattern)),
	)
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return nil, errRipgrepUnavailable
	}

	args := []string{}
	if cfg.invert {
		args = append(args, "--files-without-match")
	} else {
		args = append(args, "--files-with-matches")
	}
	args = append(args,
		"--pcre2",
		"--hidden",
		"--no-ignore-dot",
		"--no-require-git",
		"--no-messages",
		"-0",
	)
	if !cfg.invert {
		args = append(args, "-m", "1")
	}
	if cfg.noIgnore {
		// Picker subprocesses inherit --no-ignore through the checkpoint.
		// --ignore-file still applies, so .hiss continues to filter.
		args = append(args, "--no-ignore")
	}
	if hissPath != "" {
		args = append(args, "--ignore-file", hissPath)
	}
	if isSmartCaseInsensitive(pattern) {
		args = append(args, "--ignore-case")
	}
	args = append(args, "-e", pattern, "--", target)

	cmd := exec.CommandContext(reloadCancelCtx, bin, args...)
	cmd.Dir = workingDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	t0 := time.Now()
	out, err := cmd.Output()
	if benchEnabled() {
		benchRgMatchesTotal.Add(int64(time.Since(t0)))
		benchRgMatchesCalls.Add(1)
	}
	if err != nil {
		exitErr, isExit := err.(*exec.ExitError)
		if !isExit {
			finishBench("err", "true")
			return nil, fmt.Errorf("rg direct scan of %q under %q failed to run: %w", target, workingDir, err)
		}
		switch exitErr.ExitCode() {
		case 1:
			finishBench("err", "false", "matches", "0")
			return map[string]struct{}{}, nil
		case 2:
			if isRipgrepBadPatternStderr(stderr.Bytes()) {
				finishBench("err", "true", "bad_pattern", "true")
				return nil, ErrRipgrepBadPattern
			}
			// Exit 2 without a pattern error: rg hit unreadable files
			// mid-walk (locked, permission-denied, cloud placeholder)
			// but still printed rows for everything it could read. Fall
			// through and parse the partial output — a picker keystroke
			// must not die because one file was unreadable. Unreadable
			// files are simply absent from the output.
		default:
			// Includes -1 (killed by reload cancellation) — that error
			// must keep propagating so ReloadWasCancelled handling works.
			finishBench("err", "true")
			return nil, err
		}
	}

	matches := make(map[string]struct{})
	for _, match := range splitNullSeparated(out) {
		rel := normalizeRelPath(match)
		if rel == "" || rel == "." {
			continue
		}
		matches[rel] = struct{}{}
	}
	finishBench("err", "false", "matches", platform.InternalBenchInt(len(matches)))
	return matches, nil
}

// RunRipgrepDirectMatchLines is the scope-rooted sibling of
// FirstMatchLinePerFile. One rg call walks `target` and returns
// {relPath → first-match line number} in one pass.
//
// Same eligibility, smart-case, normalization, and error semantics as
// RunRipgrepDirect. Output parsing mirrors FirstMatchLinePerFile's
// NUL-delimited line-number format: `{path}\0{lineno}:{matched line}\n`.
func RunRipgrepDirectMatchLines(workingDir, target, pattern, hissPath string, opts ...DirectOption) (map[string]int, error) {
	cfg := directOptionsFrom(opts)
	finishBench := platform.InternalBenchSpan("search.rg.direct_match_lines",
		"scan_class", "content-root",
		"membership_authority", "false",
		"target", target,
		"pattern_len", platform.InternalBenchInt(len(pattern)),
	)
	bin, ok := RipgrepBinary()
	if !ok {
		finishBench("err", "true")
		return nil, errRipgrepUnavailable
	}

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
		"--hidden",
		"--no-ignore-dot",
		"--no-require-git",
	}
	if cfg.noIgnore {
		args = append(args, "--no-ignore")
	}
	if hissPath != "" {
		args = append(args, "--ignore-file", hissPath)
	}
	if isSmartCaseInsensitive(pattern) {
		args = append(args, "--ignore-case")
	}
	args = append(args, "-e", pattern, "--", target)

	cmd := exec.CommandContext(reloadCancelCtx, bin, args...)
	cmd.Dir = workingDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	t0 := time.Now()
	out, err := cmd.Output()
	if benchEnabled() {
		benchRgMatchesTotal.Add(int64(time.Since(t0)))
		benchRgMatchesCalls.Add(1)
	}
	if err != nil {
		exitErr, isExit := err.(*exec.ExitError)
		if !isExit {
			finishBench("err", "true")
			return nil, fmt.Errorf("rg direct match-lines scan of %q under %q failed to run: %w", target, workingDir, err)
		}
		switch exitErr.ExitCode() {
		case 1:
			finishBench("err", "false", "matches", "0")
			return map[string]int{}, nil
		case 2:
			if isRipgrepBadPatternStderr(stderr.Bytes()) {
				finishBench("err", "true", "bad_pattern", "true")
				return nil, ErrRipgrepBadPattern
			}
			// Exit 2 without a pattern error: unreadable files mid-walk;
			// rg still printed rows for everything it could read. Fall
			// through and parse the partial output rather than killing
			// the picker keystroke. Unreadable files are simply absent.
		default:
			// Includes -1 (killed by reload cancellation) — must keep
			// propagating so ReloadWasCancelled handling works.
			finishBench("err", "true")
			return nil, err
		}
	}

	matches := make(map[string]int)
	for _, rec := range bytes.Split(out, []byte{'\n'}) {
		if len(rec) == 0 {
			continue
		}
		nul := bytes.IndexByte(rec, 0)
		if nul < 0 {
			continue
		}
		path := normalizeRelPath(string(rec[:nul]))
		if path == "" || path == "." {
			continue
		}
		rest := rec[nul+1:]
		colon := bytes.IndexByte(rest, ':')
		if colon < 0 {
			continue
		}
		line, err := strconv.Atoi(string(rest[:colon]))
		if err != nil || line < 1 {
			continue
		}
		if _, dup := matches[path]; dup {
			continue
		}
		matches[path] = line
	}
	finishBench("err", "false", "matches", platform.InternalBenchInt(len(matches)))
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
	for _, chunk := range chunkExecArgs(absPaths, execPathChunkMaxCount, currentExecPathChunkByteLimit()) {
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
		chunk, next := sizedLineCountCandidateChunk(candidates, start, execPathChunkMaxCount, currentExecPathChunkByteLimit())
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
