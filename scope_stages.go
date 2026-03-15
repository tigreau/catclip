package catclip

import (
	"io"
	"path"
)

func applyScopeStages(resolver *scopeResolver, gitCtx gitContext, s scope, entries []fileEntry) ([]fileEntry, error) {
	for _, stage := range s.Stages {
		var err error
		switch stage.Kind {
		case scopeStageInclude:
			entries, err = applyIncludeStage(resolver, entries, stage.Values)
		case scopeStageOnly:
			entries, err = filterEntriesByStagePatterns(entries, stage.Values, true)
		case scopeStageExclude:
			entries, err = filterEntriesByStagePatterns(entries, stage.Values, false)
		case scopeStageContains:
			if len(stage.Values) == 0 {
				continue
			}
			entries = ensureEntryAbsPaths(entries, resolver.cfg.WorkingDir)
			entries, err = filterEntriesByContent(entries, stage.Values[0])
		case scopeStageChanged:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, scope{Changed: true}, entries)
		case scopeStageStaged:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, scope{Changed: true, Staged: true}, entries)
		case scopeStageUnstaged:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, scope{Changed: true, Unstaged: true}, entries)
		case scopeStageUntracked:
			if !gitCtx.Enabled {
				continue
			}
			entries, err = filterChangedEntries(gitCtx, scope{Changed: true, Untracked: true}, entries)
		case scopeStageDiff, scopeStageSnippet:
			// Output-shape modifiers do not change the selected file set.
		}
		if err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func applyIncludeStage(resolver *scopeResolver, entries []fileEntry, targets []string) ([]fileEntry, error) {
	if len(targets) == 0 {
		return entries, nil
	}

	out := append([]fileEntry(nil), entries...)
	for _, target := range targets {
		included, _, _, _, err := resolver.resolveAndDiscoverTarget(0, target, io.Discard, colorPalette{})
		if err != nil {
			return nil, err
		}
		out = append(out, included...)
	}
	return dedupeEntriesByPath(out), nil
}

func filterEntriesByStagePatterns(entries []fileEntry, patterns []string, keepMatches bool) ([]fileEntry, error) {
	if len(patterns) == 0 {
		return entries, nil
	}

	compiled := make([]compiledGlob, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := compileGlob(pattern)
		if err != nil {
			return nil, newUsageError("Error: invalid pattern %q.", pattern)
		}
		compiled = append(compiled, compiledGlob{raw: pattern, re: re})
	}

	out := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		matched := matchesCompiledGlobs(entry.RelPath, compiled)
		if keepMatches {
			if matched {
				out = append(out, entry)
			}
			continue
		}
		if !matched {
			out = append(out, entry)
		}
	}
	return out, nil
}

func matchesCompiledGlobs(relPath string, globs []compiledGlob) bool {
	basename := path.Base(relPath)
	for _, rule := range globs {
		if rule.re.MatchString(basename) || rule.re.MatchString(relPath) {
			return true
		}
	}
	return false
}
