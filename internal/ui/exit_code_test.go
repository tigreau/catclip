package ui

import "testing"

type catclipExitCoderForTest interface {
	error
	CatclipExitCode() int
}

var _ catclipExitCoderForTest = exitError{}

func TestExitErrorExposesCatclipExitCode(t *testing.T) {
	err := exitError{message: "stop", code: 2}
	if got := err.CatclipExitCode(); got != 2 {
		t.Fatalf("CatclipExitCode() = %d, want 2", got)
	}
}
