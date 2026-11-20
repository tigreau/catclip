package unpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A move entry (v0.7.16): <file path="new" from="old">. An empty body moves
// the file as it is, a body moves it and writes that content. No Git needed.
func TestMoveEntryMovesAndRewritesFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a", "old.go"), "package a\n")
	mustWrite(t, filepath.Join(root, "a", "rewrite.go"), "package before\n")
	reply := "Renamed both.\n```text\n" +
		"<file path=\"b/new.go\" from=\"a/old.go\">\n</file>\n" +
		"<file path=\"a/renamed.go\" from=\"a/rewrite.go\">\npackage after\n</file>\n```\n"
	b, err := Parse([]byte(reply))
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
	if len(p.Blocked()) != 0 || p.Moved() != 2 || len(p.Changes) != 2 {
		t.Fatalf("plan %+v", p)
	}
	if changed, added := p.Counts(); changed != 0 || added != 0 {
		t.Fatalf("moves are counted apart: changed=%d added=%d", changed, added)
	}
	diff := UnifiedDiff(p.Changes[0])
	if !strings.Contains(diff, "rename from a/old.go\nrename to b/new.go\n") || strings.Contains(diff, "@@") {
		t.Fatalf("a plain move shows only the rename:\n%s", diff)
	}
	if diff := UnifiedDiff(p.Changes[1]); !strings.Contains(diff, "-package before") || !strings.Contains(diff, "+package after") {
		t.Fatalf("a move with a body shows the content change:\n%s", diff)
	}
	if err := Check(p.Changes); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(p.Changes); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"b/new.go": "package a\n", "a/renamed.go": "package after\n"} {
		if got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path))); err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want %q", path, got, err, want)
		}
	}
	for _, gone := range []string{"a/old.go", "a/rewrite.go"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(gone))); !os.IsNotExist(err) {
			t.Errorf("%s should be gone after the move: %v", gone, err)
		}
	}
	// Unpacking the same reply again is harmless: both moves are done.
	again, err := BuildPlan(root, b)
	if err != nil || len(again.Changes) != 0 || again.Identical != 1 || len(again.Skipped) != 1 || again.Skipped[0].Path != "b/new.go" || len(again.Blocked()) != 0 {
		t.Fatalf("second plan %+v %v", again, err)
	}
}

func TestMoveEntryBlocksWhenSourceMissingOrTargetTaken(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "x.go"), "x\n")
	mustWrite(t, filepath.Join(root, "taken.go"), "mine\n")
	mustWrite(t, filepath.Join(root, "other.go"), "old\n")
	reply := "<file path=\"taken.go\" from=\"x.go\">\n</file>\n" +
		"<file path=\"y.go\" from=\"nowhere.go\">\n</file>\n" +
		"<file path=\"other.go\">\nnew\n</file>\n"
	b, err := Parse([]byte(reply))
	if err != nil {
		t.Fatal(err)
	}
	p, err := BuildPlan(root, b)
	if err != nil {
		t.Fatal(err)
	}
	problems := map[string]Problem{}
	for _, s := range p.Blocked() {
		problems[s.Path] = s.Problem
	}
	if problems["taken.go"] != ProblemMoveTarget || problems["y.go"] != ProblemMoveSource || len(problems) != 2 {
		t.Fatalf("blocked %+v", p.Blocked())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "taken.go")); string(got) != "mine\n" {
		t.Fatalf("an existing file must never be replaced by a move: %q", got)
	}
}

func TestParseRefusesContradictoryMoves(t *testing.T) {
	for name, reply := range map[string]string{
		"onto itself":                          "<file path=\"a.go\" from=\"a.go\">\n</file>\n",
		"empty from":                           "<file path=\"a.go\" from=\"\">\n</file>\n",
		"moved twice":                          "<file path=\"b.go\" from=\"a.go\">\n</file>\n<file path=\"c.go\" from=\"a.go\">\n</file>\n",
		"moved and written":                    "<file path=\"b.go\" from=\"a.go\">\n</file>\n<file path=\"a.go\">\nx\n</file>\n",
		"case-only destinations":               "<file path=\"Result\" from=\"left\">\n</file>\n<file path=\"result\" from=\"right\">\n</file>\n",
		"case-only sources":                    "<file path=\"one\" from=\"Left\">\n</file>\n<file path=\"two\" from=\"left\">\n</file>\n",
		"equivalent Unicode destinations":      "<file path=\"café.txt\" from=\"left\">\n</file>\n<file path=\"cafe\u0301.txt\" from=\"right\">\n</file>\n",
		"equivalent Unicode sources":           "<file path=\"one\" from=\"café.txt\">\n</file>\n<file path=\"two\" from=\"CAFE\u0301.txt\">\n</file>\n",
		"equivalent Unicode parent":            "<file path=\"café\" from=\"old\">\n</file>\n<file path=\"cafe\u0301/sub\">\nx\n</file>\n",
		"move and write source alias":          "<file path=\"new\" from=\"old\">\n</file>\n<file path=\"Old\">\nx\n</file>\n",
		"move and write destination alias":     "<file path=\"new\" from=\"old\">\n</file>\n<file path=\"New\">\nx\n</file>\n",
		"move destination used as folder":      "<file path=\"new\" from=\"old\">\n</file>\n<file path=\"New/sub\">\nx\n</file>\n",
		"move requires folder written as file": "<file path=\"new/sub\" from=\"old\">\n</file>\n<file path=\"New\">\nx\n</file>\n",
		"move as excerpt":                      "<file path=\"b.go\" from=\"a.go\" lines=\"1-2\" out-of=\"9\">\nx\n</file>\n",
	} {
		if _, err := Parse([]byte(reply)); err == nil {
			t.Errorf("%s: Parse should refuse %q", name, reply)
		}
	}
}

