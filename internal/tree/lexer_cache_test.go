package tree

import (
	"fmt"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// TestLexerForPathMatchesDirectMatch proves the cache returns the same lexer
// chroma's own filename match would for representative types — it is exact, not
// approximate.
func TestLexerForPathMatchesDirectMatch(t *testing.T) {
	for _, rel := range []string{"a.go", "pkg/b.go", "x.py", "y.ts", "z.md", "Makefile", "noext"} {
		direct := lexers.Match(rel)
		got := lexerForPath(rel)
		// direct may be nil for unknown types; cache returns nil too (then the
		// caller does content analysis). Compare by config name when present.
		var dName, gName string
		if direct != nil {
			dName = direct.Config().Name
		}
		if got != nil {
			gName = got.Config().Name
		}
		if dName != gName {
			t.Errorf("%s: cache picked %q, chroma picks %q", rel, gName, dName)
		}
	}
}

// TestLexerCacheKeyByType proves files of the same type share a cache key (so the
// cost is O(distinct types), not O(files)) while different types do not collide.
func TestLexerCacheKeyByType(t *testing.T) {
	if lexerCacheKey("a/b/c.go") != lexerCacheKey("x.go") {
		t.Error("two .go files should share a cache key")
	}
	if lexerCacheKey("a.go") == lexerCacheKey("a.py") {
		t.Error(".go and .py must not share a cache key")
	}
	if lexerCacheKey("dir/Makefile") != lexerCacheKey("Makefile") {
		t.Error("extension-less basenames should key the same regardless of dir")
	}
}

// BenchmarkLexerSelection isolates the sink-preview hotspot: selecting a lexer
// per <file> block. "uncached" is the pre-fix behavior (lexers.Match per block,
// re-globbing all ~250 lexers); "cached" is lexerForPath. This is the work that
// dominated `--snippet 'func' 0` previews (one block per matched file).
//
//	go test -run=^$ -bench=BenchmarkLexerSelection ./internal/tree/
func BenchmarkLexerSelection(b *testing.B) {
	// 10 distinct types spread across 500 "files" — the user's "10 file types at
	// once" case. Uncached does 500 glob scans/iter; cached does 10.
	exts := []string{".go", ".py", ".ts", ".js", ".rs", ".java", ".rb", ".c", ".cpp", ".md"}
	paths := make([]string, 0, 500)
	for i := range 500 {
		paths = append(paths, fmt.Sprintf("pkg%02d/file%03d%s", i%17, i, exts[i%len(exts)]))
	}

	b.Run("uncached_lexers.Match_per_block", func(b *testing.B) {
		for range b.N {
			for _, p := range paths {
				_ = lexers.Match(p)
			}
		}
	})

	b.Run("cached_lexerForPath_per_block", func(b *testing.B) {
		resetLexerCache()
		for range b.N {
			for _, p := range paths {
				_ = lexerForPath(p)
			}
		}
	})
}

func resetLexerCache() {
	lexerCacheMu.Lock()
	lexerCache = map[string]chroma.Lexer{}
	lexerCacheMu.Unlock()
}
