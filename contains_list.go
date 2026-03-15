package catclip

import (
	"io"
	"sort"
	"strings"
)

func runInternalContainsList(cfg runConfig, stdout io.Writer) error {
	if len(cfg.Scopes) == 0 {
		return nil
	}

	scopeIndex := len(cfg.Scopes) - 1
	currentScope := cfg.Scopes[scopeIndex]
	if strings.TrimSpace(currentScope.Contains) == "" {
		return nil
	}
	if _, err := compileContainsPattern(currentScope.Contains); err != nil {
		return nil
	}

	gitCtx := detectGitContext(cfg.WorkingDir)
	baseRules, err := loadIgnoreRules()
	if err != nil {
		return err
	}

	entries, _, _, _, err := evaluateScope(cfg, gitCtx, scopeIndex, currentScope, baseRules, io.Discard, colorPalette{})
	if err != nil {
		return err
	}

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
	if len(relPaths) == 0 {
		return nil
	}
	sort.Strings(relPaths)

	lines := formatFzfCandidates(relPaths, treeTargetKindFile, treeTargetStateText)
	if _, err := io.WriteString(stdout, strings.Join(lines, "\n")); err != nil {
		return err
	}
	_, err = io.WriteString(stdout, "\n")
	return err
}
