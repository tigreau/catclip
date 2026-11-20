package catclip

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/unpack"
)

const unpackTestBundle = "<file path=\"src/a.go\">\nnew a\n</file>\n\n" +
	"<file path=\"src/b.go\">\nnew b\n</file>\n\n" +
	"<file path=\"src/c.go\">\nsame c\n</file>\n\n" +
	"<file path=\"img/x.png\" type=\"binary\">\nFormat: x\n</file>\n\n"

func setupUnpackProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{"src/a.go": "old a\n", "src/c.go": "same c\n", "bundle.txt": unpackTestBundle} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	// Tests stub the question and picker; whether this machine has a
	// terminal must not matter.
	prevCanAsk := unpackCanAsk
	unpackCanAsk = func() bool { return true }
	t.Cleanup(func() { unpackCanAsk = prevCanAsk })
	return root
}

func stubUnpackUI(t *testing.T, rootAnswer bool, choose func(unpack.Plan) ([]int, error)) *int {
	t.Helper()
	prompts := 0
	prevPrompt, prevChoose := unpackPromptYesNo, unpackChooseChanges
	unpackPromptYesNo = func(string, bool, io.Writer) (bool, error) { prompts++; return rootAnswer, nil }
	unpackChooseChanges = choose
	t.Cleanup(func() { unpackPromptYesNo, unpackChooseChanges = prevPrompt, prevChoose })
	return &prompts
}

