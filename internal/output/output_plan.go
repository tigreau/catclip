package output

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
)

type SectionKind string

const (
	SectionKindFiles SectionKind = "files"
	SectionKindPaths SectionKind = "paths"
)

type Plan struct {
	sections []PlanSection
	items    []PlanItem
}

type PlanSection struct {
	kind  SectionKind
	items []PlanItem
}

type PlanItem struct {
	kind          SectionKind
	unit          PreparedFileUnit
	entry         discovery.Entry
	relPath       string
	mode          command.EntryMode
	bodyBytes     int64
	linesStart    int
	linesEnd      int
	snippetRanges []SnippetRange
}

type EvaluatedScope struct {
	Paths   bool
	Entries []discovery.Entry
}

type scopedFileCandidate struct {
	scopeIndex int
	entry      discovery.Entry
}

func BuildPlan(units []PreparedFileUnit) Plan {
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

func newFileOutputPlanItem(unit PreparedFileUnit) PlanItem {
	return PlanItem{
		kind:          SectionKindFiles,
		unit:          unit,
		entry:         unit.Entry,
		relPath:       unit.Entry.RelPath,
		mode:          unit.Entry.Mode,
		bodyBytes:     unit.BodyBytes,
		linesStart:    unit.Entry.LinesStart,
		linesEnd:      unit.Entry.LinesEnd,
		snippetRanges: append([]SnippetRange(nil), unit.SnippetRanges...),
	}
}

func newPathOutputPlanItem(entry discovery.Entry) PlanItem {
	return PlanItem{
		kind:      SectionKindPaths,
		entry:     entry,
		relPath:   entry.RelPath,
		mode:      entry.Mode,
		bodyBytes: int64(len(entry.RelPath) + 1),
	}
}

func PreparePlan(gitCtx git.Context, entries []discovery.Entry) (Plan, error) {
	units, err := PrepareFileUnits(gitCtx, entries)
	if err != nil {
		return Plan{}, err
	}
	return BuildPlan(units), nil
}

// buildLinesPreviewPlan prepares the file-output items needed by the hovered
// --lines preview without computing exact slice body sizes. The preview path
// does not render count/size/token summaries, so bodyBytes are unused there;
// skipping slicedLinesBodySize avoids scanning every file once during plan
// prep and again during emit. Final committed --lines output still goes through
// the normal exact-body-size path.
func buildLinesPreviewPlan(entries []discovery.Entry) Plan {
	plan := Plan{
		items: make([]PlanItem, 0, len(entries)),
	}
	for _, entry := range entries {
		plan.items = append(plan.items, newFileOutputPlanItem(PreparedFileUnit{
			Entry: entry,
		}))
	}
	if len(plan.items) > 0 {
		plan.sections = []PlanSection{{
			kind:  SectionKindFiles,
			items: append([]PlanItem(nil), plan.items...),
		}}
	}
	return plan
}

func prepareSectionedOutputPlan(gitCtx git.Context, scopes []EvaluatedScope, preserveFileOrder bool) (Plan, error) {
	fileItemsByScope, err := prepareSectionedFileItems(gitCtx, scopes, preserveFileOrder)
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

func prepareSectionedFileItems(gitCtx git.Context, scopes []EvaluatedScope, preserveOrder bool) (map[int][]PlanItem, error) {
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
				if !discovery.LinesEntriesShouldCoexist(out[idx].entry, candidate.entry) {
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
		return discovery.EntryModePriority(candidates[i].entry.Mode) > discovery.EntryModePriority(candidates[j].entry.Mode)
	})

	out := make([]scopedFileCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if len(out) == 0 || candidate.entry.RelPath != out[len(out)-1].entry.RelPath {
			out = append(out, candidate)
			continue
		}
		merged := false
		for i := len(out) - 1; i >= 0 && out[i].entry.RelPath == candidate.entry.RelPath; i-- {
			if !discovery.LinesEntriesShouldCoexist(out[i].entry, candidate.entry) {
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
	if discovery.EntryModePriority(incoming.entry.Mode) > discovery.EntryModePriority(dst.entry.Mode) {
		*dst = incoming
		return
	}
	discovery.MergeFileEntry(&dst.entry, incoming.entry)
}

// IsEmpty reports whether the plan has zero items. Replaces direct
// `len(plan.items) == 0` reads at the package boundary so external
// callers don't depend on the items slice being exposed.
func (p Plan) IsEmpty() bool { return len(p.items) == 0 }

// Len reports the raw item count. Items can contain duplicates by
// design (one file may appear in both --paths and a --files section);
// use EntriesInEmissionOrder for the deduped per-file view.
func (p Plan) Len() int { return len(p.items) }

// EntriesInEmissionOrder returns the plan's entries deduped by RelPath
// in first-seen order. The right shape for callers that want to
// process each unique file once (preview table, verbose git metrics).
// Replaces ad-hoc `seen[item.relPath]` loops at external callers.
func (p Plan) EntriesInEmissionOrder() []discovery.Entry {
	if len(p.items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(p.items))
	out := make([]discovery.Entry, 0, len(p.items))
	for _, item := range p.items {
		if _, ok := seen[item.relPath]; ok {
			continue
		}
		seen[item.relPath] = struct{}{}
		out = append(out, item.entry)
	}
	return out
}

// FirstRelPath returns the first item's RelPath, or "", false if the
// plan is empty. The single-item case is asked by the tree-target
// inference at tree_bridge.go.
func (p Plan) FirstRelPath() (string, bool) {
	if len(p.items) == 0 {
		return "", false
	}
	return p.items[0].relPath, true
}

func (p Plan) HasPaths() bool {
	for _, section := range p.sections {
		if section.kind == SectionKindPaths {
			return true
		}
	}
	for _, item := range p.items {
		if item.kind == SectionKindPaths {
			return true
		}
	}
	return false
}

func (p Plan) HasFiles() bool {
	for _, section := range p.sections {
		if section.kind == SectionKindFiles {
			return true
		}
	}
	for _, item := range p.items {
		if item.kind == SectionKindFiles {
			return true
		}
	}
	return false
}

func (p Plan) DistinctRelPaths() []string {
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

func (p Plan) SummaryCountWord() (int, string) {
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

func (p Plan) PreviewModeTags(statuses map[string]string) map[string]string {
	type presence struct {
		hasPath       bool
		hasFile       bool
		mode          command.EntryMode
		linesRanges   []previewLinesRange
		snippetRanges []SnippetRange
	}

	seen := make(map[string]presence)
	for _, item := range p.items {
		state := seen[item.relPath]
		switch item.kind {
		case SectionKindPaths:
			state.hasPath = true
		case SectionKindFiles:
			state.hasFile = true
			mode := item.mode
			if mode == command.EntryModeDiff && statuses[item.relPath] == "?" {
				mode = command.EntryModeFull
			}
			if mode == command.EntryModeLines {
				state.linesRanges = append(state.linesRanges, previewLinesRange{
					start: item.linesStart,
					end:   item.linesEnd,
				})
			}
			if mode == command.EntryModeSnippet {
				state.snippetRanges = append(state.snippetRanges, item.snippetRanges...)
			}
			if discovery.EntryModePriority(mode) >= discovery.EntryModePriority(state.mode) {
				state.mode = mode
			}
		}
		seen[item.relPath] = state
	}

	tags := make(map[string]string)
	for relPath, state := range seen {
		modeTag := ""
		switch state.mode {
		case command.EntryModeSnippet:
			modeTag = snippetTagFromRanges(state.snippetRanges)
		case command.EntryModeDiff:
			modeTag = "diff"
		case command.EntryModeLines:
			modeTag = linesTagFromRanges(state.linesRanges)
		}

		switch {
		case state.hasPath && !state.hasFile:
			tags[relPath] = "path only"
		case state.hasPath && modeTag != "":
			tags[relPath] = "path + " + modeTag
		case state.hasPath && state.hasFile:
			tags[relPath] = "path + file"
		case state.mode == command.EntryModeLines:
			tags[relPath] = modeTag
		case state.mode == command.EntryModeSnippet:
			tags[relPath] = modeTag
		case modeTag != "":
			tags[relPath] = modeTag + " only"
		}
	}
	return tags
}

func snippetTagFromRanges(ranges []SnippetRange) string {
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

func formatSnippetRange(r SnippetRange) string {
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
func (p Plan) PayloadSizes() (map[string]int64, int64) {
	sizes := make(map[string]int64, len(p.items))
	var total int64
	for _, item := range p.items {
		sizes[item.relPath] += item.bodyBytes
		total += item.bodyBytes
	}
	return sizes, total
}

func (p Plan) GitStatusPathspecs(gitCtx git.Context) []string {
	set := make(map[string]struct{}, len(p.items))
	for _, item := range p.items {
		discovery.AddGitStatusPathspec(set, gitCtx, item.entry)
	}

	pathspecs := make([]string, 0, len(set))
	for repoPath := range set {
		pathspecs = append(pathspecs, repoPath)
	}
	sort.Strings(pathspecs)
	return pathspecs
}

// MergedItems returns the plan's items collapsed by relPath via
// discovery.MergeFileEntry, preserving first-seen order. Lifted out of
// the (now-removed) TreeEntries method ahead of the output extraction:
// output owns the merge invariant, the render-shaped tree-row
// construction stays in tree_bridge.go.
func (p Plan) MergedItems() []discovery.Entry {
	merged := make(map[string]discovery.Entry, len(p.items))
	order := make([]string, 0, len(p.items))
	for _, item := range p.items {
		entry, ok := merged[item.relPath]
		if !ok {
			merged[item.relPath] = item.entry
			order = append(order, item.relPath)
			continue
		}
		discovery.MergeFileEntry(&entry, item.entry)
		merged[item.relPath] = entry
	}
	out := make([]discovery.Entry, 0, len(order))
	for _, relPath := range order {
		out = append(out, merged[relPath])
	}
	return out
}

// NewFilePlanItem and NewPathPlanItem are exported wrappers around the
// internal item constructors, for root-side tests (preview_table_test.go,
// startup_sink_picker_test.go) that need to build a Plan with specific
// items. Production code should prefer BuildPlan, PreparePlan, or
// BuildPlanForResolvedScopes.
func NewFilePlanItem(unit PreparedFileUnit) PlanItem { return newFileOutputPlanItem(unit) }
func NewPathPlanItem(entry discovery.Entry) PlanItem { return newPathOutputPlanItem(entry) }

// PlanFromItemsForTesting constructs a Plan from a raw item slice.
// Test-only — production code should build plans via BuildPlan or
// the BuildPlanForResolvedScopes path so the Plan's invariants stay
// owned by the package. Root tests use this to set up specific
// PlanItem mixes that aren't expressible via BuildPlan.
func PlanFromItemsForTesting(items []PlanItem) Plan {
	return Plan{items: append([]PlanItem(nil), items...)}
}
