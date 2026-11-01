package platform

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

var activeSpinner struct {
	mu    sync.Mutex
	state *loadingSpinnerState
}

type loadingSpinnerState struct {
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// StartLoadingSpinner retains immediate feedback for output/sink setup work.
// File discovery uses StartFileScanProgress's delayed, phase-aware display.
func StartLoadingSpinner(output *os.File, message string) func() {
	if output == nil || !IsTerminalFile(output) || os.Getenv("TERM") == "dumb" {
		return func() {}
	}
	StopActiveSpinner()
	return startProgressSpinner(output, func(time.Time) (string, string) {
		return message, ""
	}, func() {}, true)
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

func clearSpinnerLines(output io.Writer, hintRows int) {
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
