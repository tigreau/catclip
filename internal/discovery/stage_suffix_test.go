package discovery

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

func TestStageSuffixMatchesRegexp(t *testing.T) {
	patterns := []string{"*", "*.go", "*.GO", "*.tar.gz", "*name", "*]", "*.$+()^", "./*.go", "**.go", "*?.go", "*[ab].go", "*/a.go", "*.go/", "*\n.go", "*é", "plain"}
	paths := []string{"", ".", "/", "a.go", "A.GO", "src/a.go", "src/a.go/", "src/.go", "a.tar.gz", "other/name", "a]", "a.$+()^", "dir\n/a.go", "src/a\n.go", "src/a.go\n", "é/a.go", "bad\xff.go", "foo\\a.go", "plain"}
	for _, pattern := range patterns {
		matcher, err := ClassifyStageValue(pattern)
		if err != nil {
			// Non-ASCII patterns retain the current compiler's behavior.
			if isLiteralSuffixGlob(normalizeGlobStageValue(pattern)) {
				t.Fatalf("fast-path pattern rejected: %q %v", pattern, err)
			}
			continue
		}
		oracle := matcher
		oracle.suffixGlob = false
		for _, path := range paths {
			if got, want := MatchesStageValue(path, matcher), MatchesStageValue(path, oracle); got != want {
				t.Fatalf("pattern=%q path=%q: got %v want %v", pattern, path, got, want)
			}
		}
	}
	for _, pattern := range []string{"*", "*.go", "*name", "*]"} {
		matcher, err := ClassifyStageValue(pattern)
		if err != nil || !matcher.suffixGlob {
			t.Fatalf("simple suffix did not use fast path: %q %v", pattern, err)
		}
	}
}

func FuzzStageSuffixMatchesRegexp(f *testing.F) {
	for _, seed := range [][2]string{{"*.go", "dir/a.go"}, {"*", "a\nb"}, {"*.go", "dir\n/a.go"}, {"*]", "a]"}, {"*.go", "bad\xff.go"}} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, pattern, path string) {
		if len(pattern) > 256 || len(path) > 4096 {
			return
		}
		matcher, err := ClassifyStageValue(pattern)
		if err != nil || !matcher.suffixGlob {
			return
		}
		oracle := matcher
		oracle.suffixGlob = false
		if MatchesStageValue(path, matcher) != MatchesStageValue(path, oracle) {
			t.Fatalf("suffix/regex disagreement: %q %q", pattern, path)
		}
	})
}

func TestStageSuffixOrderedCombinations(t *testing.T) {
	entries := []Entry{
		{RelPath: "src/a.go", Mode: command.EntryModeLines, Lines: true, LinesStart: 2, LinesEnd: 4},
		{RelPath: "ignored/b.go", IgnoreBypassed: true, BlockSource: ".hiss", SnippetMatchLines: []int{1, 5}},
		{RelPath: "src/c.md", Mode: command.EntryModeDiff, DiffWantStaged: true},
		{RelPath: "src/UPPER.GO"}, {RelPath: "odd\n/a.go"}, {RelPath: "src/a\n.go"},
	}
	stages := []command.Stage{
		{Kind: command.StageOnly, Values: []string{"*.go", "*.md"}},
		{Kind: command.StageExclude, Values: []string{"*b.go", "[ac].md"}},
		{Kind: command.StageOnly, Values: []string{"src/", "*.go"}},
		{Kind: command.StageExclude, Values: []string{"*.md"}},
	}
	ids := []uint32{4, 0, 1, 2, 3, 0, 5}
	want := make([]Entry, len(ids))
	for i, id := range ids {
		want[i] = entries[id]
	}
	for _, stage := range stages {
		var matchers []StageValueMatcher
		for _, pattern := range stage.Values {
			matcher, err := ClassifyStageValue(pattern)
			if err != nil {
				t.Fatal(err)
			}
			matcher.suffixGlob = false
			matchers = append(matchers, matcher)
		}
		filtered := make([]Entry, 0, len(want))
		for _, entry := range want {
			if MatchesStageValues(entry.RelPath, matchers) == (stage.Kind == command.StageOnly) {
				filtered = append(filtered, entry)
			}
		}
		want = filtered
		input := make([]Entry, len(ids))
		for i, id := range ids {
			input[i] = entries[id]
		}
		got, err := filterEntriesByStagePatterns(input, stage.Values, stage.Kind == command.StageOnly)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("entry stage disagrees: %v", err)
		}
		var eligible bool
		ids, eligible, err = ApplyPathOnlyStageIDs(command.ExecutionScope{}, stage, entries, ids)
		if err != nil || !eligible || len(ids) != len(want) {
			t.Fatalf("ID stage disagrees: %v", err)
		}
		for i, id := range ids {
			if !reflect.DeepEqual(entries[id], want[i]) {
				t.Fatal("selection, order, or output attributes changed")
			}
		}
	}
}

func BenchmarkStageSuffixCorpus(b *testing.B) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		b.Skip("opt-in full corpus")
	}
	corpus := os.Getenv("CATCLIP_TEST_CORPUS")
	if corpus == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			b.Fatal(err)
		}
		corpus = filepath.Join(home, "Desktop", "catclip-test-data")
	}
	b.Chdir(corpus)
	for _, expanded := range []bool{false, true} {
		name := "visible"
		if expanded {
			name = "no-ignore-binaries"
		}
		b.Run(name, func(b *testing.B) {
			result, err := EvaluateScope(command.Invocation{WorkingDir: corpus, Headless: true, WithBinaries: expanded}, git.Context{}, 0,
				command.ExecutionScope{Targets: []string{"."}, NoIgnore: expanded}, io.Discard, platform.Palette{})
			if err != nil || len(result.Entries) == 0 {
				b.Fatalf("corpus discovery: %v", err)
			}
			for _, pattern := range []string{"*.c", "*.h", "*.md", "*", "*.[ch]"} {
				matcher, err := ClassifyStageValue(pattern)
				if err != nil {
					b.Fatal(err)
				}
				oracle := matcher
				oracle.suffixGlob = false
				wantCount := 0
				for _, entry := range result.Entries {
					want := MatchesStageValue(entry.RelPath, oracle)
					if MatchesStageValue(entry.RelPath, matcher) != want {
						b.Fatalf("parity: %q %q", pattern, entry.RelPath)
					}
					if want {
						wantCount++
					}
				}
				for _, optimized := range []bool{false, true} {
					variant := "regexp"
					selected := oracle
					if optimized {
						variant, selected = "suffix", matcher
					}
					b.Run(pattern+"/"+variant, func(b *testing.B) {
						b.ReportAllocs()
						b.ReportMetric(float64(len(result.Entries)), "entries/op")
						for i := 0; i < b.N; i++ {
							count := 0
							for _, entry := range result.Entries {
								if MatchesStageValue(entry.RelPath, selected) {
									count++
								}
							}
							if count != wantCount {
								b.Fatal("membership changed")
							}
						}
					})
				}
			}
		})
	}
}
