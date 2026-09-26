package output

import (
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
)

// Frozen pre-optimization allocation strategy. Keep sorting, merging and file
// preparation shared: this oracle checks storage/layout changes, not semantics.
func legacyAllocationBuildPlan(units []PreparedFileUnit) Plan {
	plan := Plan{
		items: make([]PlanItem, 0, len(units)),
	}
	for _, unit := range units {
		plan.items = append(plan.items, newFileOutputPlanItem(unit))
	}
	if len(plan.items) > 0 {
		plan.sections = []PlanSection{{
			kind:  SectionKindFiles,
			items: append([]PlanItem(nil), plan.items...),
		}}
	}
	return plan
}

func legacyAllocationSectionedPlan(gitCtx git.Context, scopes []EvaluatedScope, preserveFileOrder bool) (Plan, error) {
	fileItemsByScope, err := legacyAllocationSectionedItems(gitCtx, scopes, preserveFileOrder)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{
		sections: make([]PlanSection, 0, len(scopes)),
		items:    make([]PlanItem, 0, len(fileItemsByScope)),
	}

	var currentPathSeen map[string]struct{}
	for scopeIndex, scope := range scopes {
		if scope.Paths {
			if len(scope.Entries) == 0 {
				continue
			}
			if len(plan.sections) == 0 || plan.sections[len(plan.sections)-1].kind != SectionKindPaths {
				plan.sections = append(plan.sections, PlanSection{kind: SectionKindPaths})
				currentPathSeen = make(map[string]struct{}, len(scope.Entries))
			}
			section := &plan.sections[len(plan.sections)-1]
			for _, entry := range scope.Entries {
				if entry.RelPath == "" {
					continue
				}
				if _, ok := currentPathSeen[entry.RelPath]; ok {
					continue
				}
				currentPathSeen[entry.RelPath] = struct{}{}
				item := newPathOutputPlanItem(entry)
				section.items = append(section.items, item)
				plan.items = append(plan.items, item)
			}
			continue
		}

		scopeItems := fileItemsByScope[scopeIndex]
		if len(scopeItems) == 0 {
			continue
		}
		currentPathSeen = nil
		if len(plan.sections) == 0 || plan.sections[len(plan.sections)-1].kind != SectionKindFiles {
			plan.sections = append(plan.sections, PlanSection{kind: SectionKindFiles})
		}
		section := &plan.sections[len(plan.sections)-1]
		section.items = append(section.items, scopeItems...)
		plan.items = append(plan.items, scopeItems...)
	}

	return plan, nil
}

func legacyAllocationSectionedItems(gitCtx git.Context, scopes []EvaluatedScope, preserveOrder bool) (map[int][]PlanItem, error) {
	candidates := make([]scopedFileCandidate, 0)
	for scopeIndex, scope := range scopes {
		if scope.Paths {
			continue
		}
		for _, entry := range scope.Entries {
			candidates = append(candidates, scopedFileCandidate{
				scopeIndex: scopeIndex,
				entry:      entry,
			})
		}
	}

	candidates = dedupeScopedFileCandidates(candidates, preserveOrder)
	entries := make([]discovery.Entry, 0, len(candidates))
	for _, candidate := range candidates {
		entries = append(entries, candidate.entry)
	}
	snippetMatches, err := BatchSnippetMatches(entries)
	if err != nil {
		return nil, err
	}
	itemsByScope := make(map[int][]PlanItem, len(scopes))
	for _, candidate := range candidates {
		unit, keep, err := PrepareFileUnit(gitCtx, candidate.entry, snippetMatches)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		itemsByScope[candidate.scopeIndex] = append(itemsByScope[candidate.scopeIndex], newFileOutputPlanItem(unit))
	}
	return itemsByScope, nil
}
