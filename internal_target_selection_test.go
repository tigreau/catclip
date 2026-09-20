package catclip

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
)

func TestTargetSelectionFileLargeExactRoundTrip(t *testing.T) {
	project := setupTestProject(t, nil)
	var matches []discovery.TargetMatch
	var want []string
	for i := 0; i < 10000; i++ {
		rel := fmt.Sprintf("selected folder/file-%05d.go", i)
		if i == 0 {
			rel = "--literal $ and 'é'.go"
		}
		matches = append(matches, discovery.TargetMatch{Path: rel, Kind: "file"})
		want = append(want, rel)
	}
	rows, _ := discovery.TargetMatchLabels(matches)
	// Repeated rows must not duplicate targets, and the last selection must
	// survive even though the equivalent argv is well beyond Windows limits.
	rows = append(rows, rows[0])
	selection := filepath.Join(t.TempDir(), "selected rows.tsv")
	if err := os.WriteFile(selection, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := parseInProject(t, project, []string{"--internal-tree-preview", "--internal-target-selection", selection})
	got, err := specWithTargetSelection(cfg.Command, cfg.TargetSelectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if paths := command.ExecutionScopesFromSpec(got)[0].Targets; !reflect.DeepEqual(paths, want) {
		t.Fatalf("selection lost or reordered paths: got %d, want %d", len(paths), len(want))
	}
	if paths := command.ExecutionScopesFromSpec(cfg.Command)[0].Targets; !reflect.DeepEqual(paths, []string{"."}) {
		t.Fatalf("mutated original parsed command: %v", paths)
	}
}

func TestTargetSelectionFilePreviewParityWithoutEnumeration(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n", "src/nested/b.go": "package b\n", "outside.go": "package outside\n",
	})
	inventory := filepath.Join(t.TempDir(), "targets.bin")
	if err := discovery.WriteTargetPreviewInventory(inventory, git.Context{}, []discovery.TargetMatch{
		{Path: "src/a.go", Kind: "file", SizeKnown: true, SizeBytes: 10},
		{Path: "src/nested/b.go", Kind: "file", SizeKnown: true, SizeBytes: 10},
		{Path: "outside.go", Kind: "file", SizeKnown: true, SizeBytes: 16},
	}); err != nil {
		t.Fatal(err)
	}
	for _, selected := range [][]discovery.TargetMatch{
		{{Path: "src/a.go", Kind: "file"}}, // fzf sends focus when nothing is marked
		{{Path: "src", Kind: "dir"}, {Path: "src/a.go", Kind: "file"}},
		{{Path: "src/a.go", Kind: "file"}, {Path: ".", Kind: "all"}},
	} {
		rows, _ := discovery.TargetMatchLabels(selected)
		selection := filepath.Join(t.TempDir(), "selection.tsv")
		if err := os.WriteFile(selection, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		base := []string{"--internal-tree-preview", "--internal-target-inventory", inventory}
		var paths []string
		for _, match := range selected {
			paths = append(paths, match.Path)
		}
		if selected[len(selected)-1].Kind == "all" {
			paths = []string{"."}
		}
		rootsPath := filepath.Join(t.TempDir(), "roots.json")
		if err := discovery.WriteTargetRoots(rootsPath, paths); err != nil {
			t.Fatal(err)
		}
		var outputs [3]bytes.Buffer
		for i, args := range [][]string{
			append(append([]string(nil), base...), paths...),
			append(append([]string(nil), base...), "--internal-target-selection", selection),
			append(append([]string(nil), base...), "--internal-target-roots", rootsPath),
		} {
			cfg := parseInProject(t, project, args)
			var events []search.MembershipEnumerationEvent
			restore := search.SetMembershipEnumerationObserver(func(event search.MembershipEnumerationEvent) { events = append(events, event) })
			err := run(cfg, &outputs[i], &bytes.Buffer{})
			restore()
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 0 {
				t.Fatalf("inventory preview enumerated: %+v", events)
			}
		}
		if outputs[0].String() != outputs[1].String() || outputs[0].String() != outputs[2].String() {
			t.Fatalf("file-backed preview changed output:\n%s\nwant:\n%s", &outputs[1], &outputs[0])
		}
	}
}

// Use a native child process, not just the in-process decoder. The existing
// GitHub matrix runs this on Windows as well as Unix. Shell/fzf quoting has
// separate acceptance tests; this locks the bounded helper argv boundary.
func TestTargetSelectionFileNativePreviewProcess(t *testing.T) {
	project := setupTestProject(t, nil)
	var matches []discovery.TargetMatch
	for i := 0; i < 10000; i++ {
		matches = append(matches, discovery.TargetMatch{
			Path: fmt.Sprintf("selected folder/file-%05d.go", i), Kind: "file", SizeKnown: true, SizeBytes: 12,
		})
	}
	artifacts := filepath.Join(t.TempDir(), "transport with spaces")
	if err := os.MkdirAll(artifacts, 0o700); err != nil {
		t.Fatal(err)
	}
	inventory := filepath.Join(artifacts, "inventory.bin")
	if err := discovery.WriteTargetPreviewInventory(inventory, git.Context{}, matches); err != nil {
		t.Fatal(err)
	}
	rows, _ := discovery.TargetMatchLabels(matches)
	selection := filepath.Join(artifacts, "selected rows.tsv")
	if err := os.WriteFile(selection, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--quiet", "--internal-tree-preview", "--internal-target-inventory", inventory, "--internal-target-selection", selection}
	cfg := parseInProject(t, project, args)
	var want bytes.Buffer
	if err := run(cfg, &want, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if want.Len() == 0 {
		t.Fatal("preview fixture produced no output")
	}
	rootsPath := filepath.Join(artifacts, "retained roots.json")
	var targets []string
	for _, match := range matches {
		targets = append(targets, match.Path)
	}
	if err := discovery.WriteTargetRoots(rootsPath, targets); err != nil {
		t.Fatal(err)
	}
	for _, transport := range [][]string{{"--internal-target-selection", selection}, {"--internal-target-roots", rootsPath}} {
		child := exec.Command(os.Args[0], append(append([]string(nil), args[:4]...), transport...)...)
		child.Dir = project
		child.Env = append(os.Environ(), "CATCLIP_TEST_RUN_MAIN=1")
		var stderr bytes.Buffer
		child.Stderr = &stderr
		got, err := child.Output()
		if err != nil {
			t.Fatalf("native preview: %v\n%s", err, &stderr)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Fatalf("native preview bytes differ: got %d, want %d; stderr=%s", len(got), want.Len(), &stderr)
		}
	}
	// The general modifier checkpoint also carries the large semantic scope
	// without argv expansion. --paths preserves its separate output section.
	checkpointPath := filepath.Join(artifacts, "scope.json")
	scope := command.ExecutionScope{Targets: targets, Paths: true}
	var entries []discovery.Entry
	for _, match := range matches {
		entries = append(entries, discovery.Entry{RelPath: match.Path, SizeKnown: true, SizeBytes: match.SizeBytes, Mode: command.EntryModeFull})
	}
	if err := discovery.WriteCheckpoint(checkpointPath, project, discovery.CheckpointData{Scope: &scope, Entries: entries}); err != nil {
		t.Fatal(err)
	}
	args = []string{"--quiet", "--internal-tree-preview", "--internal-prediscovered", checkpointPath, "--internal-checkpoint-scope"}
	want.Reset()
	if err := run(parseInProject(t, project, args), &want, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], args...)
	child.Dir = project
	child.Env = append(os.Environ(), "CATCLIP_TEST_RUN_MAIN=1")
	var stderr bytes.Buffer
	child.Stderr = &stderr
	got, err := child.Output()
	if err != nil || !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("native checkpoint-scope parity: %v, stderr=%s", err, &stderr)
	}
}

func TestTargetSelectionFileRejectsConflictingScopes(t *testing.T) {
	project := setupTestProject(t, nil)
	for _, extra := range [][]string{
		{"src"}, {".", "--no-ignore"}, {"--paths"}, {"--depth", "2"}, {".", "--then", "."},
	} {
		args := append([]string{"--internal-tree-preview", "--internal-target-selection", "must-not-be-opened"}, extra...)
		cfg := parseInProject(t, project, args)
		_, err := specWithTargetSelection(cfg.Command, cfg.TargetSelectionPath)
		if err == nil || !strings.Contains(err.Error(), "implicit, unfiltered target scope") {
			t.Fatalf("conflicting scope %v was accepted or opened the file first: %v", extra, err)
		}
	}
}

func TestTargetSelectionFileRejectsInvalidRows(t *testing.T) {
	for _, row := range []string{"", "bad", "label\t\tfile\ttext\n", "label\t../outside\tfile\ttext\n", "label\t.\tfile\ttext\n"} {
		selection := filepath.Join(t.TempDir(), "selection.tsv")
		if err := os.WriteFile(selection, []byte(row), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readTargetSelection(selection); err == nil {
			t.Fatalf("accepted invalid selection %q", row)
		}
	}
}

func TestTargetPreviewCommandUsesBoundedSelectionTransport(t *testing.T) {
	for _, cmd := range []string{discovery.FzfPreviewCommand(false), discovery.FzfPreviewCommandWithInventory("inventory.bin")} {
		if !strings.Contains(cmd, `--internal-target-selection "{+f}"`) || strings.Contains(cmd, "{+2}") {
			t.Fatalf("unbounded or unquoted target selection transport: %s", cmd)
		}
	}
}
