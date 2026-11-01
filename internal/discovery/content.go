package discovery

import (
	"fmt"
	"iter"
	"path/filepath"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

// UsageError is the typed error returned by discovery-side helpers when user
// input that reached discovery is invalid (bad regex argument, absolute path,
// etc). CatclipExitCode participates in root's structural exit protocol.
type UsageError struct {
	message string
}

func (e UsageError) Error() string { return e.message }

func (e UsageError) CatclipExitCode() int { return 2 }

func newUsageError(format string, args ...any) error {
	return UsageError{message: fmt.Sprintf(format, args...)}
}

// FilterEntriesBySnippetContent is the --snippet stage's one-pass
// producer: a single rg match-lines call decides membership (a file
// survives iff it has matches) AND pins the matched line numbers onto
// the surviving entries, so the output build does not pay a second rg
// pass to recover what this pass already saw. Mirrors the content
// picker's contentMatchRowsWithFirstMatchLines consolidation, applied
// to the stage → plan path (filter_attribute_persistence plan).
func FilterEntriesBySnippetContent(entries []Entry, pattern string) ([]Entry, error) {
	if err := ValidateContainsPattern(pattern); err != nil {
		return nil, err
	}
	entries = ExcludeKnownBinaryEntries(entries)
	matched, err := search.RunRipgrepMatchLines(pattern, EntryAbsPaths(entries))
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		lines, ok := matched[entry.AbsPath]
		if !ok {
			lines, ok = matched[filepath.Clean(entry.AbsPath)]
		}
		if !ok || len(lines) == 0 {
			continue
		}
		entry.SnippetMatchLines = append([]int(nil), lines...)
		out = append(out, entry)
	}
	return out, nil
}

func FilterEntriesByContent(entries []Entry, pattern string) ([]Entry, error) {
	if err := ValidateContainsPattern(pattern); err != nil {
		return nil, err
	}

	entries = ExcludeKnownBinaryEntries(entries)
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

// ExcludeKnownBinaryEntries removes directly admitted binaries using retained
// evidence only. The usual all-text selection needs no allocation or new reads.
func ExcludeKnownBinaryEntries(entries []Entry) []Entry {
	for i, entry := range entries {
		if !entry.Binary || (entry.SizeKnown && entry.SizeBytes == 0) {
			continue
		}
		out := make([]Entry, 0, len(entries)-1)
		out = append(out, entries[:i]...)
		for _, rest := range entries[i+1:] {
			if !rest.Binary || (rest.SizeKnown && rest.SizeBytes == 0) {
				out = append(out, rest)
			}
		}
		return out
	}
	return entries
}

// BinaryOnlyContentDiagnostic explains only an observed all-binary input to a
// content stage. It cannot infer ignored files, read failures, or pattern misses.
func BinaryOnlyContentDiagnostic(stage command.Stage, entries iter.Seq[Entry], scopeIndex int, colors platform.Palette) *Diagnostic {
	if stage.Kind != command.StageContains && stage.Kind != command.StageSnippet && stage.Kind != command.StageNotContains {
		return nil
	}
	count, firstPath := 0, ""
	for entry := range entries {
		if !entry.HasBinaryHint() {
			return nil
		}
		if count == 0 {
			firstPath = entry.RelPath
		}
		count++
	}
	if count == 0 {
		return nil
	}
	flag := "--" + string(stage.Kind)
	subject, object := fmt.Sprintf("All %d selected files are binary", count), "these files"
	if count == 1 {
		subject, object = SingleQuoted(firstPath)+" is binary", "this file"
	}
	return &Diagnostic{ScopeIndex: scopeIndex, ExplainsEmptyResult: true,
		Message: fmt.Sprintf("%sNo text files available for %s (scope %d).%s\n  %s. %s searches text files only.\n  Remove %s and its pattern to include %s.",
			colors.Warn, flag, scopeIndex+1, colors.Reset, subject, flag, flag, object)}
}

// FilterEntriesByNotContent is the inverse of FilterEntriesByContent:
// returns the entries whose contents DO NOT match the pattern. Used
// by --not-contains to prune matching files from the candidate set.
//
// The intermediate set returned by RunRipgrepNotMatches is the rg
// view of "files without match." Membership against that set picks
// the surviving entries directly — no inversion of the chunked
// result needed.
func FilterEntriesByNotContent(entries []Entry, pattern string) ([]Entry, error) {
	if err := ValidateNotContainsPattern(pattern); err != nil {
		return nil, err
	}

	entries = ExcludeKnownBinaryEntries(entries)
	withoutMatch, err := search.RunRipgrepMatches(pattern, EntryAbsPaths(entries), true)
	if err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := withoutMatch[filepath.Clean(entry.AbsPath)]; ok {
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

// TextEntries classifies only an already selected set, at the content-filter
// boundary. It cannot discover additional files. Completed observations are
// reused across stages and scopes by the invocation classifier.
func TextEntries(entries []Entry, workingDir string) ([]Entry, error) {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.RelPath)
	}
	text, err := search.ClassifyTextPaths(workingDir, paths)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := text[entry.RelPath]; ok {
			out = append(out, entry)
		}
	}
	return out, nil
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

// ValidateNotContainsPattern is the --not-contains analog of
// ValidateContainsPattern. Same intent (non-empty check, real syntax
// errors come from rg) but the error message names the correct flag.
func ValidateNotContainsPattern(pattern string) error {
	if pattern == "" {
		return newUsageError("Error: --not-contains requires a regex pattern.")
	}
	return nil
}
