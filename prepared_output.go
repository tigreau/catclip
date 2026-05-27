package catclip

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
)

// preparedFileUnit is the post-dedupe output contract shared by preview/report
// and final emit. Payload holds fully prepared output for diff/block-snippet
// modes. Numeric snippets keep structured ranges so preview/final emit can
// stream those ranges without prebuilding XML payload bytes.
type preparedFileUnit struct {
	Entry         fileEntry
	Payload       []byte
	SnippetRanges []snippetRange
	BodyBytes     int64
}

func prepareFileUnits(gitCtx gitContext, entries []fileEntry) ([]preparedFileUnit, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	snippetMatches, err := batchSnippetMatches(entries)
	if err != nil {
		return nil, err
	}

	units := make([]preparedFileUnit, 0, len(entries))
	for _, entry := range entries {
		unit, keep, err := prepareFileUnit(gitCtx, entry, snippetMatches)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		units = append(units, unit)
	}
	return units, nil
}

// snippetMatchCache groups rg-derived match-line numbers by (pattern, absPath).
// Pattern keying is required because --then chains can apply different
// --snippet patterns to overlapping file sets.
type snippetMatchCache map[string]map[string][]int

func (c snippetMatchCache) lookup(pattern, absPath string) []int {
	if c == nil {
		return nil
	}
	if perFile, ok := c[pattern]; ok {
		return perFile[absPath]
	}
	return nil
}

// batchSnippetMatches collects all snippet entries from the prepared set,
// groups them by --snippet pattern, and runs one (chunked) rg invocation
// per pattern to gather matched line numbers. Returns nil when there are
// no snippet entries.
func batchSnippetMatches(entries []fileEntry) (snippetMatchCache, error) {
	pathsByPattern := map[string]map[string]struct{}{}
	for _, e := range entries {
		if e.Mode != entryModeSnippet || e.SnippetPattern == "" || e.AbsPath == "" {
			continue
		}
		paths, ok := pathsByPattern[e.SnippetPattern]
		if !ok {
			paths = map[string]struct{}{}
			pathsByPattern[e.SnippetPattern] = paths
		}
		paths[e.AbsPath] = struct{}{}
	}
	if len(pathsByPattern) == 0 {
		return nil, nil
	}

	cache := make(snippetMatchCache, len(pathsByPattern))
	for pattern, pathSet := range pathsByPattern {
		paths := make([]string, 0, len(pathSet))
		for p := range pathSet {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		matches, err := runRipgrepMatchLines(pattern, paths)
		if err != nil {
			return nil, fmt.Errorf("--snippet match for pattern %q: %w", pattern, err)
		}
		cache[pattern] = matches
	}
	return cache, nil
}

func prepareFileUnit(gitCtx gitContext, entry fileEntry, snippetMatches snippetMatchCache) (preparedFileUnit, bool, error) {
	unit := preparedFileUnit{Entry: entry}

	switch entry.Mode {
	case entryModeDiff:
		payload, bodyBytes, keep, err := buildPreparedDiffPayload(gitCtx, entry)
		if err != nil {
			return preparedFileUnit{}, false, err
		}
		if !keep {
			return preparedFileUnit{}, false, nil
		}
		unit.Payload = payload
		unit.BodyBytes = bodyBytes
		return unit, true, nil
	case entryModeSnippet:
		matchedLines := snippetMatches.lookup(entry.SnippetPattern, entry.AbsPath)
		if len(matchedLines) == 0 {
			return preparedFileUnit{}, false, nil
		}
		if entry.SnippetContextSet {
			ranges, bodyBytes, err := prepareNumericSnippetRanges(entry, matchedLines)
			if err != nil {
				return preparedFileUnit{}, false, err
			}
			if len(ranges) == 0 {
				return preparedFileUnit{}, false, nil
			}
			unit.SnippetRanges = ranges
			unit.BodyBytes = bodyBytes
			return unit, true, nil
		}
		payload, bodyBytes, ranges, err := buildPreparedSnippetPayload(entry, matchedLines)
		if err != nil {
			return preparedFileUnit{}, false, err
		}
		if len(payload) == 0 {
			return preparedFileUnit{}, false, nil
		}
		unit.Payload = payload
		unit.SnippetRanges = ranges
		unit.BodyBytes = bodyBytes
		return unit, true, nil
	default:
		if entry.Mode == entryModeLines && (entry.LinesStart > 0 || entry.LinesEnd > 0) {
			bodyBytes, err := slicedLinesBodySize(entry.AbsPath, entry.LinesStart, entry.LinesEnd)
			if err != nil {
				return preparedFileUnit{}, false, err
			}
			if bodyBytes == 0 {
				// No line in the requested range exists in this file —
				// it is shorter than LinesStart. Drop the unit entirely
				// so the file is absent from the tree, the file count,
				// and the emit. emitLinesFile already emits zero bytes
				// for this case; dropping here keeps the tree/count in
				// step with the body. Mirrors the snippet branch above,
				// which drops units with empty payload.
				return preparedFileUnit{}, false, nil
			}
			unit.BodyBytes = bodyBytes
			return unit, true, nil
		}
		bodyBytes, err := fileBodySize(entry)
		if err != nil {
			return preparedFileUnit{}, false, err
		}
		unit.BodyBytes = bodyBytes
		return unit, true, nil
	}
}

func slicedLinesBodySize(absPath string, start, end int) (int64, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, readBufferSize()), 10*1024*1024)
	var total int64
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if start > 0 && lineNum < start {
			continue
		}
		if end > 0 && lineNum > end {
			break
		}
		total += int64(len(scanner.Bytes())) + 1
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return total, nil
}

