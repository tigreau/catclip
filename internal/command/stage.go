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
	StageSize         StageKind = "size"
	StageDepth        StageKind = "depth"
	StageContains     StageKind = "contains"
	StageNotContains  StageKind = "not-contains"
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
// Values carry string payloads such as paths, globs, and regexes. Limit is
// the single-scalar numeric payload slot. Nums is the tuple/range numeric
// payload slot. A stage should use at most one numeric slot.
type Stage struct {
	Kind        StageKind
	Values      []string
	Limit       *int
	Nums        []int
	ExactValues bool
}
