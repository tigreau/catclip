package platform

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// FileScanProgress describes only the current unknown-name classification
// batch, not every enumerated path. Zero Total means another discovery phase.
type FileScanProgress struct {
	Completed int
	Total     int
	Started   time.Time
}

// StartFileScanProgress shares the active-spinner lifecycle with other setup
// work. The observer is enabled only for a capable terminal, never for pipes.
func StartFileScanProgress(output *os.File, workingDir string, observe func() (func() FileScanProgress, func())) func() {
	if output == nil || !IsTerminalFile(output) || os.Getenv("TERM") == "dumb" {
		return func() {}
	}
	StopActiveSpinner()
	snapshot, finish := observe()
	started := time.Now()
	coloredHint := slowProgressHint(workingDir, ActivePaletteForWriter(output))
	return startProgressSpinner(output, func(now time.Time) (string, string) {
		message, hint := fileScanProgressText(now, started, workingDir, snapshot())
		if hint != "" {
			hint = coloredHint
		}
		return message, hint
	}, finish, false)
}

// StartOutputPreparationProgress covers the work after discovery: preparing
// bodies/excerpts and their sizes can outlast classification on a large tree.
// It has no classifier observer and must not display a completed-file count.
func StartOutputPreparationProgress(output *os.File, workingDir string) func() {
	if output == nil || !IsTerminalFile(output) || os.Getenv("TERM") == "dumb" {
		return func() {}
	}
	StopActiveSpinner()
	started := time.Now()
	coloredHint := slowProgressHint(workingDir, ActivePaletteForWriter(output))
	return startProgressSpinner(output, func(now time.Time) (string, string) {
		message, hint := delayedProgressText(now.Sub(started), workingDir, "Preparing selected files…")
		if hint != "" {
			hint = coloredHint
		}
		return message, hint
	}, func() {}, false)
}

func startProgressSpinner(output *os.File, frameText func(time.Time) (string, string), finish func(), immediate bool) func() {
	state := &loadingSpinnerState{stop: make(chan struct{}), done: make(chan struct{})}
	activeSpinner.mu.Lock()
	activeSpinner.state = state
	activeSpinner.mu.Unlock()
	go func() {
		defer close(state.done)
		defer finish()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		frames := []string{"|", "/", "-", `\`}
		renderer := fileScanProgressRenderer{output: output}
		frame := 0
		if immediate {
			message, hint := frameText(time.Now())
			renderer.draw(frames[0], message, hint)
		}
		for {
			select {
			case <-state.stop:
				renderer.clear()
				activeSpinner.mu.Lock()
				if activeSpinner.state == state {
					activeSpinner.state = nil
				}
				activeSpinner.mu.Unlock()
				return
			case now := <-ticker.C:
				message, hint := frameText(now)
				renderer.draw(frames[frame%len(frames)], message, hint)
				frame++
			}
		}
	}()
	return state.stopAndWait
}

type fileScanProgressRenderer struct {
	output   io.Writer
	visible  bool
	hintRows int
	lastHint string
}

func (r *fileScanProgressRenderer) clear() {
	if r.visible {
		clearSpinnerLines(r.output, r.hintRows)
		r.visible, r.hintRows = false, 0
	}
}

func (r *fileScanProgressRenderer) draw(frame, message, hint string) {
	if message == "" {
		r.clear()
		r.lastHint = ""
		return
	}
	if hint != r.lastHint {
		r.clear()
		r.lastHint = hint
	}
	if !r.visible && hint != "" {
		// Preserve the intentional blank line after the project guidance.
		for _, line := range strings.Split(hint, "\n") {
			fmt.Fprintln(r.output, line)
			r.hintRows++
		}
	}
	fmt.Fprintf(r.output, "\r\033[K%s %s", frame, message)
	r.visible = true
}

func fileScanProgressText(now, started time.Time, workingDir string, progress FileScanProgress) (message, hint string) {
	if progress.Total > 0 {
		// A quick classification after a slow walk must not flash a message.
		if now.Sub(progress.Started) < time.Second || progress.Completed >= progress.Total {
			return "", ""
		}
		message = fmt.Sprintf("Checking unknown file types… %s / %s", progressCount(progress.Completed), progressCount(progress.Total))
	} else {
		return delayedProgressText(now.Sub(started), workingDir, "Finding files…")
	}
	return delayedProgressText(now.Sub(started), workingDir, message)
}

func delayedProgressText(elapsed time.Duration, workingDir, label string) (message, hint string) {
	if elapsed < time.Second {
		return "", ""
	}
	if elapsed >= 5*time.Second {
		// Do not speculate about antivirus or claim that a cache was written.
		hint = slowProgressHint(workingDir, Palette{})
	}
	return label, hint
}

func slowProgressHint(workingDir string, colors Palette) string {
	return fmt.Sprintf("Is %s%s%s%s the project you want to scan?\nIf not, press %s%sCtrl+C%s and run Catclip inside your project folder.\n",
		colors.Bold, colors.Warn, workingDir, colors.Reset, colors.Bold, colors.Err, colors.Reset)
}

func progressCount(n int) string {
	s := fmt.Sprint(n)
	var out strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(r)
	}
	return out.String()
}
