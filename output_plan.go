package catclip

import (
	"fmt"
	"sort"
	"strings"
)

type outputSectionKind string

const (
	outputSectionKindFiles outputSectionKind = "files"
	outputSectionKindPaths outputSectionKind = "paths"
)

type outputPlan struct {
	sections []outputPlanSection
	items    []outputPlanItem
}

type outputPlanSection struct {
	kind  outputSectionKind
	items []outputPlanItem
}

type outputPlanItem struct {
	kind          outputSectionKind
	unit          preparedFileUnit
	entry         fileEntry
	relPath       string
	mode          entryMode
	bodyBytes     int64
	linesStart    int
	linesEnd      int
	snippetRanges []snippetRange
}

type evaluatedOutputScope struct {
	Paths   bool
	Entries []fileEntry
}

type scopedFileCandidate struct {
	scopeIndex int
	entry      fileEntry
}

func buildOutputPlan(units []preparedFileUnit) outputPlan {
	plan := outputPlan{
		items: make([]outputPlanItem, 0, len(units)),
	}
	for _, unit := range units {
		plan.items = append(plan.items, newFileOutputPlanItem(unit))
	}
	if len(plan.items) > 0 {
		plan.sections = []outputPlanSection{{
			kind:  outputSectionKindFiles,
			items: append([]outputPlanItem(nil), plan.items...),
		}}
	}
	return plan
}

func newFileOutputPlanItem(unit preparedFileUnit) outputPlanItem {
	return outputPlanItem{
		kind:          outputSectionKindFiles,
		unit:          unit,
		entry:         unit.Entry,
		relPath:       unit.Entry.RelPath,
		mode:          unit.Entry.Mode,
		bodyBytes:     unit.BodyBytes,
		linesStart:    unit.Entry.LinesStart,
		linesEnd:      unit.Entry.LinesEnd,
		snippetRanges: append([]snippetRange(nil), unit.SnippetRanges...),
	}
}

func newPathOutputPlanItem(entry fileEntry) outputPlanItem {
	return outputPlanItem{
		kind:      outputSectionKindPaths,
		entry:     entry,
		relPath:   entry.RelPath,
		mode:      entry.Mode,
		bodyBytes: int64(len(entry.RelPath) + 1),
	}
}

func prepareOutputPlan(gitCtx gitContext, entries []fileEntry) (outputPlan, error) {
	units, err := prepareFileUnits(gitCtx, entries)
	if err != nil {
		return outputPlan{}, err
	}
	return buildOutputPlan(units), nil
}

// buildLinesPreviewPlan prepares the file-output items needed by the hovered
// --lines preview without computing exact slice body sizes. The preview path
// does not render count/size/token summaries, so bodyBytes are unused there;
// skipping slicedLinesBodySize avoids scanning every file once during plan
// prep and again during emit. Final committed --lines output still goes through
// the normal exact-body-size path.
func buildLinesPreviewPlan(entries []fileEntry) outputPlan {
	plan := outputPlan{
		items: make([]outputPlanItem, 0, len(entries)),
	}
	for _, entry := range entries {
		plan.items = append(plan.items, newFileOutputPlanItem(preparedFileUnit{
			Entry: entry,
		}))
	}
	if len(plan.items) > 0 {
		plan.sections = []outputPlanSection{{
			kind:  outputSectionKindFiles,
			items: append([]outputPlanItem(nil), plan.items...),
		}}
	}
	return plan
}

