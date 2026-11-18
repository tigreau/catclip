package output

import (
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"strings"
	"testing"
)

func TestScopeContributionOwnershipAndCutoff(t *testing.T) {
	e := thenFixture(t, "alpha\nbravo\ncharlie\n")
	for _, ordered := range []bool{false, true} {
		p := thenPlan(t, git.Context{}, []discovery.Entry{e, e}, false, ordered)
		g := p.ScopeContributions()
		if len(g) != 2 || len(g[0].Rows) != 1 || len(g[1].Rows) != 0 || !strings.Contains(g[1].Empty, "covered") {
			t.Fatalf("%+v", g)
		}
		// A later source projection supersedes a whole file in either scope order;
		// paths remain a separate, complete contribution of the same filename.
		for _, reverse := range []bool{false, true} {
			entries := []discovery.Entry{e, thenLines(e, 2, 3)}
			owner := 2
			if reverse {
				entries[0], entries[1] = entries[1], entries[0]
				owner = 1
			}
			p = thenPlan(t, git.Context{}, entries, true, ordered)
			g = p.ScopeContributions()
			if len(g[0].Rows) != 1 || g[0].Rows[0].Tag != "" || len(g[owner].Rows) != 1 || g[owner].Rows[0].Tag != "2-3" {
				t.Fatalf("%+v", g)
			}
			// sample.txt plus newline is 11 bytes, leaving one byte for source.
			q := p.WithTokenLimit(3)
			cg := q.ScopeContributions()
			if cg[0].Rows[0].Tag != "" || cg[owner].Rows[0].Tag != "partial" || cg[owner].Rows[0].Bytes != 1 {
				t.Fatalf("%+v", cg)
			}
			if p.ScopeContributions()[owner].Rows[0].Tag != "2-3" {
				t.Fatal("limited view mutated original")
			}
		}
	}
	p := thenPlan(t, git.Context{}, []discovery.Entry{thenLines(e, 1, 1), thenLines(e, 2, 2), thenLines(e, 3, 3)}, false, true)
	g := p.WithTokenLimit(2).ScopeContributions()
	if len(g[0].Rows) != 1 || g[1].Rows[0].Tag != "partial" || g[2].Empty != "Nothing added: token limit reached." {
		t.Fatalf("%+v", g)
	}
	// Out-of-range source is absent, not covered by another scope.
	p = thenPlan(t, git.Context{}, []discovery.Entry{thenLines(e, 99, 100), thenLines(e, 1, 1)}, false, true)
	if p.ScopeContributions()[0].Empty != "No output." {
		t.Fatal(p.ScopeContributions()[0].Empty)
	}
}

func TestScopeContributionAllSnippetRanges(t *testing.T) {
	var ranges []SnippetRange
	for i := 0; i < 37; i++ {
		ranges = append(ranges, SnippetRange{Start: i*10 + 1, End: i*10 + 3})
	}
	item := PlanItem{kind: SectionKindFiles, mode: command.EntryModeSnippet, snippetRanges: ranges}
	tag := contributionTag(item, false)
	if strings.Count(tag, ",") != 36 || !strings.HasSuffix(tag, "361-363") || strings.Contains(tag, "snippet") {
		t.Fatal(tag)
	}
	if snippetTagFromRanges(ranges) != tag {
		t.Fatal("single and multiple scope range tags differ")
	}
}

func TestScopeContributionMetadataSelection(t *testing.T) {
	e := thenFixture(t, "alpha\n")
	inv := discovery.Discovered{Scopes: []discovery.Scope{
		{Scope: command.ExecutionScope{Metadata: true}, Entries: []discovery.Entry{e}},
		{Scope: command.ExecutionScope{}, Entries: []discovery.Entry{e}},
	}}
	p, err := BuildPlanForDiscoveredInvocation(git.Context{}, inv)
	if err != nil {
		t.Fatal(err)
	}
	g := p.ScopeContributions()
	if len(g) != 2 || len(g[0].Rows) != 1 || len(g[1].Rows) != 1 || g[0].Rows[0].Bytes != 6 || g[0].Rows[0].Tag != "" {
		t.Fatalf("%+v", g)
	}
}

func TestScopeContributionBudgetBoundariesAndMetadata(t *testing.T) {
	e := thenFixture(t, "ABCD")
	inv := discovery.Discovered{Scopes: []discovery.Scope{
		{Scope: command.ExecutionScope{}, Entries: []discovery.Entry{e}},
		{Scope: command.ExecutionScope{Paths: true}, Entries: []discovery.Entry{e}},
		{Scope: command.ExecutionScope{Metadata: true}, Entries: []discovery.Entry{e}},
	}}
	p, err := BuildPlanForDiscoveredInvocation(git.Context{}, inv)
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.PrepareMetadata(func(_ []int, _ Plan) (MetadataPayload, error) { return tokenMetadata("a metadata report"), nil })
	if err != nil {
		t.Fatal(err)
	}
	// Exact first-file boundary: no partial row; both later scopes are omitted.
	g := p.WithTokenLimit(1).ScopeContributions()
	if len(g[0].Rows) != 1 || g[0].Rows[0].Tag != "" || len(g[1].Rows) != 0 || !strings.Contains(g[2].Empty, "token limit") {
		t.Fatalf("%+v", g)
	}
	// Partial paths keep the complete file identity, without tagging source.
	g = p.WithTokenLimit(2).ScopeContributions()
	if g[0].Rows[0].Tag != "" || g[1].Rows[0].Tag != "partial" || g[1].Rows[0].Entry.RelPath != e.RelPath {
		t.Fatalf("%+v", g)
	}
	// Metadata is a report about selected files, not a truncated source file.
	g = p.WithTokenLimit(4).ScopeContributions()
	if len(g[2].Rows) != 1 || g[2].Rows[0].Tag != "" || len(g[2].Notices) != 1 || !strings.Contains(g[2].Notices[0], "report incomplete") {
		t.Fatalf("%+v", g)
	}
	if p.WithTokenLimit(100).TokenLimitNotice() != "" {
		t.Fatal("fitting output reported cutoff")
	}
}

func TestSourceOnlyPlanSkipsScopeBookkeeping(t *testing.T) {
	e := thenFixture(t, "abc\n")
	inv := discovery.Discovered{Scopes: []discovery.Scope{{Entries: []discovery.Entry{e}}, {Entries: []discovery.Entry{e}}}}
	plain, err := BuildPlanForDiscoveredInvocationWithOptions(git.Context{}, inv, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	shown, err := BuildPlanForDiscoveredInvocationWithOptions(git.Context{}, inv, PlanOptions{ScopeTrees: true})
	if err != nil {
		t.Fatal(err)
	}
	if plain.HasScopeContributions() || !shown.HasScopeContributions() || thenPayload(t, plain) != thenPayload(t, shown) {
		t.Fatal("report policy changed payload or retained unnecessary groups")
	}
}
