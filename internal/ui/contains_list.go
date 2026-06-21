package ui

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
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
	Invocation command.Invocation
	Scopes     []command.ExecutionScope
}

func ContentMatchListConfigFromParsedCommand(cfg command.Parsed) contentMatchListConfig {
	return contentMatchListConfig{
		Invocation: invocationConfigFromParsedCommand(cfg),
		Scopes:     command.ExecutionScopesFromSpec(cfg.Command),
	}
}

func RunInternalContentMatchList(cfg contentMatchListConfig, stdout io.Writer) error {
	finishBench := platform.InternalBenchSpan("ui.internal.content_match_list.fallback",
		"scopes", platform.InternalBenchInt(len(cfg.Scopes)),
	)
	rows, err := contentMatchRowsForScope(cfg)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	finishWriteBench := platform.InternalBenchSpan("ui.internal.content_match_list.fallback.write_rows",
		"rows", platform.InternalBenchInt(len(rows)),
	)
	err = writeContentMatchRows(stdout, rows)
	finishWriteBench("err", platform.InternalBenchError(err))
	finishBench(
		"err", platform.InternalBenchError(err),
		"rows", platform.InternalBenchInt(len(rows)),
	)
	return err
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
			TreeTargetKindFile,
			TreeTargetStateText,
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

func contentMatchScopePattern(s command.ExecutionScope) string {
	if s.Snippet {
		return s.SnippetPattern
	}
	return s.Contains
}

func contentMatchRowsForScope(cfg contentMatchListConfig) ([]contentMatchRow, error) {
	finishBench := platform.InternalBenchSpan("ui.content_match.rows_for_scope",
		"scopes", platform.InternalBenchInt(len(cfg.Scopes)),
	)
	if len(cfg.Scopes) == 0 {
		finishBench("err", "false", "rows", "0", "reason", "no_scope")
		return nil, nil
	}

	scopeIndex := len(cfg.Scopes) - 1
	currentScope := cfg.Scopes[scopeIndex]
	pattern := contentMatchScopePattern(currentScope)
	if strings.TrimSpace(pattern) == "" {
		finishBench("err", "false", "rows", "0", "reason", "empty_pattern")
		return nil, nil
	}
	if err := discovery.ValidateContainsPattern(pattern); err != nil {
		finishBench("err", "false", "rows", "0", "bad_pattern", "true")
		return nil, nil
	}

	gitCtx := git.Detect(cfg.Invocation.WorkingDir)
	candidateScope, ok := scopeWithoutTerminalLiveContentMatchStage(currentScope)
	if !ok {
		rows, err := contentMatchRowsForScopeDoublePass(cfg, gitCtx, scopeIndex, currentScope, pattern)
		finishBench(
			"err", platform.InternalBenchError(err),
			"rows", platform.InternalBenchInt(len(rows)),
			"double_pass", "true",
		)
		return rows, err
	}
	finishEvalBench := platform.InternalBenchSpan("ui.content_match.evaluate_scope",
		"scope_index", platform.InternalBenchInt(scopeIndex),
	)
	discovered, err := discovery.EvaluateScope(cfg.Invocation, gitCtx, scopeIndex, candidateScope, io.Discard, platform.Palette{})
	finishEvalBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(discovered.Entries)),
		"bad_pattern", platform.InternalBenchCancelled(err, search.ErrRipgrepBadPattern),
	)
	if err != nil {
		// While the user types in the interactive picker the pattern can be
		// incomplete (e.g., `[` mid-character-class). rg surfaces this as a
		// compile error; we swallow it silently so the picker just shows
		// nothing rather than erroring out per-keystroke. Other errors
		// still propagate.
		if errors.Is(err, search.ErrRipgrepBadPattern) {
			finishBench("err", "false", "rows", "0", "bad_pattern", "true")
			return nil, nil
		}
		finishBench("err", platform.InternalBenchError(err))
		return nil, err
	}
	finishRowsBench := platform.InternalBenchSpan("ui.content_match.first_match_rows",
		"entries", platform.InternalBenchInt(len(discovered.Entries)),
	)
	rows, err := contentMatchRowsWithFirstMatchLines(discovered.Entries, pattern)
	finishRowsBench(
		"err", platform.InternalBenchError(err),
		"rows", platform.InternalBenchInt(len(rows)),
		"bad_pattern", platform.InternalBenchCancelled(err, search.ErrRipgrepBadPattern),
	)
	if err != nil {
		if errors.Is(err, search.ErrRipgrepBadPattern) {
			finishBench("err", "false", "rows", "0", "bad_pattern", "true")
			return nil, nil
		}
		finishBench("err", platform.InternalBenchError(err))
		return nil, err
	}
	finishBench(
		"err", "false",
		"rows", platform.InternalBenchInt(len(rows)),
		"double_pass", "false",
	)
	return rows, nil
}

