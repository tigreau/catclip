package catclip

import (
	"errors"
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
			want: "Error: --include requires a target query.\n  Example: catclip --include node_modules\n  Example: catclip src --include .env",
		},
		{
			flag: "--snippet",
			want: "Error: --snippet requires a regex pattern.\n  Example: catclip src --snippet 'TODO'",
		},
	}

	for _, tt := range tests {
		err := requiredStageValueError(tt.flag)
		var got validationFailure
		if !errors.As(err, &got) {
			t.Fatalf("%s: expected validationFailure, got %T", tt.flag, err)
		}
		if got.Reason != validationReasonRequiredValue {
			t.Fatalf("%s: reason = %q, want %q", tt.flag, got.Reason, validationReasonRequiredValue)
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
	err := containsMissingPatternError([]string{"--contains", "--snippet", "a"}, 0)

	var got validationFailure
	if !errors.As(err, &got) {
		t.Fatalf("expected validationFailure, got %T", err)
	}
	if got.Reason != validationReasonRequiredValue {
		t.Fatalf("reason = %q, want %q", got.Reason, validationReasonRequiredValue)
	}
	if got.Flag != "--contains" {
		t.Fatalf("flag = %q, want %q", got.Flag, "--contains")
	}
	if got.Suggestion == "" {
		t.Fatal("expected contains suggestion to be carried")
	}
	want := "Error: --contains requires a regex pattern.\n  Example: catclip src --contains 'TODO'\n  Did you mean: catclip . --snippet a"
	if err.Error() != want {
		t.Fatalf("message mismatch\n got: %q\nwant: %q", err.Error(), want)
	}
}

func TestScopeOrderHelpersReturnTypedValidationFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantReason validationReason
	}{
		{
			name:       "diff content order",
			err:        diffContentFilterOrderError("--contains", "--changed-diff"),
			wantReason: validationReasonDiffContentFilterOrder,
		},
		{
			name:       "diff git order",
			err:        diffGitFilterOrderError("--staged", "--changed-diff"),
			wantReason: validationReasonDiffGitFilterOrder,
		},
		{
			name:       "snippet order",
			err:        snippetContentFilterOrderError("--contains"),
			wantReason: validationReasonSnippetContentFilterOrder,
		},
		{
			name:       "output conflict",
			err:        outputModeConflictError("--snippet", "--changed-diff"),
			wantReason: validationReasonOutputModeConflict,
		},
		{
			name:       "diff snippet conflict",
			err:        diffSnippetConflictError(),
			wantReason: validationReasonDiffSnippetConflict,
		},
	}

	for _, tt := range tests {
		var got validationFailure
		if !errors.As(tt.err, &got) {
			t.Fatalf("%s: expected validationFailure, got %T", tt.name, tt.err)
		}
		if got.Reason != tt.wantReason {
			t.Fatalf("%s: reason = %q, want %q", tt.name, got.Reason, tt.wantReason)
		}
	}
}
