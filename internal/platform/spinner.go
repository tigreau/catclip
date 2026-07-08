package platform

import (
	"fmt"
	"os"
	"runtime"
	"strings"
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
	hintRows int
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
// reassurance hint that appears after hintDelay has elapsed without stop()
// being called. Used by slow discovery/content search paths to distinguish
// "still scanning" from "hung". The hint is printed as static lines above
// the spinner; only the one-line spinner is redrawn, so long hints do not
// wrap and stack on Windows terminals.
func StartLoadingSpinnerWithDelayedHint(output *os.File, message, hint string, hintDelay time.Duration) func() {
	return startLoadingSpinner(output, message, hint, hintDelay)
}

// SlowFileScanHint returns the delayed spinner hint used for file-tree and
// content-search scans. Windows gets the explicit Defender explanation because
// the first content scan after reboot pays the on-access antivirus cost once.
// Other platforms return no hint; normal Unix filesystem scans should not be
// framed as inherently slow.
func SlowFileScanHint() string {
	return slowFileScanHintForGOOS(runtime.GOOS)
}

func slowFileScanHintForGOOS(goos string) string {
	if goos == "windows" {
		return "This first Windows content search can be much slower while antivirus scans each file.\nOn large projects, this can feel 10x+ slower than later searches.\nLater searches should reuse the antivirus cache until the next reboot."
	}
	return ""
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
		hintRows := state.hintRows
		hintLines := spinnerHintLines(state.hint)
		if hintOn && hintRows == 0 && len(hintLines) > 0 {
			state.hintRows = len(hintLines)
			hintRows = state.hintRows
		}
		state.hintMu.Unlock()

		if hintOn && hintRows == len(hintLines) && len(hintLines) > 0 {
			_, _ = fmt.Fprint(output, "\r\033[K")
			for _, line := range hintLines {
				_, _ = fmt.Fprintf(output, "%s\n", line)
			}
			state.hintMu.Lock()
			state.hintOn = false
			state.hintMu.Unlock()
		}
		_, _ = fmt.Fprintf(output, "\r\033[K%s %s", frame, state.message)
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
				state.hintMu.Lock()
				hintRows := state.hintRows
				state.hintMu.Unlock()
				clearSpinnerLines(output, hintRows)
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

func clearSpinnerLines(output *os.File, hintRows int) {
	_, _ = fmt.Fprint(output, "\r\033[K")
	for i := 0; i < hintRows; i++ {
		_, _ = fmt.Fprint(output, "\033[1A\r\033[K")
	}
}

func spinnerHintLines(hint string) []string {
	hint = strings.TrimRight(hint, "\r\n")
	if hint == "" {
		return nil
	}
	return strings.Split(hint, "\n")
}

func SpinnerOutputFile(w any) *os.File {
	if file, ok := w.(*os.File); ok {
		return file
	}
	return nil
}