func TestValidatePathsChecksTheMoveSource(t *testing.T) {
	b, err := Parse([]byte("<file path=\"in.go\" from=\"../outside.go\">\n</file>\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePaths(t.TempDir(), b); err == nil {
		t.Fatal("a move from outside the project must be refused")
	}
}

func TestBinaryMove(t *testing.T) {
	const original = "before\x00after\xff\n"

	t.Run("empty body preserves bytes", func(t *testing.T) {
		root := t.TempDir()
		oldPath := filepath.Join(root, "old.bin")
		mustWrite(t, oldPath, original)

		p, err := BuildPlan(root, Bundle{Entries: []Entry{{Path: "new.bin", From: "old.bin"}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Blocked()) != 0 || len(p.Changes) != 1 {
			t.Fatalf("expected one planned change and no blocked entries, got %+v", p)
		}
		if _, err := Write(p.Changes); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(filepath.Join(root, "new.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Fatalf("moved bytes = %q, want %q", got, original)
		}
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Fatalf("expected original to be removed, stat error = %v", err)
		}
	})

	t.Run("text body is blocked", func(t *testing.T) {
		root := t.TempDir()
		oldPath := filepath.Join(root, "old.bin")
		mustWrite(t, oldPath, original)

		p, err := BuildPlan(root, Bundle{Entries: []Entry{{Path: "new.bin", From: "old.bin", Body: []byte("replacement\n")}}})
		if err != nil {
			t.Fatal(err)
		}
		blocked := p.Blocked()
		if len(blocked) != 1 || blocked[0].Problem != ProblemBinary {
			t.Fatalf("expected one ProblemBinary block, got %+v", blocked)
		}
		if len(p.Changes) != 0 {
			t.Fatalf("expected no planned changes, got %+v", p.Changes)
		}
		got, err := os.ReadFile(oldPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Fatalf("original bytes = %q, want %q", got, original)
		}
	})
}

func TestMoveRetryStaysInRootWithExistingDestination(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "src/new.go"), "unverified\n")
	mustWrite(t, filepath.Join(root, "backup/src/old.go"), "backup\n")
	b := Bundle{Entries: []Entry{{Path: "src/new.go", From: "src/old.go"}}}
	if home, found := FindBundleHome(root, b); found {
		t.Fatalf("retry redirected to %s", home)
	}
	p, err := BuildPlan(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Identical != 0 || len(p.Changes) != 0 || len(p.Skipped) != 1 ||
		p.Skipped[0].Blocking || !strings.Contains(p.Skipped[0].Reason, "already at destination") {
		t.Fatalf("retry plan %+v", p)
	}
}

func TestMoveCleanupFailureReportsWrittenDestination(t *testing.T) {
	root := t.TempDir()
	// A nonempty directory makes Remove fail deterministically on every OS.
	// Exercise the write phase directly, after its destination write succeeds.
	old := filepath.Join(root, "old")
	mustWrite(t, filepath.Join(old, "child"), "keep")
	dst := filepath.Join(root, "new")
	changes := []Change{
		{Kind: KindMoved, From: "old", FromAbs: old, Path: "new", Abs: dst, Body: []byte("saved"), Mode: 0o644},
		{Kind: KindNew, Path: "later", Abs: filepath.Join(root, "later")},
	}
	written, err := Write(changes)
	failure, ok := err.(PartialWriteError)
	if !ok || failure.MoveSource != "old" || failure.Failed != "new" ||
		strings.Join(written, ",") != "new" || strings.Join(failure.Written, ",") != "new" ||
		strings.Join(failure.NotWritten, ",") != "later" {
		t.Fatalf("written %v, error %+v", written, err)
	}
	if !strings.Contains(err.Error(), "wrote new, but could not remove old") {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "saved" {
		t.Fatalf("destination %q, error %v", data, err)
	}
}
