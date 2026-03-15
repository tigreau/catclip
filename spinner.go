package catclip

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
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// startLoadingSpinner shows a small TTY-only spinner for slow interactive
// setup work such as building picker candidate lists. It clears itself before
// returning control to fzf or normal stderr output.
func startLoadingSpinner(output *os.File, message string) func() {
	if output == nil || !isTerminalFile(output) {
		return func() {}
	}
	if os.Getenv("TERM") == "dumb" {
		return func() {}
	}

	stopActiveSpinner()

	state := &loadingSpinnerState{
		output:  output,
		message: message,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	frames := []string{"|", "/", "-", `\`}
	drawFrame := func(frame string) {
		_, _ = fmt.Fprintf(output, "\r\033[K%s %s", frame, message)
	}

	activeSpinner.mu.Lock()
	activeSpinner.state = state
	activeSpinner.mu.Unlock()

	// Draw immediately so short-lived stages still show a visible loading label.
	drawFrame(frames[0])
	go func() {
		defer close(state.done)
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()

		i := 1
		for {
			select {
			case <-state.stop:
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

func stopActiveSpinner() {
	activeSpinner.mu.Lock()
	state := activeSpinner.state
	activeSpinner.mu.Unlock()
	if state != nil {
		state.stopAndWait()
	}
}

func spinnerOutputFile(w any) *os.File {
	if file, ok := w.(*os.File); ok {
		return file
	}
	return nil
}

func outputSpinnerMessage(cfg runConfig) string {
	if cfg.OutputMode == outputModeClipboard {
		return "Copying files..."
	}
	return "Writing files..."
}
