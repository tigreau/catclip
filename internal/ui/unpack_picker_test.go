package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/unpack"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestUnpackPreviewShowsTheDiffForTheFocusedRow(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "a.go")
	if err := os.WriteFile(local, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := unpack.Plan{Root: root, Changes: []unpack.Change{
		{Kind: unpack.KindChanged, Path: "a.go", Abs: local, Body: []byte("one\n2\n")},
		{Kind: unpack.KindNew, Path: "dir/b.go", Abs: filepath.Join(root, "dir", "b.go"), Body: []byte("fresh\n")},
	}}
	state := t.TempDir()
	if err := writeUnpackPreviewState(state, plan); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunInternalUnpackPreview(state, "0", "", &out); err != nil {
		t.Fatal(err)
	}
	plain := ansiPattern.ReplaceAllString(out.String(), "")
	if !strings.Contains(plain, "-two") || !strings.Contains(plain, "+2") || !strings.Contains(plain, "--- a/a.go") {
		t.Fatalf("changed preview:\n%s", plain)
	}
	out.Reset()
	if err := RunInternalUnpackPreview(state, "1", "", &out); err != nil {
		t.Fatal(err)
	}
	plain = ansiPattern.ReplaceAllString(out.String(), "")
	if !strings.Contains(plain, "--- /dev/null") || !strings.Contains(plain, "+fresh") {
		t.Fatalf("new-file preview:\n%s", plain)
	}
	if err := RunInternalUnpackPreview(state, "7", "", &out); err == nil {
		t.Fatal("unknown row should fail")
	}
}

// Runs the real fzf (filter mode) and catclip's own result parsing, so the
// picker rows and the indices read back from them can never drift apart.
func TestUnpackPickerRowsRoundTripThroughFzf(t *testing.T) {
	plan := unpack.Plan{Changes: []unpack.Change{
		{Kind: unpack.KindNew, Path: "cmd/catclip/main.go"},
		{Kind: unpack.KindChanged, Path: "cmd/files-from-bench/main.go"},
	}}
	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		t.Skipf("fzf unavailable: %v", err)
	}
	keys, err := picker.FilterByNth(bin, "", unpackPickerLines(plan), "1,3")
	if err != nil {
		t.Fatal(err)
	}
	if got := unpackChosenIndices(keys, len(plan.Changes)); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("keys %q -> indices %v", keys, got)
	}
	keys, err = picker.FilterByNth(bin, "files-from", unpackPickerLines(plan), "1,3")
	if err != nil {
		t.Fatal(err)
	}
	if got := unpackChosenIndices(keys, len(plan.Changes)); len(got) != 1 || got[0] != 1 {
		t.Fatalf("filtered keys %q -> indices %v", keys, got)
	}
}

func TestUnpackPickerHeaderHasThreeHintRows(t *testing.T) {
	lines := strings.Split(unpackPickerHeader(2, 1, 4), "\n")
	if len(lines) != 4 || lines[0] != "2 changed · 1 new · 4 already identical" {
		t.Fatalf("%q", lines)
	}
	if !strings.Contains(lines[1], "[Tab]") || !strings.Contains(lines[1], "["+platform.MultiSelectToggleAllKey()+"]") ||
		!strings.HasPrefix(lines[2], "[Ctrl-T]") || !strings.HasPrefix(lines[3], "[Enter]") || !strings.Contains(lines[3], "[Esc]") {
		t.Fatalf("hint rows %q", lines[1:])
	}
}

func TestUnpackTreePreviewShowsWhatEnterWouldWrite(t *testing.T) {
	root := t.TempDir()
	plan := unpack.Plan{Root: root, Changes: []unpack.Change{
		{Kind: unpack.KindChanged, Path: "src/a.go", Abs: filepath.Join(root, "src", "a.go"), Body: []byte("one\n")},
		{Kind: unpack.KindNew, Path: "src/util/b.go", Abs: filepath.Join(root, "src", "util", "b.go"), Body: []byte("fresh code\n")},
		{Kind: unpack.KindNew, Path: "docs/c.md", Abs: filepath.Join(root, "docs", "c.md"), Body: []byte("x\n")},
	}}
	state := t.TempDir()
	if err := writeUnpackPreviewState(state, plan); err != nil {
		t.Fatal(err)
	}
	lines := unpackPickerLines(plan)
	selection := filepath.Join(t.TempDir(), "selected")
	// fzf writes the selected rows, one per line; here rows 0 and 1.
	if err := os.WriteFile(selection, []byte(lines[0]+"\n"+lines[1]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunInternalUnpackToggle(state); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunInternalUnpackPreview(state, "0", selection, &out); err != nil {
		t.Fatal(err)
	}
	plain := ansiPattern.ReplaceAllString(out.String(), "")
	for _, want := range []string{"a.go", "[changed]", "b.go", "[new]", "util/", "Count:", "2 files", "Size:"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in tree:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "c.md") || strings.Contains(plain, "+one") {
		t.Fatalf("tree shows unselected files or a diff:\n%s", plain)
	}
	// Toggling again returns to the focused file's diff.
	if err := RunInternalUnpackToggle(state); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := RunInternalUnpackPreview(state, "1", selection, &out); err != nil {
		t.Fatal(err)
	}
	if plain := ansiPattern.ReplaceAllString(out.String(), ""); !strings.Contains(plain, "+fresh code") || strings.Contains(plain, "Count:") {
		t.Fatalf("diff mode:\n%s", plain)
	}
}
