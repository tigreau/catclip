package picker

import (
	"slices"
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

func TestBuildArgsEmitsPreviewWindowWithoutPreviewCommand(t *testing.T) {
	args := buildArgs(Request{
		Query:         "src",
		Prompt:        "select> ",
		WithNth:       "1,2",
		PreviewWindow: DefaultPreviewWindow,
		Bindings:      []string{"start:preview(echo hi)"},
	})

	if !containsArgPair(args, "--preview-window", DefaultPreviewWindow) {
		t.Fatalf("expected preview window %q, got %#v", DefaultPreviewWindow, args)
	}
	if !containsArgPair(args, "--preview-label", defaultPreviewLabel) {
		t.Fatalf("expected preview label %q, got %#v", defaultPreviewLabel, args)
	}
	if containsArg(args, "--preview") {
		t.Fatalf("expected no --preview when PreviewCommand is empty, got %#v", args)
	}
	if !containsArgPair(args, "--bind", "start:preview(echo hi)") {
		t.Fatalf("expected start:preview binding, got %#v", args)
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

func TestFilterArgsUseRequestedSearchFields(t *testing.T) {
	args := filterArgs("doc", "2")
	if !containsArgPair(args, "--nth", "2") {
		t.Fatalf("expected path-only --nth field, got %#v", args)
	}
	if !containsArgPair(args, "--filter", "doc") {
		t.Fatalf("expected filter query, got %#v", args)
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
	return slices.Contains(args, want)
}
