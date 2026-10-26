package discovery

import "github.com/tigreau/catclip/internal/command"

// EmptyRefinement retains only negative evidence from an evaluated focused
// stage. It is not membership: later filters run solely on surviving entries.
type EmptyRefinement struct {
	Evaluated bool
	Paths     []string
}

func (r EmptyRefinement) Clone() EmptyRefinement {
	r.Paths = append([]string(nil), r.Paths...)
	return r
}

func IsRefinementStage(scope command.ExecutionScope, stage command.Stage) bool {
	if scope.Paths {
		return false
	}
	switch scope.OutputMode() {
	case command.EntryModeSnippet:
		return stage.Kind == command.StageSnippet && len(stage.Values) > 0
	case command.EntryModeDiff:
		switch stage.Kind {
		case command.StageDiff, command.StageChanged, command.StageStaged, command.StageUnstaged,
			command.StageChangedDiff, command.StageStagedDiff, command.StageUnstagedDiff:
			return true
		}
	}
	return false
}

// Record compares existing results only. Focused appliers return fresh slices;
// neither regex matching nor Git selection mutates the input membership.
func (r *EmptyRefinement) Record(before, after []Entry) {
	r.Evaluated = true
	if len(before) == len(after) {
		return
	}
	if dropped := len(before) - len(after); dropped > cap(r.Paths)-len(r.Paths) {
		paths := make([]string, len(r.Paths), len(r.Paths)+dropped)
		copy(paths, r.Paths)
		r.Paths = paths
	}
	var kept map[string]struct{}
	if len(after) > 0 {
		kept = make(map[string]struct{}, len(after))
		for _, e := range after {
			kept[e.RelPath] = struct{}{}
		}
	}
	for _, e := range before {
		if _, ok := kept[e.RelPath]; !ok {
			r.Paths = append(r.Paths, e.RelPath)
		}
	}
}