func readUnpackFile(t *testing.T, root, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if os.IsNotExist(err) {
		return "<missing>"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUnpackRootQuestionNoWritesNothing(t *testing.T) {
	root := setupUnpackProject(t)
	stubUnpackUI(t, false, func(unpack.Plan) ([]int, error) { t.Fatal("picker opened after 'no'"); return nil, nil })
	err := runUnpack(cli.UnpackOptions{Path: "bundle.txt"}, &bytes.Buffer{}, &bytes.Buffer{})
	if exitCodeForError(err) != 1 || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("got %v", err)
	}
	if readUnpackFile(t, root, "src/a.go") != "old a\n" || readUnpackFile(t, root, "src/b.go") != "<missing>" {
		t.Fatal("files changed after 'no'")
	}
}

func TestUnpackPickerCancelWritesNothing(t *testing.T) {
	root := setupUnpackProject(t)
	stubUnpackUI(t, true, func(unpack.Plan) ([]int, error) { return nil, discovery.ErrSelectionCancelled })
	err := runUnpack(cli.UnpackOptions{Path: "bundle.txt"}, &bytes.Buffer{}, &bytes.Buffer{})
	if exitCodeForError(err) != 1 || readUnpackFile(t, root, "src/a.go") != "old a\n" {
		t.Fatalf("cancel: %v", err)
	}
}

func TestUnpackWritesOnlyTheFilesKeptInThePicker(t *testing.T) {
	root := setupUnpackProject(t)
	var offered []string
	stubUnpackUI(t, true, func(p unpack.Plan) ([]int, error) {
		for _, c := range p.Changes {
			offered = append(offered, string(c.Kind)+":"+c.Path)
		}
		for i, c := range p.Changes {
			if c.Path == "src/b.go" {
				return []int{i}, nil
			}
		}
		return nil, nil
	})
	var stderr bytes.Buffer
	if err := runUnpack(cli.UnpackOptions{Path: "bundle.txt"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatal(err)
	}
	// Identical files are hidden; only new and changed ones are offered.
	if strings.Join(offered, ",") != "changed:src/a.go,new:src/b.go" {
		t.Fatalf("offered %v", offered)
	}
	if readUnpackFile(t, root, "src/a.go") != "old a\n" || readUnpackFile(t, root, "src/b.go") != "new b\n" {
		t.Fatal("wrong files written")
	}
	out := stderr.String()
	for _, want := range []string{"Wrote 1 file", "(0 replaced, 1 new)", "Left out 1 file", "    img/x.png   binary description"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "1 changed, 1 new, 1 already identical") {
		t.Fatalf("missing summary before the root question:\n%s", out)
	}
}

func TestUnpackYesSkipsEveryQuestionAndHeadlessReportsOnStdout(t *testing.T) {
	root := setupUnpackProject(t)
	prompts := stubUnpackUI(t, false, func(unpack.Plan) ([]int, error) { t.Fatal("picker opened with --yes"); return nil, nil })
	var stdout, stderr bytes.Buffer
	if err := runUnpack(cli.UnpackOptions{Path: "bundle.txt", Yes: true, Headless: true}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if *prompts != 0 || stderr.Len() != 0 {
		t.Fatalf("prompts=%d stderr=%q", *prompts, stderr.String())
	}
	if readUnpackFile(t, root, "src/a.go") != "new a\n" || readUnpackFile(t, root, "src/b.go") != "new b\n" {
		t.Fatal("--yes did not write all changes")
	}
	if !strings.Contains(stdout.String(), "Wrote 2 files") || strings.Contains(stdout.String(), "\x1b[") {
		t.Fatalf("headless report: %q", stdout.String())
	}
}

// --headless implies --yes; the stdout report names every file written and
// whether it replaced one or is new, so an agent knows what changed.
func TestUnpackHeadlessWritesAllAndListsEachFile(t *testing.T) {
	root := setupUnpackProject(t)
	prompts := stubUnpackUI(t, false, func(unpack.Plan) ([]int, error) { t.Fatal("picker opened with --headless"); return nil, nil })
	var stdout, stderr bytes.Buffer
	if err := runUnpack(cli.UnpackOptions{Path: "bundle.txt", Headless: true}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if *prompts != 0 || stderr.Len() != 0 {
		t.Fatalf("prompts=%d stderr=%q", *prompts, stderr.String())
	}
	if readUnpackFile(t, root, "src/a.go") != "new a\n" || readUnpackFile(t, root, "src/b.go") != "new b\n" {
		t.Fatal("--headless did not write the changes")
	}
	out := stdout.String()
	for _, want := range []string{"Wrote 2 files", "(1 replaced, 1 new)", "  changed  src/a.go\n", "  new      src/b.go\n", "    img/x.png   binary description"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestUnpackRefusesUnsafeBundlesBeforeAnyQuestion(t *testing.T) {
	root := setupUnpackProject(t)
	evil := unpackTestBundle + "<file path=\"../escape.txt\">\nx\n</file>\n"
	if err := os.WriteFile(filepath.Join(root, "bundle.txt"), []byte(evil), 0o644); err != nil {
		t.Fatal(err)
	}
	prompts := stubUnpackUI(t, true, func(unpack.Plan) ([]int, error) { return []int{0, 1}, nil })
	err := runUnpack(cli.UnpackOptions{Path: "bundle.txt", Yes: true}, &bytes.Buffer{}, &bytes.Buffer{})
	if exitCodeForError(err) != 1 || !strings.Contains(err.Error(), "refusing to unpack") || *prompts != 0 {
		t.Fatalf("got %v prompts=%d", err, *prompts)
	}
	if readUnpackFile(t, root, "src/b.go") != "<missing>" {
		t.Fatal("an unsafe bundle wrote other files")
	}
}

func TestParseUnpackArgs(t *testing.T) {
	opts, err := cli.ParseUnpackArgs([]string{"-y", "--unpack", "b.txt", "--headless"})
	if err != nil || opts.Path != "b.txt" || !opts.Yes || !opts.Headless {
		t.Fatalf("%+v %v", opts, err)
	}
	// Short forms work like everywhere else in catclip.
	opts, err = cli.ParseUnpackArgs([]string{"--unpack", "b.txt", "-p", "-y", "-q", "-v"})
	if err != nil || !opts.Print || !opts.Yes || !opts.Quiet || !opts.Verbose {
		t.Fatalf("short flags: %+v %v", opts, err)
	}
	// Without a FILE the bundle comes from the clipboard; a next argument
	// starting with "-" is a flag, not the file.
	for _, args := range [][]string{{"--unpack"}, {"--unpack", "--yes"}, {"--unpack", "-p"}, {"-y", "--unpack", "-q"}} {
		opts, err := cli.ParseUnpackArgs(args)
		if err != nil || opts.Path != "" || !opts.FromClipboard() {
			t.Errorf("%v: want the clipboard, got %+v %v", args, opts, err)
		}
	}
	// --headless never reads the clipboard.
	if _, err := cli.ParseUnpackArgs([]string{"--unpack", "--headless"}); err == nil || !strings.Contains(err.Error(), "catclip --unpack FILE --headless") {
		t.Fatalf("--unpack --headless without a file: %v", err)
	}
	for _, args := range [][]string{{"src", "--unpack", "b.txt"}, {"--unpack", "a", "--unpack", "b"}, {"--unpack", "b.txt", "--paths"}, {"--unpack", "a.txt", "b.txt"}, {"--unpack", ""}} {
		if _, err := cli.ParseUnpackArgs(args); err == nil {
			t.Errorf("%v should be rejected", args)
		}
	}
	if cli.IsUnpackRequest([]string{"src", "--contains", "--unpack"}) {
		t.Fatal("a --contains regex is not an --unpack request")
	}
}

func TestUnpackRootQuestionNamesTheFolder(t *testing.T) {
	root := setupUnpackProject(t)
	var question string
	prev := unpackPromptYesNo
	unpackPromptYesNo = func(q string, defaultYes bool, _ io.Writer) (bool, error) {
		if !defaultYes {
			t.Fatal("Enter must mean yes")
		}
		question = q
		return false, nil
	}
	t.Cleanup(func() { unpackPromptYesNo = prev })
	_ = runUnpack(cli.UnpackOptions{Path: "bundle.txt"}, &bytes.Buffer{}, &bytes.Buffer{})
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(question, "")
	if want := "Is " + platform.DisplayPath(root) + " the project root? [Y/n]:"; plain != want {
		t.Fatalf("question %q, want %q", plain, want)
	}
}

// --print shows the diffs on stdout for reading or piping, writes nothing,
// and keeps the summary on stderr so a pipe receives only the patch.
func TestUnpackPrintShowsDiffsAndWritesNothing(t *testing.T) {
	root := setupUnpackProject(t)
	prompts := stubUnpackUI(t, true, func(unpack.Plan) ([]int, error) { t.Fatal("picker opened with --print"); return nil, nil })
	for _, opts := range []cli.UnpackOptions{{Path: "bundle.txt", Print: true}, {Path: "bundle.txt", Print: true, Headless: true}} {
		var stdout, stderr bytes.Buffer
		if err := runUnpack(opts, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if *prompts != 0 {
			t.Fatal("--print asked a question")
		}
		if readUnpackFile(t, root, "src/a.go") != "old a\n" || readUnpackFile(t, root, "src/b.go") != "<missing>" {
			t.Fatalf("--print wrote files (%+v)", opts)
		}
		out := stdout.String()
		want := "diff --git a/src/a.go b/src/a.go\n--- a/src/a.go\n+++ b/src/a.go\n@@ -1 +1 @@\n-old a\n+new a\n" +
			"diff --git a/src/b.go b/src/b.go\nnew file mode 100644\n--- /dev/null\n+++ b/src/b.go\n@@ -0,0 +1 @@\n+new b\n"
		if out != want {
			t.Fatalf("stdout (%+v):\n%q\nwant:\n%q", opts, out, want)
		}
		switch {
		case opts.Headless && stderr.Len() != 0:
			// Headless contract: stdout is the payload, stderr errors only.
			t.Fatalf("headless stderr: %q", stderr.String())
		case !opts.Headless && (!strings.Contains(stderr.String(), "Nothing was written") || !strings.Contains(stderr.String(), "img/x.png")):
			t.Fatalf("stderr: %q", stderr.String())
		}
		if strings.Contains(out, "\x1b[") {
			t.Fatalf("diffs must be plain when not a terminal (%+v)", opts)
		}
	}
}

// Without anyone to answer (CI, cron, ssh without -t), --unpack says what to
// run instead, like bare catclip and catclip --, and writes nothing.
func TestUnpackWithoutATerminalSaysWhatToRun(t *testing.T) {
	root := setupUnpackProject(t)
	prompts := stubUnpackUI(t, true, func(unpack.Plan) ([]int, error) {
		t.Fatal("the picker must not open")
		return nil, nil
	})
	unpackCanAsk = func() bool { return false }
	err := runUnpack(cli.UnpackOptions{Path: "bundle.txt"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Run catclip --unpack in an interactive terminal, or add --yes to write every change") || *prompts != 0 {
		t.Fatalf("got %v, %d prompts", err, *prompts)
	}
	if readUnpackFile(t, root, "src/a.go") != "old a\n" {
		t.Fatal("nothing may be written")
	}
	// --yes needs nobody.
	if err := runUnpack(cli.UnpackOptions{Path: "bundle.txt", Yes: true}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}
