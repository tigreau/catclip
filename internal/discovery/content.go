package discovery

import (
	"fmt"
	"path/filepath"

	"github.com/tigreau/catclip/internal/search"
)

// UsageError is the typed error returned by discovery-side
// helpers when the user input that reached discovery is invalid (bad
// regex argument, absolute path, etc). Root exitWithError classifies it
// as exit code 2, matching the cli.UsageError precedent. Will travel
// with the discovery cluster in the next commit; on move it becomes the
// exported discovery.UsageError.
type UsageError struct {
	message string
}

func (e UsageError) Error() string { return e.message }

func newUsageError(format string, args ...any) error {
	return UsageError{message: fmt.Sprintf(format, args...)}
}

func FilterEntriesByContent(entries []Entry, pattern string) ([]Entry, error) {
	if err := ValidateContainsPattern(pattern); err != nil {
		return nil, err
	}

	matched, err := search.RunRipgrepMatches(pattern, EntryAbsPaths(entries))
	if err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := matched[filepath.Clean(entry.AbsPath)]; ok {
			out = append(out, entry)
		}
	}
	return out, nil
}

func EntryAbsPaths(entries []Entry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.AbsPath == "" {
			continue
		}
		paths = append(paths, filepath.Clean(entry.AbsPath))
	}
	return paths
}

func EnsureEntryAbsPaths(entries []Entry, workingDir string) []Entry {
	for i := range entries {
		if entries[i].AbsPath != "" {
			continue
		}
		entries[i].AbsPath = filepath.Join(workingDir, filepath.FromSlash(entries[i].RelPath))
	}
	return entries
}

// ValidateContainsPattern checks a --contains / --snippet regex pattern at
// flag-parse time. It is intentionally a non-empty check, not a regex
// compile: the runtime engine is rg/PCRE2 (see
// docs/architecture/ACTIVE_NOTE_ripgrep_is_required.md), and Go's RE2
// engine cannot validate PCRE2-specific syntax (backreferences,
// lookaround, atomic groups, etc.). Real syntax errors surface from rg
// at scope-evaluation time with rg's own error message.
func ValidateContainsPattern(pattern string) error {
	if pattern == "" {
		return newUsageError("Error: --contains requires a regex pattern.")
	}
	return nil
}
