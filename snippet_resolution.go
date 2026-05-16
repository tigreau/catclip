package catclip

import (
	"fmt"
)

type resolvedSnippet struct {
	Ranges []snippetRange
	Lines  []string
}

// resolveSnippetFromSnapshot turns a per-file matched-line list (1-indexed,
// as rg emits) into snippet ranges suitable for emission. The matching step
// is the caller's job — batched via runRipgrepMatchLines for the output
// pipeline, or via a single-file rg call for previews.
//
// Returns a zero resolvedSnippet when the snapshot isn't text or no lines
// matched. Out-of-range match indices (line numbers beyond the snapshot)
// are silently skipped — they can appear if the snapshot was loaded from
// a different revision than rg saw, which shouldn't happen in normal
// catclip flow but isn't worth crashing over.
func resolveSnippetFromSnapshot(snapshot textSnapshot, matchedLines []int) (resolvedSnippet, error) {
	if !snapshot.IsText {
		return resolvedSnippet{}, nil
	}
	if len(matchedLines) == 0 {
		return resolvedSnippet{}, nil
	}
	lines := snapshot.SnippetLines()
	ranges := buildSnippetRanges(lines, matchedLines)
	if len(ranges) == 0 {
		return resolvedSnippet{}, nil
	}
	return resolvedSnippet{Ranges: ranges, Lines: lines}, nil
}

// buildSnippetRanges expands each matched line into a blank-line-bounded
// range and dedupes overlapping ranges. Pure data transformation; no
// regex engine. Match indices are 1-indexed.
func buildSnippetRanges(lines []string, matchedLines []int) []snippetRange {
	if len(lines) == 0 || len(matchedLines) == 0 {
		return nil
	}
	ranges := make([]snippetRange, 0, len(matchedLines))
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
		ranges = append(ranges, snippetRange{Start: start, End: end})
	}
	return ranges
}
