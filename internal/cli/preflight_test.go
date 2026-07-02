package cli

import (
	"github.com/tigreau/catclip/internal/command"
	"strings"
	"testing"
)

func TestValidateStartupPreflightArgsExamples(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "target then bare modifier menu", args: []string{"a", "--"}},
		{name: "double bare modifier menu chain", args: []string{"--", "--"}},
		{name: "bare modifier menu cannot take args", args: []string{"a", "--", "a"}, wantErr: "bare -- can only be followed by another bare -- in the same scope"},
		{name: "bare include", args: []string{"--include"}, wantErr: "--include requires a target query"},
		{name: "include with value (effect-5: bare --include requires positional)", args: []string{"--include", "node_modules"}, wantErr: "--include 'node_modules' requires a positional target"},
		{name: "include with target and value", args: []string{".", "--include", "node_modules"}},
		{name: "include rejects parent traversal", args: []string{"--include", "src/../vendor"}, wantErr: "--include cannot traverse above the current target scope"},
		{name: "include rejects glob", args: []string{"--include", "*.js"}, wantErr: "--include does not accept glob patterns"},
		{name: "bare only", args: []string{"--only"}, wantErr: "--only requires a pattern"},
		{name: "only with value", args: []string{"--only", "a"}},
		{name: "only recovery prefix", args: []string{"--only", "--", "--"}, wantErr: "--only requires a pattern"},
		{name: "only value then modifier menu", args: []string{"--only", "a", "--"}},
		{name: "only cannot leave bare menu in middle", args: []string{"--only", "a", "--", "a", "--"}, wantErr: "bare -- can only be followed by another bare -- in the same scope"},
		{name: "bare exclude", args: []string{"--exclude"}, wantErr: "--exclude requires a pattern"},
		{name: "exclude value then modifier menu", args: []string{"--exclude", "a", "--"}},
		{name: "bare recent", args: []string{"--recent"}},
		{name: "recent value then modifier menu", args: []string{"--recent", "5", "--"}},
		{name: "recent invalid value", args: []string{"--recent", "a"}, wantErr: "--recent takes an optional positive integer"},
		{name: "bare size", args: []string{"--size"}},
		{name: "size min", args: []string{"--size", "10"}},
		{name: "size range then modifier menu", args: []string{"--size", "0", "100", "--"}},
		{name: "size invalid value", args: []string{"--size", "big"}, wantErr: "--size expects integer KiB values"},
		{name: "size zero max", args: []string{"--size", "0", "0"}, wantErr: "--size max must be >= 1 KiB"},
		{name: "bare depth", args: []string{"--depth"}, wantErr: "--depth requires a positive integer"},
		{name: "depth with value", args: []string{"--depth", "2"}},
		{name: "depth invalid value", args: []string{"--depth", "0"}, wantErr: "--depth takes a positive integer"},
		{name: "paths is valid prefix", args: []string{"--paths"}},
		{name: "contains after paths rejected", args: []string{"--paths", "--contains", "a"}, wantErr: "--paths finalizes the current scope"},
		{name: "bare contains", args: []string{"--contains"}, wantErr: "--contains requires a regex pattern"},
		{name: "contains with value", args: []string{"--contains", "a"}},
		{name: "contains with modifier-like value", args: []string{"--contains", "--snippet"}},
		{name: "contains with double-dash value", args: []string{"--contains", "--"}},
		{name: "bare snippet", args: []string{"--snippet"}, wantErr: "--snippet requires a regex pattern"},
		{name: "contains then bare snippet recovery", args: []string{"--contains", "a", "--snippet"}, wantErr: "--snippet requires a regex pattern"},
		{name: "snippet with value", args: []string{"--snippet", "a"}},
		{name: "snippet with modifier-like value", args: []string{"--snippet", "--contains"}},
		{name: "snippet with double-dash value", args: []string{"--snippet", "--"}},
		{name: "changed snippet is valid prefix", args: []string{"--changed", "--snippet", "TODO"}},
		{name: "bare diff is invalid prefix", args: []string{"--diff"}, wantErr: "--diff is no longer a standalone modifier"},
		{name: "bare no-bundle is valid prefix", args: []string{"--no-bundle"}},
		{name: "no-bundle with target", args: []string{"--no-bundle", "."}},
		{name: "no-bundle across then scopes", args: []string{".", "--no-bundle", "--then", "src"}},
		{name: "with-binaries before target and modifier menu", args: []string{"--with-binaries", "dist", "--"}},
		{name: "untracked diff is invalid prefix", args: []string{"--untracked", "--changed-diff"}, wantErr: "--untracked-diff doesn't make sense"},
		{name: "contains after diff rejected", args: []string{"--changed-diff", "--contains", "a"}, wantErr: "--contains must come before --changed-diff in the same scope"},
		{name: "contains then changed diff then modifier menu", args: []string{"--contains", "a", "--changed-diff", "--"}},
		{name: "snippet then changed then modifier menu", args: []string{"--snippet", "a", "--changed", "--"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStartupPreflightArgs(tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateStartupPreflightArgs returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
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
