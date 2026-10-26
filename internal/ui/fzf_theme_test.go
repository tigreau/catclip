package ui

import (
	"testing"

	"github.com/tigreau/catclip/internal/platform"
)

func TestFzfColorSpecsUseCatclipPaletteMappings(t *testing.T) {
	specs := fzfColorSpecs(platform.ANSIPalette(), true)
	want := []string{
		"prompt:6",
		"marker:5",
		"hl:4",
		"bg+:8",
		"preview-bg:0",
		"preview-label:6",
	}
	for _, spec := range want {
		if !containsString(specs, spec) {
			t.Fatalf("expected color spec %q in %#v", spec, specs)
		}
	}
}

func TestFzfColorSpecsSkipDisabledPalette(t *testing.T) {
	if got := fzfColorSpecs(platform.Palette{}, true); len(got) != 0 {
		t.Fatalf("expected no color specs when palette is disabled, got %#v", got)
	}
}

func TestMetadataPreviewUsesTerminalDefaultColors(t *testing.T) {
	specs := metadataPreviewColorSpecs(fzfColorSpecs(platform.ANSIPalette(), true))
	for _, want := range []string{"preview-bg:-1", "preview-fg:-1", "preview-label:6", "prompt:6"} {
		if !containsString(specs, want) {
			t.Fatalf("missing %s in %v", want, specs)
		}
	}
	for _, old := range []string{"preview-bg:0", "preview-fg:7"} {
		if containsString(specs, old) {
			t.Fatalf("still overriding terminal defaults: %v", specs)
		}
	}
	if len(metadataPreviewColorSpecs(nil)) != 0 {
		t.Fatal("enabled a disabled theme")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
