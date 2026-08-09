package command

// SpecState tracks whether a Spec was produced from a fully-parsed command
// line (Complete) or from an interactive picker mid-build (Partial). The
// runtime treats them slightly differently — Partial specs skip canonical
// rendering and certain validations that don't apply yet.
type SpecState string

const (
	SpecStateComplete SpecState = "complete"
	SpecStatePartial  SpecState = "partial"
)

// Spec is the typed command model after argv parsing. It's a frozen snapshot
// of the user's intent; downstream callers convert it to []ExecutionScope
// for the actual pipeline. The conversions live in this file
// (ExecutionScopesFromSpec, etc.) so the round-trip stays canonicalized at
// the boundary between argv and runtime.
type Spec struct {
	state  SpecState
	scopes []ScopeSpec
}

// ScopeSpec is one scope inside a Spec — Targets + filter/output state.
// Fields are unexported and reached through accessor methods so callers
// can't accidentally mutate the frozen snapshot.
type ScopeSpec struct {
	targets  []string
	noIgnore bool
	only     []string
	exclude  []string
	stages   []Stage

	hasContainsFilter   bool
	containsPattern     string
	notContainsPatterns []string
	hasPathsOutput      bool
	hasSnippetOutput    bool
	snippetPattern      string
	snippetContextSet   bool
	snippetContextLines int
	hasLinesOutput      bool
	linesStart          int
	linesEnd            int
	outputMode          EntryMode

	changed         bool
	staged          bool
	unstaged        bool
	untracked       bool
	hasGitSelection bool
}

// ExecutionScopeFromScopeSpec converts a ScopeSpec back into the
// ExecutionScope the pipeline consumes. Also runs the deep-include rewrite
// so the CLI form, the interactive picker form, and previews all produce
// the same canonical scope shape.
func ExecutionScopeFromScopeSpec(s ScopeSpec) ExecutionScope {
	out := ExecutionScope{
		Targets:   s.Targets(),
		NoIgnore:  s.NoIgnore(),
		Only:      s.OnlyPatterns(),
		Exclude:   s.ExcludePatterns(),
		Stages:    s.Stages(),
		Paths:     s.HasPathsOutput(),
		Changed:   s.Changed(),
		Staged:    s.Staged(),
		Unstaged:  s.Unstaged(),
		Untracked: s.Untracked(),
	}
	if s.HasContainsFilter() {
		out.Contains = s.ContainsPattern()
	}
	if s.HasNotContainsFilter() {
		out.NotContains = s.NotContainsPatterns()
	}
	if s.HasSnippetOutput() {
		out.Snippet = true
		out.SnippetPattern = s.SnippetPattern()
		out.SnippetContextSet = s.SnippetContextSet()
		out.SnippetContextLines = s.SnippetContextLines()
	}
	if s.HasLinesOutput() {
		out.Lines = true
		out.LinesStart = s.LinesStart()
		out.LinesEnd = s.LinesEnd()
	}
	if s.OutputMode() == EntryModeDiff {
		out.Diff = true
	}
	return out
}

// ExecutionScopesFromSpec is the bulk convenience wrapper.
func ExecutionScopesFromSpec(spec Spec) []ExecutionScope {
	scopeSpecs := spec.Scopes()
	out := make([]ExecutionScope, 0, len(scopeSpecs))
	for _, scopeSpec := range scopeSpecs {
		out = append(out, ExecutionScopeFromScopeSpec(scopeSpec))
	}
	return out
}

// FinalizedSpecFromExecutionScopes builds a Spec marked Complete from the
// runtime []ExecutionScope. Used by the CLI parser at the end of arg parse.
func FinalizedSpecFromExecutionScopes(scopes []ExecutionScope) Spec {
	return specFromExecutionScopes(scopes, SpecStateComplete)
}

// PartialSpecFromExecutionScopes builds a Spec marked Partial. Used by the
// interactive picker during incremental command-build.
func PartialSpecFromExecutionScopes(scopes []ExecutionScope) Spec {
	return specFromExecutionScopes(scopes, SpecStatePartial)
}

func specFromExecutionScopes(scopes []ExecutionScope, state SpecState) Spec {
	out := Spec{
		state:  state,
		scopes: make([]ScopeSpec, 0, len(scopes)),
	}
	for _, s := range scopes {
		out.scopes = append(out.scopes, scopeSpecFromExecutionScope(s))
	}
	return out
}

