package catclip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaxLinesForFilesEmpty(t *testing.T) {
	got, err := maxLinesForFiles(nil)
	if err != nil {
		t.Fatalf("maxLinesForFiles(nil) returned error: %v", err)
	}
	if got != 0 {
		t.Fatalf("expected 0 for empty input, got %d", got)
	}
}

func TestMaxLinesForFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"short.txt": "a\nb\nc\n",                // 3 lines
		"mid.txt":   "a\nb\nc\nd\ne\nf\ng\n",    // 7 lines
		"long.txt":  strings.Repeat("line\n", 25), // 25 lines
	}
	abs := make([]string, 0, len(files))
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		abs = append(abs, path)
	}

	got, err := maxLinesForFiles(abs)
	if err != nil {
		t.Fatalf("maxLinesForFiles returned error: %v", err)
	}
	if got != 25 {
		t.Fatalf("expected max=25 (longest file), got %d", got)
	}
}

func TestLinesPickerPreviewCommandsUseLinesPreviewFlag(t *testing.T) {
	// Pin the byte-faithful preview contract: the lines picker routes
	// through --internal-prediscovered + --internal-lines-preview so the
	// preview pane sees actual file content. The tree-only payload path
	// (--internal-tree-payload) strips bodies, so it must NOT be used
	// here; an earlier draft that used it produced an empty-bodies
	// preview the user noticed.
	startCmd := buildLinesPickerStartPreviewCommand("/tmp/scope.json")
	if startCmd == "" {
		t.Skip("os.Executable unavailable in this test env")
	}
	if !strings.Contains(startCmd, "--internal-prediscovered") {
		t.Fatalf("start preview missing --internal-prediscovered: %s", startCmd)
	}
	if !strings.Contains(startCmd, "--internal-lines-preview") {
		t.Fatalf("start preview missing --internal-lines-preview: %s", startCmd)
	}
	if strings.Contains(startCmd, "--internal-tree-payload") {
		t.Fatalf("start preview must not use --internal-tree-payload (strips bodies): %s", startCmd)
	}
	if !strings.Contains(startCmd, "--lines {2}") {
		t.Fatalf("start preview must substitute {2} into --lines: %s", startCmd)
	}

	endCmd := buildLinesPickerEndPreviewCommand("/tmp/scope.json", 42)
	if !strings.Contains(endCmd, "--internal-prediscovered") {
		t.Fatalf("end preview missing --internal-prediscovered: %s", endCmd)
	}
	if !strings.Contains(endCmd, "--internal-lines-preview") {
		t.Fatalf("end preview missing --internal-lines-preview: %s", endCmd)
	}
	if strings.Contains(endCmd, "--internal-tree-payload") {
		t.Fatalf("end preview must not use --internal-tree-payload: %s", endCmd)
	}
	if !strings.Contains(endCmd, "case {2} in EOF)") {
		t.Fatalf("end preview must dispatch EOF row via shell case: %s", endCmd)
	}
	if !strings.Contains(endCmd, "--lines 42 {2}") {
		t.Fatalf("end preview must include fixed start + {2}: %s", endCmd)
	}
	if !strings.Contains(endCmd, "--lines 42 ;;") {
		t.Fatalf("end preview EOF branch must drop the end arg: %s", endCmd)
	}
}

func TestStartupLinePickerLines(t *testing.T) {
	got := startupLinePickerLines(1, 3)
	want := []string{"1\t1\tLine 1", "2\t2\tLine 2", "3\t3\tLine 3"}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %v", len(got), len(want), got)
	}
	for i, row := range got {
		if row != want[i] {
			t.Fatalf("row %d: got %q, want %q", i, row, want[i])
		}
	}
}

func TestStartupLineEndPickerLinesIncludesEOFFirst(t *testing.T) {
	rows := startupLineEndPickerLines(5, 7)
	if len(rows) == 0 {
		t.Fatal("expected non-empty end picker rows")
	}
	if !strings.HasPrefix(rows[0], "EOF\tEOF\t[to end of file]") {
		t.Fatalf("first row should be EOF sentinel, got: %q", rows[0])
	}
	wantNumeric := []string{"5\t5\tLine 5", "6\t6\tLine 6", "7\t7\tLine 7"}
	if len(rows) != 1+len(wantNumeric) {
		t.Fatalf("got %d rows, want %d", len(rows), 1+len(wantNumeric))
	}
	for i, want := range wantNumeric {
		if rows[i+1] != want {
			t.Fatalf("row %d: got %q, want %q", i+1, rows[i+1], want)
		}
	}
}

func TestLinesPickerSelectionIsEOF(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"EOF\tEOF\t[to end of file]", true},
		{"EOF", true},
		{"5\t5\tLine 5", false},
		{"", false},
	}
	for _, c := range cases {
		if got := linesPickerSelectionIsEOF(c.in); got != c.want {
			t.Fatalf("linesPickerSelectionIsEOF(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseLinesPickerToken(t *testing.T) {
	n, err := parseLinesPickerToken("42\t42\tLine 42")
	if err != nil {
		t.Fatalf("parse returned error: %v", err)
	}
	if n != 42 {
		t.Fatalf("expected 42, got %d", n)
	}
}

func TestResolveStartupLinesArgsOpenEnded(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"data.txt": strings.Repeat("x\n", 30), // 30 lines
	})
	_ = parseInProject(t, project, []string{"data.txt"})

	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	installScriptFzf(t, `#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt) prompt="$2"; shift 2 ;;
		*) shift ;;
	esac
done
input="$(cat)"

case "$prompt" in
	"start-line> ")
		printf '%s\n' "$input" | grep -F $'5\t5\tLine 5' | head -n 1
		exit 0
		;;
	"end-line> ")
		printf '%s\n' "$input" | grep -F $'EOF\tEOF\t[to end of file]' | head -n 1
		exit 0
		;;
esac
echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, usedFzf, err := resolveStartupLinesArgs([]string{"data.txt"})
	if err != nil {
		t.Fatalf("resolveStartupLinesArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected lines picker to report usedFzf")
	}
	if got, want := strings.Join(args, "\n"), "data.txt\n--lines\n5"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveStartupLinesArgsRanged(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"data.txt": strings.Repeat("x\n", 30),
	})
	_ = parseInProject(t, project, []string{"data.txt"})

	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	installScriptFzf(t, `#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt) prompt="$2"; shift 2 ;;
		*) shift ;;
	esac
done
input="$(cat)"

case "$prompt" in
	"start-line> ")
		printf '%s\n' "$input" | grep -F $'10\t10\tLine 10' | head -n 1
		exit 0
		;;
	"end-line> ")
		printf '%s\n' "$input" | grep -F $'15\t15\tLine 15' | head -n 1
		exit 0
		;;
esac
echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, usedFzf, err := resolveStartupLinesArgs([]string{"data.txt"})
	if err != nil {
		t.Fatalf("resolveStartupLinesArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected lines picker to report usedFzf")
	}
	if got, want := strings.Join(args, "\n"), "data.txt\n--lines\n10\n15"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveStartupLinesArgsCancelStart(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"data.txt": strings.Repeat("x\n", 30),
	})
	_ = parseInProject(t, project, []string{"data.txt"})

	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	// Empty stdout from fzf means "selection cancelled" per the picker.
	installScriptFzf(t, "#!/bin/sh\nexit 130\n")

	_, _, err := resolveStartupLinesArgs([]string{"data.txt"})
	if err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
	if err.Error() != errSelectionCancelled.Error() {
		t.Fatalf("expected errSelectionCancelled, got %v", err)
	}
}