func prepareNumericSnippetRanges(entry fileEntry, matchedLines []int) ([]snippetRange, int64, error) {
	ranges := buildUnclampedContextSnippetRanges(matchedLines, entry.SnippetContextLines)
	if len(ranges) == 0 {
		return nil, 0, nil
	}

	f, err := os.Open(entry.AbsPath)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, readBufferSize()), 10*1024*1024)
	clamped := make([]snippetRange, 0, len(ranges))
	var bodyBytes int64
	rangeIndex := 0
	lineNum := 0
	currentHadLines := false
	currentEnd := 0

	finishCurrent := func() {
		if currentHadLines {
			clamped = append(clamped, snippetRange{Start: ranges[rangeIndex].Start, End: currentEnd})
		}
		currentHadLines = false
		currentEnd = 0
		rangeIndex++
	}

	for scanner.Scan() {
		lineNum++
		for rangeIndex < len(ranges) && lineNum > ranges[rangeIndex].End {
			finishCurrent()
		}
		if rangeIndex >= len(ranges) {
			break
		}
		current := ranges[rangeIndex]
		if lineNum < current.Start {
			continue
		}
		currentHadLines = true
		currentEnd = lineNum
		bodyBytes += int64(len(scanner.Bytes())) + 1
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	if rangeIndex < len(ranges) {
		finishCurrent()
	}
	return clamped, bodyBytes, nil
}

