package command

// StageKind identifies one scope-stage transform (--include, --only, …).
// Stages run left-to-right in scope.Stages order so reordering them
// produces different results — see RULES.md and command_spec.go for the
// canonical order rules.
type StageKind string

const (
	StageInclude      StageKind = "include"
	StageOnly         StageKind = "only"
	StageExclude      StageKind = "exclude"
	StageRecent       StageKind = "recent"
	StageDepth        StageKind = "depth"
	StageContains     StageKind = "contains"
	StageChanged      StageKind = "changed"
	StageStaged       StageKind = "staged"
	StageUnstaged     StageKind = "unstaged"
	StageUntracked    StageKind = "untracked"
	StagePaths        StageKind = "paths"
	StageDiff         StageKind = "diff"
	StageSnippet      StageKind = "snippet"
	StageChangedDiff  StageKind = "changed-diff"
	StageStagedDiff   StageKind = "staged-diff"
	StageUnstagedDiff StageKind = "unstaged-diff"
	StageLines        StageKind = "lines"
)

// Stage is a single applied transform on an executionScope's file set.
// Values, Limit, and ExactValues carry the per-stage payload the applier
// table needs to dispatch.
type Stage struct {
	Kind        StageKind
	Values      []string
	Limit       *int
	ExactValues bool
}
