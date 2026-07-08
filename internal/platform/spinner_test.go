package platform

import (
	"strings"
	"testing"
	"time"
)

// The spinner is TTY-gated (IsTerminalFile). Tests here exercise the
// two pure state-machine surfaces that don't require a real terminal:
//   - spinnerHintLines keeps delayed hints as static rows above the
//     spinner, instead of appending them to every animated redraw.
//   - StartLoadingSpinner{,WithDelayedHint} return a stop() closure
//     that must be idempotent and must not leak the delayed-hint
//     timer when the spinner completes early.
//
// The timer→hintOn transition itself is stdlib time.AfterFunc; we
// don't need to reverify it fires.

func TestSpinnerHintLinesSplitsMultilineHint(t *testing.T) {
	got := spinnerHintLines("first\nsecond\n")
	want := []string{"first", "second"}
	if len(got) != len(want) {
		t.Fatalf("line count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSpinnerHintLinesEmptyHintIsNoop(t *testing.T) {
	if got := spinnerHintLines(""); got != nil {
		t.Fatalf("empty hint should render no static rows, got %#v", got)
	}
}

func TestSlowFileScanHintExplainsWindowsDefender(t *testing.T) {
	got := slowFileScanHintForGOOS("windows")
	if !strings.Contains(got, "antivirus") || !strings.Contains(got, "next reboot") {
		t.Fatalf("windows scan hint should explain Defender-style scan cost, got %q", got)
	}
}

func TestSlowFileScanHintUsesStaticRows(t *testing.T) {
	lines := spinnerHintLines(slowFileScanHintForGOOS("windows"))
	if len(lines) != 3 {
		t.Fatalf("windows scan hint should be three static rows, got %#v", lines)
	}
	if !strings.Contains(lines[1], "10x+ slower") {
		t.Fatalf("windows scan hint should explain the rough magnitude, got %#v", lines)
	}
}

func TestSlowFileScanHintOmitsUnixFallback(t *testing.T) {
	got := slowFileScanHintForGOOS("darwin")
	if got != "" {
		t.Fatalf("non-windows scan hint = %q", got)
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
