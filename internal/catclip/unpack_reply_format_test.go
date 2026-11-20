package catclip

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/unpack"
)

func TestUnpackReplyFormatCopiesOrPrints(t *testing.T) {
	useTempHome(t)
	var copied []byte
	prev := unpackCopyText
	unpackCopyText = func(text []byte, _ platform.Palette) error { copied = text; return nil }
	t.Cleanup(func() { unpackCopyText = prev })

	var stdout, stderr bytes.Buffer
	if err := runUnpackReplyFormat(cli.ReplyFormatOptions{}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if string(copied) != cli.UnpackReplyFormatDefault || stdout.Len() != 0 {
		t.Fatalf("copy: copied %d bytes, stdout %q", len(copied), stdout.String())
	}
	if !strings.Contains(stderr.String(), "Copied the reply format for agents") || !strings.Contains(stderr.String(), "unpack-reply-format-for-agents.md: you can edit it") || !strings.Contains(stderr.String(), "catclip --unpack") {
		t.Fatalf("next steps missing:\n%s", stderr.String())
	}

	copied = nil
	stdout.Reset()
	if err := runUnpackReplyFormat(cli.ReplyFormatOptions{Print: true}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != cli.UnpackReplyFormatDefault || copied != nil {
		t.Fatal("--print must show the format and not copy it")
	}
}

func TestParseUnpackReplyFormatArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want cli.ReplyFormatOptions
	}{
		{[]string{"--unpack-reply-format"}, cli.ReplyFormatOptions{}},
		{[]string{"-p", "--unpack-reply-format"}, cli.ReplyFormatOptions{Print: true}},
		{[]string{"--unpack-reply-format", "--headless"}, cli.ReplyFormatOptions{Headless: true}},
		{[]string{"--unpack-reply-format", "--headless", "--yes"}, cli.ReplyFormatOptions{Headless: true, Yes: true}},
	} {
		if got, err := cli.ParseUnpackReplyFormatArgs(tc.args); err != nil || got != tc.want {
			t.Errorf("%v: %+v %v", tc.args, got, err)
		}
	}
	for _, args := range [][]string{{"--unpack-reply-format", "src"}, {"--unpack-reply-format", "--quiet"}} {
		if _, err := cli.ParseUnpackReplyFormatArgs(args); err == nil {
			t.Errorf("%v should be rejected", args)
		}
	}
	if !cli.IsUnpackReplyFormatRequest([]string{"--unpack-reply-format"}) || cli.IsUnpackReplyFormatRequest([]string{"--unpack"}) {
		t.Fatal("request detection")
	}
	if cli.IsUnpackReplyFormatRequest([]string{"src", "--contains", "--unpack-reply-format"}) {
		t.Fatal("a --contains regex is not a request")
	}
}

// The examples inside the reply format must themselves be valid entries:
// an agent copies them, and --unpack must read what they show.
func TestUnpackReplyFormatExamplesParse(t *testing.T) {
	b, err := unpack.Parse([]byte(cli.UnpackReplyFormatDefault))
	if err != nil {
		t.Fatalf("the reply format's own examples do not parse: %v", err)
	}
	var whole, edits, moves int
	for _, e := range b.Entries {
		if e.From != "" {
			moves++
		} else if e.Edits != nil {
			edits += len(e.Edits)
		} else {
			whole++
		}
	}
	if whole != 1 || edits != 2 || moves != 1 {
		t.Fatalf("examples: %d whole files, %d edits, %d moves; want 1, 2 and 1", whole, edits, moves)
	}
}

// Unpacking the reply format itself (copied, but the agent's answer was not)
// must not create the files from its examples.
func TestUnpackRefusesTheReplyFormatItself(t *testing.T) {
	root := setupUnpackProject(t)
	stubUnpackClipboard(t, output.ClipboardContent{Text: cli.UnpackReplyFormatDefault}, nil)
	err := runUnpack(cli.UnpackOptions{Yes: true}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "holds catclip's reply format, not a reply") {
		t.Fatalf("want the reply-format refusal, got %v", err)
	}
	if readUnpackFile(t, root, "internal/handler/health.go") != "<missing>" {
		t.Fatal("the example file must not be created")
	}
}

