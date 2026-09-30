//go:build !windows

package picker

import "testing"

func TestJetBrainsMouseDoesNotApplyOutsideWindows(t *testing.T) {
	t.Setenv("TERMINAL_EMULATOR", "JetBrains-JediTerm")
	restore, err := preparePickerTerminal()
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
}
