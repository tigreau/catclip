package catclip

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/unpack"
)

func stubUnpackClipboard(t *testing.T, content output.ClipboardContent, err error) *int {
	t.Helper()
	reads := 0
	prev := unpackReadClipboard
	unpackReadClipboard = func(platform.Palette) (output.ClipboardContent, error) { reads++; return content, err }
	t.Cleanup(func() { unpackReadClipboard = prev })
	return &reads
}

func TestUnpackReadsAChatReplyFromTheClipboard(t *testing.T) {
	root := setupUnpackProject(t)
	// A chat reply: prose and a code fence around the entries.
	reply := "Here are the changes:\n\n```\n" + unpackTestBundle + "```\nLet me know!\n"
	reads := stubUnpackClipboard(t, output.ClipboardContent{Text: reply}, nil)
	stubUnpackUI(t, true, func(p unpack.Plan) ([]int, error) { return []int{0, 1}, nil })
	var stderr bytes.Buffer
	if err := runUnpack(cli.UnpackOptions{}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("runUnpack: %v\n%s", err, stderr.String())
	}
	if *reads != 1 {
		t.Fatalf("clipboard read %d times, want 1", *reads)
	}
	if !strings.Contains(stderr.String(), "Unpacking from the clipboard: 1 changed, 1 new") {
		t.Fatalf("heading should name the clipboard:\n%s", stderr.String())
	}
	if readUnpackFile(t, root, "src/a.go") != "new a\n" || readUnpackFile(t, root, "src/b.go") != "new b\n" {
		t.Fatal("clipboard bundle was not written")
	}
}

func TestUnpackReadsOneCopiedBundleFile(t *testing.T) {
	root := setupUnpackProject(t)
	stubUnpackClipboard(t, output.ClipboardContent{Files: []string{filepath.Join(root, "bundle.txt")}}, nil)
	var stdout, stderr bytes.Buffer
	if err := runUnpack(cli.UnpackOptions{Yes: true}, &stdout, &stderr); err != nil {
		t.Fatalf("runUnpack: %v\n%s", err, stderr.String())
	}
	if readUnpackFile(t, root, "src/b.go") != "new b\n" {
		t.Fatal("copied bundle file was not unpacked")
	}
}

