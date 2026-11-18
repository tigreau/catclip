package output

import (
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

type scopeContributionStats struct {
	selected, contributed int
	covered               bool
}

// ScopeContribution is a presentation view of surviving plan items. It never
// enumerates, opens, classifies or tokenizes a file.
type ScopeContribution struct {
	Rows    []ContributionRow
	Notices []string
	Empty   string
}

type ContributionRow struct {
	Entry discovery.Entry
	Bytes int64
	Tag   string
}

func scopedFileItem(unit PreparedFileUnit, scope int) PlanItem {
	item := newFileOutputPlanItem(unit)
	item.scope = scope
	return item
}

func (p *Plan) assignFirstSelectingScopes(scopes []EvaluatedScope) {
	if len(scopes) < 2 {
		return
	}
	owners := make(map[string]int, len(p.items))
	for si, scope := range scopes {
		for _, e := range scope.Entries {
			if _, exists := owners[e.RelPath]; !exists {
				owners[e.RelPath] = si
			}
		}
	}
	// The plain builder's sections share items with the flattened plan.
	for i := range p.items {
		p.items[i].scope = owners[p.items[i].relPath]
	}
}

func (p *Plan) recordScopeContributions(scopes []EvaluatedScope) {
	if len(scopes) < 2 {
		return
	}
	p.scopeStats = make([]scopeContributionStats, len(scopes))
	for i, scope := range scopes {
		p.scopeStats[i].selected = len(scope.Entries)
	}
	for _, item := range p.items {
		p.scopeStats[item.scope].contributed++
	}
	// Only empty, previously selected scopes need coverage evidence. Absence
	// caused by a failed read or an out-of-range excerpt is not deduplication.
	type identity struct {
		kind SectionKind
		path string
	}
	var surviving map[identity]bool
	for i, stat := range p.scopeStats {
		if stat.contributed > 0 || stat.selected == 0 {
			continue
		}
		if surviving == nil {
			surviving = make(map[identity]bool, len(p.items))
			for _, item := range p.items {
				surviving[identity{item.kind, item.relPath}] = true
			}
		}
		kind := SectionKindFiles
		if scopes[i].Paths {
			kind = SectionKindPaths
		} else if scopes[i].Metadata {
			kind = SectionKindMetadata
		}
		covered := true
		for _, e := range scopes[i].Entries {
			if !surviving[identity{kind, e.RelPath}] || (kind == SectionKindFiles && !isWholeBody(e) && p.representedRequests != nil && !p.representedRequests[requestFor(e)]) {
				covered = false
				break
			}
		}
		p.scopeStats[i].covered = covered
	}
}

func (p Plan) HasScopeContributions() bool { return len(p.scopeStats) > 1 }

func (p Plan) ScopeContributions() []ScopeContribution {
	if !p.HasScopeContributions() {
		return nil
	}
	groups := make([]ScopeContribution, len(p.scopeStats))
	counts := make([]int, len(groups))
	for _, item := range p.items {
		counts[item.scope]++
	}
	for i, n := range counts {
		if n > 0 {
			groups[i].Rows = make([]ContributionRow, 0, n)
		}
	}
	for i, item := range p.items {
		partial := p.tokenLimit != nil && p.tokenLimit.partial && !p.tokenLimit.metadata && i == len(p.items)-1
		size := item.bodyBytes
		if item.kind == SectionKindMetadata || p.metadataOnly {
			size = item.entry.SizeBytes
		}
		groups[item.scope].Rows = append(groups[item.scope].Rows, ContributionRow{Entry: item.entry, Bytes: size, Tag: contributionTag(item, partial)})
	}
	for i := range groups {
		if len(groups[i].Rows) == 0 {
			switch {
			case p.tokenLimit != nil && p.scopeStats[i].contributed > 0:
				groups[i].Empty = "Nothing added: token limit reached."
			case p.scopeStats[i].covered:
				groups[i].Empty = "Nothing added: selected output is covered by another scope."
			default:
				groups[i].Empty = "No output."
			}
		}
	}
	for _, n := range p.refinementNotices {
		groups[n.scope].Empty = "No output."
		if text := refinementOmissionText(n); text != "" {
			groups[n.scope].Notices = append(groups[n.scope].Notices, text)
		}
	}
	if p.tokenLimit != nil && p.tokenLimit.metadata && len(p.sections) > 0 {
		seen := make(map[int]bool)
		for _, item := range p.sections[len(p.sections)-1].items {
			if !seen[item.scope] {
				groups[item.scope].Notices = append(groups[item.scope].Notices, "Metadata report incomplete; tree shows selected files.")
				seen[item.scope] = true
			}
		}
	}
	return groups
}

func contributionTag(item PlanItem, partial bool) string {
	var tags []string
	if item.unit.sourceType == "untracked" {
		tags = append(tags, "untracked")
	}
	// Requested ranges are not evidence of the emitted end of a byte cutoff.
	// The emitter reports the precise cutoff line; do not reread for a tree tag.
	if !partial && item.kind == SectionKindFiles {
		switch item.mode {
		case command.EntryModeLines:
			if item.linesStart > 0 {
				tags = append(tags, strings.TrimPrefix(formatLinesRange(item.linesStart, item.linesEnd), "lines "))
			}
		case command.EntryModeSnippet:
			var ranges []string
			for _, r := range item.snippetRanges {
				ranges = append(ranges, formatSnippetRange(r))
			}

			if len(ranges) > 0 {
				tags = append(tags, strings.Join(ranges, ", "))
			}
		}
	}
	if partial {
		tags = append(tags, "partial")
	}
	return strings.Join(tags, "] [")
}
