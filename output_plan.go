package catclip

import "sort"

type outputPlan struct {
	items []outputPlanItem
}

type outputPlanItem struct {
	unit    preparedFileUnit
	relPath string
	mode    entryMode
}

func buildOutputPlan(units []preparedFileUnit) outputPlan {
	plan := outputPlan{
		items: make([]outputPlanItem, 0, len(units)),
	}
	for _, unit := range units {
		plan.items = append(plan.items, outputPlanItem{
			unit:    unit,
			relPath: unit.Entry.RelPath,
			mode:    unit.Entry.Mode,
		})
	}
	return plan
}

func prepareOutputPlan(gitCtx gitContext, entries []fileEntry) (outputPlan, error) {
	units, err := prepareFileUnits(gitCtx, entries)
	if err != nil {
		return outputPlan{}, err
	}
	return buildOutputPlan(units), nil
}

func (p outputPlan) PreviewModeTags(statuses map[string]string) map[string]string {
	tags := make(map[string]string)
	for _, item := range p.items {
		switch item.mode {
		case entryModeDiff:
			if statuses[item.relPath] == "?" {
				continue
			}
			tags[item.relPath] = "diff only"
		case entryModeSnippet:
			tags[item.relPath] = "snippet only"
		}
	}
	return tags
}

func (p outputPlan) BodySizes() (map[string]int64, int64) {
	sizes := make(map[string]int64, len(p.items))
	var total int64
	for _, item := range p.items {
		sizes[item.relPath] = item.unit.BodyBytes
		total += item.unit.BodyBytes
	}
	return sizes, total
}

func (p outputPlan) GitStatusPathspecs(gitCtx gitContext) []string {
	set := make(map[string]struct{}, len(p.items))
	for _, item := range p.items {
		entry := item.unit.Entry
		repoPath := ""
		if entry.TargetRoot != "" && entry.TargetRoot != "." {
			repoPath = gitCtx.toRepoPath(entry.TargetRoot)
		} else {
			repoPath = gitCtx.toRepoPath(entry.RelPath)
		}
		repoPath = normalizeRelPath(repoPath)
		if repoPath == "" || repoPath == "." {
			continue
		}
		set[repoPath] = struct{}{}
	}

	pathspecs := make([]string, 0, len(set))
	for repoPath := range set {
		pathspecs = append(pathspecs, repoPath)
	}
	sort.Strings(pathspecs)
	return pathspecs
}

func (p outputPlan) TreeEntries(report outputReport) []treeDocumentEntry {
	docEntries := make([]treeDocumentEntry, 0, len(p.items))
	for _, item := range p.items {
		entry := item.unit.Entry
		treeEntry := treeDocumentEntry{
			Path:             entry.RelPath,
			GitStatus:        report.statuses[entry.RelPath],
			ModeTag:          report.modeTags[entry.RelPath],
			TargetRoot:       entry.TargetRoot,
			AllowedByInclude: entry.AllowedByInclude,
			BlockRule:        entry.BlockRule,
			BlockSource:      entry.BlockSource,
		}
		if size, ok := report.sizes[entry.RelPath]; ok {
			sizeCopy := size
			treeEntry.Size = &sizeCopy
		}
		docEntries = append(docEntries, treeEntry)
	}
	return docEntries
}
