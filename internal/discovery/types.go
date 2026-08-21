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
	IgnoreBypassed    bool
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
// discovery. Used by ancestor-probe messages and no-ignore basename
// lookup, and propagated onto Entry.BlockSource.
type BlockInfo struct {
	Rule   string
	Source string
}

// SkippedMatch records a basename lookup hit that was rejected
// because the path is blocked by an ignore rule. Surfaces in
// "skipped because ignored" warnings.
type SkippedMatch struct {
	RelPath     string
	BlockSource string
}

// TargetMatch is the structured candidate produced by interactive
// target pickers (including the --no-ignore mixed picker). Carries
// enough state for the picker UI and headless-ambiguity Diagnostics.
type TargetMatch struct {
	Path         string
	Kind         string
	State        string
	Ignored      bool
	IgnoreSource string
	SizeBytes    int64
	SizeKnown    bool
}

// StartupTargetOutcome describes the routing decision established by the
// startup target probe. The startup UI consumes this once instead of asking
// separate reachability, determinism, and non-empty questions that each run
// fuzzy matching over the same project inventory.
type StartupTargetOutcome uint8

const (
	StartupTargetDirect StartupTargetOutcome = iota
	StartupTargetBlocked
	StartupTargetMissing
	StartupTargetUniqueFuzzy
	StartupTargetAmbiguousFuzzy
)

// StartupTargetProbe is the structured result of probing one startup target.
// Matches is populated for fuzzy outcomes so diagnostics
// and future callers can retain the evidence behind the routing decision.
type StartupTargetProbe struct {
	Outcome StartupTargetOutcome
	Matches []TargetMatch
}

// BypassesPicker reports whether fzf cannot help with this target. Normal
// discovery must run so it can emit the precise ignored or not-found message.
func (p StartupTargetProbe) BypassesPicker() bool {
	return p.Outcome == StartupTargetBlocked || p.Outcome == StartupTargetMissing
}

// RequiresPicker reports whether the target has more than one valid answer.
func (p StartupTargetProbe) RequiresPicker() bool {
	return p.Outcome == StartupTargetAmbiguousFuzzy
}

// Diagnostic is a single user-facing message plus the independent effects
// root needs when aggregating a run. The booleans are deliberately not a kind
// enum: hard target errors become both IsError and IsScopeUnsatisfiable at the
// scope boundary, while target-not-found remains a partial-result outcome.
//
// IsError drops sibling entries from the same scope rather than emitting a
// subset of a request that contained a hard target failure.
// IsTargetNotFound makes the invocation exit 1 while allowing other resolved
// targets and scopes to emit.
// IsScopeUnsatisfiable means the whole scope could not run. An all-empty run
// exits 2; mixed successful/unsatisfiable scopes retain the current partial
// output plus exit-1 behavior.
// ExplainsEmptyResult suppresses generic no-files speculation only when every
// empty scope is covered by a precise diagnostic.
// ScopeIndex >= 0 names the owning --then scope. ScopeIndex == -1 is reserved
// for invocation-wide parser warnings, which never explain a scope's emptiness.
type Diagnostic struct {
	Message              string
	IsError              bool
	IsTargetNotFound     bool
	IsScopeUnsatisfiable bool
	ExplainsEmptyResult  bool
	ScopeIndex           int
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