func TestUnpackUsesCurrentDirectoryWhenSiblingContainsBundlePaths(t *testing.T) {
	source := setupUnpackProject(t)
	destination := t.TempDir()
	t.Chdir(destination)
	destination, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	stubUnpackClipboard(t, output.ClipboardContent{Files: []string{filepath.Join(source, "bundle.txt")}}, nil)
	prompts := stubUnpackUI(t, true, func(plan unpack.Plan) ([]int, error) {
		if plan.Root != destination || len(plan.Changes) != 3 {
			t.Fatalf("unexpected destination or changes: %+v", plan)
		}
		for _, change := range plan.Changes {
			if change.Kind != unpack.KindNew {
				t.Fatalf("expected a new file at %s, got %s", change.Path, change.Kind)
			}
		}
		return []int{0}, nil
	})
	if err := runUnpack(cli.UnpackOptions{}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if *prompts != 1 || readUnpackFile(t, destination, "src/a.go") != "new a\n" ||
		readUnpackFile(t, destination, "src/b.go") != "<missing>" ||
		readUnpackFile(t, source, "src/a.go") != "old a\n" {
		t.Fatal("expected a confirmation and only the selected write in the current directory")
	}
}

func TestUnpackClipboardProblemsWriteNothingAndSayWhatToDo(t *testing.T) {
	root := setupUnpackProject(t)
	for _, tt := range []struct {
		name    string
		content output.ClipboardContent
		readErr error
		want    []string
	}{
		{"empty", output.ClipboardContent{}, nil, []string{"The clipboard is empty", "catclip --unpack FILE"}},
		{"plain chat text", output.ClipboardContent{Text: "Sure! Change line 12 to use Greet()."}, nil,
			[]string{"does not hold a catclip bundle", "catclip --unpack-reply-format", "catclip --unpack FILE"}},
		{"several copied files", output.ClipboardContent{Files: []string{"a.txt", "b.txt"}}, nil,
			[]string{"holds 2 copied files", "catclip --unpack FILE"}},
		{"read error", output.ClipboardContent{}, errors.New("Error: No clipboard tool found to read the clipboard."), []string{"No clipboard tool found", "catclip --unpack FILE"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stubUnpackClipboard(t, tt.content, tt.readErr)
			prompts := stubUnpackUI(t, true, func(unpack.Plan) ([]int, error) { t.Fatal("picker must not open"); return nil, nil })
			err := runUnpack(cli.UnpackOptions{}, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || exitCodeForError(err) != 1 {
				t.Fatalf("want exit 1, got %v", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("message missing %q:\n%s", w, err.Error())
				}
			}
			if *prompts != 0 || readUnpackFile(t, root, "src/a.go") != "old a\n" {
				t.Fatal("nothing may be asked or written")
			}
		})
	}
}

func TestUnpackBundleFileNotABundleMentionsTheReplyFormat(t *testing.T) {
	root := setupUnpackProject(t)
	if err := os.WriteFile(filepath.Join(root, "reply.txt"), []byte("Sure, here is the change: ..."), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runUnpack(cli.UnpackOptions{Path: "reply.txt", Yes: true}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "catclip --unpack-reply-format") {
		t.Fatalf("want the reply-format hint, got %v", err)
	}
}

// A copied path (Explorer's "Copy as path", with quotes) is applied like a
// copied file: --unpack applies whatever was copied.
func TestUnpackReadsACopiedPath(t *testing.T) {
	root := setupUnpackProject(t)
	stubUnpackClipboard(t, output.ClipboardContent{Text: "\"" + filepath.Join(root, "bundle.txt") + "\"\n"}, nil)
	var stderr bytes.Buffer
	if err := runUnpack(cli.UnpackOptions{Yes: true}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("runUnpack: %v\n%s", err, stderr.String())
	}
	if readUnpackFile(t, root, "src/a.go") != "new a\n" || readUnpackFile(t, root, "src/b.go") != "new b\n" {
		t.Fatal("the bundle at the copied path was not unpacked")
	}
	// A path to nothing is just text that is not a bundle.
	stubUnpackClipboard(t, output.ClipboardContent{Text: filepath.Join(root, "missing.txt")}, nil)
	if err := runUnpack(cli.UnpackOptions{Yes: true}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "does not hold a catclip bundle") {
		t.Fatalf("missing path: %v", err)
	}
}

// A reply is one change: if one entry cannot be applied (here an excerpt of a
// file that changed since it was copied), nothing is written, so the project
// never ends up half-changed. --print still shows what can be shown.
func TestUnpackWritesNothingWhenAnEntryCannotBeApplied(t *testing.T) {
	root := setupUnpackProject(t)
	reply := "<file path=\"src/b.go\">\nnew b\n</file>\n" +
		"<file path=\"src/a.go\" lines=\"1-1\" out-of=\"9\">\nchanged a\n</file>\n" // src/a.go has 1 line, not 9
	stubUnpackClipboard(t, output.ClipboardContent{Text: reply}, nil)
	prompts := stubUnpackUI(t, true, func(unpack.Plan) ([]int, error) { t.Fatal("picker must not open"); return nil, nil })

	err := runUnpack(cli.UnpackOptions{Yes: true}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || exitCodeForError(err) != 1 {
		t.Fatalf("want exit 1, got %v", err)
	}
	// Grouped by problem: explained once, each file with its own tag (so an
	// agent sees its markup), the fixes once with a ready catclip command;
	// the reply's other file (src/b.go) is named as not written either.
	for _, want := range []string{"Error: Nothing was written, because of these files:\n",
		"These lines were cut from a different version of each file",
		"\n    src/a.go   lines=\"1-1\" out-of=\"9\", but the file has 1 line now\n",
		"→ From a chat: paste this message into the chat, then run\n      catclip src/a.go\n",
		"→ From a teammate: ask them to send the whole files.",
		"so the\n  other file in it was not written either.",
		"Then copy the new reply or bundle and unpack it."} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q:\n%s", want, err)
		}
	}
	if *prompts != 0 || readUnpackFile(t, root, "src/b.go") != "<missing>" || readUnpackFile(t, root, "src/a.go") != "old a\n" {
		t.Fatal("nothing may be asked or written")
	}

	var stdout bytes.Buffer
	err = runUnpack(cli.UnpackOptions{Print: true}, &stdout, &bytes.Buffer{})
	if err == nil || exitCodeForError(err) != 1 || !strings.Contains(stdout.String(), "+++ b/src/b.go") {
		t.Fatalf("--print should show the applicable diff and still fail: %v\n%s", err, stdout.String())
	}
}

// Entries skipped by design (a binary description, a Git patch) never block.
func TestUnpackByDesignSkipsDoNotBlock(t *testing.T) {
	root := setupUnpackProject(t)
	reply := "<file path=\"src/b.go\">\nnew b\n</file>\n<file path=\"img/x.png\" type=\"binary\">\nFormat: PNG\n</file>\n"
	stubUnpackClipboard(t, output.ClipboardContent{Text: reply}, nil)
	if err := runUnpack(cli.UnpackOptions{Yes: true}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if readUnpackFile(t, root, "src/b.go") != "new b\n" {
		t.Fatal("src/b.go should be written")
	}
}

// Many blocked files stay readable: each problem is explained once, at most
// 20 files are listed (then "… and N more"), the fixes follow once, and the
// catclip command still names every file (user, 2026-11-20).
func TestBlockedReplyGroupsFilesByProblem(t *testing.T) {
	var blocked []unpack.Skipped
	for i := 1; i <= 25; i++ {
		blocked = append(blocked, unpack.Skipped{Path: fmt.Sprintf("src/f%02d.go", i), Problem: unpack.ProblemChanged,
			Detail: `lines="1-2" out-of="9", but the file has 12 lines now`, Blocking: true})
	}
	blocked = append(blocked, unpack.Skipped{Path: "my file.go", Problem: unpack.ProblemChanged, Detail: "x", Blocking: true})
	blocked = append(blocked, unpack.Skipped{Path: "src/App.tsx", Problem: unpack.ProblemShortened, Detail: `"// ... rest unchanged"`, Blocking: true})
	msg := blockedReplyMessage(blocked, 0, platform.Palette{})
	if n := strings.Count(msg, "These lines were cut from a different version"); n != 1 {
		t.Fatalf("explanation shown %d times:\n%s", n, msg)
	}
	if n := strings.Count(msg, "From a teammate"); n != 1 {
		t.Fatalf("fix shown %d times:\n%s", n, msg)
	}
	if !strings.Contains(msg, "    … and 6 more\n") || strings.Contains(msg, "src/f21.go   lines") {
		t.Fatalf("list not capped at 20:\n%s", msg)
	}
	if !strings.Contains(msg, "src/f25.go") || !strings.Contains(msg, `"my file.go"`) {
		t.Fatalf("the catclip command must name every file, quoting spaces:\n%s", msg)
	}
	if !strings.Contains(msg, "The code was shortened") || !strings.Contains(msg, `src/App.tsx   "// ... rest unchanged"`) {
		t.Fatalf("shortened group missing:\n%s", msg)
	}
}
