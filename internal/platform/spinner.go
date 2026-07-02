package platform

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var activeSpinner struct {
	mu    sync.Mutex
	state *loadingSpinnerState
}

type loadingSpinnerState struct {
	output   *os.File
	message  string
	hint     string // Rendered after hintDelay elapses; empty means no hint.
	hintOn   bool
	hintMu   sync.Mutex
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// StartLoadingSpinner shows a small TTY-only spinner for slow interactive
// setup work such as building picker candidate lists. It clears itself before
// returning control to fzf or normal stderr output.
func StartLoadingSpinner(output *os.File, message string) func() {
	return startLoadingSpinner(output, message, "", 0)
}

// StartLoadingSpinnerWithDelayedHint is StartLoadingSpinner plus a
// reassurance hint that appends to the rendered message after hintDelay
// has elapsed without stop() being called. Used by the target-picker
// spinner to signal "first run is supposed to be slow" past the
// cold-boot Defender-scan patience threshold. Both message and hint
// stay stable; only their concatenation toggles at hintDelay.
func StartLoadingSpinnerWithDelayedHint(output *os.File, message, hint string, hintDelay time.Duration) func() {
	return startLoadingSpinner(output, message, hint, hintDelay)
}

func startLoadingSpinner(output *os.File, message, hint string, hintDelay time.Duration) func() {
	if output == nil || !IsTerminalFile(output) {
		return func() {}
	}
	if os.Getenv("TERM") == "dumb" {
		return func() {}
	}

	StopActiveSpinner()

	state := &loadingSpinnerState{
		output:  output,
		message: message,
		hint:    hint,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	frames := []string{"|", "/", "-", `\`}
	drawFrame := func(frame string) {
		state.hintMu.Lock()
		hintOn := state.hintOn
		state.hintMu.Unlock()
		_, _ = fmt.Fprintf(output, "\r\033[K%s %s", frame, spinnerMessageWithHint(state.message, state.hint, hintOn))
	}

	activeSpinner.mu.Lock()
	activeSpinner.state = state
	activeSpinner.mu.Unlock()

	// Draw immediately so short-lived stages still show a visible loading label.
	drawFrame(frames[0])

	// Schedule the hint transition. AfterFunc's timer is cancelled by
	// stop() below so a fast completion never leaks the goroutine.
	var hintTimer *time.Timer
	if hint != "" && hintDelay > 0 {
		hintTimer = time.AfterFunc(hintDelay, func() {
			state.hintMu.Lock()
			state.hintOn = true
			state.hintMu.Unlock()
		})
	}

	go func() {
		defer close(state.done)
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()

		i := 1
		for {
			select {
			case <-state.stop:
				if hintTimer != nil {
					hintTimer.Stop()
				}
				_, _ = fmt.Fprint(output, "\r\033[K")
				activeSpinner.mu.Lock()
				if activeSpinner.state == state {
					activeSpinner.state = nil
				}
				activeSpinner.mu.Unlock()
				return
			case <-ticker.C:
				drawFrame(frames[i%len(frames)])
				i++
			}
		}
	}()

	return state.stopAndWait
}

func (s *loadingSpinnerState) stopAndWait() {
	s.stopOnce.Do(func() {
		close(s.stop)
		<-s.done
	})
}

func StopActiveSpinner() {
	activeSpinner.mu.Lock()
	state := activeSpinner.state
	activeSpinner.mu.Unlock()
	if state != nil {
		state.stopAndWait()
	}
}

// spinnerMessageWithHint returns the message that renders next to the
// spinning frame. When hintOn is true (fired by the delayed timer),
// hint is appended after a space. Pure function so the hint-append
// rule is unit-testable without wiring a TTY.
func spinnerMessageWithHint(message, hint string, hintOn bool) string {
	if hintOn && hint != "" {
		return message + " " + hint
	}
	return message
}

func SpinnerOutputFile(w any) *os.File {
	if file, ok := w.(*os.File); ok {
		return file
	}
	return nil
}