func scopeSpecFromExecutionScope(s ExecutionScope) ScopeSpec {
	return ScopeSpec{
		targets:             cloneStringSlice(s.Targets),
		noIgnore:            s.NoIgnore || s.HasStage(StageNoIgnore),
		only:                cloneStringSlice(s.Only),
		exclude:             cloneStringSlice(s.Exclude),
		stages:              cloneStages(s.Stages),
		hasContainsFilter:   s.HasStage(StageContains),
		containsPattern:     s.Contains,
		notContainsPatterns: cloneStringSlice(s.NotContains),
		hasPathsOutput:      s.Paths || s.HasStage(StagePaths),
		hasSnippetOutput:    s.Snippet || s.HasStage(StageSnippet),
		snippetPattern:      s.SnippetPattern,
		snippetContextSet:   s.SnippetContextSet,
		snippetContextLines: s.SnippetContextLines,
		hasLinesOutput:      s.Lines || s.HasStage(StageLines),
		linesStart:          s.LinesStart,
		linesEnd:            s.LinesEnd,
		outputMode:          s.OutputMode(),
		changed:             s.Changed,
		staged:              s.Staged,
		unstaged:            s.Unstaged,
		untracked:           s.Untracked,
		hasGitSelection:     s.HasGitSelection(),
	}
}

// --- Spec accessors ---

func (s Spec) Complete() bool {
	return s.state == SpecStateComplete
}

func (s Spec) Scopes() []ScopeSpec {
	out := make([]ScopeSpec, 0, len(s.scopes))
	for _, scopeSpec := range s.scopes {
		out = append(out, scopeSpec.clone())
	}
	return out
}

// --- ScopeSpec accessors ---

func (s ScopeSpec) Targets() []string         { return cloneStringSlice(s.targets) }
func (s ScopeSpec) NoIgnore() bool            { return s.noIgnore }
func (s ScopeSpec) OnlyPatterns() []string    { return cloneStringSlice(s.only) }
func (s ScopeSpec) ExcludePatterns() []string { return cloneStringSlice(s.exclude) }
func (s ScopeSpec) Stages() []Stage           { return cloneStages(s.stages) }

func (s ScopeSpec) HasContainsFilter() bool       { return s.hasContainsFilter }
func (s ScopeSpec) ContainsPattern() string       { return s.containsPattern }
func (s ScopeSpec) NotContainsPatterns() []string { return cloneStringSlice(s.notContainsPatterns) }
func (s ScopeSpec) HasNotContainsFilter() bool    { return len(s.notContainsPatterns) > 0 }
func (s ScopeSpec) HasPathsOutput() bool          { return s.hasPathsOutput }
func (s ScopeSpec) HasSnippetOutput() bool        { return s.hasSnippetOutput }
func (s ScopeSpec) SnippetPattern() string        { return s.snippetPattern }
func (s ScopeSpec) SnippetContextSet() bool       { return s.snippetContextSet }
func (s ScopeSpec) SnippetContextLines() int      { return s.snippetContextLines }
func (s ScopeSpec) HasLinesOutput() bool          { return s.hasLinesOutput }
func (s ScopeSpec) LinesStart() int               { return s.linesStart }
func (s ScopeSpec) LinesEnd() int                 { return s.linesEnd }
func (s ScopeSpec) OutputMode() EntryMode         { return s.outputMode }
func (s ScopeSpec) Changed() bool                 { return s.changed }
func (s ScopeSpec) Staged() bool                  { return s.staged }
func (s ScopeSpec) Unstaged() bool                { return s.unstaged }
func (s ScopeSpec) Untracked() bool               { return s.untracked }
func (s ScopeSpec) HasGitSelection() bool         { return s.hasGitSelection }

func (s ScopeSpec) clone() ScopeSpec {
	s.targets = cloneStringSlice(s.targets)
	s.only = cloneStringSlice(s.only)
	s.exclude = cloneStringSlice(s.exclude)
	s.stages = cloneStages(s.stages)
	s.notContainsPatterns = cloneStringSlice(s.notContainsPatterns)
	return s
}
