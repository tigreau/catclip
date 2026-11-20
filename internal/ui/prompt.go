package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/tigreau/catclip/internal/platform"
)

// headlessPromptGuardCount tracks how many in-flight code paths have
// claimed "this run must not prompt the user" — typically because
// --headless is set or the process is running as an --internal-* fzf
// child that has no stdin/tty access. Any PromptYesNo / readLine /
// readPromptResponse call that fires while this counter is > 0 returns
// a BUG error so we catch the regression instead of hanging.
//
// Migrated from root cli.go ahead of the v0.6.0 internal/ui bundled
// move (commit 2E) so the prompt machinery's eventual landing site
// already exists. Two root callers (preview.go's "Proceed?" confirm
// and cli.go's "Are you sure?" hiss reset) switch to ui.PromptYesNo
// in this prep commit.
var headlessPromptGuardCount atomic.Int32

// PushHeadlessPromptGuard arms the guard for the duration of a run.
// Returns a restore function the caller defers. The guard is a counter
// because internal/headless invocations can nest (e.g. a sink picker
// preview re-execing into the binary).
func PushHeadlessPromptGuard(enabled bool) func() {
	if !enabled {
		return func() {}
	}
	headlessPromptGuardCount.Add(1)
	return func() {
		headlessPromptGuardCount.Add(-1)
	}
}

// headlessPromptGuardActive reports whether prompting is currently
// forbidden. Exposed so other UI surfaces (interactive picker drivers)
// can short-circuit before invoking the prompt machinery.
func headlessPromptGuardActive() bool {
	return headlessPromptGuardCount.Load() > 0
}

func headlessPromptBugError() error {
	return fmt.Errorf("BUG: reached interactive prompt in headless mode (--headless or --internal-*).\n  This is a catclip bug; please report it.")
}

// PromptYesNo writes prompt to stderr and reads a y/n answer.
// defaultYes determines the default when the user presses Enter; the
// returned bool is the chosen answer. Returns the bug error if the
// headless guard is active.
func PromptYesNo(prompt string, defaultYes bool, stderr io.Writer) (bool, error) {
	defaultResponse := "n"
	if defaultYes {
		defaultResponse = "y"
	}

	response, ok, err := readPromptResponse(prompt, stderr)
	if err != nil {
		return false, err
	}
	if ok {
		response = strings.TrimSpace(strings.ToLower(response))
		if response == "" {
			response = defaultResponse
		}
		return response == "y" || response == "yes", nil
	}
	return defaultYes, nil
}

// readPromptResponse asks on stdin only when stdin is a real console a person
// can type into. A redirected stdin, such as NUL on Windows (a character
// device that reads as an empty line), is never read as an answer: the
// question goes to the controlling terminal instead, and when there is none
// nobody answered (ok is false).
func readPromptResponse(prompt string, stderr io.Writer) (string, bool, error) {
	if headlessPromptGuardActive() {
		return "", false, headlessPromptBugError()
	}
	if platform.IsConsoleInput(os.Stdin) {
		response, ok := readPromptKey(os.Stdin, stderr, prompt)
		return response, ok, nil
	}

	ttyIn, ttyOut, err := platform.OpenPromptTTY()
	if err != nil {
		return "", false, nil
	}
	defer platform.ClosePromptTTY(ttyIn, ttyOut)

	response, ok := readPromptKey(ttyIn, ttyOut, prompt)
	return response, ok, nil
}

func readPromptKey(input *os.File, output io.Writer, prompt string) (string, bool) {
	if response, ok := platform.ReadPromptByte(input, output, prompt); ok {
		return response, true
	}
	return readPromptLine(input, output, prompt)
}

func readPromptLine(input *os.File, output io.Writer, prompt string) (string, bool) {
	if _, err := fmt.Fprintf(output, "%s ", prompt); err != nil {
		return "", false
	}
	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		return "", false
	}
	return scanner.Text(), true
}

// PromptChoice accepts a single 1 or 2. Zero means no terminal input.
func PromptChoice(prompt string, stderr io.Writer) (int, error) {
	response, ok, err := readPromptResponse(prompt, stderr)
	if err != nil || !ok {
		return 0, err
	}
	switch response {
	case "1":
		return 1, nil
	case "2":
		return 2, nil
	default:
		return 0, fmt.Errorf("Invalid choice. Choose 1 or 2.")
	}
}
