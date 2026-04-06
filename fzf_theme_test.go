package catclip

import "testing"

func TestFzfColorSpecsUseCatclipPaletteMappings(t *testing.T) {
	specs := fzfColorSpecs(ansiColorPalette(), true)
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
	if got := fzfColorSpecs(colorPalette{}, true); len(got) != 0 {
		t.Fatalf("expected no color specs when palette is disabled, got %#v", got)
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