// An agent that needs more code replies without code and asks for a catclip
// command; --unpack points to it instead of saying the reply is not a bundle.
func TestUnpackPointsToTheCatclipCommandTheReplyAsksFor(t *testing.T) {
	setupUnpackProject(t)
	reply := "I need two more excerpts before I can change this. Please run:\n\n" +
		"```\ncatclip internal/store/store.go cmd/api/main.go\n```\n\nand paste the output.\n"
	stubUnpackClipboard(t, output.ClipboardContent{Text: reply}, nil)
	err := runUnpack(cli.UnpackOptions{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "asks for more of your code first. Run:\n  catclip internal/store/store.go cmd/api/main.go\n  paste the output into the chat, then copy the new reply") {
		t.Fatalf("got %v", err)
	}

	// Several commands, one per line: each run replaces the clipboard, so the
	// user pastes after each one.
	reply = "Please run these and paste each output:\n\n```\ncatclip internal/store/store.go\ncatclip cmd/api/main.go --lines 1 20\n```\n"
	stubUnpackClipboard(t, output.ClipboardContent{Text: reply}, nil)
	err = runUnpack(cli.UnpackOptions{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Run:\n  catclip internal/store/store.go\n  catclip cmd/api/main.go --lines 1 20\n  one at a time, pasting each output into the chat") {
		t.Fatalf("got %v", err)
	}
}

// The reply's prose (an agent's assumptions) is reported after writing, also
// with --yes, where no picker showed it.
func TestUnpackReportsTheReplyNotes(t *testing.T) {
	root := setupUnpackProject(t)
	reply := "I changed b.go. I assumed nothing else calls it.\n\n```\n<file path=\"src/b.go\">\nnew b\n</file>\n```\n\nTell me if it should stay exported.\n"
	stubUnpackClipboard(t, output.ClipboardContent{Text: reply}, nil)
	var stderr bytes.Buffer
	if err := runUnpack(cli.UnpackOptions{Yes: true}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatal(err)
	}
	got := stderr.String()
	if !strings.Contains(got, "Notes from the reply:\n  I changed b.go. I assumed nothing else calls it.\n  \n  Tell me if it should stay exported.\n") || strings.Contains(got, "```") {
		t.Fatalf("notes:\n%s", got)
	}
	if readUnpackFile(t, root, "src/b.go") != "new b\n" {
		t.Fatal("b.go not written")
	}
}

// useTempHome points catclip's config folder at a temporary home.
func useTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// The reply format is an editable file in catclip's config folder: created
// the first time; when it differs from catclip's own text (edited, or written
// by an older catclip), catclip asks every time which one to use, and 1
// takes catclip's version. Without a terminal it never asks.
func TestUnpackReplyFormatFileAsksWhichToUse(t *testing.T) {
	useTempHome(t)
	builtIn := cli.UnpackReplyFormatDefault
	path := unpackReplyFormatPath()
	if filepath.Base(path) != "unpack-reply-format-for-agents.md" || filepath.Base(filepath.Dir(path)) != "catclip" {
		t.Fatalf("path %s", path)
	}
	var questions []string
	choice := 2
	prev := unpackPromptChoice
	unpackPromptChoice = func(q string, _ io.Writer) (int, error) {
		questions = append(questions, q)
		return choice, nil
	}
	t.Cleanup(func() { unpackPromptChoice = prev })

	src, err := loadUnpackReplyFormat(cli.ReplyFormatOptions{}, true, &bytes.Buffer{})
	if err != nil || src.text != builtIn || src.path != path || src.custom || len(questions) != 0 {
		t.Fatalf("first use: %+v %v %v", src, err, questions)
	}
	if data, _ := os.ReadFile(path); string(data) != builtIn {
		t.Fatal("the file should hold the built-in text")
	}
	if _, err := loadUnpackReplyFormat(cli.ReplyFormatOptions{}, true, &bytes.Buffer{}); err != nil || len(questions) != 0 {
		t.Fatalf("identical file must not ask: %v %v", err, questions)
	}

	// Different: asked every time; 2 keeps the user's copy.
	mine := "# catclip for web agents\n\nMy own rules.\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		src, err = loadUnpackReplyFormat(cli.ReplyFormatOptions{}, true, &bytes.Buffer{})
		if err != nil || src.text != mine || !src.custom || len(questions) != i {
			t.Fatalf("keep %d: %+v %v %v", i, src, err, questions)
		}
	}
	for _, want := range []string{"Your reply format is different from catclip's.", "You may have edited your copy, or catclip was updated with a newer version.", "1. Use catclip's version (your copy is replaced)", "2. Keep using your copy", "Choose 1 or 2:"} {
		if !strings.Contains(questions[0], want) {
			t.Errorf("question missing %q:\n%s", want, questions[0])
		}
	}

	// --print keeps the guide on stdout and explains the source on stderr.
	prevAsk := unpackReplyFormatCanAsk
	t.Cleanup(func() { unpackReplyFormatCanAsk = prevAsk })
	unpackReplyFormatCanAsk = func() bool { return true }
	var out, note bytes.Buffer
	if err := runUnpackReplyFormat(cli.ReplyFormatOptions{Print: true}, &out, &note); err != nil || out.String() != mine || !strings.Contains(note.String(), platform.DisplayPath(path)) || !strings.Contains(note.String(), "--print --yes") || len(questions) != 2 {
		t.Fatalf("--print: %q %q %v %v", out.String(), note.String(), err, questions)
	}

	note.Reset()

	// Copying without a terminal (a script): never asks, keeps the copy
	// silently; the hint is only for --headless.
	if src, _ = loadUnpackReplyFormat(cli.ReplyFormatOptions{}, false, &note); src.text != mine || note.Len() != 0 || len(questions) != 2 {
		t.Fatalf("without a terminal: %q %v", note.String(), questions)
	}

	// --headless (agents): prints the copy, no question, none of the user's
	// next steps, and the --yes hint because it differs.
	out.Reset()
	if err := runUnpackReplyFormat(cli.ReplyFormatOptions{Headless: true}, &out, &note); err != nil || out.String() != mine || len(questions) != 2 {
		t.Fatalf("--headless: %q %v", out.String(), err)
	}
	if !strings.Contains(note.String(), "differs from catclip's; using yours.") || !strings.Contains(note.String(), "catclip --unpack-reply-format --headless --yes") || strings.Contains(note.String(), "Paste it") {
		t.Fatalf("headless hint:\n%s", note.String())
	}

	// A question nobody answers (input ended: NUL, which Windows reports as
	// a terminal) keeps the copy.
	choice = 0
	if src, err = loadUnpackReplyFormat(cli.ReplyFormatOptions{}, true, &bytes.Buffer{}); err != nil || src.text != mine || len(questions) != 3 {
		t.Fatalf("no answer: %+v %v", src, err)
	}
	if data, _ := os.ReadFile(path); string(data) != mine {
		t.Fatal("no answer must not replace the copy")
	}

	// --headless --yes takes catclip's version without asking; after that
	// the copy matches, so later runs neither ask nor hint.
	note.Reset()
	out.Reset()
	if err := runUnpackReplyFormat(cli.ReplyFormatOptions{Headless: true, Yes: true}, &out, &note); err != nil || out.String() != builtIn || note.Len() != 0 || len(questions) != 3 {
		t.Fatalf("--yes: %q %v", note.String(), err)
	}
	if data, _ := os.ReadFile(path); string(data) != builtIn {
		t.Fatal("--yes must replace the copy")
	}
	out.Reset()
	if err := runUnpackReplyFormat(cli.ReplyFormatOptions{Headless: true}, &out, &note); err != nil || out.String() != builtIn || note.Len() != 0 {
		t.Fatalf("after --yes: %q", note.String())
	}
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1 uses catclip's version and replaces the copy.
	choice = 1
	src, err = loadUnpackReplyFormat(cli.ReplyFormatOptions{}, true, &bytes.Buffer{})
	data, _ := os.ReadFile(path)
	if err != nil || src.text != builtIn || src.custom || string(data) != builtIn {
		t.Fatalf("replace: %+v %v", src, err)
	}
}

func TestUnpackReplyFormatPrintIsReadOnly(t *testing.T) {
	config := filepath.Join(t.TempDir(), "absent")
	t.Setenv("XDG_CONFIG_HOME", config)
	path := unpackReplyFormatPath()
	for _, present := range []bool{false, true} {
		const saved = "personal rules\n"
		if present {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(saved), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, headless := range []bool{false, true} {
			for _, yes := range []bool{false, true} {
				var stdout bytes.Buffer
				opts := cli.ReplyFormatOptions{Print: true, Headless: headless, Yes: yes}
				if err := runUnpackReplyFormat(opts, &stdout, io.Discard); err != nil {
					t.Fatal(err)
				}
				want := cli.UnpackReplyFormatDefault
				if present && !yes {
					want = saved
				}
				if stdout.String() != want {
					t.Fatalf("wrong guide for %+v, present=%t", opts, present)
				}
				if present {
					if data, err := os.ReadFile(path); err != nil || string(data) != saved {
						t.Fatalf("saved config changed: %q %v", data, err)
					}
				} else if _, err := os.Stat(config); !os.IsNotExist(err) {
					t.Fatalf("print created config: %v", err)
				}
			}
		}
	}
}
