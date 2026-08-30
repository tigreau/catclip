package discovery

import (
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
)

func TestApplyGitStatusStageIDs(t *testing.T) {
	entries := []Entry{
		{RelPath: "clean.txt"},
		{RelPath: "staged.txt"},
		{RelPath: "unstaged.txt"},
		{RelPath: "both.txt"},
		{RelPath: "untracked.txt"},
	}
	ids := []uint32{4, 0, 3, 1, 2}
	statuses := map[string]string{
		"staged.txt":    "S",
		"unstaged.txt":  "M",
		"both.txt":      "SM",
		"untracked.txt": "?",
	}
	tests := []struct {
		kind command.StageKind
		want []uint32
	}{
		{kind: command.StageChanged, want: []uint32{4, 3, 1, 2}},
		{kind: command.StageChangedDiff, want: []uint32{4, 3, 1, 2}},
		{kind: command.StageStaged, want: []uint32{3, 1}},
		{kind: command.StageStagedDiff, want: []uint32{3, 1}},
		{kind: command.StageUnstaged, want: []uint32{3, 2}},
		{kind: command.StageUnstagedDiff, want: []uint32{3, 2}},
		{kind: command.StageUntracked, want: []uint32{4}},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			got, ok := ApplyGitStatusStageIDs(git.Context{Enabled: true}, command.Stage{Kind: tc.kind}, entries, ids, statuses)
			if !ok || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ApplyGitStatusStageIDs(%s) = %v, %v; want %v, true", tc.kind, got, ok, tc.want)
			}
		})
	}
}

func TestApplyGitStatusStageIDsRejectsInvalidIDAndUnsupportedStage(t *testing.T) {
	entries := []Entry{{RelPath: "a.txt"}}
	if _, ok := ApplyGitStatusStageIDs(git.Context{Enabled: true}, command.Stage{Kind: command.StageOnly}, entries, []uint32{0}, nil); ok {
		t.Fatal("unsupported stage was accepted")
	}
	if _, ok := ApplyGitStatusStageIDs(git.Context{Enabled: true}, command.Stage{Kind: command.StageChanged}, entries, []uint32{1}, nil); ok {
		t.Fatal("invalid retained ID was accepted")
	}
	if _, ok := ApplyGitStatusStageIDs(git.Context{}, command.Stage{Kind: command.StageChanged}, entries, []uint32{1}, nil); ok {
		t.Fatal("invalid retained ID was accepted outside a Git repository")
	}
}

func TestApplyGitStatusStageIDsDoesNotPreserveMembershipWithoutGit(t *testing.T) {
	entries := []Entry{{RelPath: "a.go"}}
	got, ok := ApplyGitStatusStageIDs(git.Context{}, command.Stage{Kind: command.StageChanged}, entries, []uint32{0}, nil)
	if !ok || len(got) != 0 {
		t.Fatalf("non-Git projection = %v, %v; want empty, true", got, ok)
	}
}
