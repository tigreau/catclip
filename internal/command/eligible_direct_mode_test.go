package command

import "testing"

func TestIsDirectModeEligibleMatrix(t *testing.T) {
	containsScope := func() ExecutionScope {
		return ExecutionScope{
			Targets:  []string{"."},
			Contains: "TODO",
			Stages:   []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
		}
	}
	withNarrowingStage := func(kind StageKind) ExecutionScope {
		scope := containsScope()
		scope.Stages = []Stage{
			{Kind: kind, Values: []string{"x"}},
			{Kind: StageContains, Values: []string{"TODO"}},
		}
		return scope
	}

	changed := containsScope()
	changed.Changed = true
	staged := containsScope()
	staged.Staged = true
	unstaged := containsScope()
	unstaged.Unstaged = true
	untracked := containsScope()
	untracked.Untracked = true
	withBinaries := Invocation{WithBinaries: true}

	tests := []struct {
		name       string
		invocation Invocation
		scope      ExecutionScope
		want       bool
	}{
		{name: "bare contains", scope: containsScope(), want: true},
		{
			name: "bare snippet",
			scope: ExecutionScope{
				Targets:        []string{"src"},
				Snippet:        true,
				SnippetPattern: "TODO",
				Stages:         []Stage{{Kind: StageSnippet, Values: []string{"TODO"}}},
			},
			want: true,
		},
		{name: "without content match", scope: ExecutionScope{Targets: []string{"."}}},
		{name: "with binaries", invocation: withBinaries, scope: containsScope()},
		{
			name: "multiple targets",
			scope: ExecutionScope{
				Targets:  []string{"src", "docs"},
				Contains: "TODO",
				Stages:   []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
			},
		},
		{
			name: "positional glob target",
			scope: ExecutionScope{
				Targets:        []string{"*.go"},
				Snippet:        true,
				SnippetPattern: "func",
				Stages:         []Stage{{Kind: StageSnippet, Values: []string{"func"}}},
			},
		},
		{
			name: "include",
			scope: ExecutionScope{
				Targets:         []string{"."},
				IncludedTargets: []string{"docs"},
				Contains:        "TODO",
				Stages:          []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
			},
		},
		{
			name: "only",
			scope: ExecutionScope{
				Targets:  []string{"."},
				Contains: "TODO",
				Only:     []string{"*.ts"},
				Stages:   []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
			},
		},
		{
			name: "exclude",
			scope: ExecutionScope{
				Targets:  []string{"."},
				Contains: "TODO",
				Exclude:  []string{"*.css"},
				Stages:   []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
			},
		},
		{name: "changed", scope: changed},
		{name: "staged", scope: staged},
		{name: "unstaged", scope: unstaged},
		{name: "untracked", scope: untracked},
		{name: "recent stage", scope: withNarrowingStage(StageRecent)},
		{name: "size stage", scope: withNarrowingStage(StageSize)},
		{name: "depth stage", scope: withNarrowingStage(StageDepth)},
		{name: "only stage", scope: withNarrowingStage(StageOnly)},
		{name: "exclude stage", scope: withNarrowingStage(StageExclude)},
		{name: "include stage", scope: withNarrowingStage(StageInclude)},
		{
			name: "bare not-contains",
			scope: ExecutionScope{
				Targets:     []string{"."},
				NotContains: []string{"TODO"},
				Stages:      []Stage{{Kind: StageNotContains, Values: []string{"TODO"}}},
			},
			want: true,
		},
		{
			name: "contains and not-contains",
			scope: ExecutionScope{
				Targets:     []string{"."},
				Contains:    "TODO",
				NotContains: []string{"FIXME"},
				Stages: []Stage{
					{Kind: StageContains, Values: []string{"TODO"}},
					{Kind: StageNotContains, Values: []string{"FIXME"}},
				},
			},
			want: true,
		},
		{
			name: "multiple not-contains",
			scope: ExecutionScope{
				Targets:     []string{"."},
				NotContains: []string{"TODO", "FIXME"},
				Stages: []Stage{
					{Kind: StageNotContains, Values: []string{"TODO"}},
					{Kind: StageNotContains, Values: []string{"FIXME"}},
				},
			},
			want: true,
		},
		{
			name: "output stage",
			scope: ExecutionScope{
				Targets:  []string{"."},
				Contains: "TODO",
				Paths:    true,
				Stages: []Stage{
					{Kind: StageContains, Values: []string{"TODO"}},
					{Kind: StagePaths},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDirectModeEligible(tt.invocation, tt.scope); got != tt.want {
				t.Fatalf("IsDirectModeEligible() = %v, want %v", got, tt.want)
			}
		})
	}
}
