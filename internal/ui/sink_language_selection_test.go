package ui

import (
	"regexp"
	"strings"
	"testing"
)

func TestSinkPreviewConservativeLanguageSelection(t *testing.T) {
	for _, tc := range []struct {
		path, body string
		colored    bool
	}{
		{".mailmap", "var load tool <someone@example.invalid>\n", false},
		{".gitignore", "preload/\ntools/\n", false},
		{"notes.unknown", "func _ready():\n    var value = load('x')\n", false},
		{".clang-format", "IndentWidth: 4\n", true},
		{"entrypoint.unknown", "#!/bin/sh\necho hello\n", true},
		{"main.go", "package main\nfunc main() {}\n", true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			raw := "<file path=\"" + tc.path + "\">\n" + tc.body + "</file>\n"
			got := string(highlightFileBlocksForSinkPreview([]byte(raw)))
			if strings.Contains(got, "\x1b[") != tc.colored {
				t.Fatalf("unexpected syntax coloring: %q", got)
			}
			stripANSI := regexp.MustCompile(`\x1b\[[0-9;]*m`)
			if stripANSI.ReplaceAllString(got, "") != raw {
				t.Fatal("preview changed payload text or wrappers")
			}
		})
	}
}
