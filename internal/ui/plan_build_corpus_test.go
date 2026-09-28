package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/output"
)

// Measures the actual planner after discovery/classification/stats, without
// opening a picker, rendering previews or emitting to the clipboard.
func TestPlanBuildCorpusTiming(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full corpus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(home, "Desktop", "catclip-test-data"))
	defer scopeViewMemoReset()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"files", []string{"."}},
		{"paths", []string{".", "--paths"}},
		{"mixed", []string{".", "--paths", "--then", "."}},
		{"no_ignore_binaries", []string{".", "--no-ignore", "--with-binaries"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scopeViewMemoReset()
			if tc.name == "mixed" {
				// Interactive --then seals the first scope before starting the
				// second; priming only the last scope is not a valid handoff.
				first, err := resolvedCurrentScopeViewForArgs([]string{".", "--paths"})
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := retainedScopeViewEntriesWithMetadata(first); !ok {
					t.Fatal("first-scope metadata not ready")
				}
			}
			view, err := resolvedCurrentScopeViewForArgs(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := retainedScopeViewEntriesWithMetadata(view); !ok {
				t.Fatal("metadata not ready")
			}
			ctx, err := buildStartupSinkPickerContext(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			for _, scope := range ctx.Discovery.Invocation.Scopes {
				for _, entry := range scope.Entries {
					if !entry.SizeKnown {
						t.Fatalf("uncaptured size in prepared scope: %s", entry.RelPath)
					}
				}
			}
			var times, allocs []float64
			for i := 0; i < 5; i++ {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				start := time.Now()
				plan, err := output.BuildPlanForDiscoveredInvocation(ctx.Git, ctx.Discovery.Invocation)
				elapsed := time.Since(start)
				runtime.ReadMemStats(&after)
				if err != nil {
					t.Fatal(err)
				}
				if plan.Len() != ctx.Plan.Len() {
					t.Fatal("plan membership changed")
				}
				times = append(times, float64(elapsed)/float64(time.Millisecond))
				allocs = append(allocs, float64(after.TotalAlloc-before.TotalAlloc)/(1<<20))
				t.Logf("round=%d items=%d ms=%.3f alloc_MiB=%.3f", i, plan.Len(), times[i], allocs[i])
			}
			slices.Sort(times)
			slices.Sort(allocs)
			t.Logf("SUMMARY median_ms=%.3f range_ms=%.3f..%.3f alloc_MiB=%.3f", times[2], times[0], times[4], allocs[2])
			if dir := os.Getenv("CATCLIP_BENCH_ARTIFACT_DIR"); dir != "" {
				f, err := os.Create(filepath.Join(dir, tc.name+".cpu"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := pprof.StartCPUProfile(f); err != nil {
					t.Fatal(err)
				}
				defer pprof.StopCPUProfile()
				for i := 0; i < 5; i++ {
					if _, err := output.BuildPlanForDiscoveredInvocation(ctx.Git, ctx.Discovery.Invocation); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
