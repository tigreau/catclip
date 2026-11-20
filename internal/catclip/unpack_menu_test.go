package catclip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/ui"
	"github.com/tigreau/catclip/internal/unpack"
)

const unpackMenuTestBundle = "<file path=\"hello.txt\">\nhello\n</file>\n"

// unpackMenuTestProject moves into an empty project folder and returns a
// bundle file kept outside it.
func unpackMenuTestProject(t *testing.T) (dir, bundle string) {
	t.Helper()
	dir = t.TempDir()
	bundle = filepath.Join(t.TempDir(), "reply.txt")
	if err := os.WriteFile(bundle, []byte(unpackMenuTestBundle), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir, bundle
}

func TestUnpackMenuRootQuestionNoGoesBackOnlyFromTheMenu(t *testing.T) {
	dir, bundle := unpackMenuTestProject(t)
	oldCan, oldAsk := unpackCanAsk, unpackPromptYesNo
	t.Cleanup(func() { unpackCanAsk, unpackPromptYesNo = oldCan, oldAsk })
	unpackCanAsk = func() bool { return true }
	unpackPromptYesNo = func(string, bool, io.Writer) (bool, error) { return false, nil }

	var out, errOut bytes.Buffer
	err := runUnpack(cli.UnpackOptions{Path: bundle, CanGoBack: true}, &out, &errOut)
	if !errors.Is(err, discovery.ErrSelectionCancelled) {
		t.Fatalf("from the menu, n should go back, got %v", err)
	}
	err = runUnpack(cli.UnpackOptions{Path: bundle}, &out, &errOut)
	if err == nil || errors.Is(err, discovery.ErrSelectionCancelled) || !strings.Contains(err.Error(), "Unpack cancelled") {
		t.Fatalf("on the command line, n should cancel, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "hello.txt")); statErr == nil {
		t.Fatal("nothing should have been written")
	}
}

func TestUnpackMenuPickerEscGoesBackOnlyFromTheMenu(t *testing.T) {
	dir, bundle := unpackMenuTestProject(t)
	oldCan, oldAsk, oldChoose := unpackCanAsk, unpackPromptYesNo, unpackChooseChanges
	t.Cleanup(func() { unpackCanAsk, unpackPromptYesNo, unpackChooseChanges = oldCan, oldAsk, oldChoose })
	unpackCanAsk = func() bool { return true }
	unpackPromptYesNo = func(string, bool, io.Writer) (bool, error) { return true, nil }
	unpackChooseChanges = func(unpack.Plan) ([]int, error) { return nil, discovery.ErrSelectionCancelled }

	var out, errOut bytes.Buffer
	err := runUnpack(cli.UnpackOptions{Path: bundle, CanGoBack: true}, &out, &errOut)
	if !errors.Is(err, discovery.ErrSelectionCancelled) {
		t.Fatalf("from the menu, Esc in the picker should go back, got %v", err)
	}
	err = runUnpack(cli.UnpackOptions{Path: bundle}, &out, &errOut)
	if err == nil || errors.Is(err, discovery.ErrSelectionCancelled) || !strings.Contains(err.Error(), "Unpack cancelled") {
		t.Fatalf("on the command line, Esc should cancel, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "hello.txt")); statErr == nil {
		t.Fatal("nothing should have been written")
	}
}

func TestUnpackMenuPreviewSummarisesAFile(t *testing.T) {
	_, bundle := unpackMenuTestProject(t)
	// The preview is colored with the theme; compare the plain text.
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	if err := runUnpackMenuPreview([]string{"file", bundle}, &buf); err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	for _, want := range []string{"0 changed · 1 new · 0 already identical", "hello.txt", "never deletes"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preview lacks %q:\n%s", want, text)
		}
	}
}

func TestUnpackMenuPreviewNamesTheLikelyCauseForAClipboardWithoutABundle(t *testing.T) {
	unpackMenuTestProject(t)
	old := unpackReadClipboard
	t.Cleanup(func() { unpackReadClipboard = old })
	unpackReadClipboard = func(platform.Palette) (output.ClipboardContent, error) {
		return output.ClipboardContent{Text: "just some chat text"}, nil
	}
	var buf bytes.Buffer
	if err := runUnpackMenuPreview([]string{"clipboard"}, &buf); err != nil {
		t.Fatal(err)
	}
	// Menu wording: use [reply format] and [file], never the command line's
	// "run catclip --unpack ..." (user, 2026-11-19).
	got := buf.String()
	if !strings.Contains(got, "This doesn't look like a catclip reply.") || !strings.Contains(got, "use [reply format]") ||
		!strings.Contains(got, "use [file]") || strings.Contains(got, "catclip --unpack") {
		t.Fatalf("preview:\n%s", got)
	}

	// An empty clipboard: the same, and the command line keeps its advice.
	unpackReadClipboard = func(platform.Palette) (output.ClipboardContent, error) {
		return output.ClipboardContent{}, nil
	}
	buf.Reset()
	if err := runUnpackMenuPreview([]string{"clipboard"}, &buf); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "The clipboard is empty.") || !strings.Contains(got, "come back") ||
		!strings.Contains(got, "use [file]") || strings.Contains(got, "catclip --unpack") {
		t.Fatalf("empty clipboard preview:\n%s", got)
	}
	err := runUnpack(cli.UnpackOptions{Yes: true}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "Error: The clipboard is empty.\n  Copy the agent's whole reply, then run catclip --unpack again.\n  To use a bundle file instead: catclip --unpack FILE" {
		t.Fatalf("command-line message changed: %v", err)
	}
	if summary, ok := unpackClipboardSummary(); ok {
		t.Fatalf("no bundle on the clipboard, got summary %q", summary)
	}
}

func TestUnpackMenuSessionEndedShowsTheUnpackCommand(t *testing.T) {
	var buf bytes.Buffer
	done := fmt.Errorf("wrapped: %w", &ui.StartupUnpackDone{Args: []string{"--unpack"}})
	ended, err := unpackMenuSessionEnded(done, &buf)
	if err != nil || !ended {
		t.Fatalf("ended=%v err=%v", ended, err)
	}
	if !strings.Contains(buf.String(), "Resolved command:") || !strings.Contains(buf.String(), "--unpack") {
		t.Fatalf("output:\n%s", buf.String())
	}
	if ended, _ := unpackMenuSessionEnded(errors.New("other"), &buf); ended {
		t.Fatal("other errors must not end the session quietly")
	}
}
