package unpack

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const sampleBundle = "<file path=\"src/main.go\">\npackage main\n</file>\n\n" +
	"<file path=\"src/new.go\">\npackage newer\n</file>\n\n" +
	"<file path=\"src/same.go\">\nsame\n</file>\n\n" +
	"src/listed-only.go\n" +
	"<file path=\"src/auth.go\" lines=\"40-88\" out-of=\"88\">\n40\tx\n</file>\n\n" +
	"<file path=\"src/num.go\" numbered=\"true\">\n     1\tx\n</file>\n\n" +
	"<file path=\"assets/logo.png\" type=\"binary\">\nFormat: x\n</file>\n\n" +
	"<file path=\"src/p.go\" type=\"diff\">\n@@\n</file>\n\n" +
	"<file path=\"src/cut.go\" partial=\"true\">\nhalf\n</file>\n\n" +
	"<file path=\"src/untracked.go\" type=\"untracked\">\nwhole\n</file>\n\n" +
	"\n[catclip: output truncated — estimated token limit: 10]\n"

func TestParseKeepsOnlyCompleteFilesAndListsTheRest(t *testing.T) {
	b, err := Parse([]byte(sampleBundle))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range b.Entries {
		paths = append(paths, e.Path)
	}
	// Since v0.7.15 excerpts (lines="A-B") are line edits and numbered whole
	// files are unpacked after their number column is stripped.
	if !reflect.DeepEqual(paths, []string{"src/main.go", "src/new.go", "src/same.go", "src/auth.go", "src/num.go", "src/untracked.go"}) {
		t.Fatalf("entries %v", paths)
	}
	if string(b.Entries[0].Body) != "package main\n" || string(b.Entries[5].Body) != "whole\n" || string(b.Entries[4].Body) != "x\n" {
		t.Fatalf("bodies %q %q %q", b.Entries[0].Body, b.Entries[5].Body, b.Entries[4].Body)
	}
	if e := b.Entries[3]; len(e.Edits) != 1 || e.Edits[0].Start != 40 || e.Edits[0].End != 88 || e.Edits[0].Total != 88 || string(e.Edits[0].Body) != "x\n" {
		t.Fatalf("excerpt edit %+v", e)
	}
	reasons := map[string]string{}
	for _, s := range b.Skipped {
		reasons[s.Path] = s.Reason
	}
	for path, want := range map[string]string{
		"assets/logo.png": "binary",
		"src/p.go":        "Git patch", "src/cut.go": "token-limit",
	} {
		if !strings.Contains(reasons[path], want) {
			t.Errorf("%s: reason %q, want %q", path, reasons[path], want)
		}
	}
}

func TestParseBodyIsExactlyBetweenTheTags(t *testing.T) {
	b, err := Parse([]byte("<file path=\"a.txt\">\r\nline1\r\nline2\r\n</file>\r\n\r\n<file path=\"empty.txt\">\n</file>\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Entries[0].Body) != "line1\r\nline2\r\n" || len(b.Entries[1].Body) != 0 {
		t.Fatalf("%q %q", b.Entries[0].Body, b.Entries[1].Body)
	}
}

func TestParseRejectsBrokenAndForeignInput(t *testing.T) {
	for name, input := range map[string]string{
		"no wrappers":     "just some text\n",
		"unclosed":        "<file path=\"a\">\nbody\n",
		"bad tag":         "<file path=\"a\" weird>\nx\n</file>\n",
		"conflicting dup": "<file path=\"a\">\nx\n</file>\n<file path=\"a\">\ny\n</file>\n",
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := Parse([]byte("plain\n")); !errors.Is(err, ErrNotBundle) {
		t.Fatalf("want ErrNotBundle, got %v", err)
	}
	b, err := Parse([]byte("<file path=\"a\">\nx\n</file>\n<file path=\"a\">\nx\n</file>\n"))
	if err != nil || len(b.Entries) != 1 {
		t.Fatalf("identical duplicates collapse: %v %v", b.Entries, err)
	}
}

func TestValidateNameRefusesUnsafePaths(t *testing.T) {
	for _, rel := range []string{"", "/etc/passwd", `C:\x`, "C:/x", "../x", "a/../../x", "a\\b", ".git/config", "a/.GIT/hooks/x", "a//b", "./a", "a\x00b"} {
		if err := validateName(rel, false); err == nil {
			t.Errorf("%q should be refused", rel)
		}
	}
	for _, rel := range []string{"con.txt", "a/NUL", "a/b:c", "x/y.", "lpt1"} {
		if err := validateName(rel, true); err == nil {
			t.Errorf("%q should be refused on Windows", rel)
		}
	}
	for _, rel := range []string{"src/main.go", ".github/workflows/ci.yml", "docs/a b/ü.md", ".gitignore"} {
		if err := validateName(rel, true); err != nil {
			t.Errorf("%q refused: %v", rel, err)
		}
	}
}

func TestValidatePathsRefusesSymlinkedFolders(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	err := ValidatePaths(root, Bundle{Entries: []Entry{{Path: "link/evil.txt"}}})
	var unsafe UnsafePathError
	if !errors.As(err, &unsafe) {
		t.Fatalf("expected UnsafePathError, got %v", err)
	}
}

func TestBuildPlanCheckWriteRoundTrip(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "src", "main.go"), "package old\n")
	mustWrite(t, filepath.Join(root, "src", "same.go"), "same\n")
	mustWrite(t, filepath.Join(root, "src", "keep.go"), "mine\n")
	b, err := Parse([]byte(sampleBundle))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePaths(root, b); err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(root, b)
	if err != nil {
		t.Fatal(err)
	}
	changed, added := p.Counts()
	if changed != 1 || added != 3 || p.Identical != 1 {
		t.Fatalf("changed=%d added=%d identical=%d", changed, added, p.Identical)
	}
	if err := Check(p.Changes); err != nil {
		t.Fatal(err)
	}
	written, err := Write(p.Changes)
	if err != nil || len(written) != 4 {
		t.Fatalf("written %v err %v", written, err)
	}
	for path, want := range map[string]string{
		"src/main.go": "package main\n", "src/new.go": "package newer\n",
		"src/untracked.go": "whole\n", "src/keep.go": "mine\n", "src/same.go": "same\n",
	} {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want %q", path, got, err, want)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, "src", ".catclip-unpack*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
	// Unpacking the same bundle again changes nothing. The excerpt edits
	// src/auth.go, which does not exist: it is skipped both times rather
	// than creating a file that holds only the excerpt.
	again, err := BuildPlan(root, b)
	if err != nil || len(again.Changes) != 0 || again.Identical != 5 {
		t.Fatalf("second plan %+v %v", again, err)
	}
	if last := again.Skipped[len(again.Skipped)-1]; last.Path != "src/auth.go" || last.Problem != ProblemMissing {
		t.Fatalf("the excerpt for a missing file should be skipped, got %+v", again.Skipped)
	}
}

func TestCheckBlocksReadOnlyFilesBeforeWriting(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("root ignores read-only permissions")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked.txt")
	mustWrite(t, locked, "old\n")
	if err := os.Chmod(locked, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o644) })
	changes := []Change{
		{Kind: KindNew, Path: "fresh.txt", Abs: filepath.Join(root, "fresh.txt"), Body: []byte("x"), Mode: 0o644},
		{Kind: KindChanged, Path: "locked.txt", Abs: locked, Body: []byte("new\n"), Mode: 0o644},
	}
	var blocked BlockedError
	if err := Check(changes); !errors.As(err, &blocked) || len(blocked.Problems) != 1 || !strings.HasPrefix(blocked.Problems[0], "locked.txt") {
		t.Fatalf("expected locked.txt blocked, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatal("check wrote a file")
	}
}

