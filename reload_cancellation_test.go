package catclip

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestReloadCancellationHonoredByRipgrep proves the rg content-match path runs
// under reloadCancelCtx: when that context is cancelled (as happens when fzf
// terminates a superseded reload), the rg exec does not complete normally, so
// an orphaned full scan cannot keep running. A baseline (Background ctx) match
// confirms the path works normally when not cancelled.
func TestReloadCancellationHonoredByRipgrep(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("TODO here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Baseline: default Background context behaves exactly like exec.Command.
	got, err := runRipgrepMatches("TODO", []string{f})
	if err != nil {
		t.Fatalf("baseline runRipgrepMatches: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("baseline expected a match")
	}

	// Arm a cancelled context: the rg exec must not complete normally.
	saved := reloadCancelCtx
	defer func() { reloadCancelCtx = saved }()
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	reloadCancelCtx = cctx
	if _, err := runRipgrepMatches("TODO", []string{f}); err == nil {
		t.Fatal("expected runRipgrepMatches to fail under a cancelled reloadCancelCtx (rg should not run/complete)")
	}
}

// TestReloadCancellationDefaultsToBackground guards the invariant that normal,
// non-interactive runs are unaffected: reloadCancelCtx must not be cancelled
// unless installReloadCancellation armed it.
func TestReloadCancellationDefaultsToBackground(t *testing.T) {
	if reloadWasCancelled() {
		t.Fatal("reloadCancelCtx should default to a live context for normal runs")
	}
}
