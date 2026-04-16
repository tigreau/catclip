package catclip

import (
	"fmt"
	"regexp"
)

type resolvedSnippet struct {
	Ranges []snippetRange
	Lines  []string
}

func resolveSnippetFromSnapshot(snapshot textSnapshot, pattern string) (resolvedSnippet, error) {
	if !snapshot.IsText {
		return resolvedSnippet{}, nil
	}
	re, err := compileContainsPattern(pattern)
	if err != nil {
		return resolvedSnippet{}, err
	}
	lines := snapshot.SnippetLines()
	ranges := extractSnippetRangesFromLines(lines, re)
	if len(ranges) == 0 {
		return resolvedSnippet{}, nil
	}
	return resolvedSnippet{Ranges: ranges, Lines: lines}, nil
}

func extractSnippetRangesFromLines(lines []string, re *regexp.Regexp) []snippetRange {
	if re == nil || len(lines) == 0 {
		return nil
	}

	matchedLines := make([]int, 0)
	for i, line := range lines {
		if re.MatchString(line) {
			matchedLines = append(matchedLines, i+1)
		}
	}
	if len(matchedLines) == 0 {
		return nil
	}

	ranges := make([]snippetRange, 0, len(matchedLines))
	seen := make(map[string]struct{}, len(matchedLines))
	total := len(lines)
	for _, matchLine := range matchedLines {
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
