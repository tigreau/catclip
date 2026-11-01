package output

import (
	"fmt"
	"sort"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
)

// Request identity is deliberately separate from file membership. Two scopes
// may select the same path while asking different questions about its content.
type outputRequest struct {
	path             string
	mode             command.EntryMode
	start, end       int
	pattern          string
	contextSet       bool
	context          int
	staged, unstaged bool
}

func requestFor(e discovery.Entry) outputRequest {
	k := outputRequest{path: e.RelPath, mode: e.Mode}
	switch e.Mode {
	case command.EntryModeSnippet:
		k.pattern, k.contextSet, k.context = e.SnippetPattern, e.SnippetContextSet, e.SnippetContextLines
		if !k.contextSet {
			k.context = 0
		}
	case command.EntryModeLines:
		k.start, k.end = e.LinesStart, e.LinesEnd
	case command.EntryModeDiff:
		k.staged, k.unstaged = e.DiffWantStaged, e.DiffWantUnstaged
		if !k.staged && !k.unstaged {
			k.staged, k.unstaged = true, true
		}
	default:
		k.mode = command.EntryModeFull
	}
	return k
}

type snippetBlock struct {
	start, end     int // offsets recorded by the producer, never parsed from source
	bodyBytes      int64
	carriageReturn bool // smart snippets retain CRLF; numeric snippets normalize it
}

type excerptKey struct {
	path                     string
	start, end               int
	numbered, carriageReturn bool
}

// Whole-file-only unions already have correct path deduplication. Preserve that
// cheaper planner; request identity is needed only for focused output modes.
func needsRequestMerge(scopes []EvaluatedScope) bool {
	if len(scopes) < 2 {
		return false
	}
	for _, s := range scopes {
		if s.Paths || s.Metadata {
			continue
		}
		for _, e := range s.Entries {
			if !isWholeBody(e) {
				return true
			}
		}
	}
	return false
}

// dedupeOutputRequests consumes its owned candidate slice. It suppresses whole
// bodies only when a focused request exists, without merging Git comparisons.
func dedupeOutputRequests(in []scopedFileCandidate, preserveOrder bool) []scopedFileCandidate {
	if !preserveOrder {
		// Ordinary output is path-sorted already. Work one equal-path group at
		// a time instead of indexing an entire broad directory in several maps.
		sort.SliceStable(in, func(i, j int) bool { return in[i].entry.RelPath < in[j].entry.RelPath })
		out := in[:0]
		for start := 0; start < len(in); {
			end := start + 1
			for end < len(in) && in[end].entry.RelPath == in[start].entry.RelPath {
				end++
			}
			if end == start+1 {
				out = append(out, in[start])
			} else {
				out = append(out, dedupeOutputRequests(in[start:end], true)...)
			}
			start = end
		}
		return out
	}
	type selection struct{ focused, numbered bool }
	paths := make(map[string]selection)
	for _, c := range in {
		if c.entry.Mode == "" || c.entry.Mode == command.EntryModeFull {
			continue
		}
		s := paths[c.entry.RelPath]
		if !isWholeBody(c.entry) {
			s.focused = true
		}
		if c.entry.Mode == command.EntryModeLines && isWholeBody(c.entry) {
			s.numbered = true
		}
		paths[c.entry.RelPath] = s
	}
	seen := make(map[outputRequest]int)
	fullSeen := make(map[string]int)
	out := in[:0]
	for _, c := range in {
		s := paths[c.entry.RelPath]
		if isWholeBody(c.entry) && (s.focused || (s.numbered && c.entry.Mode != command.EntryModeLines)) {
			continue
		}
		k := requestFor(c.entry)
		i, ok := seen[k]
		if isWholeBody(c.entry) {
			i, ok = fullSeen[c.entry.RelPath]
		}
		if ok {
			// Equal requests have equal mode flags, so this only fills metadata.
			discovery.MergeFileEntry(&out[i].entry, c.entry)
			continue
		}
		if isWholeBody(c.entry) {
			fullSeen[c.entry.RelPath] = len(out)
		} else {
			seen[k] = len(out)
		}
		out = append(out, c)
	}
	return out
}

