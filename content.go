package catclip

import (
	"bytes"
	"path/filepath"
)

func filterEntriesByContent(entries []fileEntry, pattern string) ([]fileEntry, error) {
	if err := validateContainsPattern(pattern); err != nil {
		return nil, err
	}

	matched, err := runRipgrepMatches(pattern, entryAbsPaths(entries))
	if err != nil {
		return nil, err
	}

	out := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := matched[filepath.Clean(entry.AbsPath)]; ok {
			out = append(out, entry)
		}
	}
	return out, nil
}

func entryAbsPaths(entries []fileEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.AbsPath == "" {
			continue
		}
		paths = append(paths, filepath.Clean(entry.AbsPath))
	}
	return paths
}

func ensureEntryAbsPaths(entries []fileEntry, workingDir string) []fileEntry {
	for i := range entries {
		if entries[i].AbsPath != "" {
			continue
		}
		entries[i].AbsPath = filepath.Join(workingDir, filepath.FromSlash(entries[i].RelPath))
	}
	return entries
}

// validateContainsPattern checks a --contains / --snippet regex pattern at
// flag-parse time. It is intentionally a non-empty check, not a regex
// compile: the runtime engine is rg/PCRE2 (see
// docs/architecture/ACTIVE_NOTE_ripgrep_is_required.md), and Go's RE2
// engine cannot validate PCRE2-specific syntax (backreferences,
// lookaround, atomic groups, etc.). Real syntax errors surface from rg
// at scope-evaluation time with rg's own error message.
func validateContainsPattern(pattern string) error {
	if pattern == "" {
		return newUsageError("Error: --contains requires a regex pattern.")
	}
	return nil
}

func splitLogicalLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	lines := make([]string, 0, bytes.Count(data, []byte{'\n'})+1)
	start := 0
	for start < len(data) {
		offset := bytes.IndexByte(data[start:], '\n')
		if offset < 0 {
			lines = append(lines, string(data[start:]))
			break
		}
		end := start + offset
		lines = append(lines, string(data[start:end]))
		start = end + 1
	}
	return lines
}
