package search

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
)

// TestRunRipgrepDirectMatchesChunkedPath asserts that for direct-mode
// eligible patterns, RunRipgrepDirect returns the same file set as
// RunRipgrepMatches over the same files. Covers the smart-case engagement
// case that the pre-impl parity testing identified as the critical bug
// the v0.6.3 plan had to guard against.
func TestRunRipgrepDirectMatchesChunkedPath(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go":     "package main\n// TODO one\n",
		"b.go":     "package main\n// Function comment\n",
		"sub/c.go": "package sub\n// function two\n",
		"sub/d.md": "TODO docs\n",
		"empty.go": "",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := mkdir(filepath.Dir(full)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(full, content); err != nil {
			t.Fatal(err)
		}
	}

	absPaths := []string{}
	for rel := range files {
		absPaths = append(absPaths, filepath.Join(dir, rel))
	}
	sort.Strings(absPaths)

	tests := []struct {
		name    string
		pattern string
	}{
		{"all_lowercase_smart_case", "function"},
		{"mixed_case_sensitive", "Function"},
		{"all_uppercase_sensitive", "TODO"},
		{"all_lowercase_match_anywhere", "todo"},
		{"regex_no_matches", "XYZ_NEVER_HITS"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunked, err := RunRipgrepMatches(tc.pattern, absPaths)
			if err != nil {
				t.Fatalf("RunRipgrepMatches: %v", err)
			}
			direct, err := RunRipgrepDirect(dir, ".", tc.pattern, "")
			if err != nil {
				t.Fatalf("RunRipgrepDirect: %v", err)
			}

			chunkedRel := map[string]struct{}{}
			for absPath := range chunked {
				rel, relErr := filepath.Rel(dir, absPath)
				if relErr != nil {
					t.Fatalf("filepath.Rel: %v", relErr)
				}
				chunkedRel[filepath.ToSlash(rel)] = struct{}{}
			}
			directNorm := map[string]struct{}{}
			for rel := range direct {
				directNorm[filepath.ToSlash(rel)] = struct{}{}
			}
			if !reflect.DeepEqual(chunkedRel, directNorm) {
				t.Fatalf("file sets differ for %q\nchunked: %v\ndirect:  %v", tc.pattern, sortedKeys(chunkedRel), sortedKeys(directNorm))
			}
		})
	}
}

// TestRunRipgrepDirectMatchLinesMatchesChunkedPath asserts byte-identical
// line-number parity between RunRipgrepDirectMatchLines and the chunked
// FirstMatchLinePerFile. Line-number parity is critical because the
// picker uses the line number to set fzf's preview offset; wrong numbers
// would render the wrong snippet in the preview pane.
func TestRunRipgrepDirectMatchLinesMatchesChunkedPath(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go":     "package main\n// TODO one\n// TODO two\n",
		"b.go":     "package main\nfunc f() {}\n// TODO trailing\n",
		"sub/c.go": "package sub\n// not interesting\n// TODO here\n",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := mkdir(filepath.Dir(full)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(full, content); err != nil {
			t.Fatal(err)
		}
	}

	absPaths := []string{
		filepath.Join(dir, "a.go"),
		filepath.Join(dir, "b.go"),
		filepath.Join(dir, "sub/c.go"),
	}

	chunked, err := FirstMatchLinePerFile("TODO", absPaths)
	if err != nil {
		t.Fatalf("FirstMatchLinePerFile: %v", err)
	}
	direct, err := RunRipgrepDirectMatchLines(dir, ".", "TODO", "")
	if err != nil {
		t.Fatalf("RunRipgrepDirectMatchLines: %v", err)
	}

	chunkedRel := map[string]int{}
	for absPath, line := range chunked {
		rel, relErr := filepath.Rel(dir, absPath)
		if relErr != nil {
			t.Fatalf("filepath.Rel: %v", relErr)
		}
		chunkedRel[filepath.ToSlash(rel)] = line
	}
	directNorm := map[string]int{}
	for rel, line := range direct {
		directNorm[filepath.ToSlash(rel)] = line
	}
	if !reflect.DeepEqual(chunkedRel, directNorm) {
		t.Fatalf("line-number maps differ\nchunked: %v\ndirect:  %v", chunkedRel, directNorm)
	}
}

// TestRunRipgrepDirectBadPattern surfaces ErrRipgrepBadPattern for
// patterns rg can't compile, matching the chunked path's contract.
func TestRunRipgrepDirectBadPattern(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "f.go"), "package x\n"); err != nil {
		t.Fatal(err)
	}
	_, err := RunRipgrepDirect(dir, ".", "(", "")
	if err != ErrRipgrepBadPattern {
		t.Fatalf("RunRipgrepDirect with bad pattern: got %v, want ErrRipgrepBadPattern", err)
	}
}

