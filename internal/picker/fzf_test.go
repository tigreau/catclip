package picker

import (
	"testing"
)

func TestBuildArgsUsesLargerLabeledPreviewPane(t *testing.T) {
	args := buildArgs(Request{
		Query:          "src",
		Prompt:         "select> ",
		WithNth:        "1,2",
		PreviewCommand: "cat preview",
		ColorSpecs:     []string{"prompt:6", "preview-bg:0"},
		Exact:          true,
	})

	if !containsArgPair(args, "--preview-window", defaultPreviewWindow) {
		t.Fatalf("expected preview window %q, got %#v", defaultPreviewWindow, args)
	}
	if !containsArgPair(args, "--preview-label", defaultPreviewLabel) {
		t.Fatalf("expected preview label %q, got %#v", defaultPreviewLabel, args)
	}
	if !containsArgPair(args, "--color", "prompt:6") {
		t.Fatalf("expected prompt color override, got %#v", args)
	}
	if !containsArgPair(args, "--color", "preview-bg:0") {
		t.Fatalf("expected preview background override, got %#v", args)
	}
	if !containsArg(args, "--exact") {
		t.Fatalf("expected exact matching flag, got %#v", args)
	}
}

func TestParseMatchesFallsBackToFirstFieldWhenSecondFieldIsEmpty(t *testing.T) {
	got := parseMatches("[all current matches]\t\t\t\nmain.ts\tsrc/main.ts\tfile\ttext\n")
	want := []string{"[all current matches]", "src/main.ts"}
	if len(got) != len(want) {
		t.Fatalf("expected %d matches, got %#v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected matches %#v, got %#v", want, got)
		}
	}
}

func containsArgPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
