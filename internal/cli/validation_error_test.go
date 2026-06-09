package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestRequiredStageValueErrorCarriesTypedReasonAndCurrentMessage(t *testing.T) {
	tests := []struct {
		flag string
		want string
	}{
		{
			flag: "--only",
			want: "Error: --only requires a pattern.\n  Example: catclip src --only '*.ts'",
		},
		{
			flag: "--exclude",
			want: "Error: --exclude requires a pattern.\n  Example: catclip src --exclude '*.test.*'",
		},
		{
			flag: "--include",
			want: "Error: --include requires a target query.\n  Example: catclip . --include node_modules\n  Example: catclip src --include vendor",
		},
		{
			flag: "--snippet",
			want: "Error: --snippet requires a regex pattern.\n  Example: catclip src --snippet 'TODO'",
		},
		{
			flag: "--depth",
			want: "Error: --depth requires a positive integer.\n  Example: catclip src --depth 2",
		},
	}

	for _, tt := range tests {
		err := RequiredStageValueError(tt.flag)
		var got ValidationFailure
		if !errors.As(err, &got) {
			t.Fatalf("%s: expected ValidationFailure, got %T", tt.flag, err)
		}
		if got.Reason != ReasonRequiredValue {
			t.Fatalf("%s: reason = %q, want %q", tt.flag, got.Reason, ReasonRequiredValue)
		}
		if got.Flag != tt.flag {
			t.Fatalf("%s: flag = %q", tt.flag, got.Flag)
		}
		if err.Error() != tt.want {
			t.Fatalf("%s: message mismatch\n got: %q\nwant: %q", tt.flag, err.Error(), tt.want)
		}
	}
}

func TestContainsMissingPatternErrorCarriesSuggestion(t *testing.T) {
	err := ContainsMissingPatternError([]string{"--contains", "--snippet", "a"}, 0)

	var got ValidationFailure
	if !errors.As(err, &got) {
		t.Fatalf("expected ValidationFailure, got %T", err)
	}
	if got.Reason != ReasonRequiredValue {
		t.Fatalf("reason = %q, want %q", got.Reason, ReasonRequiredValue)
	}
	if got.Flag != "--contains" {
		t.Fatalf("flag = %q, want %q", got.Flag, "--contains")
	}
	if got.Suggestion == "" {
		t.Fatal("expected contains suggestion to be carried")
	}
	want := "Error: --contains requires a regex pattern.\n  Example: catclip src --contains 'TODO'\n  Did you mean: catclip . --snippet 'a'"
	if err.Error() != want {
		t.Fatalf("message mismatch\n got: %q\nwant: %q", err.Error(), want)
	}
}

func TestScopeOrderHelpersReturnTypedValidationFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantReason Reason
	}{
		{
			name:       "diff content order",
			err:        diffContentFilterOrderError("--contains", "--changed-diff"),
			wantReason: ReasonDiffContentFilterOrder,
		},
		{
			name:       "diff git order",
			err:        diffGitFilterOrderError("--staged", "--changed-diff"),
			wantReason: ReasonDiffGitFilterOrder,
		},
		{
			name:       "snippet order",
			err:        snippetContentFilterOrderError("--contains"),
			wantReason: ReasonSnippetContentFilterOrder,
		},
		{
			name:       "output conflict",
			err:        outputModeConflictError("--snippet", "--changed-diff"),
			wantReason: ReasonOutputModeConflict,
		},
		{
			name:       "diff snippet conflict",
			err:        diffSnippetConflictError(),
			wantReason: ReasonDiffSnippetConflict,
		},
	}

	for _, tt := range tests {
		var got ValidationFailure
		if !errors.As(tt.err, &got) {
			t.Fatalf("%s: expected ValidationFailure, got %T", tt.name, tt.err)
		}
		if got.Reason != tt.wantReason {
			t.Fatalf("%s: reason = %q, want %q", tt.name, got.Reason, tt.wantReason)
		}
	}
}

func TestIncludeOrderingErrorsReturnTypedValidationFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantReason Reason
		wantMsg    string
	}{
		{
			name:       "include after modifier",
			err:        includeAfterModifierError(),
			wantReason: ReasonIncludeAfterModifier,
			wantMsg:    "--include must come before other modifiers",
		},
		{
			name:       "repeated include",
			err:        repeatedIncludeError(),
			wantReason: ReasonRepeatedInclude,
			wantMsg:    "--include can only appear once per scope",
		},
	}

	for _, tt := range tests {
		var got ValidationFailure
		if !errors.As(tt.err, &got) {
			t.Fatalf("%s: expected ValidationFailure, got %T", tt.name, tt.err)
		}
		if got.Reason != tt.wantReason {
			t.Fatalf("%s: reason = %q, want %q", tt.name, got.Reason, tt.wantReason)
		}
		if msg := tt.err.Error(); !strings.Contains(msg, tt.wantMsg) {
			t.Fatalf("%s: message %q missing %q", tt.name, msg, tt.wantMsg)
		}
	}
}