func contentMatchRowsForScopeDoublePass(cfg contentMatchListConfig, gitCtx git.Context, scopeIndex int, scope command.ExecutionScope, pattern string) ([]contentMatchRow, error) {
	finishBench := platform.InternalBenchSpan("ui.content_match.double_pass",
		"scope_index", platform.InternalBenchInt(scopeIndex),
	)
	discovered, err := discovery.EvaluateScope(cfg.Invocation, gitCtx, scopeIndex, scope, io.Discard, platform.Palette{})
	finishBench(
		"phase", "evaluate_scope",
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(discovered.Entries)),
		"bad_pattern", platform.InternalBenchCancelled(err, search.ErrRipgrepBadPattern),
	)
	if err != nil {
		if errors.Is(err, search.ErrRipgrepBadPattern) {
			return nil, nil
		}
		return nil, err
	}
	rows := contentMatchRowsFromEntries(discovered.Entries)
	finishAttachBench := platform.InternalBenchSpan("ui.content_match.double_pass.attach_first_lines",
		"rows", platform.InternalBenchInt(len(rows)),
		"entries", platform.InternalBenchInt(len(discovered.Entries)),
	)
	rows = attachFirstMatchLines(rows, discovered.Entries, pattern)
	finishAttachBench("rows", platform.InternalBenchInt(len(rows)))
	return rows, nil
}

func contentMatchRowsFromEntries(entries []discovery.Entry) []contentMatchRow {
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

func scopeWithoutTerminalLiveContentMatchStage(scope command.ExecutionScope) (command.ExecutionScope, bool) {
	liveKind := command.StageContains
	if scope.Snippet {
		liveKind = command.StageSnippet
	}
	if len(scope.Stages) == 0 || scope.Stages[len(scope.Stages)-1].Kind != liveKind {
		return scope, false
	}

	out := scope
	out.Stages = append([]command.Stage(nil), out.Stages[:len(out.Stages)-1]...)
	if liveKind == command.StageSnippet {
		out.Snippet = false
		out.SnippetPattern = ""
		out.SnippetContextSet = false
		out.SnippetContextLines = 0
	} else {
		out.Contains = ""
	}
	return out, true
}

// contentMatchRowsWithFirstMatchLines is the content picker hot path: one rg
// pass supplies both membership (which files match) and the first-match line
// used by fzf's preview-window offset. The old path filtered with
// runRipgrepMatches, then ran firstMatchLinePerFile over the matched set; this
// keeps the metadata together so large-scope picker reloads do not scan content
// twice for the live query.
func contentMatchRowsWithFirstMatchLines(entries []discovery.Entry, pattern string) ([]contentMatchRow, error) {
	if len(entries) == 0 || strings.TrimSpace(pattern) == "" {
		return nil, nil
	}
	finishBench := platform.InternalBenchSpan("ui.content_match.first_match_lines",
		"entries", platform.InternalBenchInt(len(entries)),
	)
	firstLines, err := search.FirstMatchLinePerFile(pattern, discovery.EntryAbsPaths(entries))
	finishBench(
		"err", platform.InternalBenchError(err),
		"matches", platform.InternalBenchInt(len(firstLines)),
		"bad_pattern", platform.InternalBenchCancelled(err, search.ErrRipgrepBadPattern),
	)
	if err != nil {
		return nil, err
	}
	if len(firstLines) == 0 {
		return nil, nil
	}

	rows := make([]contentMatchRow, 0, len(firstLines))
	seen := make(map[string]struct{}, len(firstLines))
	for _, entry := range entries {
		relPath := normalizeRelPath(entry.RelPath)
		if relPath == "" || strings.TrimSpace(entry.AbsPath) == "" {
			continue
		}
		if _, ok := seen[relPath]; ok {
			continue
		}
		if line, ok := firstLines[filepath.Clean(entry.AbsPath)]; ok && line > 0 {
			seen[relPath] = struct{}{}
			rows = append(rows, contentMatchRow{RelPath: relPath, FirstMatchLine: line})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].RelPath < rows[j].RelPath
	})
	return rows, nil
}

// attachFirstMatchLines is the semantics-preserving fallback for unusual
// internal invocations where the live content stage is not terminal. In normal
// interactive picker reloads the live query is terminal, so
// contentMatchRowsWithFirstMatchLines avoids this second rg pass.
func attachFirstMatchLines(rows []contentMatchRow, entries []discovery.Entry, pattern string) []contentMatchRow {
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
		absByRel[rel] = filepath.Clean(e.AbsPath)
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
	finishBench := platform.InternalBenchSpan("ui.content_match.attach_first_lines",
		"rows", platform.InternalBenchInt(len(rows)),
		"paths", platform.InternalBenchInt(len(absPaths)),
	)
	firstLines, err := search.FirstMatchLinePerFile(pattern, absPaths)
	finishBench(
		"err", platform.InternalBenchError(err),
		"matches", platform.InternalBenchInt(len(firstLines)),
		"bad_pattern", platform.InternalBenchCancelled(err, search.ErrRipgrepBadPattern),
	)
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
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return nil, err
	}
	rows, err := contentMatchRowsForScope(ContentMatchListConfigFromParsedCommand(cfg))
	if err != nil {
		return nil, err
	}
	relPaths := make([]string, 0, len(rows))
	for _, row := range rows {
		relPaths = append(relPaths, row.RelPath)
	}
	return relPaths, nil
}

func sortedUniqueEntryRelPaths(entries []discovery.Entry) []string {
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
