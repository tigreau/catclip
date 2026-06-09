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
