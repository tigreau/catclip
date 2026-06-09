package ui

import "fmt"

// UsageError is the typed error returned by UI-side helpers when the
// user input that reached an interactive flow or internal preview
// runner is invalid (e.g. --internal-prediscovered with multiple
// preview scopes; a startup picker that received a malformed token).
//
// Root exitWithError classifies it as exit code 2, matching the
// cli.UsageError / discovery.UsageError / output.UsageError pattern.
// This type ships ahead of the bundled internal/ui move (commit 2E)
// so call sites that currently use root newUsageError have a typed
// destination available — preventing the bundle move from forcing
// root → ui imports just to construct usage errors.
type UsageError struct {
	message string
}

func (e UsageError) Error() string { return e.message }

// newUsageError constructs a UsageError with a printf-formatted
// message. Mirrors the cli/discovery/output newUsageError shape so
// migrating call sites is a mechanical rename.
func newUsageError(format string, args ...any) error {
	return UsageError{message: fmt.Sprintf(format, args...)}
}
