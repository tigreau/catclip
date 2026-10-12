//go:build windows || linux

package fileclip

import "os/exec"

// powershellClipboardCommand creates an automation-only Windows PowerShell
// session for native Windows and WSL file references. Interactive startup can
// load PSReadLine and request publisher trust even when stdin carries a script.
func powershellClipboardCommand(executable, script string) *exec.Cmd {
	return exec.Command(executable, "-NoProfile", "-NonInteractive", "-Command", script)
}