// TestRunRipgrepDirectMatchLinesBadPattern mirrors the bad-pattern
// assertion for the snippet flow.
func TestRunRipgrepDirectMatchLinesBadPattern(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "f.go"), "package x\n"); err != nil {
		t.Fatal(err)
	}
	_, err := RunRipgrepDirectMatchLines(dir, ".", "(", "")
	if err != ErrRipgrepBadPattern {
		t.Fatalf("RunRipgrepDirectMatchLines with bad pattern: got %v, want ErrRipgrepBadPattern", err)
	}
}

// TestRunRipgrepDirectNoIgnoreSurfacesGitignored asserts that DirectNoIgnore
// causes rg to walk paths a .gitignore would otherwise hide. Mirrors the
// picker-include flow: parent had --include, so the picker subprocess sets
// CheckpointData.NoIgnore=true and direct rg follows suit.
func TestRunRipgrepDirectNoIgnoreSurfacesGitignored(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, ".gitignore"), "ignored/\n"); err != nil {
		t.Fatal(err)
	}
	if err := osMkdirAll(filepath.Join(dir, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "ignored", "hit.go"), "// TODO inside ignored\n"); err != nil {
		t.Fatal(err)
	}

	defaultMatches, err := RunRipgrepDirect(dir, ".", "TODO", "")
	if err != nil {
		t.Fatalf("RunRipgrepDirect default: %v", err)
	}
	if _, ok := defaultMatches["ignored/hit.go"]; ok {
		t.Fatalf("default mode should respect .gitignore; got %v", defaultMatches)
	}

	noIgnoreMatches, err := RunRipgrepDirect(dir, ".", "TODO", "", DirectNoIgnore())
	if err != nil {
		t.Fatalf("RunRipgrepDirect(DirectNoIgnore): %v", err)
	}
	if _, ok := noIgnoreMatches["ignored/hit.go"]; !ok {
		t.Fatalf("DirectNoIgnore should surface gitignored matches; got %v", noIgnoreMatches)
	}
}

// TestRunRipgrepDirectNoMatchReturnsEmpty asserts rg exit-1 (no match)
// surfaces as an empty map with no error, matching the chunked path.
func TestRunRipgrepDirectNoMatchReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "f.go"), "package x\n"); err != nil {
		t.Fatal(err)
	}
	matches, err := RunRipgrepDirect(dir, ".", "XYZ_NEVER_HITS", "")
	if err != nil {
		t.Fatalf("RunRipgrepDirect: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("no-match should return empty map, got %v", matches)
	}
}

// TestRunRipgrepDirectStripsLeadingDotSlash ensures direct mode's path
// normalization handles the "./" prefix rg emits when the scope target
// is ".". Catclip downstream expects unprefixed relative paths.
func TestRunRipgrepDirectStripsLeadingDotSlash(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "f.go"), "// TODO here\n"); err != nil {
		t.Fatal(err)
	}
	matches, err := RunRipgrepDirect(dir, ".", "TODO", "")
	if err != nil {
		t.Fatalf("RunRipgrepDirect: %v", err)
	}
	if _, ok := matches["f.go"]; !ok {
		t.Fatalf("expected unprefixed 'f.go' in matches, got %v", matches)
	}
	if _, ok := matches["./f.go"]; ok {
		t.Fatalf("unexpected './'-prefixed key in matches: %v", matches)
	}
}

// TestRunRipgrepMatchesInvertIsInverse asserts that for any pattern P,
// the union (matches(P) ∪ matches(P, invert=true)) covers the full
// input absPaths set when every file has at least one line (so rg's
// binary detection doesn't trim either side). This is the fundamental
// contract --not-contains relies on.
func TestRunRipgrepMatchesInvertIsInverse(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go":  "// TODO one\n",
		"b.go":  "// no match\n",
		"c.go":  "// TODO three\n",
		"d.md":  "TODO docs\n",
		"e.txt": "plain text\n",
		"f.py":  "# nothing here\n",
	}
	for rel, content := range files {
		if err := writeFile(filepath.Join(dir, rel), content); err != nil {
			t.Fatal(err)
		}
	}
	absPaths := []string{}
	for rel := range files {
		absPaths = append(absPaths, filepath.Join(dir, rel))
	}
	sort.Strings(absPaths)

	matched, err := RunRipgrepMatches("TODO", absPaths)
	if err != nil {
		t.Fatalf("RunRipgrepMatches: %v", err)
	}
	notMatched, err := RunRipgrepMatches("TODO", absPaths, true)
	if err != nil {
		t.Fatalf("RunRipgrepMatches(invert): %v", err)
	}

	// Disjoint: no overlap.
	for path := range matched {
		if _, dup := notMatched[path]; dup {
			t.Fatalf("path %q in both matched and notMatched sets", path)
		}
	}
	// Cover: matched ∪ notMatched covers every input file (all are
	// text in this fixture; no rg binary trimming).
	got := len(matched) + len(notMatched)
	if got != len(absPaths) {
		t.Fatalf("matched+notMatched=%d, want %d (matched=%d notMatched=%d)",
			got, len(absPaths), len(matched), len(notMatched))
	}
}

