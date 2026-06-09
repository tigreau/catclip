package platform

import "os"

// CanPromptInteractively reports whether the process can read a y/n response
// from the user, either via stdin (if it is a terminal) or by opening the
// controlling tty directly.
func CanPromptInteractively() bool {
	if IsTerminalFile(os.Stdin) {
		return true
	}
	ttyIn, ttyOut, err := OpenPromptTTY()
	if err != nil {
		return false
	}
	ClosePromptTTY(ttyIn, ttyOut)
	return true
}
