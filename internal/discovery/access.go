package discovery

import (
	"path/filepath"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/search"
)

func (r *Resolver) metadataWithoutBody(rel string) bool {
	return r.Cfg.PayloadKind == command.PayloadMetadata && search.BodyPermissionDenied(filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(rel)))
}

// AccessFailuresForScopes filters observation evidence to the current request.
// It cannot add paths to payload membership or turn an unknown file into text.
func AccessFailuresForScopes(root string, scopes []Scope, metadata bool) []search.AccessFailure {
	var out []search.AccessFailure
	for _, issue := range search.AccessFailures(root) {
		for _, scope := range scopes {
			if (metadata || scope.Scope.Metadata || scope.Scope.Paths) && !issue.Metadata {
				continue
			}
			roots := scope.Scope.Targets
			if len(scope.ResolvedTargets) > 0 {
				roots = nil
				for _, target := range scope.ResolvedTargets {
					roots = append(roots, target.Path)
				}
			}
			covered := len(roots) == 0
			for _, target := range roots {
				if target == "." || CoveredBySelection(issue.Path, []string{target}) {
					covered = true
					break
				}
				matcher, err := ClassifyStageValue(target)
				if err == nil && MatchesStageValues(issue.Path, []StageValueMatcher{matcher}) {
					covered = true
					break
				}
			}
			if !covered {
				continue
			}
			entries := []Entry{{RelPath: issue.Path}}
			ids := []uint32{0}
			for _, stage := range scope.Scope.Stages {
				if stage.Kind != command.StageOnly && stage.Kind != command.StageExclude {
					continue
				}
				filtered, handled, err := ApplyPathOnlyStageIDs(scope.Scope, stage, entries, ids)
				if err == nil && handled {
					ids = filtered
				}
			}
			if len(ids) > 0 {
				out = append(out, issue)
				break
			}
		}
	}
	return out
}
