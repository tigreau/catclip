package output

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
)

func TestPreparedDiffBatchMatchesIndividualComparisons(t *testing.T) {
	for _, scenario := range []string{"normal", "unborn", "prefix", "rename"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			run := func(args ...string) {
				t.Helper()
				if _, err := git.Capture(root, args...); err != nil {
					t.Fatalf("git %v: %v", args, err)
				}
			}
			run("init", "-q")
			run("config", "core.autocrlf", "false")
			write := func(name, body string) {
				t.Helper()
				p := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			names := []string{"dir/a.go", "dir/b.go", "dir/sp ace.go", "dir/é.go", "dir/[literal].go", "dir/binary.dat", "dir/gone.go"}
			for _, name := range names {
				write(name, "before\n")
			}
			run("add", ".")
			if scenario != "unborn" {
				run("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "initial")
			}
			for _, name := range names {
				write(name, "staged\n")
			}
			run("add", ".")
			for _, name := range names {
				write(name, "unstaged\n")
			}
			write("dir/binary.dat", "binary\x00data")
			if scenario == "prefix" {
				run("config", "diff.noprefix", "true")
			}
			if scenario == "rename" {
				run("mv", "dir/a.go", "dir/renamed.go")
				names[0] = "dir/renamed.go"
			}
			if err := os.Remove(filepath.Join(root, "dir/gone.go")); err != nil {
				t.Fatal(err)
			}
			write("dir/new.go", "untracked\n")
			names = append(names, "dir/new.go")
			ctx := git.Detect(filepath.Join(root, "dir"))
			var entries []discovery.Entry
			for _, flags := range [][2]bool{{false, true}, {true, false}, {true, true}} {
				for _, name := range names {
					entries = append(entries, discovery.Entry{RelPath: strings.TrimPrefix(name, "dir/"), AbsPath: filepath.Join(root, name), Mode: command.EntryModeDiff, DiffWantStaged: flags[0], DiffWantUnstaged: flags[1]})
				}
			}
			// Deleted files remain tracked and have patches; untracked bodies are
			// deliberately retained. Mixed comparison kinds must never coalesce.
			entries = append(entries, entries[0]) // shared observation survives until its last use
			got, err := PrepareFileUnits(ctx, entries)
			if err != nil {
				t.Fatal(err)
			}
			var want []PreparedFileUnit
			for _, e := range entries {
				unit, keep, err := PrepareFileUnit(ctx, e, nil)
				if err != nil {
					t.Fatal(err)
				}
				if keep {
					want = append(want, unit)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("batch changed prepared payloads (%d vs %d units)", len(got), len(want))
			}
			for _, name := range []string{"[literal].go", "new.go"} {
				found := false
				for _, u := range got {
					if u.Entry.RelPath == name {
						found = true
					}
				}
				if !found {
					t.Fatalf("lost %s", name)
				}
			}
		})
	}
}

func TestPreparedDiffBatchBoundsGitProcesses(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_EXTERNAL_DIFF", "")
	// Git treats a present-but-empty external command differently from absence.
	if err := os.Unsetenv("GIT_EXTERNAL_DIFF"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := git.Capture(root, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q")
	run("config", "core.autocrlf", "false")
	names := []string{"a.go", "b.go", "c.go"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte("before\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "initial")
	var entries []discovery.Entry
	for _, name := range names {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte("after\n"), 0644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, discovery.Entry{RelPath: name, AbsPath: p, Mode: command.EntryModeDiff, DiffWantUnstaged: true})
	}
	ctx := git.Detect(root)
	trace := filepath.Join(t.TempDir(), "git.jsonl")
	t.Setenv("GIT_TRACE2_EVENT", filepath.ToSlash(trace))
	units, err := PrepareFileUnits(ctx, entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 3 {
		t.Fatal(len(units))
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, line := range strings.Split(string(data), "\n") {
		var event struct{ Event string }
		if json.Unmarshal([]byte(line), &event) == nil && event.Event == "start" {
			starts++
		}
	}
	if starts != 3 {
		t.Fatalf("Git starts=%d want tracking + config guard + one patch batch", starts)
	}
	// --paths selects the sectioned planner, which must retain batching.
	if err := os.WriteFile(trace, nil, 0600); err != nil {
		t.Fatal(err)
	}
	plan := thenPlan(t, ctx, entries, true, false)
	if plan.Len() != 4 {
		t.Fatal(plan.Len())
	}
	data, err = os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	starts = 0
	for _, line := range strings.Split(string(data), "\n") {
		var event struct{ Event string }
		if json.Unmarshal([]byte(line), &event) == nil && event.Event == "start" {
			starts++
		}
	}
	if starts != 3 {
		t.Fatalf("sectioned Git starts=%d want 3", starts)
	}
}
