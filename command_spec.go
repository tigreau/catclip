package catclip

type commandSpecState string

const (
	commandSpecStateComplete commandSpecState = "complete"
	commandSpecStatePartial  commandSpecState = "partial"
)

type commandSpec struct {
	state  commandSpecState
	scopes []commandScopeSpec
}

type commandScopeSpec struct {
	targets         []string
	includedTargets []string
	only            []string
	exclude         []string
	stages          []scopeStage

	hasContainsFilter bool
	containsPattern   string
	hasPathsOutput    bool
	hasSnippetOutput  bool
	snippetPattern    string
	hasLinesOutput    bool
	linesStart        int
	linesEnd          int
	outputMode        entryMode

	changed         bool
	staged          bool
	unstaged        bool
	untracked       bool
	hasGitSelection bool
}

func configCommandSpec(cfg runConfig) commandSpec {
	return cfg.Command
}

func configCommandScopes(cfg runConfig) []commandScopeSpec {
	return configCommandSpec(cfg).Scopes()
}

func executionScopeFromCommandScopeSpec(s commandScopeSpec) executionScope {
	out := executionScope{
		Targets:         s.Targets(),
		IncludedTargets: s.IncludedTargets(),
		Only:            s.OnlyPatterns(),
		Exclude:         s.ExcludePatterns(),
		Stages:          s.Stages(),
		Paths:           s.HasPathsOutput(),
		Changed:         s.Changed(),
		Staged:          s.Staged(),
		Unstaged:        s.Unstaged(),
		Untracked:       s.Untracked(),
	}
	if s.HasContainsFilter() {
		out.Contains = s.ContainsPattern()
	}
	if s.HasSnippetOutput() {
		out.Snippet = true
		out.SnippetPattern = s.SnippetPattern()
	}
	if s.HasLinesOutput() {
		out.Lines = true
		out.LinesStart = s.LinesStart()
		out.LinesEnd = s.LinesEnd()
	}
	if s.OutputMode() == entryModeDiff {
		out.Diff = true
	}
	return out
}

func executionScopesFromCommandSpec(spec commandSpec) []executionScope {
	scopeSpecs := spec.Scopes()
	out := make([]executionScope, 0, len(scopeSpecs))
	for _, scopeSpec := range scopeSpecs {
		out = append(out, executionScopeFromCommandScopeSpec(scopeSpec))
	}
	return out
}

func finalizedCommandSpecFromExecutionScopes(scopes []executionScope) commandSpec {
	return commandSpecFromExecutionScopes(scopes, commandSpecStateComplete)
}

func partialCommandSpecFromExecutionScopes(scopes []executionScope) commandSpec {
	return commandSpecFromExecutionScopes(scopes, commandSpecStatePartial)
}

func commandSpecFromExecutionScopes(scopes []executionScope, state commandSpecState) commandSpec {
	out := commandSpec{
		state:  state,
		scopes: make([]commandScopeSpec, 0, len(scopes)),
	}
	for _, s := range scopes {
		out.scopes = append(out.scopes, commandScopeSpecFromExecutionScope(s))
	}
	return out
}

func commandScopeSpecFromExecutionScope(s executionScope) commandScopeSpec {
	return commandScopeSpec{
		targets:           cloneStringSlice(s.Targets),
		includedTargets:   cloneStringSlice(s.IncludedTargets),
		only:              cloneStringSlice(s.Only),
		exclude:           cloneStringSlice(s.Exclude),
		stages:            cloneScopeStages(s.Stages),
		hasContainsFilter: executionScopeHasStage(s, scopeStageContains),
		containsPattern:   s.Contains,
		hasPathsOutput:    s.Paths || executionScopeHasStage(s, scopeStagePaths),
		hasSnippetOutput:  s.Snippet || executionScopeHasStage(s, scopeStageSnippet),
		snippetPattern:    s.SnippetPattern,
		hasLinesOutput:    s.Lines || executionScopeHasStage(s, scopeStageLines),
		linesStart:        s.LinesStart,
		linesEnd:          s.LinesEnd,
		outputMode:        executionScopeOutputMode(s),
		changed:           s.Changed,
		staged:            s.Staged,
		unstaged:          s.Unstaged,
		untracked:         s.Untracked,
		hasGitSelection:   executionScopeHasGitSelection(s),
	}
}

func (s commandSpec) Complete() bool {
	return s.state == commandSpecStateComplete
}

func (s commandSpec) Scopes() []commandScopeSpec {
	out := make([]commandScopeSpec, 0, len(s.scopes))
	for _, scopeSpec := range s.scopes {
		out = append(out, scopeSpec.clone())
	}
	return out
}

func (s commandScopeSpec) Targets() []string {
	return cloneStringSlice(s.targets)
}

func (s commandScopeSpec) IncludedTargets() []string {
	return cloneStringSlice(s.includedTargets)
}

func (s commandScopeSpec) OnlyPatterns() []string {
	return cloneStringSlice(s.only)
}

func (s commandScopeSpec) ExcludePatterns() []string {
	return cloneStringSlice(s.exclude)
}

func (s commandScopeSpec) Stages() []scopeStage {
	return cloneScopeStages(s.stages)
}

func (s commandScopeSpec) HasContainsFilter() bool {
	return s.hasContainsFilter
}

func (s commandScopeSpec) ContainsPattern() string {
	return s.containsPattern
}

func (s commandScopeSpec) HasPathsOutput() bool {
	return s.hasPathsOutput
}

func (s commandScopeSpec) HasSnippetOutput() bool {
	return s.hasSnippetOutput
}

func (s commandScopeSpec) SnippetPattern() string {
	return s.snippetPattern
}

func (s commandScopeSpec) HasLinesOutput() bool {
	return s.hasLinesOutput
}

func (s commandScopeSpec) LinesStart() int {
	return s.linesStart
}

func (s commandScopeSpec) LinesEnd() int {
	return s.linesEnd
}

func (s commandScopeSpec) OutputMode() entryMode {
	return s.outputMode
}

func (s commandScopeSpec) Changed() bool {
	return s.changed
}

func (s commandScopeSpec) Staged() bool {
	return s.staged
}

func (s commandScopeSpec) Unstaged() bool {
	return s.unstaged
}

func (s commandScopeSpec) Untracked() bool {
	return s.untracked
}

func (s commandScopeSpec) HasGitSelection() bool {
	return s.hasGitSelection
}

func (s commandScopeSpec) clone() commandScopeSpec {
	s.targets = cloneStringSlice(s.targets)
	s.includedTargets = cloneStringSlice(s.includedTargets)
	s.only = cloneStringSlice(s.only)
	s.exclude = cloneStringSlice(s.exclude)
	s.stages = cloneScopeStages(s.stages)
	return s
}

func executionScopeHasStage(s executionScope, kind scopeStageKind) bool {
	for _, stage := range s.Stages {
		if stage.Kind == kind {
			return true
		}
	}
	return false
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneScopeStages(in []scopeStage) []scopeStage {
	if len(in) == 0 {
		return nil
	}
	out := make([]scopeStage, 0, len(in))
	for _, stage := range in {
		cloned := scopeStage{
			Kind:   stage.Kind,
			Values: cloneStringSlice(stage.Values),
		}
		if stage.Limit != nil {
			limit := *stage.Limit
			cloned.Limit = &limit
		}
		out = append(out, cloned)
	}
	return out
}
