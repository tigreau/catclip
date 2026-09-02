package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
)

func TestParseArgsNoSetsNeverEmitPolicy(t *testing.T) {
	for _, args := range [][]string{
		{"src", "--no"},
		{"--no", "src"},
		{"src", "--no", "--no"},
		{"--verbose", "--no-tree", "--no-bundle", "--raw", "--no", "src"},
		{"--no", "src", "--paths", "--then", "docs", "--lines", "1", "2"},
		{"--metadata", "--no", "src"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cfg, err := ParseArgs(args)
			if err != nil {
				t.Fatalf("ParseArgs() error = %v", err)
			}
			if cfg.EmissionPolicy != command.EmissionNever {
				t.Fatalf("EmissionPolicy = %v, want EmissionNever", cfg.EmissionPolicy)
			}
		})
	}
}

func TestParseArgsNoRejectsContradictoryEmissionFlagsInEitherOrder(t *testing.T) {
	tests := []struct {
		args     []string
		wantText string
	}{
		{args: []string{"src", "--no", "--yes"}, wantText: "--yes cannot be combined with --no"},
		{args: []string{"src", "--yes", "--no"}, wantText: "--yes cannot be combined with --no"},
		{args: []string{"src", "--no", "--print"}, wantText: "--no cannot be combined with --print"},
		{args: []string{"src", "--print", "--no"}, wantText: "--no cannot be combined with --print"},
		{args: []string{"src", "--no", "--headless"}, wantText: "--no cannot be combined with --headless"},
		{args: []string{"src", "--headless", "--no"}, wantText: "--no cannot be combined with --headless"},
		{args: []string{"src", "--no", "--quiet"}, wantText: "--no cannot be combined with --quiet"},
		{args: []string{"src", "--quiet", "--no"}, wantText: "--no cannot be combined with --quiet"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			_, err := ParseArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("ParseArgs() error = %v, want text %q", err, tt.wantText)
			}
			var failure ValidationFailure
			if !errors.As(err, &failure) || failure.Reason != ReasonEmissionPolicyConflict {
				t.Fatalf("ParseArgs() error = %#v, want typed emission-policy conflict", err)
			}
		})
	}
}

func TestParseArgsNoDoesNotClaimShortN(t *testing.T) {
	_, err := ParseArgs([]string{"src", "-n"})
	if err == nil || !strings.Contains(err.Error(), "Unknown option '-n'") {
		t.Fatalf("ParseArgs(-n) error = %v, want unknown-option error", err)
	}
}

func TestParseArgsMetadataReplacesPublicPreview(t *testing.T) {
	cfg, err := ParseArgs([]string{"src", "--metadata"})
	if err != nil {
		t.Fatalf("ParseArgs(--metadata): %v", err)
	}
	if cfg.PayloadKind != command.PayloadMetadata || cfg.OutputMode != command.OutputModeClipboard {
		t.Fatalf("metadata config = %#v", cfg)
	}
	_, err = ParseArgs([]string{"src", "--preview"})
	if err == nil || !strings.Contains(err.Error(), "Unknown option '--preview'") {
		t.Fatalf("removed --preview error = %v", err)
	}
}

func TestMetadataRejectsEveryContentProjectionInBothOrders(t *testing.T) {
	conflicts := [][]string{
		{"--paths"},
		{"--lines", "1", "2"},
		{"--snippet", "TODO"},
		{"--changed-diff"},
		{"--staged-diff"},
		{"--unstaged-diff"},
		{"--raw"},
	}
	for _, conflict := range conflicts {
		for _, metadataFirst := range []bool{false, true} {
			args := []string{"src"}
			if metadataFirst {
				args = append(args, "--metadata")
			}
			args = append(args, conflict...)
			if !metadataFirst {
				args = append(args, "--metadata")
			}
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				_, err := ParseArgs(args)
				if err == nil {
					t.Fatal("expected metadata output conflict")
				}
				var failure ValidationFailure
				if !errors.As(err, &failure) || failure.Reason != ReasonMetadataOutputConflict {
					t.Fatalf("error = %#v, want typed metadata conflict", err)
				}
			})
		}
	}
}

func TestMetadataComposesWithMembershipFiltersAndThen(t *testing.T) {
	args := []string{
		"src", "--no-ignore", "--only", "*.go", "--exclude", "*_test.go",
		"--recent", "5", "--size", "0", "100", "--depth", "3",
		"--contains", "TODO", "--not-contains", "DONE", "--changed",
		"--then", "docs", "--untracked", "--metadata", "--print",
	}
	cfg, err := ParseArgs(args)
	if err != nil {
		t.Fatalf("ParseArgs() error = %v", err)
	}
	if cfg.PayloadKind != command.PayloadMetadata || cfg.OutputMode != command.OutputModeStdout || len(cfg.Command.Scopes()) != 2 {
		t.Fatalf("metadata membership config = %#v", cfg)
	}
	if got := FormatResolvedStartupCommand(args); strings.Count(got, "--metadata") != 1 || !strings.Contains(got, "--then") {
		t.Fatalf("canonical metadata command = %q", got)
	}
}

