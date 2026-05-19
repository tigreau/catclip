package catclip

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const contentMatchAllMatchesLabel = "[all current matches]"

// contentMatchAllMatchesPreviewLine is the offset substituted into fzf's
// --preview-window for the [all current matches] row. Any positive integer
// works — the row's preview is the scope tree, not a file, so the line
// offset is ignored in practice. Using "1" keeps the substitution well-
// formed (avoids `+/2` ending up in fzf's flag parsing).
const contentMatchAllMatchesPreviewLine = "1"

type contentMatchRow struct {
	RelPath string
	// FirstMatchLine is the 1-indexed line number of the first match for
	// the current --contains pattern in this file. 0 when unknown (the
	// fallback path that doesn't run rg before emitting rows). Used by
	// fzf's --preview-window +{N}-/2 placeholder to center the preview
	// pane on the first hit.
	FirstMatchLine int
}

type contentMatchListConfig struct {
	Invocation invocationConfig
	Scopes     []executionScope
}

func contentMatchListConfigFromParsedCommand(cfg parsedCommand) contentMatchListConfig {
	return contentMatchListConfig{
		Invocation: invocationConfigFromParsedCommand(cfg),
		Scopes:     executionScopesFromCommandSpec(cfg.Command),
	}
}

func runInternalContentMatchList(cfg contentMatchListConfig, stdout io.Writer) error {
	rows, err := contentMatchRowsForScope(cfg)
	if err != nil {
		return err
	}
	return writeContentMatchRows(stdout, rows)
}

func writeContentMatchRows(stdout io.Writer, rows []contentMatchRow) error {
	if len(rows) == 0 {
		return nil
	}

	// Six TSV columns per row. The picker only displays column 1 (via
	// fzf's --with-nth=1) and searches column 1 (--nth=1). The trailing
	// columns drive fzf placeholder substitution:
	//
	//   {3} -> relPath, fed into --internal-file-preview for the focused-
	//          file preview
	//   {6} -> first-match line number, fed into --preview-window's
	//          `+{N}-/2` offset so the preview opens centered on the hit
	//
	// The [all current matches] row uses contentMatchAllMatchesPreviewLine
	// (a positive integer) in column 6 so the substitution stays
	// well-formed even though the row's preview is the scope tree, not a
	// file — see chooseContentMatchesWithFzf.
	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, strings.Join([]string{
		contentMatchAllMatchesLabel,
		"",
		"",
		"",
		"",
		contentMatchAllMatchesPreviewLine,
	}, "\t"))
	for _, row := range rows {
		firstLine := row.FirstMatchLine
		if firstLine < 1 {
			firstLine = 1
		}
		lines = append(lines, strings.Join([]string{
			pickerFilePathDisplayLabel(row.RelPath),
			row.RelPath,
			row.RelPath,
			treeTargetKindFile,
			treeTargetStateText,
			strconv.Itoa(firstLine),
		}, "\t"))
	}
	if _, err := io.WriteString(stdout, strings.Join(lines, "\n")); err != nil {
		return err
	}
	_, err := io.WriteString(stdout, "\n")
	return err
}

func pickerFilePathDisplayLabel(relPath string) string {
	base := pathBase(relPath)
	if base == relPath {
		return relPath
	}
	return fmt.Sprintf("%s  %s", base, relPath)
}

func contentMatchScopePattern(s executionScope) string {
	if s.Snippet {
		return s.SnippetPattern
	}
	return s.Contains
}

