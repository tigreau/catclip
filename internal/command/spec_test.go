package command

import (
	"reflect"
	"strings"
	"testing"
)

// TestStageFieldsSurviveSpecRoundTrip pins every Stage field — Kind, Values,
// Limit, ExactValues — through ExecutionScope → Spec → ExecutionScope. The
// motivating case is ExactValues, which the previous cloneScopeStages at
// root silently dropped (caught 2026-06-04 by review of the v0.6.0 command
// extraction); the others are listed so a future field addition that misses
// the clone path fails this test instead of getting silently truncated.
func TestStageFieldsSurviveSpecRoundTrip(t *testing.T) {
	limit := 7
	original := ExecutionScope{
		Targets: []string{"src"},
		Stages: []Stage{
			{
				Kind:        StageOnly,
				Values:      []string{"*.go", "*.ts"},
				Limit:       &limit,
				ExactValues: true,
			},
			{
				Kind:        StageExclude,
				Values:      []string{"*_test.go"},
				ExactValues: false,
			},
		},
	}

	spec := FinalizedSpecFromExecutionScopes([]ExecutionScope{original})
	roundTripped := ExecutionScopesFromSpec(spec)
	if got, want := len(roundTripped), 1; got != want {
		t.Fatalf("expected 1 scope after round-trip, got %d", got)
	}
	got := roundTripped[0]

	if !reflect.DeepEqual(got.Targets, original.Targets) {
		t.Errorf("Targets lost: got %v, want %v", got.Targets, original.Targets)
	}
	if len(got.Stages) != len(original.Stages) {
		t.Fatalf("expected %d stages, got %d", len(original.Stages), len(got.Stages))
	}
	for i, want := range original.Stages {
		if got.Stages[i].Kind != want.Kind {
			t.Errorf("stage[%d].Kind: got %q, want %q", i, got.Stages[i].Kind, want.Kind)
		}
		if !reflect.DeepEqual(got.Stages[i].Values, want.Values) {
			t.Errorf("stage[%d].Values: got %v, want %v", i, got.Stages[i].Values, want.Values)
		}
		if got.Stages[i].ExactValues != want.ExactValues {
			t.Errorf("stage[%d].ExactValues: got %v, want %v (regression: cloneScopeStages used to drop this field silently)", i, got.Stages[i].ExactValues, want.ExactValues)
		}
		switch {
		case got.Stages[i].Limit == nil && want.Limit != nil:
			t.Errorf("stage[%d].Limit: dropped pointer; got nil, want %d", i, *want.Limit)
		case got.Stages[i].Limit != nil && want.Limit == nil:
			t.Errorf("stage[%d].Limit: gained pointer; got %d, want nil", i, *got.Stages[i].Limit)
		case got.Stages[i].Limit != nil && want.Limit != nil && *got.Stages[i].Limit != *want.Limit:
			t.Errorf("stage[%d].Limit: got %d, want %d", i, *got.Stages[i].Limit, *want.Limit)
		}
	}
}

// TestCloneStagesPreservesExactValues is the unit-level counterpart focused
// strictly on the helper, so a future refactor that bypasses
// FinalizedSpecFromExecutionScopes and clones stages directly still trips
// this test if it loses ExactValues.
func TestCloneStagesPreservesExactValues(t *testing.T) {
	in := []Stage{
		{Kind: StageOnly, Values: []string{"a"}, ExactValues: true},
		{Kind: StageExclude, Values: []string{"b"}, ExactValues: false},
	}
	got := cloneStages(in)
	if len(got) != len(in) {
		t.Fatalf("len: got %d, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i].ExactValues != in[i].ExactValues {
			t.Errorf("stage[%d].ExactValues: got %v, want %v", i, got[i].ExactValues, in[i].ExactValues)
		}
	}
}

func TestDeepIncludeCanonicalRenderAuthorizesParentAndNarrowsToFiles(t *testing.T) {
	spec := FinalizedSpecFromExecutionScopes([]ExecutionScope{{
		Targets:         []string{"architecture"},
		IncludedTargets: []string{"architecture/TEST_CONTRACTS.md", "architecture/MODIFIER_INDEX.md"},
		Stages: []Stage{{
			Kind:   StageInclude,
			Values: []string{"architecture/TEST_CONTRACTS.md", "architecture/MODIFIER_INDEX.md"},
		}},
	}})
	resolved := Resolved{
		Config: Invocation{},
		Scopes: ExecutionScopesFromSpec(spec),
	}

	got := CanonicalResolvedInvocationCommand(resolved, RenderFlags{})
	wantParts := []string{
		"catclip architecture",
		"--include architecture",
		"--only architecture/TEST_CONTRACTS.md architecture/MODIFIER_INDEX.md",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("canonical command missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--include architecture/TEST_CONTRACTS.md") {
		t.Fatalf("canonical command kept descendant-only include instead of authorizing parent:\n%s", got)
	}
}
