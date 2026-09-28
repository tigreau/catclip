package output

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
)

func TestPlanAllocationStrategyPreservesScopesAndPayload(t *testing.T) {
	root := t.TempDir()
	body := "alpha\nKEEP beta\ngamma\n"
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rng := rand.New(rand.NewSource(74))
	for iteration := 0; iteration < 150; iteration++ {
		var inv discovery.Discovered
		for s := 0; s < rng.Intn(7); s++ {
			scope := discovery.Scope{Scope: command.ExecutionScope{Paths: rng.Intn(2) == 0}}
			if rng.Intn(2) == 0 {
				scope.Scope.Stages = []command.Stage{{Kind: command.StageRecent}}
			}
			for e := 0; e < rng.Intn(20); e++ {
				name := []string{"a.txt", "b.txt", "c.txt"}[rng.Intn(3)]
				entry := discovery.Entry{RelPath: name, AbsPath: filepath.Join(root, name), SizeKnown: true, SizeBytes: int64(len(body)), Mode: command.EntryModeFull}
				switch rng.Intn(5) {
				case 1:
					entry.Mode, entry.LinesStart, entry.LinesEnd = command.EntryModeLines, 1, 1
				case 2:
					entry.Mode, entry.LinesStart, entry.LinesEnd = command.EntryModeLines, 2, 3
				case 3:
					entry.Mode, entry.LinesStart, entry.LinesEnd = command.EntryModeLines, 99, 100
				case 4:
					entry.Mode, entry.SnippetPattern, entry.SnippetMatchLines = command.EntryModeSnippet, "KEEP", []int{2}
					entry.SnippetContextSet = rng.Intn(2) == 0
				}
				if rng.Intn(2) == 0 {
					entry.TargetRoot = "."
				}
				scope.Entries = append(scope.Entries, entry)
			}
			inv.Scopes = append(inv.Scopes, scope)
		}
		before, err := json.Marshal(inv)
		if err != nil {
			t.Fatal(err)
		}
		var scopes []command.ExecutionScope
		var evaluated []EvaluatedScope
		var entries []discovery.Entry
		for _, scope := range inv.Scopes {
			scopes = append(scopes, scope.Scope)
			copied := append([]discovery.Entry(nil), scope.Entries...)
			entries = append(entries, copied...)
			evaluated = append(evaluated, EvaluatedScope{Paths: scope.Scope.Paths, Entries: copied})
		}
		preserve := ExecutionScopesPreserveEvaluatedOrder(scopes)
		var want Plan
		if ExecutionScopesUsePathsStage(scopes) {
			want, err = legacyAllocationSectionedPlan(git.Context{}, evaluated, preserve)
		} else {
			if preserve {
				entries = discovery.DedupeEntriesByPathPreserveOrder(entries)
			} else {
				entries = discovery.DedupeEntriesByPath(entries)
			}
			var units []PreparedFileUnit
			units, err = PrepareFileUnits(git.Context{}, entries)
			want = legacyAllocationBuildPlan(units)
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := BuildPlanForDiscoveredInvocation(git.Context{}, inv)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("iteration %d: plan differs from allocation oracle", iteration)
		}
		after, err := json.Marshal(inv)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("iteration %d: planner mutated input", iteration)
		}
		var wantBytes, gotBytes bytes.Buffer
		if err := WriteOutputPlanPayloadWithoutPrefetch(&wantBytes, EmitConfig{}, want); err != nil {
			t.Fatal(err)
		}
		if err := WriteOutputPlanPayloadWithoutPrefetch(&gotBytes, EmitConfig{}, got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(wantBytes.Bytes(), gotBytes.Bytes()) {
			t.Fatalf("iteration %d: payload differs", iteration)
		}
		for _, section := range got.sections {
			if cap(section.items) != len(section.items) {
				t.Fatal("section can append into another section")
			}
		}
	}
}