// TestRunRipgrepDirectInvertIsInverse mirrors the above for the
// direct path (single rg call per side).
func TestRunRipgrepDirectInvertIsInverse(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go":     "// TODO one\n",
		"b.go":     "// no match\n",
		"sub/c.go": "// TODO three\n",
		"sub/d.md": "TODO docs\n",
		"e.txt":    "plain text\n",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := mkdir(filepath.Dir(full)); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(full, content); err != nil {
			t.Fatal(err)
		}
	}

	matched, err := RunRipgrepDirect(dir, ".", "TODO", "")
	if err != nil {
		t.Fatalf("RunRipgrepDirect: %v", err)
	}
	notMatched, err := RunRipgrepDirect(dir, ".", "TODO", "", DirectInvert())
	if err != nil {
		t.Fatalf("RunRipgrepDirect(invert): %v", err)
	}

	for path := range matched {
		if _, dup := notMatched[path]; dup {
			t.Fatalf("path %q in both matched and notMatched sets", path)
		}
	}
	got := len(matched) + len(notMatched)
	if got != len(files) {
		t.Fatalf("matched+notMatched=%d, want %d (matched=%d notMatched=%d)",
			got, len(files), len(matched), len(notMatched))
	}
}

func TestRunRipgrepDirectInvertBadPattern(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "f.go"), "package x\n"); err != nil {
		t.Fatal(err)
	}
	_, err := RunRipgrepDirect(dir, ".", "(", "", DirectInvert())
	if err != ErrRipgrepBadPattern {
		t.Fatalf("RunRipgrepDirect(invert) with bad pattern: got %v, want ErrRipgrepBadPattern", err)
	}
}

func TestRunRipgrepMatchesInvertBadPattern(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "f.go"), "package x\n"); err != nil {
		t.Fatal(err)
	}
	_, err := RunRipgrepMatches("(", []string{filepath.Join(dir, "f.go")}, true)
	if err != ErrRipgrepBadPattern {
		t.Fatalf("RunRipgrepMatches(invert) with bad pattern: got %v, want ErrRipgrepBadPattern", err)
	}
}

// TestRunRipgrepMatchesInvertSmartCaseEngagement asserts the
// inverse relationship holds when smart-case engages (all-lowercase
// pattern → case-insensitive). The critical pre-impl finding for
// direct-rg was missing --smart-case; this test guards --not-contains
// against the same regression.
func TestRunRipgrepMatchesInvertSmartCaseEngagement(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go": "func Hello()\n", // matches "func" both case-sensitive and -insensitive
		"b.go": "Func Hello()\n", // matches "func" only when smart-case
		"c.go": "type X int\n",   // never matches "func"
	}
	for rel, content := range files {
		if err := writeFile(filepath.Join(dir, rel), content); err != nil {
			t.Fatal(err)
		}
	}
	absPaths := []string{}
	for rel := range files {
		absPaths = append(absPaths, filepath.Join(dir, rel))
	}

	matched, err := RunRipgrepMatches("func", absPaths)
	if err != nil {
		t.Fatalf("RunRipgrepMatches: %v", err)
	}
	notMatched, err := RunRipgrepMatches("func", absPaths, true)
	if err != nil {
		t.Fatalf("RunRipgrepMatches(invert): %v", err)
	}

	// All-lowercase "func" → smart-case engages → "Func" in b.go matches too.
	if len(matched) != 2 {
		t.Fatalf("matched=%d, want 2 (smart-case should catch 'Func')", len(matched))
	}
	if len(notMatched) != 1 {
		t.Fatalf("notMatched=%d, want 1 (only c.go has no 'func' under smart-case)", len(notMatched))
	}
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestRunRipgrepDirectToleratesUnreadableFiles pins the exit-2 tolerance
// added 2026-07-04: rg exits 2 when the walk hits an unreadable file
// (locked, permission-denied, cloud placeholder) even under
// --no-messages, while still printing rows for everything it could
// read. A picker keystroke must not die for that — unreadable files are
// simply absent from the result.
func TestRunRipgrepDirectToleratesUnreadableFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 does not block reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "readable.go"), "// TODO here\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "locked.go"), "// TODO locked\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "locked.go"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(dir, "locked.go"), 0o644)

	matches, err := RunRipgrepDirect(dir, ".", "TODO", "")
	if err != nil {
		t.Fatalf("expected partial results despite unreadable file, got error: %v", err)
	}
	if _, ok := matches["readable.go"]; !ok {
		t.Fatalf("expected readable.go in matches, got %v", matches)
	}
	if _, ok := matches["locked.go"]; ok {
		t.Fatalf("unreadable locked.go must be absent from matches, got %v", matches)
	}

	lines, err := RunRipgrepDirectMatchLines(dir, ".", "TODO", "")
	if err != nil {
		t.Fatalf("expected partial match-lines despite unreadable file, got error: %v", err)
	}
	if lines["readable.go"] != 1 {
		t.Fatalf("expected readable.go line 1, got %v", lines)
	}
	if _, ok := lines["locked.go"]; ok {
		t.Fatalf("unreadable locked.go must be absent from match-lines, got %v", lines)
	}
}
