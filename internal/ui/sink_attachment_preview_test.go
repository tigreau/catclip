package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
)

func attachmentPreviewPlan(t *testing.T) output.Plan {
	t.Helper()
	root := t.TempDir()
	write := func(name string, body []byte) discovery.Entry {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return discovery.Entry{AbsPath: path, RelPath: name}
	}
	plan, err := output.PreparePlan(git.Context{}, []discovery.Entry{
		write("main.go", []byte("package main\n")),
		write("logo.png", []byte{0x89, 'P', 'N', 'G', 0, 1, 2}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// The output picker must show what each choice really copies.
func TestSinkPreviewTextMatchesTheFocusedChoice(t *testing.T) {
	plan := attachmentPreviewPlan(t)
	ctx := StartupSinkPickerContext{Plan: plan, Emit: output.EmitConfig{OutputMode: command.OutputModeClipboard}}
	if !sinkPreviewNeedsClipboardVariant(ctx) {
		t.Fatal("a clipboard copy with a binary needs its own preview")
	}
	clipboard, err := renderSinkPreviewWithModeContext(context.Background(), ctx, sinkPreviewModeClipboardText, output.PreviewByteLimit)
	if err != nil {
		t.Fatal(err)
	}
	printed, err := renderSinkPreviewWithModeContext(context.Background(), ctx, sinkPreviewModeOutputText, output.PreviewByteLimit)
	if err != nil {
		t.Fatal(err)
	}
	clip, text := stripANSIForTest(string(clipboard.Body)), stripANSIForTest(string(printed.Body))
	if !strings.Contains(clip, `Attached separately as the original file "logo.png".`) || strings.Contains(clip, "Binary contents omitted.") {
		t.Fatalf("clipboard preview:\n%s", clip)
	}
	if !strings.Contains(text, "Binary contents omitted.") || strings.Contains(text, "Attached separately") {
		t.Fatalf("printed preview:\n%s", text)
	}
	textOnly := ctx
	textOnly.Emit.NoBundle = true
	if sinkPreviewNeedsClipboardVariant(textOnly) {
		t.Fatal("--no-bundle attaches nothing")
	}
}

func TestSinkFocusPicksTheMatchingOutputPreview(t *testing.T) {
	dir := t.TempDir()
	modePath, outputPath, treePath := filepath.Join(dir, "mode"), filepath.Join(dir, "output.txt"), filepath.Join(dir, "tree.txt")
	for path, body := range map[string]string{modePath: "output\n", outputPath: "PRINTED", sinkClipboardOutputPath(outputPath): "CLIPBOARD", treePath: "TREE"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	preview := func() string {
		var out bytes.Buffer
		if err := RunInternalSinkPreview(modePath, outputPath, treePath, &out); err != nil {
			t.Fatal(err)
		}
		return stripANSIForTest(out.String())
	}
	for _, step := range []struct{ choice, want string }{{"bundle", "CLIPBOARD"}, {"stdout", "PRINTED"}, {"headless", "PRINTED"}, {"text", "PRINTED"}, {"clipboard", "CLIPBOARD"}} {
		if err := RunInternalSinkFocus(modePath, "", step.choice, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		if got := preview(); !strings.Contains(got, step.want) {
			t.Fatalf("focus %s: preview %q, want %s", step.choice, got, step.want)
		}
	}
}

func TestSinkChoicesDescribeAttachmentsTruthfully(t *testing.T) {
	labels := func(choices []startupSinkChoice) string {
		var parts []string
		for _, c := range choices {
			parts = append(parts, c.Key+"="+c.Label+": "+c.Description)
		}
		return strings.Join(parts, "\n")
	}
	mixed := labels(startupSinkChoicesWithAttachments(command.Parsed{}, 2, true))
	for _, want := range []string{"bundle=Clipboard - bundle + 2 binary files", "each selected binary as its original file", "text=Clipboard - text only", "binaries appear as descriptions, not files", "stdout=", "headless="} {
		if !strings.Contains(mixed, want) {
			t.Fatalf("missing %q in:\n%s", want, mixed)
		}
	}
	if strings.Contains(mixed, "Clipboard - bundle as file") {
		t.Fatalf("plain bundle choice must be replaced:\n%s", mixed)
	}
	only := labels(startupSinkChoicesWithAttachments(command.Parsed{}, 1, false))
	if !strings.Contains(only, "bundle=Clipboard - 1 binary file: Copy each selected binary as its original file.") {
		t.Fatalf("binary-only:\n%s", only)
	}
}
