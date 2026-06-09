package output

import (
	"fmt"
	"sort"
)

type ResolvedSnippet struct {
	Ranges []SnippetRange
	Lines  []string
}

// SnippetBoundaryMode selects how a matched line expands into a snippet range.
type SnippetBoundaryMode string

const (
	// SnippetBoundaryBlock expands each match outward to the nearest blank
	// lines (the default --snippet REGEX behavior).
	SnippetBoundaryBlock SnippetBoundaryMode = "block"
	// SnippetBoundaryContext expands each match to a fixed number of lines
	// before and after (rg/grep -C N), for --snippet REGEX N.
	SnippetBoundaryContext SnippetBoundaryMode = "context"
)

// SnippetOptions carries the boundary mode and, for context mode, the number
// of lines on each side of a match. Context is meaningful only in context mode.
type SnippetOptions struct {
	Mode    SnippetBoundaryMode
	Context int
}

// SnippetOptionsFor builds options from the set/lines fields carried on scopes
// and entries: unset is block mode, set is fixed-context mode.
func SnippetOptionsFor(contextSet bool, contextLines int) SnippetOptions {
	if contextSet {
		return SnippetOptions{Mode: SnippetBoundaryContext, Context: contextLines}
	}
	return SnippetOptions{Mode: SnippetBoundaryBlock}
}

// ResolveSnippetFromSnapshot turns a per-file matched-line list (1-indexed,
// as rg emits) into snippet ranges suitable for emission. The matching step
// is the caller's job — batched via runRipgrepMatchLines for the output
// pipeline, or via a single-file rg call for previews.
//
// Returns a zero ResolvedSnippet when the snapshot isn't text or no lines
// matched. Out-of-range match indices (line numbers beyond the snapshot)
// are silently skipped — they can appear if the snapshot was loaded from
// a different revision than rg saw, which shouldn't happen in normal
// catclip flow but isn't worth crashing over.
func ResolveSnippetFromSnapshot(snapshot TextSnapshot, matchedLines []int, opts SnippetOptions) (ResolvedSnippet, error) {
	if !snapshot.IsText {
		return ResolvedSnippet{}, nil
	}
	return resolveSnippetFromLines(snapshot.SnippetLines(), matchedLines, opts)
}

// resolveSnippetFromLines is ResolveSnippetFromSnapshot's core over already-split
// lines. Callers resolving several boundary widths for one file split the body
// once (SnippetLines re-splits the whole file on every call) and reuse the
// lines across widths, instead of re-splitting per width.
func resolveSnippetFromLines(lines []string, matchedLines []int, opts SnippetOptions) (ResolvedSnippet, error) {
	if len(lines) == 0 || len(matchedLines) == 0 {
		return ResolvedSnippet{}, nil
	}
	var ranges []SnippetRange
	switch opts.Mode {
	case SnippetBoundaryContext:
		ranges = BuildContextSnippetRanges(lines, matchedLines, opts.Context)
	default:
		ranges = buildSnippetRanges(lines, matchedLines)
	}
	if len(ranges) == 0 {
		return ResolvedSnippet{}, nil
	}
	return ResolvedSnippet{Ranges: ranges, Lines: lines}, nil
}

// buildSnippetRanges expands each matched line into a blank-line-bounded
// range and dedupes overlapping ranges. Pure data transformation; no
// regex engine. Match indices are 1-indexed.
func buildSnippetRanges(lines []string, matchedLines []int) []SnippetRange {
	if len(lines) == 0 || len(matchedLines) == 0 {
		return nil
	}
	ranges := make([]SnippetRange, 0, len(matchedLines))
	seen := make(map[string]struct{}, len(matchedLines))
	total := len(lines)
	for _, matchLine := range matchedLines {
		if matchLine < 1 || matchLine > total {
			continue
		}
		start := matchLine
		for start > 1 && lines[start-2] != "" {
			start--
		}
		end := matchLine
		for end < total && lines[end] != "" {
			end++
		}
		key := fmt.Sprintf("%d:%d", start, end)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ranges = append(ranges, SnippetRange{Start: start, End: end})
	}
	return ranges
}

// BuildContextSnippetRanges expands each matched line to [match-context,
// match+context], clamped to file bounds, then sorts and merges ranges that
// overlap or are adjacent (rg/grep -C N semantics). context 0 yields the
// matching line only. Match indices are 1-indexed.
func BuildContextSnippetRanges(lines []string, matchedLines []int, context int) []SnippetRange {
	if len(lines) == 0 || len(matchedLines) == 0 {
		return nil
	}
	total := len(lines)
	windows := make([]SnippetRange, 0, len(matchedLines))
	for _, matchLine := range matchedLines {
		if matchLine < 1 || matchLine > total {
			continue
		}
		start := matchLine - context
		if start < 1 {
			start = 1
		}
		end := matchLine + context
		if end > total {
			end = total
		}
		windows = append(windows, SnippetRange{Start: start, End: end})
	}
	if len(windows) == 0 {
		return nil
	}
	sort.Slice(windows, func(i, j int) bool {
		if windows[i].Start != windows[j].Start {
			return windows[i].Start < windows[j].Start
		}
		return windows[i].End < windows[j].End
	})
	merged := make([]SnippetRange, 0, len(windows))
	current := windows[0]
	for _, w := range windows[1:] {
		// Merge overlapping or adjacent windows (gap of zero lines).
		if w.Start <= current.End+1 {
			if w.End > current.End {
				current.End = w.End
			}
			continue
		}
		merged = append(merged, current)
		current = w
	}
	merged = append(merged, current)
	return merged
}
