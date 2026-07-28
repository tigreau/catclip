package cli

import (
	"strings"
	"testing"
)

func TestValidateDirectPathPatternsRejectsAdjacentStars(t *testing.T) {
	for _, tt := range []struct {
		surface string
		values  []string
		want    string
	}{
		{surface: "--only", values: []string{"*.go", "src/**/*.go"}, want: "src/**/*.go"},
		{surface: "--exclude", values: []string{"build/**"}, want: "build/**"},
		{surface: "target", values: []string{"cmd/**"}, want: "cmd/**"},
	} {
		err := validateDirectPathPatterns(tt.surface, tt.values, false)
		if err == nil {
			t.Fatalf("%s %v succeeded, want doublestar rejection", tt.surface, tt.values)
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s %v error = %q, want offending value %q", tt.surface, tt.values, err, tt.want)
		}
	}
}

func TestValidateDirectPathPatternsPreservesExactAndOtherLanguages(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		exact  bool
	}{
		{name: "single star filter", values: []string{"src/*.tsx"}},
		{name: "stdin sentinel", values: []string{"-"}},
		{name: "exact stdin row", values: []string{"literal**name.txt"}, exact: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateDirectPathPatterns("--only", tt.values, tt.exact); err != nil {
				t.Fatalf("validateDirectPathPatterns(%v, exact=%v): %v", tt.values, tt.exact, err)
			}
		})
	}
}

func TestUnsupportedDoubleStarGuidanceSimplifiesAllStarPatterns(t *testing.T) {
	tests := []struct {
		name    string
		surface string
		value   string
		want    string
		forbid  string
	}{
		{
			name:    "root target",
			surface: "target",
			value:   "**",
			want:    "catclip .",
			forbid:  "--only",
		},
		{
			name:    "directory target",
			surface: "target",
			value:   "cmd/**",
			want:    "catclip cmd",
			forbid:  "--only",
		},
		{
			name:    "root only filter",
			surface: "--only",
			value:   "**",
			want:    "remove the --only stage",
			forbid:  "catclip . --only '*'",
		},
		{
			name:    "directory only filter",
			surface: "--only",
			value:   "cmd/**",
			want:    "catclip cmd",
			forbid:  "--only '*'",
		},
		{
			name:    "constrained target",
			surface: "target",
			value:   "cmd/**/*.go",
			want:    "catclip cmd --only '*.go'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderUnsupportedDoubleStarValidationFailure(tt.surface, tt.value)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("guidance for %s %q missing %q:\n%s", tt.surface, tt.value, tt.want, got)
			}
			if tt.forbid != "" && strings.Contains(got, tt.forbid) {
				t.Fatalf("guidance for %s %q contains redundant %q:\n%s", tt.surface, tt.value, tt.forbid, got)
			}
		})
	}
}

func TestStartupPreflightRejectsDoubleStarBeforePicker(t *testing.T) {
	for _, args := range [][]string{
		{"src/**/*.tsx"},
		{"src", "--only", "**/*.tsx"},
		{"src", "--exclude", "build/**"},
		{"src", "--only", "*.tsx", "--then", "docs", "--exclude", "***"},
	} {
		err := ValidateStartupPreflightArgs(args)
		if err == nil || !strings.Contains(err.Error(), "do not support '**'") {
			t.Fatalf("ValidateStartupPreflightArgs(%v) = %v, want doublestar rejection", args, err)
		}
	}
}
