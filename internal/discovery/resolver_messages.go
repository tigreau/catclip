package discovery

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

// globZeroMatchWarning classifies why a glob-shaped target produced zero
// matches and emits guidance in catclip's actual matcher grammar. Positional
// globs use path.Match over files, so a glob ending in `/` can never match.
// Positional `**` is rejected before this formatter runs. Recursive recovery
// uses an exact directory target, optionally followed by a cwd-relative
// --only filter.
func globZeroMatchWarning(r *Resolver, pattern string, scopeIndex int, colors platform.Palette) string {
	prefix := longestLiteralPathPrefix(pattern)
	if prefix == "" || prefix == "." {
		return globWithoutLiteralPrefixWarning(pattern, scopeIndex, colors)
	}
	suffix := strings.TrimPrefix(pattern, prefix+"/")
	if suffix == pattern {
		// prefix did not consume any portion of pattern; treat as missing
		return targetNotFoundWarning(pattern, scopeIndex, colors)
	}
	isDir, err := r.targetPathIsDirectory(prefix)
	if err != nil || !isDir {
		return unresolvedGlobPrefixWarning(pattern, prefix, suffix, scopeIndex, colors)
	}

	directoryShaped := strings.HasSuffix(strings.ReplaceAll(pattern, "\\", "/"), "/")
	block, _ := r.dirBlockedBy(prefix)
	if block != nil && block.Source != "" {
		return ignoredGlobZeroMatchWarning(pattern, prefix, suffix, block.Source, scopeIndex, directoryShaped, colors)
	}
	return visibleGlobZeroMatchWarning(pattern, prefix, suffix, scopeIndex, directoryShaped, colors)
}

