package catclip

import (
	"strings"
	"testing"
)

func TestCommandSpecFromScopesCopiesAndDerivesFields(t *testing.T) {
	limit := 5
	scopes := []executionScope{
		{
			Targets:         []string{"src"},
			IncludedTargets: []string{"vendor"},
			Only:            []string{"*.go"},
			Exclude:         []string{"*_test.go"},
			Contains:        "TODO",
			SnippetPattern:  "func",
			Snippet:         true,
			Changed:         true,
			Staged:          true,
			Stages: []scopeStage{
				{Kind: scopeStageContains, Values: []string{"TODO"}},
				{Kind: scopeStageSnippet, Values: []string{"func"}},
				{Kind: scopeStageRecent, Limit: &limit},
			},
		},
	}

	spec := finalizedCommandSpecFromExecutionScopes(scopes)
	if !spec.Complete() {
		t.Fatal("expected finalized command spec to be complete")
	}

	scopeSpecs := spec.Scopes()
	if got, want := len(scopeSpecs), 1; got != want {
		t.Fatalf("expected %d command scope, got %d", want, got)
	}
	scopeSpec := scopeSpecs[0]
	if got, want := scopeSpec.OutputMode(), entryModeSnippet; got != want {
		t.Fatalf("OutputMode() = %q, want %q", got, want)
	}
	if !scopeSpec.HasContainsFilter() || scopeSpec.ContainsPattern() != "TODO" {
		t.Fatalf("expected contains filter TODO, got has=%v pattern=%q", scopeSpec.HasContainsFilter(), scopeSpec.ContainsPattern())
	}
	if !scopeSpec.HasSnippetOutput() || scopeSpec.SnippetPattern() != "func" {
		t.Fatalf("expected snippet output func, got has=%v pattern=%q", scopeSpec.HasSnippetOutput(), scopeSpec.SnippetPattern())
	}
	if !scopeSpec.HasGitSelection() || !scopeSpec.Changed() || !scopeSpec.Staged() || scopeSpec.Unstaged() || scopeSpec.Untracked() {
		t.Fatalf("unexpected git selection state: changed=%v staged=%v unstaged=%v untracked=%v has=%v", scopeSpec.Changed(), scopeSpec.Staged(), scopeSpec.Unstaged(), scopeSpec.Untracked(), scopeSpec.HasGitSelection())
	}

	scopes[0].Targets[0] = "mutated-source"
	targets := scopeSpec.Targets()
	targets[0] = "mutated-copy"
	if got := scopeSpec.Targets()[0]; got != "src" {
		t.Fatalf("expected command scope target copy to stay src, got %q", got)
	}

	stages := scopeSpec.Stages()
	stages[0].Values[0] = "mutated-stage-value"
	*stages[2].Limit = 99
	stages = scopeSpec.Stages()
	if got := stages[0].Values[0]; got != "TODO" {
		t.Fatalf("expected command scope stage value copy to stay TODO, got %q", got)
	}
	if stages[2].Limit == nil || *stages[2].Limit != 5 {
		t.Fatalf("expected command scope stage limit copy to stay 5, got %+v", stages[2].Limit)
	}
}

