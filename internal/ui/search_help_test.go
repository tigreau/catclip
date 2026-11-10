package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

func TestContentPickerHelpUsesAuthoredThemeRendering(t *testing.T) {
	for _, kind := range []command.StageKind{command.StageSnippet, command.StageContains, command.StageNotContains} {
		scope := command.ExecutionScope{Stages: []command.Stage{{Kind: kind}}}
		if !internalPreviewPatternIsEmpty(scope) {
			t.Fatalf("%s empty query did not select help", kind)
		}
		doc := buildInternalContentHintDocument(scope)
		if doc.File.HighlightPath != renderpkg.SearchHelpHighlight || doc.File.Path != "" {
			t.Fatalf("%s help still masquerades as source", kind)
		}
		if kind == command.StageNotContains && (!strings.Contains(doc.File.Content, "Excludes files with a match anywhere") || strings.Contains(doc.File.Content, "Lists every matching line")) {
			t.Fatal("not-contains help describes contains behavior")
		}
		for _, colors := range []platform.Palette{{}, platform.ANSIPalette()} {
			var got bytes.Buffer
			if err := renderTreeDocument(&got, doc, FzfFilterTreeRenderOptions(), colors); err != nil {
				t.Fatal(err)
			}
			plain := string(ansiEscape.ReplaceAll(got.Bytes(), nil))
			for _, want := range []string{"PCRE2 regex", "Smart-case:", `\s`, "Symbols:"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("%s help lost %q", kind, want)
				}
			}
			if colors.Reset == "" && strings.ContainsRune(got.String(), '\x1b') {
				t.Fatal("disabled help colors gained escapes")
			}
		}
	}
}