func contentMatchRowsForScope(cfg contentMatchListConfig) ([]contentMatchRow, error) {
	if len(cfg.Scopes) == 0 {
		return nil, nil
	}

	scopeIndex := len(cfg.Scopes) - 1
	currentScope := cfg.Scopes[scopeIndex]
	patternText := strings.TrimSpace(contentMatchScopePattern(currentScope))
	if patternText == "" {
		return nil, nil
	}
	if err := validateContainsPattern(patternText); err != nil {
		return nil, nil
	}

	gitCtx := detectGitContext(cfg.Invocation.WorkingDir)
	discovered, err := evaluateScope(cfg.Invocation, gitCtx, scopeIndex, currentScope, io.Discard, colorPalette{})
	if err != nil {
		// While the user types in the interactive picker the pattern can be
		// incomplete (e.g., `[` mid-character-class). rg surfaces this as a
		// compile error; we swallow it silently so the picker just shows
		// nothing rather than erroring out per-keystroke. Other errors
		// still propagate.
		if errors.Is(err, errRipgrepBadPattern) {
			return nil, nil
		}
		return nil, err
	}
	rows := contentMatchRowsFromEntries(discovered.Entries)
	return attachFirstMatchLines(rows, discovered.Entries, patternText), nil
}

func contentMatchRowsFromEntries(entries []fileEntry) []contentMatchRow {
	rows := make([]contentMatchRow, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		relPath := normalizeRelPath(entry.RelPath)
		if relPath == "" {
			continue
		}
		if _, ok := seen[relPath]; ok {
			continue
		}
		seen[relPath] = struct{}{}
		rows = append(rows, contentMatchRow{RelPath: relPath})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].RelPath < rows[j].RelPath
	})
	return rows
}

// attachFirstMatchLines runs rg once with --max-count 1 over the rows'
// resolved absolute paths and fills in each row's FirstMatchLine. Rows
// whose corresponding file is not in the rg result keep their existing
// value (default 0, downgraded to 1 by writeContentMatchRows).
//
// pattern is the live content-picker query; entries provide the
// rel->abs mapping so we can run rg with absolute paths (cwd-independent)
// and then resolve the result back to rows.
func attachFirstMatchLines(rows []contentMatchRow, entries []fileEntry, pattern string) []contentMatchRow {
	if len(rows) == 0 || strings.TrimSpace(pattern) == "" {
		return rows
	}
	absByRel := make(map[string]string, len(entries))
	for _, e := range entries {
		rel := normalizeRelPath(e.RelPath)
		if rel == "" || strings.TrimSpace(e.AbsPath) == "" {
			continue
		}
		if _, dup := absByRel[rel]; dup {
			continue
		}
		absByRel[rel] = e.AbsPath
	}
	absPaths := make([]string, 0, len(rows))
	for _, row := range rows {
		if abs := absByRel[row.RelPath]; abs != "" {
			absPaths = append(absPaths, abs)
		}
	}
	if len(absPaths) == 0 {
		return rows
	}
	firstLines, err := firstMatchLinePerFile(pattern, absPaths)
	if err != nil || len(firstLines) == 0 {
		return rows
	}
	for i := range rows {
		abs := absByRel[rows[i].RelPath]
		if abs == "" {
			continue
		}
		if line, ok := firstLines[abs]; ok && line > 0 {
			rows[i].FirstMatchLine = line
		}
	}
	return rows
}

func contentMatchPathsForArgs(currentArgs []string, flag, query string) ([]string, error) {
	args := append([]string(nil), currentArgs...)
	args = append(args, flag, query)
	cfg, err := parseArgsAllowImplicitDot(args)
	if err != nil {
		return nil, err
	}
	rows, err := contentMatchRowsForScope(contentMatchListConfigFromParsedCommand(cfg))
	if err != nil {
		return nil, err
	}
	relPaths := make([]string, 0, len(rows))
	for _, row := range rows {
		relPaths = append(relPaths, row.RelPath)
	}
	return relPaths, nil
}

func sortedUniqueEntryRelPaths(entries []fileEntry) []string {
	seen := make(map[string]struct{}, len(entries))
	relPaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.RelPath == "" {
			continue
		}
		if _, ok := seen[entry.RelPath]; ok {
			continue
		}
		seen[entry.RelPath] = struct{}{}
		relPaths = append(relPaths, entry.RelPath)
	}
	sort.Strings(relPaths)
	return relPaths
}