// prepareMergedFileItems is only used across scopes. Single-scope preparation
// keeps its existing fast path. No discovery/classification takes place here.
func prepareMergedFileItems(ctx git.Context, scopes []EvaluatedScope, preserveOrder bool, omissions *lineOmissions, raw ...bool) ([]scopedPreparedUnit, map[outputRequest]bool, error) {
	count := 0
	for _, s := range scopes {
		if !s.Paths && !s.Metadata {
			count += len(s.Entries)
		}
	}
	candidates := make([]scopedFileCandidate, 0, count)
	for i, s := range scopes {
		if s.Paths || s.Metadata {
			continue
		}
		for _, e := range s.Entries {
			candidates = append(candidates, scopedFileCandidate{i, e})
		}
	}
	// Exact requests collapse before any content preparation or Git command.
	// Delay whole-body suppression until untracked fallbacks are identified.
	seen := make(map[outputRequest]bool)
	var entries []discovery.Entry
	for _, c := range candidates {
		if c.entry.Mode != command.EntryModeDiff {
			continue
		}
		k := requestFor(c.entry)
		if !seen[k] {
			seen[k] = true
			entries = append(entries, c.entry)
		}
	}
	diffs, err := prepareDiffCache(ctx, entries, true)
	if err != nil {
		return nil, nil, err
	}
	untracked := make(map[string][]outputRequest)
	for i := range candidates {
		e := &candidates[i].entry
		if e.Mode != command.EntryModeDiff {
			continue
		}
		if observation, ok := diffs[diffKey(*e)]; ok && !observation.tracked {
			untracked[e.RelPath] = append(untracked[e.RelPath], requestFor(*e))
			e.Mode = command.EntryModeFull
		}
	}
	if len(untracked) > 0 {
		negative := make(map[string]bool)
		for _, s := range scopes {
			if !s.Paths && !s.Metadata {
				for _, path := range s.EmptyRefinement.Paths {
					negative[path] = true
				}
			}
		}
		out := candidates[:0]
		for _, c := range candidates {
			if _, fallback := untracked[c.entry.RelPath]; fallback && negative[c.entry.RelPath] && isWholeBody(c.entry) {
				continue
			}
			out = append(out, c)
		}
		candidates = out
	}
	candidates = dedupeOutputRequests(candidates, preserveOrder)
	entries = entries[:0]
	for _, c := range candidates {
		if c.entry.Mode == command.EntryModeSnippet {
			entries = append(entries, c.entry)
		}
	}
	matches, err := BatchSnippetMatches(entries)
	if err != nil {
		return nil, nil, err
	}
	result := make([]scopedPreparedUnit, 0, len(candidates))
	represented := make(map[outputRequest]bool)
	excerpts := make(map[excerptKey]bool)
	for _, c := range candidates {
		sourceType := ""
		if _, ok := untracked[c.entry.RelPath]; ok {
			sourceType = "untracked"
		}
		unit, blocks, keep, err := prepareMergeUnit(ctx, c.entry, matches, omissions, diffs, sourceType, raw...)
		if err != nil {
			return nil, nil, fmt.Errorf("scope %d, %s: %w", c.scopeIndex+1, c.entry.RelPath, err)
		}
		if !keep {
			continue
		}
		if c.entry.Mode == command.EntryModeSnippet || c.entry.Mode == command.EntryModeDiff {
			represented[requestFor(c.entry)] = true
		}
		for _, k := range untracked[c.entry.RelPath] {
			represented[k] = true
		}
		if !retainDistinctExcerpts(&unit, blocks, excerpts) {
			continue
		}
		result = append(result, scopedPreparedUnit{c.scopeIndex, unit})
	}
	return result, represented, nil
}

type scopedPreparedUnit struct {
	scopeIndex int
	unit       PreparedFileUnit
}

func prepareMergeUnit(ctx git.Context, e discovery.Entry, matches SnippetMatchCache, omissions *lineOmissions, diffs preparedDiffCache, sourceType string, raw ...bool) (PreparedFileUnit, []snippetBlock, bool, error) {
	if e.Mode != command.EntryModeSnippet {
		u, keep, err := prepareFileUnitForOutput(ctx, e, matches, omissions, rawPreparation(raw), diffs)
		if u.sourceType != "binary" {
			u.sourceType = sourceType
		}
		return u, nil, keep, err
	}
	lines := e.SnippetMatchLines
	if len(lines) == 0 {
		lines = matches.Lookup(e.SnippetPattern, e.AbsPath)
	}
	if len(lines) == 0 {
		return PreparedFileUnit{}, nil, false, nil
	}
	u := PreparedFileUnit{Entry: e, sourceType: sourceType}
	// Boundaries are temporary construction state, not part of every prepared
	// file unit or retained preview. Ordinary full-file plans pay no slice cost.
	var blocks []snippetBlock
	var err error
	if e.SnippetContextSet {
		u.SnippetRanges, u.BodyBytes, err = prepareNumericSnippetRanges(e, lines, &blocks)
	} else {
		var snapshot TextSnapshot
		snapshot, err = LoadTextSnapshot(e.AbsPath, e.RelPath)
		if err == nil && snapshot.IsText {
			u.Payload, u.BodyBytes, u.SnippetRanges, err = buildSnippetPayloadFromLines(e.RelPath, snapshot.SnippetLines(), lines, SnippetOptionsFor(false, 0), sourceType, &blocks)
		}
	}
	return u, blocks, len(u.SnippetRanges) > 0, err
}

func retainDistinctExcerpts(u *PreparedFileUnit, blocks []snippetBlock, seen map[excerptKey]bool) bool {
	if u.Entry.Mode == command.EntryModeLines && u.Entry.LinesStart > 0 {
		k := excerptKey{path: u.Entry.RelPath, start: u.Entry.LinesStart, end: u.Entry.LinesEnd, numbered: true}
		if seen[k] {
			return false
		}
		seen[k] = true
		return true
	}
	if len(blocks) == 0 {
		return true
	}
	var ranges []SnippetRange
	var payload []byte
	var total int64
	duplicate := false
	for i, r := range u.SnippetRanges {
		b := blocks[i]
		k := excerptKey{path: u.Entry.RelPath, start: r.Start, end: r.End, carriageReturn: b.carriageReturn}
		if seen[k] {
			duplicate = true
			continue
		}
		seen[k] = true
		ranges = append(ranges, r)
		total += b.bodyBytes
	}
	if duplicate && len(u.Payload) > 0 {
		// Retained ranges stay in their producer's order; avoid a second map.
		n := 0
		for i, r := range u.SnippetRanges {
			if n < len(ranges) && r == ranges[n] {
				b := blocks[i]
				payload = append(payload, u.Payload[b.start:b.end]...)
				n++
			}
		}
		u.Payload = payload
	}
	if duplicate {
		u.SnippetRanges, u.BodyBytes = ranges, total
	}
	return len(ranges) > 0
}
