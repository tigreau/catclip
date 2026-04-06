package catclip

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

const containsAllMatchesLabel = "[all current matches]"

func runInternalContainsList(cfg runConfig, stdout io.Writer) error {
	relPaths, err := containsScopeMatchPaths(cfg)
	if err != nil {
		return err
	}
	if len(relPaths) == 0 {
		return nil
	}

	lines := make([]string, 0, len(relPaths)+1)
	lines = append(lines, strings.Join([]string{
		containsAllMatchesLabel,
		"",
		"",
		"",
		"",
	}, "\t"))
	for _, relPath := range relPaths {
		lines = append(lines, strings.Join([]string{
			fmt.Sprintf("%s  %s", pathBase(relPath), relPath),
			relPath,
			relPath,
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

func containsScopeMatchPaths(cfg runConfig) ([]string, error) {
	if len(cfg.Scopes) == 0 {
		return nil, nil
	}

	scopeIndex := len(cfg.Scopes) - 1
	currentScope := cfg.Scopes[scopeIndex]
	if strings.TrimSpace(currentScope.Contains) == "" {
		return nil, nil
	}
	if _, err := compileContainsPattern(currentScope.Contains); err != nil {
		return nil, nil
	}

	gitCtx := detectGitContext(cfg.WorkingDir)
	baseRules, err := loadIgnoreRules()
	if err != nil {
		return nil, err
	}

	entries, _, _, _, err := evaluateScope(cfg, gitCtx, scopeIndex, currentScope, baseRules, io.Discard, colorPalette{})
	if err != nil {
		return nil, err
	}
	return sortedUniqueEntryRelPaths(entries), nil
}

func containsMatchPathsForArgs(currentArgs []string, query string) ([]string, error) {
	args := append([]string(nil), currentArgs...)
	args = append(args, "--contains", query)
	cfg, err := parseArgs(args)
	if err != nil {
		return nil, err
	}
	return containsScopeMatchPaths(cfg)
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
