package platform

import "os"

// CanPromptInteractively reports whether the process can read a y/n response
// from the user, either via stdin (if it is a console a person can type into)
// or by opening the controlling tty directly. A redirected stdin such as NUL
// on Windows is not a console: nobody can answer through it.
func CanPromptInteractively() bool {
	if IsConsoleInput(os.Stdin) {
		return true
	}
	ttyIn, ttyOut, err := OpenPromptTTY()
	if err != nil {
		return false
	}
	ClosePromptTTY(ttyIn, ttyOut)
	return true
}
