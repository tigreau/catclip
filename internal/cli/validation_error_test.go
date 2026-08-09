package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestValidationFailureConstructors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ValidationFailure
	}{
		{name: "required only", err: RequiredStageValueError("--only"), want: ValidationFailure{Reason: ReasonRequiredValue, Flag: "--only"}},
		{name: "required exclude", err: RequiredStageValueError("--exclude"), want: ValidationFailure{Reason: ReasonRequiredValue, Flag: "--exclude"}},
		{name: "required contains", err: RequiredStageValueError("--contains"), want: ValidationFailure{Reason: ReasonRequiredValue, Flag: "--contains"}},
		{name: "required not-contains", err: RequiredStageValueError("--not-contains"), want: ValidationFailure{Reason: ReasonRequiredValue, Flag: "--not-contains"}},
		{name: "required snippet", err: RequiredStageValueError("--snippet"), want: ValidationFailure{Reason: ReasonRequiredValue, Flag: "--snippet"}},
		{name: "required depth", err: RequiredStageValueError("--depth"), want: ValidationFailure{Reason: ReasonRequiredValue, Flag: "--depth"}},
		{
			name: "contains suggestion",
			err:  ContainsMissingPatternError([]string{"--contains", "--snippet", "a"}, 0),
			want: ValidationFailure{Reason: ReasonRequiredValue, Flag: "--contains", Suggestion: "catclip . --snippet 'a'"},
		},
		{name: "diff content order", err: diffContentFilterOrderError("--contains", "--changed-diff"), want: ValidationFailure{Reason: ReasonDiffContentFilterOrder, Flag: "--contains", BoundaryFlag: "--changed-diff"}},
		{name: "diff git order", err: diffGitFilterOrderError("--staged", "--changed-diff"), want: ValidationFailure{Reason: ReasonDiffGitFilterOrder, Flag: "--staged", BoundaryFlag: "--changed-diff"}},
		{name: "snippet order", err: snippetContentFilterOrderError("--contains"), want: ValidationFailure{Reason: ReasonSnippetContentFilterOrder, Flag: "--contains"}},
		{name: "output conflict", err: outputModeConflictError("--snippet", "--changed-diff"), want: ValidationFailure{Reason: ReasonOutputModeConflict, Flag: "--changed-diff", BoundaryFlag: "--snippet"}},
		{name: "diff snippet conflict", err: diffSnippetConflictError(), want: ValidationFailure{Reason: ReasonDiffSnippetConflict}},
		{name: "terminal boundary", err: terminalBoundaryOrderError("--paths", "--contains"), want: ValidationFailure{Reason: ReasonTerminalBoundaryOrder, BoundaryFlag: "--paths", NextFlag: "--contains"}},
		{name: "no ignore missing target", err: NoIgnoreMissingPositionalTargetError(), want: ValidationFailure{Reason: ReasonNoIgnoreMissingPositionalTarget, Flag: "--no-ignore"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got ValidationFailure
			if !errors.As(tt.err, &got) {
				t.Fatalf("expected ValidationFailure, got %T", tt.err)
			}
			if got != tt.want {
				t.Fatalf("validation fields mismatch\n got: %#v\nwant: %#v", got, tt.want)
			}
		})
	}
}

func TestValidationFailureRendering(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		want         string
		wantContains string
	}{
		{name: "required only", err: RequiredStageValueError("--only"), want: "Error: --only requires a pattern.\n  Example: catclip src --only '*.ts'"},
		{name: "required exclude", err: RequiredStageValueError("--exclude"), want: "Error: --exclude requires a pattern.\n  Example: catclip src --exclude '*.test.*'"},
		{name: "required snippet", err: RequiredStageValueError("--snippet"), want: "Error: --snippet requires a regex pattern.\n  Example: catclip src --snippet 'TODO'"},
		{name: "required depth", err: RequiredStageValueError("--depth"), want: "Error: --depth requires a positive integer.\n  Example: catclip src --depth 2"},
		{name: "contains suggestion", err: ContainsMissingPatternError([]string{"--contains", "--snippet", "a"}, 0), want: "Error: --contains requires a regex pattern.\n  Example: catclip src --contains 'TODO'\n  Did you mean: catclip . --snippet 'a'"},
		{name: "include unsupported", err: IncludeUnsupportedError(), want: "Error: --include is not a supported option.\n\n  Name an ignored file or directory as a target:\n    catclip src/generated\n\n  To disable ignore rules below a target:\n    catclip src --no-ignore"},
		{name: "no ignore missing target", err: NoIgnoreMissingPositionalTargetError(), wantContains: "catclip src --no-ignore"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if tt.want != "" && got != tt.want {
				t.Fatalf("message mismatch\n got: %q\nwant: %q", got, tt.want)
			}
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Fatalf("message %q missing %q", got, tt.wantContains)
			}
		})
	}
}

func TestCanonicalValidationConstructors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "unknown option",
			err:  UnknownOptionError("--wat"),
			want: "Error: Unknown option '--wat'\n  Run 'catclip --help' for available options.",
		},
		{
			name: "snippet context",
			err:  ValidateSnippetContext(SnippetContextMax + 1),
			want: "Error: --snippet context must be between 0 and 200 (got 201).\n  Use: --snippet 'REGEX' N for N lines around each match (0 = matching line only).",
		},
		{
			name: "lines start",
			err:  LinesStartError(0),
			want: "Error: --lines start must be >= 1 (got 0).\n  Line numbers are 1-based, matching editors and compiler output.",
		},
		{
			name: "lines ordering",
			err:  LinesEndBeforeStartError(4, 5),
			want: "Error: --lines end (4) must be >= start (5).\n  Use: --lines START END where END >= START.",
		},
		{
			name: "lines value",
			err:  LinesInvalidValueError("last"),
			want: "Error: --lines expects line numbers: --lines [START [END]]\n  START and END must be integers (got \"last\").",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatal("constructor returned nil")
			}
			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("message mismatch\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
	if err := ValidateSnippetContext(0); err != nil {
		t.Fatalf("minimum snippet context rejected: %v", err)
	}
	if err := ValidateSnippetContext(SnippetContextMax); err != nil {
		t.Fatalf("maximum snippet context rejected: %v", err)
	}
}
