package platform

import (
	"testing"
	"time"
)

// The spinner is TTY-gated (IsTerminalFile). Tests here exercise the
// two pure state-machine surfaces that don't require a real terminal:
//   - spinnerMessageWithHint composes the rendered text based on the
//     hintOn flag; the delayed-timer branch's user-visible effect is
//     entirely this function's output.
//   - StartLoadingSpinner{,WithDelayedHint} return a stop() closure
//     that must be idempotent and must not leak the delayed-hint
//     timer when the spinner completes early.
//
// The timer→hintOn transition itself is stdlib time.AfterFunc; we
// don't need to reverify it fires. The value we DO need to verify is
// that the render composition consults hintOn correctly.

func TestSpinnerMessageWithHint_NoHintBeforeFlag(t *testing.T) {
	got := spinnerMessageWithHint("Scanning files...", "(first run is supposed to be slow)", false)
	if got != "Scanning files..." {
		t.Fatalf("hintOn=false should render bare message; got %q", got)
	}
}

func TestSpinnerMessageWithHint_HintAppendedWhenFlagOn(t *testing.T) {
	got := spinnerMessageWithHint("Scanning files...", "(first run is supposed to be slow)", true)
	want := "Scanning files... (first run is supposed to be slow)"
	if got != want {
		t.Fatalf("hintOn=true should append hint; got %q, want %q", got, want)
	}
}

func TestSpinnerMessageWithHint_EmptyHintIsNoop(t *testing.T) {
	// StartLoadingSpinner (no-hint variant) sets hint="", so even if
	// the flag were flipped defensively, no dangling space is emitted.
	got := spinnerMessageWithHint("Loading targets...", "", true)
	if got != "Loading targets..." {
		t.Fatalf("empty hint should render bare message; got %q", got)
	}
}

func TestStartLoadingSpinnerNoop_ReturnsCallableStop(t *testing.T) {
	// Non-TTY output → helper returns a no-op stop(); calling it
	// multiple times is safe. Guards against a regression where the
	// state machine gets initialized despite the TTY gate.
	stop := StartLoadingSpinner(nil, "test")
	stop()
	stop() // idempotent
}

func TestStartLoadingSpinnerWithDelayedHint_Noop_StopStillSafe(t *testing.T) {
	stop := StartLoadingSpinnerWithDelayedHint(nil, "test", "hint", 50*time.Millisecond)
	stop()
	stop() // idempotent
}
