package command

// EntryMode describes how a single file entry should be emitted: full body,
// a line slice, a snippet, or a diff. The same constants thread through
// fileEntry.Mode (root domain), ScopeSpec.OutputMode(), and the emit layer.
type EntryMode string

const (
	EntryModeFull    EntryMode = "full"
	EntryModeLines   EntryMode = "lines"
	EntryModeSnippet EntryMode = "snippet"
	EntryModeDiff    EntryMode = "diff"
)