func TestUnifiedDiff(t *testing.T) {
	changed := Change{Kind: KindChanged, Path: "a.go", Local: []byte("one\ntwo\nthree\nfour\n"), Body: []byte("one\n2\nthree\nfour\nfive")}
	got := UnifiedDiff(changed)
	want := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,4 +1,5 @@\n one\n-two\n+2\n three\n four\n+five\n\\ No newline at end of file\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	fresh := UnifiedDiff(Change{Kind: KindNew, Path: "n.go", Body: []byte("a\nb\n")})
	if fresh != "diff --git a/n.go b/n.go\nnew file mode 100644\n--- /dev/null\n+++ b/n.go\n@@ -0,0 +1,2 @@\n+a\n+b\n" {
		t.Fatalf("new file diff:\n%s", fresh)
	}
	var far strings.Builder
	for i := 0; i < 40; i++ {
		far.WriteString("same\n")
	}
	base := far.String()
	split := UnifiedDiff(Change{Kind: KindChanged, Path: "f", Local: []byte("x\n" + base + "y\n"), Body: []byte("X\n" + base + "Y\n")})
	if strings.Count(split, "@@ -") != 2 {
		t.Fatalf("distant changes should make two hunks:\n%s", split)
	}
}

func TestUnifiedDiffSummarizesHugeChanges(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i < maxDiffEdits+10; i++ {
		a.WriteString("a\n")
		b.WriteString("b\n")
	}
	got := UnifiedDiff(Change{Kind: KindChanged, Path: "big", Local: []byte(a.String()), Body: []byte(b.String())})
	if !strings.Contains(got, "too many differences to preview") {
		t.Fatalf("expected a summary, got %d bytes", len(got))
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePathsRefusesWindowsJunctions(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows-only")
	}
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Skipf("mklink /J failed: %v %s", err, out)
	}
	err := ValidatePaths(root, Bundle{Entries: []Entry{{Path: "junction/evil.txt"}}})
	var unsafe UnsafePathError
	if !errors.As(err, &unsafe) {
		t.Fatalf("expected UnsafePathError for a junction, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "evil.txt")); !os.IsNotExist(statErr) {
		t.Fatal("validation wrote through the junction")
	}
}

// A chat reply may wrap the bundle in prose and code fences, or indent the
// whole block (for example inside a markdown list).
func TestParseReadsAgentRepliesWithProseFencesAndIndentation(t *testing.T) {
	reply := "Sure! Here are the changes:\n\n```\n" +
		"<file path=\"src/main.go\">\npackage main\n\n\tfunc main() {}\n</file>\n```\n\nLet me know!\n"
	indented := strings.ReplaceAll("  - Changes:\n\n    ```\n    <file path=\"src/main.go\">\n    package main\n\n    \tfunc main() {}\n    </file>\n    ```\n", "\r", "")
	for name, input := range map[string]string{"plain": reply, "indented": indented} {
		b, err := Parse([]byte(input))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(b.Entries) != 1 || b.Entries[0].Path != "src/main.go" || string(b.Entries[0].Body) != "package main\n\n\tfunc main() {}\n" {
			t.Fatalf("%s: %+v", name, b.Entries)
		}
	}
	// An indented block needs its closing tag at the same indentation.
	if _, err := Parse([]byte("    <file path=\"a\">\n    x\n</file>\n")); err == nil {
		t.Fatal("mismatched indentation should not close the entry")
	}
}
