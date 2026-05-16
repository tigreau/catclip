package catclip

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const contentMatchAllMatchesLabel = "[all current matches]"

type contentMatchRow struct {
	RelPath string
}

func runInternalContentMatchList(cfg runConfig, stdout io.Writer) error {
	rows, err := contentMatchRowsForScope(cfg)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, strings.Join([]string{
		contentMatchAllMatchesLabel,
		"",
		"",
		"",
		"",
	}, "\t"))
	for _, row := range rows {
		lines = append(lines, strings.Join([]string{
			pickerFilePathDisplayLabel(row.RelPath),
			row.RelPath,
			row.RelPath,
			treeTargetKindFile,
			treeTargetStateText,
		}, "\t"))
	}
	if _, err := io.WriteString(stdout, strings.Join(lines, "\n")); err != nil {
		return err
	}
	_, err = io.WriteString(stdout, "\n")
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

func contentMatchRowsForScope(cfg runConfig) ([]contentMatchRow, error) {
	scopeSpecs := configCommandScopes(cfg)
	if len(scopeSpecs) == 0 {
		return nil, nil
	}

	scopeIndex := len(scopeSpecs) - 1
	currentScope := executionScopeFromCommandScopeSpec(scopeSpecs[scopeIndex])
	patternText := strings.TrimSpace(contentMatchScopePattern(currentScope))
	if patternText == "" {
		return nil, nil
	}
	if err := validateContainsPattern(patternText); err != nil {
		return nil, nil
	}

	gitCtx := detectGitContext(cfg.WorkingDir)
	entries, _, _, _, err := evaluateScope(cfg, gitCtx, scopeIndex, currentScope, io.Discard, colorPalette{})
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
	return rows, nil
}

func contentMatchPathsForArgs(currentArgs []string, flag, query string) ([]string, error) {
	args := append([]string(nil), currentArgs...)
	args = append(args, flag, query)
	cfg, err := parseArgsAllowImplicitDot(args)
	if err != nil {
		return nil, err
	}
	rows, err := contentMatchRowsForScope(cfg)
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
