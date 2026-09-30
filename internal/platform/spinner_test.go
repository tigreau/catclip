package platform

import (
	"reflect"
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

func TestSpinnerHintLines(t *testing.T) {
	tests := []struct {
		name string
		hint string
		want []string
	}{
		{name: "multiline", hint: "first\nsecond\n", want: []string{"first", "second"}},
		{name: "empty", hint: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := spinnerHintLines(tt.hint); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("spinnerHintLines() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSlowFileScanHint(t *testing.T) {
	for _, dir := range []string{`C:\Users\ExampleUser`, "/home/example/project"} {
		hint := SlowFileScanHint(dir)
		for _, want := range []string{"after 5 seconds", dir, "project you want to run Catclip in?"} {
			if !strings.Contains(hint, want) {
				t.Fatalf("scan hint for %q missing %q: %q", dir, want, hint)
			}
		}
	}
}

func TestStartLoadingSpinnerNoopStopIsIdempotent(t *testing.T) {
	tests := []struct {
		name  string
		start func() func()
	}{
		{name: "plain", start: func() func() { return StartLoadingSpinner(nil, "test") }},
		{name: "delayed hint", start: func() func() { return StartLoadingSpinnerWithDelayedHint(nil, "test", "hint", 50*time.Millisecond) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stop := tt.start()
			stop()
			stop()
		})
	}
}
