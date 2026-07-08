package discovery

import (
	"regexp"
	"time"

	"github.com/tigreau/catclip/internal/command"
)

// Entry is the per-file row produced by discovery. Output, preview,
// emit, and pickers consume it. Field shapes are POD so the type can
// cross package boundaries cleanly. Was root catclip.Entry before
// the v0.6.0 discovery extraction.
type Entry struct {
	AbsPath             string
	RelPath             string
	ModTime             time.Time
	SizeBytes           int64
	SizeKnown           bool
	TargetRoot          string
	GitVisible          bool
	Mode                command.EntryMode
	SnippetPattern      string
	SnippetContextSet   bool
	SnippetContextLines int
	// SnippetMatchLines carries the --snippet pattern's matched line
	// numbers, pinned by the snippet content stage in the same rg pass
	// that decides membership (the filter-attribute-persistence model:
	// pin → carry → consume). Valid only for SnippetPattern, and
	// consumers are mode-gated on EntryModeSnippet, so a stale value is
	// structurally unreadable. Absent (nil) is always safe — the
	// BatchSnippetMatches fallback recomputes. Serialized in checkpoint
	// JSON; old checkpoints yield nil → fallback.
	SnippetMatchLines []int
	Lines             bool
	LinesStart        int
	LinesEnd          int
	DiffWantStaged    bool
	DiffWantUnstaged  bool
	AllowedByInclude  bool
	BlockSource       string
}

// Discovered is the typed output of running discovery across all
// scopes of an invocation: the original Config plus per-scope Scope
// payloads. Was root catclip.Discovered (renamed to avoid
// collision with command.Invocation when the cluster moved).
type Discovered struct {
	Config command.Invocation
	Scopes []Scope
}

// BlockInfo describes the ignore-source that blocked a path during
// discovery. Used by ancestor-probe messages and basename-include
// lookup, and propagated onto Entry.BlockSource.
type BlockInfo struct {
	Rule   string
	Source string
}

// SkippedMatch records a basename-include lookup hit that was rejected
// because the path is blocked by an ignore rule. Surfaces in
// "skipped because ignored" warnings.
type SkippedMatch struct {
	RelPath     string
	BlockSource string
}

// TargetMatch is the structured candidate produced by interactive
// target pickers (--include selection, root-target fzf). Carries
// enough state for the picker UI and headless-ambiguity Diagnostics.
type TargetMatch struct {
	Path         string
	Kind         string
	State        string
	Ignored      bool
	IgnoreSource string
}

// Diagnostic is a single user-facing message produced during
// discovery (or, occasionally, by the parser). Field shapes are
// exported so root can construct parser warnings before discovery
// runs. Was root catclip.Diagnostic.
type Diagnostic struct {
	Message              string
	IsError              bool
	IsTargetNotFound     bool
	IsScopeUnsatisfiable bool
}

// compiledGlob caches a raw glob pattern alongside its regexp.
// Cluster-internal: only the StageValueMatcher and resolver glob path
// hold these.
type compiledGlob struct {
	raw string
	re  *regexp.Regexp
}

// VisibleDirIndex and visibleFileIndex are the resolver's cached
// project-wide visibility tables. Built lazily; reset between scopes
// only if include-state changes. VisibleDirIndex is exported (along
// with its fields) so root-side tests can inspect the resolver's
// dir-set after BuildVisibleDirIndex.
type VisibleDirIndex struct {
	Dirs        []string
	Set         map[string]struct{}
	SymlinkDirs []string
}

type visibleFileIndex struct {
	byBase        map[string][]Entry
	skippedByBase map[string][]SkippedMatch
}