func globWithoutLiteralPrefixWarning(pattern string, scopeIndex int, colors platform.Palette) string {
	if core, ok := outerStarFuzzyCore(pattern); ok {
		return fmt.Sprintf("%sWarning:%s No files matched %s (scope %d).\n\n  %sTarget globs select files and '*' does not cross folders.%s\n  %sFor fuzzy file and folder navigation:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(pattern), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, ShellQuoteArg(core), colors.Reset)
	}

	return fmt.Sprintf("%sWarning:%s No files matched %s (scope %d).\n\n  %sTarget globs match complete filenames and cwd-relative file paths; they do not fall back to fuzzy navigation.%s",
		colors.Warn, colors.Reset, SingleQuoted(pattern), scopeIndex+1,
		colors.Dim, colors.Reset)
}

func unresolvedGlobPrefixWarning(pattern, prefix, suffix string, scopeIndex int, colors platform.Palette) string {
	commandPrefix := ShellQuoteArg(prefix)
	normalizedSuffix := strings.TrimSuffix(strings.ReplaceAll(suffix, "\\", "/"), "/")
	allStars := normalizedSuffix != "" && strings.Trim(normalizedSuffix, "*") == ""
	if !allStars && !strings.Contains(normalizedSuffix, "/") {
		filter := ShellQuoteArg(normalizedSuffix)
		return fmt.Sprintf("%sWarning:%s No files matched %s (scope %d).\n\n  %sNo cwd-relative directory %s exists; target globs do not fuzzy-match directory segments.%s\n  %sTo choose a directory matching %s and search recursively:%s\n    %scatclip %s --only %s%s\n  %sTo keep only direct matches:%s\n    %scatclip %s --depth 1 --only %s%s",
			colors.Warn, colors.Reset, SingleQuoted(pattern), scopeIndex+1,
			colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
			colors.Dim, SingleQuoted(prefix), colors.Reset,
			colors.OK, commandPrefix, filter, colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, commandPrefix, filter, colors.Reset)
	}

	return fmt.Sprintf("%sWarning:%s No files matched %s (scope %d).\n\n  %sNo cwd-relative directory %s exists; target globs do not fuzzy-match directory segments.%s\n  %sTo choose a directory matching %s and include everything below it:%s\n    %scatclip %s%s\n  %sTo keep only its direct files:%s\n    %scatclip %s --depth 1%s",
		colors.Warn, colors.Reset, SingleQuoted(pattern), scopeIndex+1,
		colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
		colors.Dim, SingleQuoted(prefix), colors.Reset,
		colors.OK, commandPrefix, colors.Reset,
		colors.Dim, colors.Reset,
		colors.OK, commandPrefix, colors.Reset)
}

func visibleGlobZeroMatchWarning(pattern, prefix, suffix string, scopeIndex int, directoryShaped bool, colors platform.Palette) string {
	if directoryShaped {
		return fmt.Sprintf("%sWarning:%s No files matched %s (scope %d).\n\n  %sTarget globs match files, not directory names; file paths do not end in '/'.%s\n  %sTo include everything below %s:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(pattern), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
			colors.OK, ShellQuoteArg(prefix), colors.Reset)
	}
	if globSuffixIsOnlyStars(suffix) {
		return fmt.Sprintf("%sWarning:%s No files matched %s (scope %d).\n\n  %s%s checks direct files in %s; target '*' does not cross folders.%s\n  %sTo include everything below %s:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(pattern), scopeIndex+1,
			colors.Dim, SingleQuoted(pattern), SingleQuoted(prefix+"/"), colors.Reset,
			colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
			colors.OK, ShellQuoteArg(prefix), colors.Reset)
	}
	filterPattern := recursiveGlobFilterPattern(pattern, suffix)

	return fmt.Sprintf("%sWarning:%s No files matched %s (scope %d).\n\n  %s%s checks %s directly in %s; target '*' does not cross folders.%s\n  %sTo search recursively below %s:%s\n    %scatclip %s --only %s%s",
		colors.Warn, colors.Reset, SingleQuoted(pattern), scopeIndex+1,
		colors.Dim, SingleQuoted(pattern), SingleQuoted(suffix), SingleQuoted(prefix+"/"), colors.Reset,
		colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
		colors.OK, ShellQuoteArg(prefix), ShellQuoteArg(filterPattern), colors.Reset)
}

func ignoredGlobZeroMatchWarning(pattern, prefix, suffix, source string, scopeIndex int, directoryShaped bool, colors platform.Palette) string {
	if directoryShaped {
		return fmt.Sprintf("%sWarning:%s %s is ignored by %s%s (scope %d).\n\n  %sTarget globs match files, not directory names; file paths do not end in '/'.%s\n  %sName the ignored directory directly:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(prefix+"/"), source, colors.Reset, scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, ShellQuoteArg(prefix), colors.Reset)
	}
	if globSuffixIsOnlyStars(suffix) {
		return fmt.Sprintf("%sWarning:%s %s is ignored by %s%s (scope %d).\n\n  %s%s checks direct files in %s; target '*' does not cross folders.%s\n  %sName the ignored directory directly:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(prefix+"/"), source, colors.Reset, scopeIndex+1,
			colors.Dim, SingleQuoted(pattern), SingleQuoted(prefix+"/"), colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, ShellQuoteArg(prefix), colors.Reset)
	}
	filterPattern := recursiveGlobFilterPattern(pattern, suffix)

	return fmt.Sprintf("%sWarning:%s %s is ignored by %s%s (scope %d).\n\n  %s%s checks %s directly in %s; target '*' does not cross folders.%s\n  %sName the ignored directory, then filter it:%s\n    %scatclip %s --only %s%s",
		colors.Warn, colors.Reset, SingleQuoted(prefix+"/"), source, colors.Reset, scopeIndex+1,
		colors.Dim, SingleQuoted(pattern), SingleQuoted(suffix), SingleQuoted(prefix+"/"), colors.Reset,
		colors.Dim, colors.Reset,
		colors.OK, ShellQuoteArg(prefix), ShellQuoteArg(filterPattern), colors.Reset)
}

func globSuffixIsOnlyStars(suffix string) bool {
	normalized := strings.TrimSuffix(strings.ReplaceAll(suffix, "\\", "/"), "/")
	return normalized != "" && strings.Trim(normalized, "*") == ""
}

func unsupportedTargetDoublestarMessage(pattern string) string {
	if core, ok := outerStarFuzzyCore(pattern); ok {
		deterministic := collapseRepeatedStars(pattern)
		return fmt.Sprintf("Error: Positional target patterns do not support '**': %s.\n\n  For fuzzy file and folder navigation:\n    catclip %s\n\n  For a deterministic filename glob:\n    catclip %s",
			SingleQuoted(pattern), ShellQuoteArg(core), ShellQuoteArg(deterministic))
	}

	prefix := longestLiteralPathPrefix(pattern)
	if prefix == "" || prefix == "." {
		prefix = "."
	}
	base := path.Base(strings.ReplaceAll(pattern, "\\", "/"))
	if base == "**" || base == "." || base == "/" {
		return fmt.Sprintf("Error: Positional target patterns do not support '**': %s.\n\n  Use a directory target for recursive traversal:\n    catclip %s",
			SingleQuoted(pattern), ShellQuoteArg(prefix))
	}
	base = collapseRepeatedStars(base)
	return fmt.Sprintf("Error: Positional target patterns do not support '**': %s.\n\n  Use a directory target plus --only for recursive file matching:\n    catclip %s --only %s",
		SingleQuoted(pattern), ShellQuoteArg(prefix), ShellQuoteArg(base))
}

// outerStarFuzzyCore is diagnostic-only. It recognizes the old wrapper-star
// spelling when removing its outer stars yields a valid relative plain target.
// Execution never calls this helper to rewrite or resolve a target.
func outerStarFuzzyCore(pattern string) (string, bool) {
	if len(pattern) < 3 || pattern[0] != '*' || pattern[len(pattern)-1] != '*' {
		return "", false
	}
	core := strings.Trim(pattern, "*")
	if core == "" || hasGlobChars(core) || strings.Contains(core, "]") {
		return "", false
	}
	normalized := strings.ReplaceAll(core, "\\", "/")
	if strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "~/") || ContainsParentTraversal(normalized) {
		return "", false
	}
	firstSegment := strings.SplitN(normalized, "/", 2)[0]
	if strings.Contains(firstSegment, ":") {
		return "", false
	}
	return core, true
}

func collapseRepeatedStars(pattern string) string {
	for strings.Contains(pattern, "**") {
		pattern = strings.ReplaceAll(pattern, "**", "*")
	}
	return pattern
}

func trailingSlashGlobStageWarning(kind command.StageKind, value string, scopeIndex int, colors platform.Palette) string {
	flag := "--" + string(kind)
	return fmt.Sprintf("%sWarning:%s %s pattern %s cannot match file paths (scope %d).\n\n  %sGlob patterns ending in '/' look for a file path ending in '/', but file paths do not have that ending.%s\n  %sTo select a directory subtree, use a literal directory name such as %s %s.%s",
		colors.Warn, colors.Reset, flag, SingleQuoted(value), scopeIndex+1,
		colors.Dim, colors.Reset,
		colors.Dim, flag, SingleQuoted("output/"), colors.Reset)
}

func recursiveGlobFilterPattern(pattern, suffix string) string {
	if !strings.Contains(suffix, "/") {
		return suffix
	}
	return pattern
}

func targetNotFoundWarning(target string, scopeIndex int, colors platform.Palette) string {
	if strings.Contains(target, "/") {
		return fmt.Sprintf("%sWarning:%s Target %s not found (scope %d).\n\n  %sIgnored paths must be named by their complete relative path, or discovered with --no-ignore.%s\n  %sExample:%s %scatclip . --no-ignore%s",
			colors.Warn, colors.Reset, SingleQuoted(target), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset, colors.OK, colors.Reset)
	}
	if prefersDirectFileLookup(target) {
		return fmt.Sprintf("%sWarning:%s No file named %s found (scope %d).\n\n  %sDirect file targets use exact basenames first. Non-exact file shorthand is resolved by fzf across visible directories.%s\n\n  %sFor ignored files, name the complete relative path or add --no-ignore.%s",
			colors.Warn, colors.Reset, SingleQuoted(target), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset)
	}
	return fmt.Sprintf("%sWarning:%s No file or directory %s found (scope %d).\n\n  %sDirectory shorthand is resolved by fzf. File targets use exact basenames first, then fzf across visible directories.%s\n\n  %sFor ignored paths, name the complete relative path or add --no-ignore.%s",
		colors.Warn, colors.Reset, SingleQuoted(target), scopeIndex+1,
		colors.Dim, colors.Reset,
		colors.Dim, colors.Reset)
}

// IgnoreRemovalHint formats the "To remove permanently" line for an ignored
// target. It branches on the source: --hiss only edits ~/.config/catclip/.hiss,
// so for any other source (.gitignore, .git/info/exclude, global excludes)
// pointing at --hiss is wrong advice — the user can't delete a .gitignore rule
// via --hiss. For those, send them to --all-ignore-rules, which lists every
// rule with its file:line so they can find and edit the right place.
func IgnoreRemovalHint(source string, colors platform.Palette) string {
	if source == ".hiss" {
		return fmt.Sprintf("\n  %sTo remove permanently:%s   %scatclip --hiss%s %s(delete the rule)%s",
			colors.Dim, colors.Reset, colors.OK, colors.Reset, colors.Dim, colors.Reset)
	}
	return fmt.Sprintf("\n  %sTo remove permanently:%s   find the rule with %scatclip --all-ignore-rules%s, then edit that file",
		colors.Dim, colors.Reset, colors.OK, colors.Reset)
}

// scopeTargetsForHint renders the user's positional target list as a
// shell-safe command tail. Falls back to the single relTarget when
// scopeTargets is empty (defensive; the resolver always has at least
// one). Preserves the user's original argv order so hints don't
// silently reshuffle their input.
func formatSkippedMatchesWarning(matches []SkippedMatch) []string {
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].RelPath < matches[j].RelPath
	})

	label := "matches"
	if len(matches) == 1 {
		label = "match"
	}
	lines := []string{fmt.Sprintf("Warning: %d %s skipped by ignore rules:", len(matches), label)}
	for _, match := range matches {
		lines = append(lines, fmt.Sprintf("  %s  [%s]", match.RelPath, match.BlockSource))
	}
	return []string{strings.Join(lines, "\n")}
}