func prepareSectionedOutputPlan(gitCtx gitContext, scopes []evaluatedOutputScope, preserveFileOrder bool) (outputPlan, error) {
	fileItemsByScope, err := prepareSectionedFileItems(gitCtx, scopes, preserveFileOrder)
	if err != nil {
		return outputPlan{}, err
	}

	plan := outputPlan{
		sections: make([]outputPlanSection, 0, len(scopes)),
		items:    make([]outputPlanItem, 0, len(fileItemsByScope)),
	}

	var currentPathSeen map[string]struct{}
	for scopeIndex, scope := range scopes {
		if scope.Paths {
			if len(scope.Entries) == 0 {
				continue
			}
			if len(plan.sections) == 0 || plan.sections[len(plan.sections)-1].kind != outputSectionKindPaths {
				plan.sections = append(plan.sections, outputPlanSection{kind: outputSectionKindPaths})
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
		if len(plan.sections) == 0 || plan.sections[len(plan.sections)-1].kind != outputSectionKindFiles {
			plan.sections = append(plan.sections, outputPlanSection{kind: outputSectionKindFiles})
		}
		section := &plan.sections[len(plan.sections)-1]
		section.items = append(section.items, scopeItems...)
		plan.items = append(plan.items, scopeItems...)
	}

	return plan, nil
}

func prepareSectionedFileItems(gitCtx gitContext, scopes []evaluatedOutputScope, preserveOrder bool) (map[int][]outputPlanItem, error) {
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
	entries := make([]fileEntry, 0, len(candidates))
	for _, candidate := range candidates {
		entries = append(entries, candidate.entry)
	}
	snippetMatches, err := batchSnippetMatches(entries)
	if err != nil {
		return nil, err
	}
	itemsByScope := make(map[int][]outputPlanItem, len(scopes))
	for _, candidate := range candidates {
		unit, keep, err := prepareFileUnit(gitCtx, candidate.entry, snippetMatches)
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

func dedupeScopedFileCandidates(candidates []scopedFileCandidate, preserveOrder bool) []scopedFileCandidate {
	if len(candidates) == 0 {
		return nil
	}

	if preserveOrder {
		out := make([]scopedFileCandidate, 0, len(candidates))
		indicesByPath := make(map[string][]int, len(candidates))
		for _, candidate := range candidates {
			indices, ok := indicesByPath[candidate.entry.RelPath]
			if !ok {
				indicesByPath[candidate.entry.RelPath] = []int{len(out)}
				out = append(out, candidate)
				continue
			}
			merged := false
			for _, idx := range indices {
				if !linesEntriesShouldCoexist(out[idx].entry, candidate.entry) {
					mergeScopedFileCandidate(&out[idx], candidate)
					merged = true
					break
				}
			}
			if !merged {
				indicesByPath[candidate.entry.RelPath] = append(indices, len(out))
				out = append(out, candidate)
			}
		}
		return out
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].entry.RelPath != candidates[j].entry.RelPath {
			return candidates[i].entry.RelPath < candidates[j].entry.RelPath
		}
		return entryModePriority(candidates[i].entry.Mode) > entryModePriority(candidates[j].entry.Mode)
	})

	out := make([]scopedFileCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if len(out) == 0 || candidate.entry.RelPath != out[len(out)-1].entry.RelPath {
			out = append(out, candidate)
			continue
		}
		merged := false
		for i := len(out) - 1; i >= 0 && out[i].entry.RelPath == candidate.entry.RelPath; i-- {
			if !linesEntriesShouldCoexist(out[i].entry, candidate.entry) {
				mergeScopedFileCandidate(&out[i], candidate)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, candidate)
		}
	}
	return out
}

func mergeScopedFileCandidate(dst *scopedFileCandidate, incoming scopedFileCandidate) {
	if entryModePriority(incoming.entry.Mode) > entryModePriority(dst.entry.Mode) {
		*dst = incoming
		return
	}
	mergeFileEntry(&dst.entry, incoming.entry)
}

func (p outputPlan) HasPaths() bool {
	for _, section := range p.sections {
		if section.kind == outputSectionKindPaths {
			return true
		}
	}
	for _, item := range p.items {
		if item.kind == outputSectionKindPaths {
			return true
		}
	}
	return false
}

func (p outputPlan) HasFiles() bool {
	for _, section := range p.sections {
		if section.kind == outputSectionKindFiles {
			return true
		}
	}
	for _, item := range p.items {
		if item.kind == outputSectionKindFiles {
			return true
		}
	}
	return false
}

func (p outputPlan) DistinctRelPaths() []string {
	if len(p.items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(p.items))
	paths := make([]string, 0, len(p.items))
	for _, item := range p.items {
		if item.relPath == "" {
			continue
		}
		if _, ok := seen[item.relPath]; ok {
			continue
		}
		seen[item.relPath] = struct{}{}
		paths = append(paths, item.relPath)
	}
	return paths
}

func (p outputPlan) SummaryCountWord() (int, string) {
	count := len(p.DistinctRelPaths())
	switch {
	case p.HasPaths() && !p.HasFiles():
		if count == 1 {
			return count, "path"
		}
		return count, "paths"
	case !p.HasPaths() && p.HasFiles():
		if count == 1 {
			return count, "file"
		}
		return count, "files"
	default:
		if count == 1 {
			return count, "item"
		}
		return count, "items"
	}
}

type previewLinesRange struct {
	start int
	end   int
}

func (p outputPlan) PreviewModeTags(statuses map[string]string) map[string]string {
	type presence struct {
		hasPath       bool
		hasFile       bool
		mode          entryMode
		linesRanges   []previewLinesRange
		snippetRanges []snippetRange
	}

	seen := make(map[string]presence)
	for _, item := range p.items {
		state := seen[item.relPath]
		switch item.kind {
		case outputSectionKindPaths:
			state.hasPath = true
		case outputSectionKindFiles:
			state.hasFile = true
			mode := item.mode
			if mode == entryModeDiff && statuses[item.relPath] == "?" {
				mode = entryModeFull
			}
			if mode == entryModeLines {
				state.linesRanges = append(state.linesRanges, previewLinesRange{
					start: item.linesStart,
					end:   item.linesEnd,
				})
			}
			if mode == entryModeSnippet {
				state.snippetRanges = append(state.snippetRanges, item.snippetRanges...)
			}
			if entryModePriority(mode) >= entryModePriority(state.mode) {
				state.mode = mode
			}
		}
		seen[item.relPath] = state
	}

	tags := make(map[string]string)
	for relPath, state := range seen {
		modeTag := ""
		switch state.mode {
		case entryModeSnippet:
			modeTag = snippetTagFromRanges(state.snippetRanges)
		case entryModeDiff:
			modeTag = "diff"
		case entryModeLines:
			modeTag = linesTagFromRanges(state.linesRanges)
		}

		switch {
		case state.hasPath && !state.hasFile:
			tags[relPath] = "path only"
		case state.hasPath && modeTag != "":
			tags[relPath] = "path + " + modeTag
		case state.hasPath && state.hasFile:
			tags[relPath] = "path + file"
		case state.mode == entryModeLines:
			tags[relPath] = modeTag
		case state.mode == entryModeSnippet:
			tags[relPath] = modeTag
		case modeTag != "":
			tags[relPath] = modeTag + " only"
		}
	}
	return tags
}

func snippetTagFromRanges(ranges []snippetRange) string {
	if len(ranges) == 0 {
		return "snippet"
	}
	first := formatSnippetRange(ranges[0])
	if len(ranges) == 1 {
		return "snippet " + first
	}
	second := formatSnippetRange(ranges[1])
	if len(ranges) == 2 {
		return fmt.Sprintf("2 snippets %s,%s", first, second)
	}
	return fmt.Sprintf("%d snippets %s,%s,...", len(ranges), first, second)
}

func formatSnippetRange(r snippetRange) string {
	return fmt.Sprintf("%d-%d", r.Start, r.End)
}

func linesTagFromRanges(ranges []previewLinesRange) string {
	if len(ranges) == 0 {
		return "numbered"
	}
	// Bare lines (start == 0) means full file numbered — absorbs any ranges.
	for _, r := range ranges {
		if r.start == 0 {
			return "numbered"
		}
	}
	if len(ranges) == 1 {
		return formatLinesRange(ranges[0].start, ranges[0].end)
	}
	parts := make([]string, len(ranges))
	for i, r := range ranges {
		parts[i] = formatLinesRange(r.start, r.end)
	}
	return strings.Join(parts, ", ")
}

func formatLinesRange(start, end int) string {
	if end > 0 {
		return fmt.Sprintf("lines %d-%d", start, end)
	}
	return fmt.Sprintf("lines %d-", start)
}

// PayloadSizes returns emitted payload bytes keyed by relPath.
// Path-only items contribute the bytes of the emitted `path\n` line because
// that text is real payload and should count toward size/token estimates.
func (p outputPlan) PayloadSizes() (map[string]int64, int64) {
	sizes := make(map[string]int64, len(p.items))
	var total int64
	for _, item := range p.items {
		sizes[item.relPath] += item.bodyBytes
		total += item.bodyBytes
	}
	return sizes, total
}

func (p outputPlan) GitStatusPathspecs(gitCtx gitContext) []string {
	set := make(map[string]struct{}, len(p.items))
	for _, item := range p.items {
		addGitStatusPathspec(set, gitCtx, item.entry)
	}

	pathspecs := make([]string, 0, len(set))
	for repoPath := range set {
		pathspecs = append(pathspecs, repoPath)
	}
	sort.Strings(pathspecs)
	return pathspecs
}

func gitStatusPathspecsForEntries(gitCtx gitContext, entries []fileEntry) []string {
	set := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		addGitStatusPathspec(set, gitCtx, entry)
	}
	pathspecs := make([]string, 0, len(set))
	for repoPath := range set {
		pathspecs = append(pathspecs, repoPath)
	}
	sort.Strings(pathspecs)
	return pathspecs
}

func addGitStatusPathspec(set map[string]struct{}, gitCtx gitContext, entry fileEntry) {
	repoPath := ""
	if entry.TargetRoot != "" && entry.TargetRoot != "." {
		repoPath = gitCtx.toRepoPath(entry.TargetRoot)
	} else {
		repoPath = gitCtx.toRepoPath(entry.RelPath)
	}
	repoPath = normalizeRelPath(repoPath)
	if repoPath == "" || repoPath == "." {
		return
	}
	set[repoPath] = struct{}{}
}

func (p outputPlan) TreeEntries(report outputReport) []treeDocumentEntry {
	merged := make(map[string]fileEntry, len(p.items))
	order := make([]string, 0, len(p.items))
	for _, item := range p.items {
		entry, ok := merged[item.relPath]
		if !ok {
			merged[item.relPath] = item.entry
			order = append(order, item.relPath)
			continue
		}
		mergeFileEntry(&entry, item.entry)
		merged[item.relPath] = entry
	}

	docEntries := make([]treeDocumentEntry, 0, len(order))
	for _, relPath := range order {
		entry := merged[relPath]
		treeEntry := treeDocumentEntry{
			Path:             relPath,
			GitStatus:        report.statuses[relPath],
			ModeTag:          report.modeTags[relPath],
			TargetRoot:       entry.TargetRoot,
			AllowedByInclude: entry.AllowedByInclude,
			BlockSource:      entry.BlockSource,
		}
		if size, ok := report.sizes[relPath]; ok {
			sizeCopy := size
			treeEntry.Size = &sizeCopy
		}
		docEntries = append(docEntries, treeEntry)
	}
	return docEntries
}
