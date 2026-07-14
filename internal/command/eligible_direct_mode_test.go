package command

import "testing"

func TestIsDirectModeEligibleBareContainsScope(t *testing.T) {
	inv := Invocation{}
	scope := ExecutionScope{
		Targets:  []string{"."},
		Contains: "TODO",
		Stages:   []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
	}
	if !IsDirectModeEligible(inv, scope) {
		t.Fatal("bare scope with --contains should be eligible")
	}
}

func TestIsDirectModeEligibleBareSnippetScope(t *testing.T) {
	inv := Invocation{}
	scope := ExecutionScope{
		Targets:        []string{"src"},
		Snippet:        true,
		SnippetPattern: "TODO",
		Stages:         []Stage{{Kind: StageSnippet, Values: []string{"TODO"}}},
	}
	if !IsDirectModeEligible(inv, scope) {
		t.Fatal("bare scope with --snippet should be eligible")
	}
}

func TestIsDirectModeIneligibleWithoutContentMatch(t *testing.T) {
	inv := Invocation{}
	scope := ExecutionScope{Targets: []string{"."}}
	if IsDirectModeEligible(inv, scope) {
		t.Fatal("scope without --contains/--snippet must be ineligible")
	}
}

func TestIsDirectModeIneligibleWithBinaries(t *testing.T) {
	inv := Invocation{WithBinaries: true}
	scope := ExecutionScope{
		Targets:  []string{"."},
		Contains: "TODO",
		Stages:   []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
	}
	if IsDirectModeEligible(inv, scope) {
		t.Fatal("--with-binaries must disable direct mode (rg default skips binaries)")
	}
}

func TestIsDirectModeIneligibleMultiTarget(t *testing.T) {
	inv := Invocation{}
	scope := ExecutionScope{
		Targets:  []string{"src", "docs"},
		Contains: "TODO",
		Stages:   []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
	}
	if IsDirectModeEligible(inv, scope) {
		t.Fatal("multi-target scope must be ineligible (direct mode passes a single positional)")
	}
}

func TestIsDirectModeIneligibleWithPositionalGlobTarget(t *testing.T) {
	scope := ExecutionScope{
		Targets:        []string{"*.go"},
		Snippet:        true,
		SnippetPattern: "func",
		Stages:         []Stage{{Kind: StageSnippet, Values: []string{"func"}}},
	}
	if IsDirectModeEligible(Invocation{}, scope) {
		t.Fatal("positional glob target must use discovered entries, not direct rg")
	}
}

func TestIsDirectModeIneligibleWithInclude(t *testing.T) {
	inv := Invocation{}
	scope := ExecutionScope{
		Targets:         []string{"."},
		IncludedTargets: []string{"docs"},
		Contains:        "TODO",
		Stages:          []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
	}
	if IsDirectModeEligible(inv, scope) {
		t.Fatal("--include must be ineligible (different ignore semantics)")
	}
}

func TestIsDirectModeIneligibleWithOnlyOrExclude(t *testing.T) {
	cases := []struct {
		name  string
		scope ExecutionScope
	}{
		{"only", ExecutionScope{Targets: []string{"."}, Contains: "TODO", Only: []string{"*.ts"}, Stages: []Stage{{Kind: StageContains, Values: []string{"TODO"}}}}},
		{"exclude", ExecutionScope{Targets: []string{"."}, Contains: "TODO", Exclude: []string{"*.css"}, Stages: []Stage{{Kind: StageContains, Values: []string{"TODO"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if IsDirectModeEligible(Invocation{}, tc.scope) {
				t.Fatalf("--%s must be ineligible (glob semantics diverge from rg)", tc.name)
			}
		})
	}
}

func TestIsDirectModeIneligibleWithGitSelection(t *testing.T) {
	for _, name := range []string{"Changed", "Staged", "Unstaged", "Untracked"} {
		t.Run(name, func(t *testing.T) {
			scope := ExecutionScope{
				Targets:  []string{"."},
				Contains: "TODO",
				Stages:   []Stage{{Kind: StageContains, Values: []string{"TODO"}}},
			}
			switch name {
			case "Changed":
				scope.Changed = true
			case "Staged":
				scope.Staged = true
			case "Unstaged":
				scope.Unstaged = true
			case "Untracked":
				scope.Untracked = true
			}
			if IsDirectModeEligible(Invocation{}, scope) {
				t.Fatalf("--%s must be ineligible (narrows scope)", name)
			}
		})
	}
}

func TestIsDirectModeIneligibleWithNarrowingStages(t *testing.T) {
	for _, kind := range []StageKind{StageRecent, StageSize, StageDepth, StageOnly, StageExclude, StageInclude} {
		t.Run(string(kind), func(t *testing.T) {
			scope := ExecutionScope{
				Targets:  []string{"."},
				Contains: "TODO",
				Stages: []Stage{
					{Kind: kind, Values: []string{"x"}},
					{Kind: StageContains, Values: []string{"TODO"}},
				},
			}
			if IsDirectModeEligible(Invocation{}, scope) {
				t.Fatalf("stage %s must be ineligible (narrowing)", kind)
			}
		})
	}
}

func TestIsDirectModeEligibleBareNotContainsScope(t *testing.T) {
	scope := ExecutionScope{
		Targets:     []string{"."},
		NotContains: []string{"TODO"},
		Stages:      []Stage{{Kind: StageNotContains, Values: []string{"TODO"}}},
	}
	if !IsDirectModeEligible(Invocation{}, scope) {
		t.Fatal("bare scope with only --not-contains should be eligible")
	}
}

func TestIsDirectModeEligibleMixedContainsAndNotContains(t *testing.T) {
	scope := ExecutionScope{
		Targets:     []string{"."},
		Contains:    "TODO",
		NotContains: []string{"FIXME"},
		Stages: []Stage{
			{Kind: StageContains, Values: []string{"TODO"}},
			{Kind: StageNotContains, Values: []string{"FIXME"}},
		},
	}
	if !IsDirectModeEligible(Invocation{}, scope) {
		t.Fatal("mixed --contains + --not-contains scope should be eligible")
	}
}

func TestIsDirectModeEligibleMultipleNotContains(t *testing.T) {
	scope := ExecutionScope{
		Targets:     []string{"."},
		NotContains: []string{"TODO", "FIXME"},
		Stages: []Stage{
			{Kind: StageNotContains, Values: []string{"TODO"}},
			{Kind: StageNotContains, Values: []string{"FIXME"}},
		},
	}
	if !IsDirectModeEligible(Invocation{}, scope) {
		t.Fatal("multiple --not-contains should be eligible (each is one rg call)")
	}
}

func TestIsDirectModeEligibleAllowsOutputStages(t *testing.T) {
	scope := ExecutionScope{
		Targets:  []string{"."},
		Contains: "TODO",
		Paths:    true,
		Stages: []Stage{
			{Kind: StageContains, Values: []string{"TODO"}},
			{Kind: StagePaths},
		},
	}
	if !IsDirectModeEligible(Invocation{}, scope) {
		t.Fatal("output-only stages (paths/lines) should not disable eligibility")
	}
}
