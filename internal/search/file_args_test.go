package search

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestRipgrepFileArgBatchesPreservePolicyAndExactRoots(t *testing.T) {
	for _, goos := range []string{"windows", "linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			opts := RipgrepFileOptions{HissPath: `C:\config space\catclip\.hiss`, Basenames: []string{"*.go", "!skip*", "keep.go"}}
			for i := 0; i < 4000; i++ {
				opts.Paths = append(opts.Paths, fmt.Sprintf("selected folder/文件-%04d-%s.go", i, strings.Repeat("x", 24)))
			}
			original := append([]string(nil), opts.Paths...)
			for _, debug := range []bool{false, true} {
				batches, err := ripgrepFileArgBatches("rg executable", opts, debug, goos)
				if err != nil {
					t.Fatal(err)
				}
				if len(batches) < 2 {
					t.Fatal("fixture did not cross argument budget")
				}
				baseOpts := opts
				baseOpts.Paths = nil
				base := append(ripgrepFileArgs(baseOpts, debug), "--")
				var got []string
				for _, args := range batches {
					if len(args) <= len(base) || !reflect.DeepEqual(args[:len(base)], base) {
						t.Fatalf("batch changed ordered policy or lost all roots: %v", args)
					}
					units := commandArgUnits("rg executable", goos)
					for _, arg := range args {
						units += commandArgUnits(arg, goos)
					}
					if units > execPathChunkByteLimit(goos) {
						t.Fatalf("batch exceeds budget: %d", units)
					}
					got = append(got, args[len(base):]...)
				}
				if !reflect.DeepEqual(got, original) || !reflect.DeepEqual(opts.Paths, original) {
					t.Fatal("roots were lost, reordered, quoted, or mutated")
				}
			}
		})
	}
}

func TestRipgrepFileArgBatchesSmallShapeAndOverflow(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		for _, opts := range []RipgrepFileOptions{
			{}, {Paths: []string{"", ".", "./"}},
			{Paths: []string{".", "./src", "--dash", "dir with space/é.go"}},
			{NoIgnore: true, HissPath: "unused", Paths: []string{"src"}},
		} {
			got, err := ripgrepFileArgBatches("rg", opts, false, goos)
			if err != nil || !reflect.DeepEqual(got, [][]string{ripgrepFileArgs(opts, false)}) {
				t.Fatalf("small command changed (%s): %v, %v", goos, got, err)
			}
		}
		huge := strings.Repeat("x", execPathChunkByteLimit(goos))
		for _, opts := range []RipgrepFileOptions{
			{Paths: []string{huge}}, {Basenames: []string{huge}}, {HissPath: huge},
		} {
			if _, err := ripgrepFileArgBatches("rg", opts, false, goos); err == nil {
				t.Fatalf("accepted oversized fixed argument/target on %s", goos)
			}
		}
	}
}

