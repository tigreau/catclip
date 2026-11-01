package ui

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestMetadataRootCompactionUsesResolutionEvidence(t *testing.T) {
	// No paths exist on disk: only retained kinds can establish coverage.
	kinds := map[string]discovery.ResolvedTargetKind{
		"src": discovery.ResolvedTargetDir, "src/a.go": discovery.ResolvedTargetFile,
		"src2": discovery.ResolvedTargetDir, "src2/nested": discovery.ResolvedTargetDir,
		"file": discovery.ResolvedTargetFile, "file/child": discovery.ResolvedTargetFile,
	}
	got := compactMetadataRoots(t.TempDir(), []string{"src/a.go", "src2/nested", "src2", "src", "file/child", "file", "src"}, kinds)
	want := []string{"file", "src", "src2", "file/child"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMetadataStringRootCompactionBoundaries(t *testing.T) {
	got := compactMetadataStringRoots([]string{"a", "a", "a-b", "a/b", "a/b/c", "ab", "b/c"})
	want := []string{"a", "a-b", "ab", "b/c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func BenchmarkMetadataRootCompaction(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			roots := make([]string, n)
			kinds := make(map[string]discovery.ResolvedTargetKind, n)
			for i := range roots {
				roots[i] = fmt.Sprintf("file-%06d.go", i)
				kinds[roots[i]] = discovery.ResolvedTargetFile
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := compactMetadataRoots("unused", append([]string(nil), roots...), kinds); len(got) != n {
					b.Fatal(len(got))
				}
			}
		})
	}
}