func TestRepeatedNoRendersOnceAsGlobalPolicy(t *testing.T) {
	got := FormatResolvedStartupCommand([]string{"src", "--no", "--then", "docs", "--no"})
	if count := strings.Count(got, "--no"); count != 1 {
		t.Fatalf("resolved command contains --no %d times, want once: %q", count, got)
	}
	if !strings.HasPrefix(got, "catclip --no ") {
		t.Fatalf("resolved command did not hoist --no globally: %q", got)
	}
}

func TestParseArgsNoCanRemainARegexValue(t *testing.T) {
	cfg, err := ParseArgs([]string{"src", "--contains", "--no"})
	if err != nil {
		t.Fatalf("ParseArgs() error = %v", err)
	}
	if cfg.EmissionPolicy != command.EmissionDefault {
		t.Fatalf("EmissionPolicy = %v, want default", cfg.EmissionPolicy)
	}
	scope := cfg.Command.Scopes()[0]
	if got := scope.ContainsPattern(); got != "--no" {
		t.Fatalf("contains pattern = %q, want --no", got)
	}
}

// TestStrictAndPreflightAgreeOnValueConsumption is the phase-4
// differential pin: the strict parser (ParseArgs) and the preflight
// parser (StartupPreflightCommandSpec) now share consumption helpers
// for --recent/--size/--depth and one equals-form rejector, so for
// these vectors they must make the same accept/reject decision with
// the same error text. A divergence means someone edited one parser's
// path without the shared helper.
func TestStrictAndPreflightAgreeOnValueConsumption(t *testing.T) {
	vectors := [][]string{
		{"src", "--metadata"},
		{"src", "--metadata", "--paths"},
		{"src", "--snippet", "TODO", "--metadata"},
		{"src", "--then", "docs", "--lines", "1", "2", "--metadata"},
		{"src", "--metadata", "--raw"},
		{"src", "--preview"},
		{"src", "--recent"},
		{"src", "--recent", "5"},
		{"src", "--recent", "0"},
		{"src", "--recent", "x"},
		{"src", "--recent", "--paths"},
		{"src", "--recent", "--diff"},
		{"src", "--depth", "2"},
		{"src", "--depth", "0"},
		{"src", "--depth"},
		{"src", "--depth", "--paths"},
		{"src", "--size"},
		{"src", "--size", "10"},
		{"src", "--size", "10", "100"},
		{"src", "--size", "x"},
		{"src", "--size", "--diff"},
		{"src", "--recent=5"},
		{"src", "--size=10"},
		{"src", "--depth=2"},
		{"src", "--contains=TODO"},
		{"src", "--not-contains=TODO"},
		{"src", "--snippet=TODO"},
		{"src", "--include=x"},
		{"src", "--wat"},
		{"src", "--snippet", "TODO", "201"},
		{"src", "--contains", "--no"},
		{"src", "--lines", "0"},
		{"src", "--lines", "5", "4"},
		{"src", "--lines", "last"},
	}
	for _, args := range vectors {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, strictErr := ParseArgs(args)
			_, preflightErr := StartupPreflightCommandSpec(args)
			if (strictErr == nil) != (preflightErr == nil) {
				t.Fatalf("accept/reject diverges:\n  strict:    %v\n  preflight: %v", strictErr, preflightErr)
			}
			if strictErr == nil {
				return
			}

			var strictFailure, preflightFailure ValidationFailure
			strictTyped := errors.As(strictErr, &strictFailure)
			preflightTyped := errors.As(preflightErr, &preflightFailure)
			if strictTyped != preflightTyped {
				t.Fatalf("structured error ownership diverges:\n  strict:    %T %v\n  preflight: %T %v", strictErr, strictErr, preflightErr, preflightErr)
			}
			if strictTyped {
				if strictFailure != preflightFailure {
					t.Fatalf("validation fields diverge:\n  strict:    %#v\n  preflight: %#v", strictFailure, preflightFailure)
				}
				return
			}
			if strictErr.Error() != preflightErr.Error() {
				t.Fatalf("untyped error text diverges:\n  strict:    %v\n  preflight: %v", strictErr, preflightErr)
			}
		})
	}
}
