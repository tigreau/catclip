package render

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/output"
)

var benchFilterPalette = Palette{Reset: "\x1b[0m", Bold: "\x1b[1m", Tree: "\x1b[37m"}

func TestFilterPreviewCombinedParity(t *testing.T) {
	for _, name := range []string{"test.go", "test.py", ".gitignore", "test.gd"} {
		for _, body := range []string{"", "\n", "func hello() {}\n\n// TODO café\n", "class Hello:\r\n    pass\r\n", "αβ todo\rx\n", "\x1b[0mTODO\n"} {
			for _, pattern := range []string{"", "func|todo", "TODO", "^", "$", "x*", "[", "(?=todo)", "αβ", "no-match"} {
				f := &FilePreview{Path: name, Content: body, MatchPattern: pattern, FocusLines: []int{-1, 1, 3, 100}}
				for _, cap := range []int{0, 2} {
					opts := RenderOptions{PreviewTheme: "fzf-dark", MaxLines: cap}
					var a, b bytes.Buffer
					if err := renderFilePreview(&a, f, opts, benchFilterPalette); err != nil {
						t.Fatal(err)
					}
					if err := benchCombinedFilePreview(&b, f, opts, benchFilterPalette); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(a.Bytes(), b.Bytes()) {
						t.Fatalf("parity: name=%s body=%q pattern=%q cap=%d", name, body, pattern, cap)
					}
				}
			}
		}
	}
}

func TestFilterPreviewEarlyCapMultilineDiagnostic(t *testing.T) {
	opts := RenderOptions{PreviewTheme: "fzf-dark", MaxLines: 200}
	for _, tc := range []struct{ path, start, end string }{
		{"example.py", "text = \"\"\"\n", "\"\"\"\n"},
		{"example.go", "package main\nvar text = `\n", "`\n"},
		{"example.c", "/*\n", "*/\n"},
		{"example.js", "const text = `\n", "`;\n"},
	} {
		f := &FilePreview{Path: tc.path, Content: tc.start + strings.Repeat("hello\n", 220) + tc.end, MatchPattern: "hello"}
		var late, early bytes.Buffer
		if err := renderFilePreview(&late, f, opts, benchFilterPalette); err != nil {
			t.Fatal(err)
		}
		if err := benchEarlyCapPreview(&early, f, opts, benchFilterPalette); err != nil {
			t.Fatal(err)
		}
		t.Logf("multiline=%s ANSI_parity=%t", tc.path, bytes.Equal(late.Bytes(), early.Bytes()))
	}
}

func BenchmarkFilterPreviewMatchPasses(b *testing.B) {
	for _, n := range []int{200, 20000} {
		body := strings.Repeat("func example() { return value } // TODO café\n", n)
		raw := splitPreviewLines(body)
		colored := splitPreviewLines(highlightFilePreview("test.go", body, RenderOptions{PreviewTheme: "fzf-dark"}))
		for _, pattern := range []string{"func|return|TODO", "not_present", ""} {
			for _, combined := range []bool{false, true} {
				b.Run(fmt.Sprintf("lines=%d/pattern=%s/combined=%t", n, pattern, combined), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						lines := slices.Clone(colored)
						if combined {
							_, _ = benchCombinedMatches(lines, raw, pattern, nil)
						} else {
							_ = overlayPreviewMatchHighlights(lines, raw, pattern)
							_ = previewHighlightedLineNumbers(raw, pattern, nil)
						}
					}
				})
			}
		}
	}
}

