package output

import "fmt"

// Report is the aggregated post-plan accounting shared by emit
// and preview. Lifted out of main.go ahead of the v0.6.0 output
// extraction so the type travels with the rest of the output cluster.
//
// Fields are exported so root/UI presentation code (preview.go, metadata_report.go,
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

// UsageError is the typed error returned by output-side helpers when user
// input that reached emit/plan validation is invalid (e.g. --raw combined
// with snippet output). CatclipExitCode participates in root's structural
// exit protocol.
type UsageError struct {
	message string
}

func (e UsageError) Error() string { return e.message }

func (e UsageError) CatclipExitCode() int { return 2 }

func newUsageError(format string, args ...any) error {
	return UsageError{message: fmt.Sprintf(format, args...)}
}
