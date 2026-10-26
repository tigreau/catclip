package discovery

import (
	"fmt"
	"reflect"
	"testing"
)

func TestEmptyRefinementEvidenceIsOnlyRemovedMembership(t *testing.T) {
	r := EmptyRefinement{}
	before := []Entry{{RelPath: "a"}, {RelPath: "b"}, {RelPath: "c"}}
	r.Record(before, []Entry{before[1], before[0]})
	if !r.Evaluated || !reflect.DeepEqual(r.Paths, []string{"c"}) {
		t.Fatal(r)
	}
	r.Record([]Entry{before[1], before[0]}, nil)
	if !reflect.DeepEqual(r.Paths, []string{"c", "b", "a"}) {
		t.Fatal(r)
	}
	detached := r.Clone()
	detached.Paths[0] = "mutated"
	if r.Paths[0] != "c" {
		t.Fatal("aliased evidence")
	}
}

func BenchmarkEmptyRefinementCapture(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		entries := make([]Entry, n)
		for i := range entries {
			entries[i].RelPath = fmt.Sprintf("src/%06d.txt", i)
		}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var r EmptyRefinement
				r.Record(entries, nil)
				if len(r.Paths) != n {
					b.Fatal(len(r.Paths))
				}
			}
		})
	}
}
