package command

// ExecutionScope is the post-parse, post-resolve view of a single scope:
// the targets to walk, the filter/output stages to apply in order, and the
// flags that pick output shape (lines / snippet / diff / paths). The
// pipeline's stage appliers consume an ExecutionScope plus their per-stage
// inputs; nothing in this file knows about filesystem state.
type ExecutionScope struct {
	Targets         []string
	IncludedTargets []string
	Only            []string
	Exclude         []string
	Contains        string
	NotContains     []string
	SnippetPattern  string
	// SnippetContextSet=false is blank-line block mode (the default).
	// SnippetContextSet=true is fixed-context mode: SnippetContextLines (0..200)
	// lines before and after each match; 0 emits matching lines only.
	SnippetContextSet   bool
	SnippetContextLines int
	Lines               bool
	LinesStart          int
	LinesEnd            int
	Stages              []Stage
	Paths               bool
	Snippet             bool
	Changed             bool
	Staged              bool
	Unstaged            bool
	Untracked           bool
	Diff                bool
}

// OutputMode returns the EntryMode the scope's output flags imply.
func (s ExecutionScope) OutputMode() EntryMode {
	if s.Diff {
		return EntryModeDiff
	}
	if s.Snippet {
		return EntryModeSnippet
	}
	if s.Lines {
		return EntryModeLines
	}
	return EntryModeFull
}

// HasGitSelection reports whether any of the git-selection filter flags
// are active. Useful to early-exit when a non-git scope can skip the git
// runner entirely.
func (s ExecutionScope) HasGitSelection() bool {
	return s.Changed || s.Staged || s.Unstaged || s.Untracked
}

// HasStage reports whether the scope already carries a stage of the given
// kind. Used by the spec round-trip to derive the boolean flag-mirrors
// (HasPathsOutput / HasContainsFilter / etc.).
func (s ExecutionScope) HasStage(kind StageKind) bool {
	for _, stage := range s.Stages {
		if stage.Kind == kind {
			return true
		}
	}
	return false
}

// GitSelectionRequiresGitRepoMessage is the canonical error string returned
// when --changed / --staged / --unstaged / --untracked (or their -diff
// variants) are used outside a git repo. Lives in command so non-CLI
// callers (interactive picker validation) can use the same text.
func GitSelectionRequiresGitRepoMessage() string {
	return "Error: --changed/--staged/--unstaged/--untracked (and -diff variants) require a git repository.\n  cwd is not a git repo or git is not installed."
}

// IsDirectModeEligible reports whether the scope qualifies for the
// "direct rg" content-match shortcut: instead of catclip walking the
// scope, applying filter stages, and then chunking rg invocations
// across the resulting path list, the direct path lets rg do walk +
// content match in one call against the scope target.
//
// The predicate is deliberately narrow. The only piece of
// configuration the canonical rg command can faithfully reproduce is
// the regex pattern itself — every other catclip modifier (--only,
// --exclude, --recent, --depth, --include, git filters, multi-leg
// --then, --with-binaries) diverges in semantics or invariants from
// what rg would do natively. Anything outside this set must fall
// back to the chunked path. See
// docs/versions/v0.6.3/reports/ACTIVE_PLAN_direct_rg_contains_snippet.md
// for the parity analysis.
//
// invocation.WithBinaries is the only top-level invocation flag that
// blocks eligibility; the rest are all scope-level.
func IsDirectModeEligible(invocation Invocation, scope ExecutionScope) bool {
	hasContains := scope.Contains != ""
	hasSnippet := scope.Snippet && scope.SnippetPattern != ""
	hasNotContains := len(scope.NotContains) > 0
	if !hasContains && !hasSnippet && !hasNotContains {
		return false
	}
	if invocation.WithBinaries {
		return false
	}
	if len(scope.Targets) != 1 {
		return false
	}
	if len(scope.IncludedTargets) > 0 {
		return false
	}
	if len(scope.Only) > 0 || len(scope.Exclude) > 0 {
		return false
	}
	if scope.HasGitSelection() {
		return false
	}
	for _, stage := range scope.Stages {
		switch stage.Kind {
		case StageContains, StageSnippet, StageNotContains, StagePaths, StageLines:
			// Content predicates (contains / snippet / not-contains)
			// each map cleanly to one rg call. Terminal/output stages
			// (paths / lines) don't change the file set.
		default:
			// Any narrowing stage (Recent / Size / Depth / Only /
			// Exclude / Include / git variants) means the scope's
			// file set is not directly expressible as
			// `rg PATTERN <target>`.
			return false
		}
	}
	return true
}
