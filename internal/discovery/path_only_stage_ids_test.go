package discovery

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestApplyPathOnlyStageIDsMatchesEntryStage(t *testing.T) {
	depth := 2
	inventory := []Entry{
		{RelPath: "src/a.go", TargetRoot: "src"},
		{RelPath: "src/a.md", TargetRoot: "src"},
		{RelPath: "src/nested/b.go", TargetRoot: "src"},
		{RelPath: "src/nested/deep/c.go", TargetRoot: "src"},
	}
	all := []uint32{0, 1, 2, 3}
	tests := []command.Stage{
		{Kind: command.StageOnly, Values: []string{"*.go"}},
		{Kind: command.StageExclude, Values: []string{"*.md"}},
		{Kind: command.StageOnly, Values: []string{"src/a.go", "src/nested/b.go"}, ExactValues: true},
		{Kind: command.StageDepth, Limit: &depth},
		{Kind: command.StagePaths},
		{Kind: command.StageLines},
	}
	for _, stage := range tests {
		t.Run(string(stage.Kind), func(t *testing.T) {
			applier := stageApplierTable[stage.Kind]
			want, err := applier(stageContext{Scope: command.ExecutionScope{}, Stage: stage}, inventory)
			if err != nil {
				t.Fatalf("entry stage: %v", err)
			}
			ids, eligible, err := ApplyPathOnlyStageIDs(command.ExecutionScope{}, stage, inventory, all)
			if err != nil || !eligible {
				t.Fatalf("ID stage: eligible=%v err=%v", eligible, err)
			}
			got := make([]Entry, 0, len(ids))
			for _, id := range ids {
				got = append(got, inventory[id])
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ID stage differs: got=%v want=%v", got, want)
			}
		})
	}
}

func TestApplyPathOnlyStageIDsRejectsInventoryMismatch(t *testing.T) {
	stage := command.Stage{Kind: command.StageOnly, Values: []string{"*"}}
	if got, eligible, err := ApplyPathOnlyStageIDs(command.ExecutionScope{}, stage, []Entry{{RelPath: "a"}}, []uint32{1}); err != nil || eligible || got != nil {
		t.Fatalf("inventory mismatch should request fallback: got=%v eligible=%v err=%v", got, eligible, err)
	}
}

func BenchmarkApplyPathOnlyStageIDs(b *testing.B) {
	const count = 200_000
	inventory := make([]Entry, count)
	ids := make([]uint32, count)
	for i := range inventory {
		ext := ".go"
		if i%3 == 0 {
			ext = ".md"
		}
		inventory[i] = Entry{RelPath: fmt.Sprintf("src/package-%04d/file-%06d%s", i%1000, i, ext), TargetRoot: "src"}
		ids[i] = uint32(i)
	}
	stage := command.Stage{Kind: command.StageOnly, Values: []string{"*.go"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, eligible, err := ApplyPathOnlyStageIDs(command.ExecutionScope{}, stage, inventory, ids)
		if err != nil || !eligible || len(out) == 0 {
			b.Fatalf("eligible=%v entries=%d err=%v", eligible, len(out), err)
		}
	}
}
