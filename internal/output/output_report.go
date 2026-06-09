package output

import "fmt"

// Report is the aggregated post-plan accounting shared by emit
// and preview. Lifted out of main.go ahead of the v0.6.0 output
// extraction so the type travels with the rest of the output cluster.
//
// Fields are exported so root preview code (preview.go, preview_table.go,
// tree_bridge.go) can read/write them after the type moves into
// internal/output as output.Report.
type Report struct {
	Sizes     map[string]int64
	Statuses  map[string]string
	ModeTags  map[string]string
	HumanSize string
	Tokens    int64
	CountWord string
	Notices   []string
}

// UsageError is the typed error returned by output-side helpers
// when the user input that reached emit/plan validation is invalid
// (e.g. --raw combined with snippet output). Root exitWithError
// classifies it as exit code 2, matching the cli.UsageError /
// discovery.UsageError precedent. Travels with output in the bundled
// move; on relocation it becomes the exported output.UsageError.
type UsageError struct {
	message string
}

func (e UsageError) Error() string { return e.message }

func newUsageError(format string, args ...any) error {
	return UsageError{message: fmt.Sprintf(format, args...)}
}
