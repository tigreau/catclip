package output

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

func TestEmptyRefinementOnlySuppressesWholeBodies(t *testing.T) {
	scopes := []EvaluatedScope{
		{Entries: []discovery.Entry{
			{RelPath: "a", Mode: command.EntryModeFull},
			{RelPath: "a", Mode: command.EntryModeLines, LinesStart: 2, LinesEnd: 3},
			{RelPath: "b", Mode: command.EntryModeLines, LinesStart: 0},
		}},
		{Paths: true, Entries: []discovery.Entry{{RelPath: "a"}, {RelPath: "b"}}},
		{EmptyRefinement: discovery.EmptyRefinement{Evaluated: true, Paths: []string{"a", "b", "c"}}},
	}
	cmds := []command.ExecutionScope{{}, {Paths: true}, {Snippet: true}}
	got, m := beginRefinementMerge(cmds, scopes)
	if len(got[0].Entries) != 1 || got[0].Entries[0].LinesStart != 2 || len(got[1].Entries) != 2 {
		t.Fatalf("wrong suppression: %+v", got)
	}
	if len(scopes[0].Entries) != 3 {
		t.Fatal("mutated source scope")
	}
	if m.notices[2].count != 2 || !reflect.DeepEqual(m.notices[2].paths, []string{"a", "b"}) {
		t.Fatal(m.notices)
	}
}

func TestEmptyRefinementSkipsExplainedScope(t *testing.T) {
	scopes := []EvaluatedScope{{Entries: []discovery.Entry{{RelPath: "a"}}}, {EmptyExplained: true, EmptyRefinement: discovery.EmptyRefinement{Evaluated: true}}}
	_, m := beginRefinementMerge([]command.ExecutionScope{{}, {Snippet: true}}, scopes)
	var p Plan
	m.finish(&p)
	if len(p.RefinementNoticeText()) != 0 {
		t.Fatal(p.RefinementNoticeText())
	}
}

func BenchmarkEmptyRefinementBookkeeping(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		entries := make([]discovery.Entry, n)
		paths := make([]string, n)
		for i := range entries {
			paths[i] = fmt.Sprintf("src/%06d.txt", i)
			entries[i] = discovery.Entry{RelPath: paths[i], Mode: command.EntryModeFull}
		}
		for _, focused := range []int{0, 1, n} {
			b.Run(fmt.Sprintf("entries=%d/focused=%d", n, focused), func(b *testing.B) {
				scopes := []EvaluatedScope{{Entries: entries}}
				cmds := []command.ExecutionScope{{}}
				if focused > 0 {
					scopes = append(scopes, EvaluatedScope{EmptyRefinement: discovery.EmptyRefinement{Evaluated: true, Paths: paths[:focused]}})
					cmds = append(cmds, command.ExecutionScope{Snippet: true})
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					filtered, m := beginRefinementMerge(cmds, scopes)
					if len(filtered) == 0 {
						b.Fatal("no scopes")
					}
					var p Plan
					m.finish(&p)
				}
			})
		}
	}
}