// Inventories the entire corpus, then samples size quantiles per language.
// Timings isolate warm renderer work: not rg, body I/O, snippet resolution,
// fzf, process startup, discovery, or a full-corpus content search.
func TestFilterPreviewCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full-inventory/sample-render benchmark")
	}
	root := os.Getenv("CATCLIP_TEST_CORPUS")
	if root == "" {
		root = "/Users/chris/Desktop/catclip-test-data"
	}
	rg := os.Getenv("CATCLIP_RG")
	if rg == "" {
		rg = "rg"
	}
	cmd := exec.Command(rg, "--files", "--hidden", "--no-ignore", "-0")
	cmd.Dir = root
	listed, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	type candidate struct {
		path string
		size int64
	}
	groups := map[string][]candidate{}
	keys := []string{".go", ".js", ".ts", ".py", ".gd", ".c", ".h", ".gitignore", ".clang-format"}
	count, eligible := 0, 0
	for _, p := range bytes.Split(listed, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		count++
		path := string(p)
		key := filepath.Ext(path)
		if slices.Contains(keys, filepath.Base(path)) {
			key = filepath.Base(path)
		}
		if !slices.Contains(keys, key) {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, path))
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 256*1024 {
			continue
		}
		groups[key] = append(groups[key], candidate{path, info.Size()})
		eligible++
	}
	t.Logf("inventory=%d eligible_1_to_262144_bytes=%d languages=%d", count, eligible, len(groups))
	type sample struct {
		mode string
		file *FilePreview
	}
	var samples []sample
	pattern := `func|def|class|return|TODO`
	compiledPattern := pattern
	if isSmartCaseInsensitive(pattern) {
		compiledPattern = "(?i)" + pattern
	}
	re := regexp.MustCompile(compiledPattern)
	files, totalBytes := 0, 0
	for _, key := range keys {
		group := groups[key]
		slices.SortFunc(group, func(a, b candidate) int {
			if a.size < b.size {
				return -1
			}
			if a.size > b.size {
				return 1
			}
			return strings.Compare(a.path, b.path)
		})
		if len(group) == 0 {
			continue
		}
		seen := map[string]bool{}
		for _, q := range []int{50, 99, 100} {
			c := group[(len(group)-1)*q/100]
			if seen[c.path] {
				continue
			}
			seen[c.path] = true
			snap, err := output.LoadTextSnapshot(filepath.Join(root, c.path), c.path)
			if err != nil {
				t.Fatal(err)
			}
			if !snap.IsText {
				continue
			}
			body := snap.PreviewText()
			files++
			totalBytes += len(body)
			t.Logf("sample=%q bytes=%d quantile=%d", c.path, len(body), q)
			samples = append(samples, sample{"contains", &FilePreview{Path: c.path, Content: body, MatchPattern: pattern}}, sample{"not-contains", &FilePreview{Path: c.path, Content: body}})
			var matches []int
			for i, line := range snap.SnippetLines() {
				if re.MatchString(line) {
					matches = append(matches, i+1)
				}
			}
			for _, numeric := range []bool{false, true} {
				resolved, err := output.ResolveSnippetFromSnapshot(snap, matches, output.SnippetOptionsFor(numeric, 2))
				if err != nil {
					t.Fatal(err)
				}
				if len(resolved.Ranges) == 0 {
					continue
				}
				var lines []string
				for i, r := range resolved.Ranges {
					if i > 0 {
						lines = append(lines, "")
					}
					lines = append(lines, fmt.Sprintf("[lines %d-%d]", r.Start, r.End))
					lines = append(lines, resolved.Lines[r.Start-1:r.End]...)
				}
				mode := "snippet-smart"
				if numeric {
					mode = "snippet-2"
				}
				samples = append(samples, sample{mode, &FilePreview{Path: c.path, Content: strings.Join(lines, "\n"), MatchPattern: pattern}})
			}
		}
	}
	t.Logf("sampled_files=%d body_bytes=%d preview_cases=%d", files, totalBytes, len(samples))
	opts := RenderOptions{PreviewTheme: "fzf-dark"}
	type variant struct {
		name string
		fn   func(io.Writer, *FilePreview, RenderOptions, Palette) error
	}
	variants := []variant{{"current", renderFilePreview}, {"combined-regex", benchCombinedFilePreview}}
	for _, mode := range []string{"contains", "not-contains", "snippet-smart", "snippet-2"} {
		var selected []*FilePreview
		for _, s := range samples {
			if s.mode == mode {
				selected = append(selected, s.file)
			}
		}
		if len(selected) == 0 {
			continue
		}
		for _, f := range selected {
			var a, b bytes.Buffer
			if err := variants[0].fn(&a, f, opts, benchFilterPalette); err != nil {
				t.Fatal(err)
			}
			if err := variants[1].fn(&b, f, opts, benchFilterPalette); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a.Bytes(), b.Bytes()) {
				t.Fatalf("corpus parity: %s %s", mode, f.Path)
			}
		}
		timings := make([][]float64, len(variants))
		for round := 0; round < 5; round++ {
			for j := range variants {
				v := (j + round) % len(variants)
				start := time.Now()
				for _, f := range selected {
					if err := variants[v].fn(io.Discard, f, opts, benchFilterPalette); err != nil {
						t.Fatal(err)
					}
				}
				timings[v] = append(timings[v], float64(time.Since(start))/float64(time.Millisecond))
			}
		}
		for i, v := range variants {
			slices.Sort(timings[i])
			t.Logf("mode=%s files=%d variant=%s median_ms=%.3f range_ms=%.3f..%.3f", mode, len(selected), v.name, timings[i][2], timings[i][0], timings[i][4])
		}
	}
	// Separate policy experiment. Compare early versus late truncation of the
	// SAME 200-line preview; neither is today's unlimited filter preview.
	opts.MaxLines = 200
	for _, s := range samples {
		if s.mode != "contains" || len(splitPreviewLines(s.file.Content)) <= 200 {
			continue
		}
		var a, b bytes.Buffer
		if err := renderFilePreview(&a, s.file, opts, benchFilterPalette); err != nil {
			t.Fatal(err)
		}
		if err := benchEarlyCapPreview(&b, s.file, opts, benchFilterPalette); err != nil {
			t.Fatal(err)
		}
		var late, early []float64
		for round := 0; round < 3; round++ {
			for j := 0; j < 2; j++ {
				v := (j + round) % 2
				fn := renderFilePreview
				if v == 1 {
					fn = benchEarlyCapPreview
				}
				start := time.Now()
				if err := fn(io.Discard, s.file, opts, benchFilterPalette); err != nil {
					t.Fatal(err)
				}
				ms := float64(time.Since(start)) / float64(time.Millisecond)
				if v == 0 {
					late = append(late, ms)
				} else {
					early = append(early, ms)
				}
			}
		}
		slices.Sort(late)
		slices.Sort(early)
		t.Logf("hypothetical_cap=200 file=%q lines=%d ANSI_parity=%t late_ms=%.3f early_ms=%.3f", s.file.Path, len(splitPreviewLines(s.file.Content)), bytes.Equal(a.Bytes(), b.Bytes()), late[1], early[1])
	}
}
