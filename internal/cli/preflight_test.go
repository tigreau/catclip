package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestValidateStartupPreflightArgsExamples(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantReason Reason
		wantText   string
	}{
		{name: "target then bare modifier menu", args: []string{"a", "--"}},
		{name: "double bare modifier menu chain", args: []string{"--", "--"}},
		{name: "bare modifier menu cannot take args", args: []string{"a", "--", "a"}, wantReason: ReasonBarePlaceholderOrder},
		{name: "bare only", args: []string{"--only"}, wantReason: ReasonRequiredValue},
		{name: "only with value", args: []string{"--only", "a"}},
		{name: "only recovery prefix", args: []string{"--only", "--", "--"}, wantReason: ReasonRequiredValue},
		{name: "only value then modifier menu", args: []string{"--only", "a", "--"}},
		{name: "only cannot leave bare menu in middle", args: []string{"--only", "a", "--", "a", "--"}, wantReason: ReasonBarePlaceholderOrder},
		{name: "bare exclude", args: []string{"--exclude"}, wantReason: ReasonRequiredValue},
		{name: "exclude value then modifier menu", args: []string{"--exclude", "a", "--"}},
		{name: "bare recent", args: []string{"--recent"}},
		{name: "recent value then modifier menu", args: []string{"--recent", "5", "--"}},
		{name: "recent invalid value", args: []string{"--recent", "a"}, wantText: "--recent takes an optional positive integer"},
		{name: "bare size", args: []string{"--size"}},
		{name: "size min", args: []string{"--size", "10"}},
		{name: "size range then modifier menu", args: []string{"--size", "0", "100", "--"}},
		{name: "size invalid value", args: []string{"--size", "big"}, wantText: "--size expects integer KiB values"},
		{name: "size zero max", args: []string{"--size", "0", "0"}, wantText: "--size max must be >= 1 KiB"},
		{name: "bare depth", args: []string{"--depth"}, wantReason: ReasonRequiredValue},
		{name: "depth with value", args: []string{"--depth", "2"}},
		{name: "depth invalid value", args: []string{"--depth", "0"}, wantText: "--depth takes a positive integer"},
		{name: "paths is valid prefix", args: []string{"--paths"}},
		{name: "contains after paths rejected", args: []string{"--paths", "--contains", "a"}, wantReason: ReasonTerminalBoundaryOrder},
		{name: "bare contains", args: []string{"--contains"}, wantReason: ReasonRequiredValue},
		{name: "contains with value", args: []string{"--contains", "a"}},
		{name: "contains with modifier-like value", args: []string{"--contains", "--snippet"}},
		{name: "contains with double-dash value", args: []string{"--contains", "--"}},
		{name: "bare snippet", args: []string{"--snippet"}, wantReason: ReasonRequiredValue},
		{name: "contains then bare snippet recovery", args: []string{"--contains", "a", "--snippet"}, wantReason: ReasonRequiredValue},
		{name: "snippet with value", args: []string{"--snippet", "a"}},
		{name: "snippet with modifier-like value", args: []string{"--snippet", "--contains"}},
		{name: "snippet with double-dash value", args: []string{"--snippet", "--"}},
		{name: "changed snippet is valid prefix", args: []string{"--changed", "--snippet", "TODO"}},
		{name: "bare diff is invalid prefix", args: []string{"--diff"}, wantReason: ReasonDiffStandalone},
		{name: "bare no-bundle is valid prefix", args: []string{"--no-bundle"}},
		{name: "no-bundle with target", args: []string{"--no-bundle", "."}},
		{name: "no-bundle across then scopes", args: []string{".", "--no-bundle", "--then", "src"}},
		{name: "with-binaries before target and modifier menu", args: []string{"--with-binaries", "dist", "--"}},
		{name: "untracked diff is invalid prefix", args: []string{"--untracked", "--changed-diff"}, wantReason: ReasonUntrackedDiff},
		{name: "contains after diff rejected", args: []string{"--changed-diff", "--contains", "a"}, wantReason: ReasonDiffContentFilterOrder},
		{name: "contains then changed diff then modifier menu", args: []string{"--contains", "a", "--changed-diff", "--"}},
		{name: "snippet then changed then modifier menu", args: []string{"--snippet", "a", "--changed", "--"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStartupPreflightArgs(tt.args)
			if tt.wantReason == "" && tt.wantText == "" {
				if err != nil {
					t.Fatalf("validateStartupPreflightArgs returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}
			if tt.wantReason != "" {
				var failure ValidationFailure
				if !errors.As(err, &failure) {
					t.Fatalf("expected ValidationFailure reason %q, got %T: %v", tt.wantReason, err, err)
				}
				if failure.Reason != tt.wantReason {
					t.Fatalf("reason = %q, want %q", failure.Reason, tt.wantReason)
				}
				return
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("expected error containing %q, got %v", tt.wantText, err)
			}
		})
	}
}

func TestNoIgnoreAndRemovedIncludeCommandShapeContract(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantReason Reason
		wantText   string
	}{
		{name: "modifier menu is the unseeded interactive entry", args: []string{"src", "--"}},
		{name: "no ignore is valid", args: []string{"src", "--no-ignore"}},
		{name: "bare no ignore is the combined interactive entry", args: []string{"--no-ignore"}},
		{name: "no ignore must lead its scope", args: []string{"src", "--only", "*.ts", "--no-ignore"}, wantReason: ReasonNoIgnoreAfterModifier},
		{name: "no ignore cannot repeat", args: []string{"src", "--no-ignore", "--no-ignore"}, wantReason: ReasonRepeatedNoIgnore},
		{name: "then starts a fresh no ignore scope", args: []string{"src", "--no-ignore", "--then", "docs", "--no-ignore"}},
		{name: "include alone is removed", args: []string{"src", "--include"}, wantText: "--include is not a supported option"},
		{name: "include with value is removed", args: []string{"src", "--include", "src/build"}, wantText: "--include is not a supported option"},
		{name: "include equals value is removed", args: []string{"src", "--include=src/build"}, wantText: "--include is not a supported option"},
		{name: "include after modifier is still removed", args: []string{"src", "--only", "*.ts", "--include", "src/build"}, wantText: "--include is not a supported option"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStartupPreflightArgs(tt.args)
			if tt.wantReason == "" && tt.wantText == "" {
				if err != nil {
					t.Fatalf("ValidateStartupPreflightArgs(%q) returned error: %v", tt.args, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateStartupPreflightArgs(%q) succeeded; want validation error", tt.args)
			}
			if tt.wantReason != "" {
				var failure ValidationFailure
				if !errors.As(err, &failure) {
					t.Fatalf("ValidateStartupPreflightArgs(%q) error = %T %v; want reason %q", tt.args, err, err, tt.wantReason)
				}
				if failure.Reason != tt.wantReason {
					t.Fatalf("ValidateStartupPreflightArgs(%q) reason = %q; want %q", tt.args, failure.Reason, tt.wantReason)
				}
				return
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("ValidateStartupPreflightArgs(%q) error = %v; want text %q", tt.args, err, tt.wantText)
			}
		})
	}
}

func TestStartupPreflightCommandSpecBuildsCompleteSpec(t *testing.T) {
	spec, err := StartupPreflightCommandSpec([]string{"src", "--contains", "TODO", "--then", "docs", "--snippet", "FIXME"})
	if err != nil {
		t.Fatalf("startupPreflightCommandSpec returned error: %v", err)
	}
	if !spec.Complete() {
		t.Fatal("expected complete startup preflight command spec")
	}

	scopeSpecs := spec.Scopes()
	if got, want := len(scopeSpecs), 2; got != want {
		t.Fatalf("expected %d scopes, got %d", want, got)
	}
	first := scopeSpecs[0]
	if got, want := strings.Join(first.Targets(), "\n"), "src"; got != want {
		t.Fatalf("first Targets() = %q, want %q", got, want)
	}
	if !first.HasContainsFilter() || first.ContainsPattern() != "TODO" {
		t.Fatalf("expected first scope contains TODO, got has=%v pattern=%q", first.HasContainsFilter(), first.ContainsPattern())
	}
	if got, want := first.OutputMode(), command.EntryModeFull; got != want {
		t.Fatalf("first OutputMode() = %q, want %q", got, want)
	}

	second := scopeSpecs[1]
	if got, want := strings.Join(second.Targets(), "\n"), "docs"; got != want {
		t.Fatalf("second Targets() = %q, want %q", got, want)
	}
	if !second.HasSnippetOutput() || second.SnippetPattern() != "FIXME" {
		t.Fatalf("expected second scope snippet FIXME, got has=%v pattern=%q", second.HasSnippetOutput(), second.SnippetPattern())
	}
	if got, want := second.OutputMode(), command.EntryModeSnippet; got != want {
		t.Fatalf("second OutputMode() = %q, want %q", got, want)
	}
}

func TestStartupPreflightCommandSpecBuildsPartialSpecForModifierPlaceholder(t *testing.T) {
	spec, err := StartupPreflightCommandSpec([]string{"src", "--contains", "TODO", "--"})
	if err != nil {
		t.Fatalf("startupPreflightCommandSpec returned error: %v", err)
	}
	if spec.Complete() {
		t.Fatal("expected partial startup preflight command spec")
	}

	scopeSpecs := spec.Scopes()
	if got, want := len(scopeSpecs), 1; got != want {
		t.Fatalf("expected %d scope, got %d", want, got)
	}
	scopeSpec := scopeSpecs[0]
	if got, want := strings.Join(scopeSpec.Targets(), "\n"), "src"; got != want {
		t.Fatalf("Targets() = %q, want %q", got, want)
	}
	if !scopeSpec.HasContainsFilter() || scopeSpec.ContainsPattern() != "TODO" {
		t.Fatalf("expected contains TODO, got has=%v pattern=%q", scopeSpec.HasContainsFilter(), scopeSpec.ContainsPattern())
	}
}

func TestStartupPreflightCommandSpecDefaultsBarePlaceholderScopeToDot(t *testing.T) {
	spec, err := StartupPreflightCommandSpec([]string{"--", "--"})
	if err != nil {
		t.Fatalf("startupPreflightCommandSpec returned error: %v", err)
	}
	if spec.Complete() {
		t.Fatal("expected partial startup preflight command spec")
	}

	scopeSpecs := spec.Scopes()
	if got, want := len(scopeSpecs), 1; got != want {
		t.Fatalf("expected %d default scope, got %d", want, got)
	}
	if got, want := strings.Join(scopeSpecs[0].Targets(), "\n"), "."; got != want {
		t.Fatalf("Targets() = %q, want %q", got, want)
	}
}

func BenchmarkStartupPreflightCommandSpecInteractiveSized(b *testing.B) {
	args := []string{
		"src",
		"--only", "*.go", "*.md",
		"--exclude", "vendor", "*.snap",
		"--contains", "TODO",
		"--then",
		"tests",
		"--unstaged",
		"--snippet", "FIXME",
		"--depth", "2",
		"--recent", "10",
		"--then",
		"internal",
		"--staged-diff",
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		spec, err := StartupPreflightCommandSpec(args)
		if err != nil {
			b.Fatal(err)
		}
		if len(spec.Scopes()) != 3 {
			b.Fatalf("expected 3 scopes")
		}
	}
}