func SingleQuoted(value string) string {
	return "'" + value + "'"
}

func WriteNoFilesMatchedMessage(scopes []command.ExecutionScope, stderr io.Writer, colors platform.Palette, hadSelectionCancel bool) error {
	if hadSelectionCancel {
		return nil
	}

	anyChanged := false
	hasStaged := false
	hasUnstaged := false
	hasUntracked := false
	for _, s := range scopes {
		anyChanged = anyChanged || s.HasGitSelection()
		hasStaged = hasStaged || s.Staged
		hasUnstaged = hasUnstaged || s.Unstaged
		hasUntracked = hasUntracked || s.Untracked
	}

	if anyChanged {
		flags := "--changed"
		if hasStaged || hasUnstaged || hasUntracked {
			var parts []string
			if hasStaged {
				parts = append(parts, "--staged")
			}
			if hasUnstaged {
				parts = append(parts, "--unstaged")
			}
			if hasUntracked {
				parts = append(parts, "--untracked")
			}
			flags = strings.Join(parts, "/")
		}

		if _, err := fmt.Fprintf(stderr, "%sNo %s files found.%s\n", colors.Warn, flags, colors.Reset); err != nil {
			return err
		}
		switch {
		case hasStaged && !hasUnstaged && !hasUntracked:
			_, _ = fmt.Fprintf(stderr, "  %sNo files are staged for commit. Use 'git add' to stage changes.%s\n", colors.Dim, colors.Reset)
		case hasUnstaged && !hasStaged && !hasUntracked:
			_, _ = fmt.Fprintf(stderr, "  %sNo tracked files have uncommitted modifications.%s\n", colors.Dim, colors.Reset)
		case hasUntracked && !hasStaged && !hasUnstaged:
			_, _ = fmt.Fprintf(stderr, "  %sNo new untracked files in the target directories.%s\n", colors.Dim, colors.Reset)
		default:
			_, _ = fmt.Fprintf(stderr, "  %sYour working tree may be clean, or the target has no modifications.%s\n", colors.Dim, colors.Reset)
		}
		_, err := fmt.Fprintf(stderr, "  %sRun without %s to select all files.%s\n", colors.Dim, flags, colors.Reset)
		return err
	}

	if _, err := fmt.Fprintf(stderr, "\n%sNo text files found matching your criteria.%s\n", colors.Warn, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "\n  %sPossible causes:%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s  1. Directory is empty or contains only binary files%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s  2. All files were ignored by .gitignore or .hiss rules%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s  3. Typo in target name%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	// Add a case-sensitivity bullet only when a regex filter was used —
	// --contains, --not-contains, and --snippet are PCRE2 regex matchers,
	// case-sensitive by default (well: smart-case). --only/--exclude are
	// shell globs and don't have this concern; targets aren't
	// pattern-matched at all. Keeping the bullet conditional avoids
	// misleading users who hit zero-match for an unrelated reason.
	usedRegexFilter := false
	for _, s := range scopes {
		if s.Contains != "" || s.Snippet || len(s.NotContains) > 0 {
			usedRegexFilter = true
			break
		}
	}
	if usedRegexFilter {
		if _, err := fmt.Fprintf(stderr, "  %s  4. Pattern contains uppercase letters (smart-case: uppercase = exact match)%s\n", colors.Dim, colors.Reset); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(stderr, "\n  %sTry: catclip --all-ignore-rules             # list every active ignore rule (.hiss + .gitignore)%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s     catclip <ignored-path>                 # name an ignored path directly%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stderr, "  %s     catclip --hiss                         # edit catclip's own ignore rules%s\n", colors.Dim, colors.Reset)
	return err
}
