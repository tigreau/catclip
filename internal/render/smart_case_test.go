package render

import (
	"reflect"
	"testing"
)

func TestIsSmartCaseInsensitive(t *testing.T) {
	tests := []struct {
		pattern  string
		expected bool
	}{
		{"todo", true},
		{"TODO", false},
		{"Config", false},
		{"config", true},
		{"[a-z]+", true},
		{"(?i)foo", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got := isSmartCaseInsensitive(tt.pattern)
			if got != tt.expected {
				t.Errorf("isSmartCaseInsensitive(%q) = %v, want %v", tt.pattern, got, tt.expected)
			}
		})
	}
}

func TestPreviewHighlightedLineNumbers_SmartCase(t *testing.T) {
	rawLines := []string{
		"This is a config file.",
		"It contains Config settings.",
		"And some CONFIG keys.",
	}

	tests := []struct {
		name     string
		pattern  string
		expected map[int]struct{}
	}{
		{
			name:    "Lowercase pattern matches all cases",
			pattern: "config",
			expected: map[int]struct{}{
				1: {},
				2: {},
				3: {},
			},
		},
		{
			name:    "Uppercase pattern matches exact case",
			pattern: "Config",
			expected: map[int]struct{}{
				2: {},
			},
		},
		{
			name:    "Explicit ignore case matches all",
			pattern: "(?i)Config",
			expected: map[int]struct{}{
				1: {},
				2: {},
				3: {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// focusLines is empty for this test
			got := previewHighlightedLineNumbers(rawLines, tt.pattern, nil)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("previewHighlightedLineNumbers(%q) = %v, want %v", tt.pattern, got, tt.expected)
			}
		})
	}
}