func TestParseArgsBuildsCommandSpecForContentQueryScope(t *testing.T) {
	cfg, err := parseArgs([]string{".", "--contains", "keep", "--only", "README.md", "--snippet", "show"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scopeSpecs := cfg.Command.Scopes()
	if got, want := len(scopeSpecs), 1; got != want {
		t.Fatalf("expected %d command scope, got %d", want, got)
	}
	scopeSpec := scopeSpecs[0]
	if got, want := scopeSpec.OutputMode(), entryModeSnippet; got != want {
		t.Fatalf("OutputMode() = %q, want %q", got, want)
	}
	if !scopeSpec.HasContainsFilter() || scopeSpec.ContainsPattern() != "keep" {
		t.Fatalf("expected contains filter keep, got has=%v pattern=%q", scopeSpec.HasContainsFilter(), scopeSpec.ContainsPattern())
	}
	if !scopeSpec.HasSnippetOutput() || scopeSpec.SnippetPattern() != "show" {
		t.Fatalf("expected snippet output show, got has=%v pattern=%q", scopeSpec.HasSnippetOutput(), scopeSpec.SnippetPattern())
	}
	if got, want := strings.Join(scopeSpec.OnlyPatterns(), "\n"), "README.md"; got != want {
		t.Fatalf("OnlyPatterns() = %q, want %q", got, want)
	}
}

func TestParseArgsBuildsCommandSpecForMultiScopeGitDiff(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--contains", "TODO", "--then", "docs", "--staged-diff"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scopeSpecs := cfg.Command.Scopes()
	if got, want := len(scopeSpecs), 2; got != want {
		t.Fatalf("expected %d command scopes, got %d", want, got)
	}
	first := scopeSpecs[0]
	if got, want := first.OutputMode(), entryModeFull; got != want {
		t.Fatalf("first OutputMode() = %q, want %q", got, want)
	}
	if !first.HasContainsFilter() || first.ContainsPattern() != "TODO" {
		t.Fatalf("expected first scope contains filter TODO, got has=%v pattern=%q", first.HasContainsFilter(), first.ContainsPattern())
	}
	if first.HasGitSelection() {
		t.Fatal("did not expect first scope to have git selection")
	}

	second := scopeSpecs[1]
	if got, want := second.OutputMode(), entryModeDiff; got != want {
		t.Fatalf("second OutputMode() = %q, want %q", got, want)
	}
	if !second.HasGitSelection() || !second.Changed() || !second.Staged() {
		t.Fatalf("expected second scope changed+staged git selection, got changed=%v staged=%v has=%v", second.Changed(), second.Staged(), second.HasGitSelection())
	}
	if got, want := strings.Join(second.Targets(), "\n"), "docs"; got != want {
		t.Fatalf("second Targets() = %q, want %q", got, want)
	}
}

func TestParseArgsBuildsCommandSpecForPathsScope(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--only", "*.ts", "--paths"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scopeSpecs := cfg.Command.Scopes()
	if got, want := len(scopeSpecs), 1; got != want {
		t.Fatalf("expected %d command scope, got %d", want, got)
	}
	scopeSpec := scopeSpecs[0]
	if !scopeSpec.HasPathsOutput() {
		t.Fatal("expected paths output mode")
	}
	if got, want := scopeSpec.OutputMode(), entryModeFull; got != want {
		t.Fatalf("OutputMode() = %q, want %q", got, want)
	}
	if got, want := strings.Join(scopeSpec.OnlyPatterns(), "\n"), "*.ts"; got != want {
		t.Fatalf("OnlyPatterns() = %q, want %q", got, want)
	}
}

func BenchmarkFinalizedCommandSpecFromScopesInteractiveSized(b *testing.B) {
	limit := 10
	scopes := []executionScope{
		{
			Targets:         []string{"src", "docs"},
			IncludedTargets: []string{"node_modules"},
			Only:            []string{"*.go", "*.md"},
			Exclude:         []string{"vendor", "*.snap"},
			Contains:        "TODO",
			Stages: []scopeStage{
				{Kind: scopeStageOnly, Values: []string{"*.go", "*.md"}},
				{Kind: scopeStageExclude, Values: []string{"vendor", "*.snap"}},
				{Kind: scopeStageContains, Values: []string{"TODO"}},
			},
		},
		{
			Targets:        []string{"tests"},
			Snippet:        true,
			SnippetPattern: "FIXME",
			Changed:        true,
			Unstaged:       true,
			Stages: []scopeStage{
				{Kind: scopeStageUnstaged},
				{Kind: scopeStageSnippet, Values: []string{"FIXME"}},
				{Kind: scopeStageRecent, Limit: &limit},
			},
		},
		{
			Targets: []string{"internal"},
			Changed: true,
			Staged:  true,
			Diff:    true,
			Stages: []scopeStage{
				{Kind: scopeStageStagedDiff},
			},
		},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		spec := finalizedCommandSpecFromExecutionScopes(scopes)
		if len(spec.Scopes()) != len(scopes) {
			b.Fatalf("expected %d scopes", len(scopes))
		}
	}
}