// This runs real rg processes on every CI OS, including Windows. Ordinary
// short existing paths exceed even the Unix argv budget; no long-path support,
// shell scripts, or platform skips are needed to exercise the Windows failure.
func TestRipgrepLargeRootBatchesNativeParity(t *testing.T) {
	root := t.TempDir()
	writeMembershipFixture(t, root, ".gitignore", "*.tmp\n")
	writeMembershipFixture(t, root, "rules.hiss", "*.secret\n")
	writeMembershipFixture(t, root, "outside.go", "must never leak\n")
	var roots, visible, all []string
	for i := 0; i < 800; i++ {
		dir := fmt.Sprintf("picked %04d %s", i, strings.Repeat("x", 72))
		if i == 0 {
			dir = "picked & $ 'é' " + strings.Repeat("x", 72)
		}
		roots = append(roots, dir)
		for _, name := range []string{"keep.go", "drop.tmp", "machine.secret"} {
			rel := dir + "/" + name
			writeMembershipFixture(t, root, rel, "x\n")
			all = append(all, rel)
			if name == "keep.go" {
				visible = append(visible, rel)
			}
		}
	}
	// Duplicate roots at the end straddle batches. The union must stay exact.
	roots = append(roots, roots[:10]...)
	roots = append(roots, visible[0]) // overlapping explicit file and directory
	sort.Strings(visible)
	sort.Strings(all)
	opts := RipgrepFileOptions{Paths: roots, HissPath: filepath.Join(root, "rules.hiss")}
	bin, ok := RipgrepBinary()
	if !ok {
		t.Fatal("rg is required for native transport regression")
	}
	for _, noIgnore := range []bool{false, true} {
		t.Run(fmt.Sprintf("files/no_ignore_%t", noIgnore), func(t *testing.T) {
			options := opts
			options.NoIgnore = noIgnore
			batches, err := ripgrepFileArgBatches(bin, options, false, runtime.GOOS)
			if err != nil || len(batches) < 2 {
				t.Fatalf("fixture does not batch: %d, %v", len(batches), err)
			}
			var events []MembershipEnumerationEvent
			restore := SetMembershipEnumerationObserver(func(e MembershipEnumerationEvent) { events = append(events, e) })
			defer restore()
			got, err := RunRipgrepFiles(root, options)
			want := visible
			if noIgnore {
				want = all
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("batched membership differs: got %d, want %d, err %v", len(got), len(want), err)
			}
			if len(events) != len(batches) {
				t.Fatalf("events=%d, actual batches=%d", len(events), len(batches))
			}
		})
	}
	t.Run("visible_set", func(t *testing.T) {
		got, err := runRipgrepVisibleFiles(root, opts.HissPath, roots, MembershipEnumerationContext{})
		if err != nil || len(got) != len(visible) {
			t.Fatalf("visible union: %d, %v", len(got), err)
		}
		for _, rel := range visible {
			if _, ok := got[rel]; !ok {
				t.Fatalf("visible union lost %s", rel)
			}
		}
	})
	t.Run("diagnostic", func(t *testing.T) {
		var gotVisible []string
		ignored := make(map[string]bool)
		counts, err := RunRipgrepIgnoreTrace(root, opts, func(rel string) { gotVisible = append(gotVisible, rel) }, func(record IgnoreTraceRecord) { ignored[record.Path] = true })
		sort.Strings(gotVisible)
		if err != nil || !reflect.DeepEqual(gotVisible, visible) || counts.Visible != len(visible) || counts.Ignored != 1600 || len(ignored) != 1600 {
			t.Fatalf("diagnostic union lost or duplicated paths: counts=%+v uniqueIgnored=%d err=%v", counts, len(ignored), err)
		}
	})
	t.Run("ordered_globs", func(t *testing.T) {
		options := opts
		options.Basenames = []string{"*", "!*.secret", "!*.tmp"}
		got, err := RunRipgrepFiles(root, options)
		if err != nil || !reflect.DeepEqual(got, visible) {
			t.Fatalf("ordered globs changed: %d, %v", len(got), err)
		}
	})
	t.Run("later_batch_failure", func(t *testing.T) {
		options := opts
		options.Paths = append(append([]string(nil), roots...), "missing-target")
		got, err := RunRipgrepFiles(root, options)
		if err == nil || got != nil {
			t.Fatalf("partial union returned on failed batch: %d, %v", len(got), err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := runRipgrepFileBatches(ctx, bin, root, opts, MembershipEnumerationFiles)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("cancellation returned partial success: %d, %v", len(got), err)
		}
	})
	t.Run("empty_batches_before_match", func(t *testing.T) {
		writeMembershipFixture(t, root, "last/only.final", "x\n")
		options := opts
		// Explicit files bypass rg's glob filtering; omit the overlap file
		// so preceding directory batches genuinely have no matching files.
		options.Paths = append(append([]string(nil), roots[:len(roots)-1]...), "last")
		options.Basenames = []string{"*.final"}
		got, err := RunRipgrepFiles(root, options)
		if err != nil || !reflect.DeepEqual(got, []string{"last/only.final"}) {
			t.Fatalf("exit-1 batch dropped later match: %v, %v", got, err)
		}
	})
}
