package picker

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const pickerBenchHelperEnv = "CATCLIP_TEST_PICKER_BENCH_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(pickerBenchHelperEnv) == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		fmt.Fprintln(os.Stdout, "chosen\tchosen/path.ts")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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

func TestBuildArgsEmitsOptionalFooter(t *testing.T) {
	args := buildArgs(Request{
		Prompt:       "filter> ",
		WithNth:      "1,3",
		Footer:       "Filters",
		FooterBorder: "rounded",
	})

	if !containsArgPair(args, "--footer", "Filters") {
		t.Fatalf("expected footer text, got %#v", args)
	}
	if !containsArgPair(args, "--footer-border", "rounded") {
		t.Fatalf("expected rounded footer border, got %#v", args)
	}
}

func TestBuildArgsOmitsFooterOptionsWhenFooterIsEmpty(t *testing.T) {
	args := buildArgs(Request{
		Prompt:       "select> ",
		WithNth:      "1,2",
		FooterBorder: "rounded",
	})

	if containsArg(args, "--footer") || containsArg(args, "--footer-border") {
		t.Fatalf("expected no footer options, got %#v", args)
	}
}

func TestRevealHeaderAfterQueryChangeBindingRunsOnce(t *testing.T) {
	binding := RevealHeaderAfterQueryChangeBinding("line one\nline two")
	if !strings.HasPrefix(binding, "change:change-header{") {
		t.Fatalf("expected native change-header action, got %q", binding)
	}
	if !strings.HasSuffix(binding, "}+unbind(change)") {
		t.Fatalf("expected reveal binding to remove itself, got %q", binding)
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

func TestPickerBenchFieldsIdentifyPromptAndPreviewModeWithoutQuery(t *testing.T) {
	fields := pickerBenchFields(Request{
		Query:          "private search text",
		Prompt:         "only> ",
		PreviewCommand: "catclip --internal-tree-preview private/path",
		Bindings:       []string{"change:reload(private command)"},
		Lines:          []string{"private row"},
		Multi:          true,
		NoSort:         true,
	})
	joined := strings.Join(fields, " ")
	for _, want := range []string{"prompt only>", "preview_mode focus", "lines 1", "multi true", "no_sort true"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("bench fields missing %q in %#v", want, fields)
		}
	}
	for _, private := range []string{"private search text", "private/path", "private command", "private row"} {
		if strings.Contains(joined, private) {
			t.Fatalf("bench fields leaked %q in %#v", private, fields)
		}
	}
}

func TestPickerBenchFieldsDistinguishBindingPreview(t *testing.T) {
	fields := pickerBenchFields(Request{PreviewWindow: DefaultPreviewWindow})
	joined := strings.Join(fields, " ")
	if !strings.Contains(joined, "preview_mode binding") {
		t.Fatalf("expected binding preview mode, got %#v", fields)
	}
}

func TestRunBenchModeLogsFzfLifecycleWithReexecHelper(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "picker-bench.log")
	t.Setenv(pickerBenchHelperEnv, "1")
	t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", logPath)

	result, err := Run(os.Args[0], Request{
		Prompt:  "only> ",
		WithNth: "1",
		Nth:     "1",
		Lines:   []string{"chosen\tchosen/path.ts"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Matches) != 1 || result.Matches[0] != "chosen/path.ts" {
		t.Fatalf("unexpected result: %#v", result)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read bench log: %v", err)
	}
	log := string(data)
	for _, want := range []string{
		`event="picker.fzf.prepare"`,
		`event="picker.fzf.ready"`,
		`event="picker.fzf.start"`,
		`event="picker.fzf.run"`,
		`prompt="only>"`,
		`preview_mode="none"`,
		`input_bytes="22"`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("bench log missing %q:\n%s", want, log)
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
	return slices.Contains(args, want)
}