func buildUnclampedContextSnippetRanges(matchedLines []int, context int) []snippetRange {
	if len(matchedLines) == 0 {
		return nil
	}
	windows := make([]snippetRange, 0, len(matchedLines))
	for _, matchLine := range matchedLines {
		if matchLine < 1 {
			continue
		}
		start := matchLine - context
		if start < 1 {
			start = 1
		}
		windows = append(windows, snippetRange{Start: start, End: matchLine + context})
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
	merged := make([]snippetRange, 0, len(windows))
	current := windows[0]
	for _, w := range windows[1:] {
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

func buildPreparedSnippetPayload(entry fileEntry, matchedLines []int) ([]byte, int64, []snippetRange, error) {
	snapshot, err := loadTextSnapshot(entry.AbsPath, entry.RelPath)
	if err != nil {
		return nil, 0, nil, err
	}
	return buildPreparedSnippetPayloadFromSnapshot(entry.RelPath, snapshot, matchedLines, snippetOptionsFor(entry.SnippetContextSet, entry.SnippetContextLines))
}

// buildPreparedSnippetPayloadFromSnapshot is the snapshot-in byte producer for
// snippet payloads, split out of buildPreparedSnippetPayload so callers that
// already hold a textSnapshot (and want several boundary widths from the same
// read) can reuse one load. Byte output is identical to the load-then-build
// path for the same (snapshot, matchedLines, opts).
func buildPreparedSnippetPayloadFromSnapshot(relPath string, snapshot textSnapshot, matchedLines []int, opts snippetOptions) ([]byte, int64, []snippetRange, error) {
	if !snapshot.IsText {
		return nil, 0, nil, nil
	}
	return buildPreparedSnippetPayloadFromLines(relPath, snapshot.SnippetLines(), matchedLines, opts)
}

// buildPreparedSnippetPayloadFromLines is the lines-in byte producer. The
// boundary picker splits a file's body once (SnippetLines re-splits on every
// call) and feeds the same lines to all 8 widths via this entry point, avoiding
// 8x re-splitting churn per file. Byte output is identical to the snapshot path.
func buildPreparedSnippetPayloadFromLines(relPath string, lines []string, matchedLines []int, opts snippetOptions) ([]byte, int64, []snippetRange, error) {
	snippet, err := resolveSnippetFromLines(lines, matchedLines, opts)
	if err != nil {
		return nil, 0, nil, err
	}
	if len(snippet.Ranges) == 0 {
		return nil, 0, nil, nil
	}

	var payload bytes.Buffer
	var bodyBytes int64
	for _, r := range snippet.Ranges {
		if _, err := fmt.Fprintf(&payload, "<file path=\"%s\" lines=\"%d-%d\">\n", relPath, r.Start, r.End); err != nil {
			return nil, 0, nil, err
		}
		for i := r.Start - 1; i < r.End; i++ {
			if _, err := payload.WriteString(snippet.Lines[i]); err != nil {
				return nil, 0, nil, err
			}
			if err := payload.WriteByte('\n'); err != nil {
				return nil, 0, nil, err
			}
			bodyBytes += int64(len(snippet.Lines[i]) + 1)
		}
		if _, err := payload.Write(fileCloseTag); err != nil {
			return nil, 0, nil, err
		}
	}
	return payload.Bytes(), bodyBytes, snippet.Ranges, nil
}

func buildPreparedDiffPayload(gitCtx gitContext, entry fileEntry) ([]byte, int64, bool, error) {
	diffOutput, diffType, tracked, err := diffEntryOutput(gitCtx, entry)
	if err != nil {
		return nil, 0, false, err
	}
	if !tracked {
		snapshot, err := loadBodySnapshot(entry.AbsPath, entry.RelPath)
		if err != nil {
			return nil, 0, false, err
		}
		payload, bodyBytes := buildWrappedPayload(entry.RelPath, "untracked", snapshot.RawBytes)
		return payload, bodyBytes, true, nil
	}
	if strings.TrimSpace(diffOutput) == "" {
		return nil, 0, false, nil
	}

	body := []byte(diffOutput)
	payload, bodyBytes := buildWrappedPayload(entry.RelPath, diffType, body)
	return payload, bodyBytes, true, nil
}

func buildWrappedPayload(relPath, typeAttr string, body []byte) ([]byte, int64) {
	payload := make([]byte, 0, len(body)+len(relPath)+len(typeAttr)+32)
	payload = append(payload, buildFileOpenTag(relPath, typeAttr)...)
	payload = append(payload, body...)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		payload = append(payload, fileCloseTagWithNewline...)
		return payload, int64(len(body) + 1)
	}
	payload = append(payload, fileCloseTag...)
	return payload, int64(len(body))
}

func fileBodySize(entry fileEntry) (int64, error) {
	if entry.SizeKnown {
		return entry.SizeBytes, nil
	}
	info, err := os.Lstat(entry.AbsPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
