package ui

import "fmt"

// UsageError is the typed error returned by UI-side helpers when the
// user input that reached an interactive flow or internal preview
// runner is invalid (e.g. --internal-prediscovered with multiple
// preview scopes; a startup picker that received a malformed token).
//
// CatclipExitCode participates in root's structural exit protocol, matching
// cli.UsageError, discovery.UsageError, and output.UsageError without making
// root enumerate those concrete package types.
type UsageError struct {
	message string
}

func (e UsageError) Error() string { return e.message }

func (e UsageError) CatclipExitCode() int { return 2 }

// newUsageError constructs a UsageError with a printf-formatted
// message. Mirrors the cli/discovery/output newUsageError shape so
// migrating call sites is a mechanical rename.
func newUsageError(format string, args ...any) error {
	return UsageError{message: fmt.Sprintf(format, args...)}
}
