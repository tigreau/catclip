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
// globs use path.Match over files, so `**` is not recursive and a glob ending
// in `/` can never match. Recursive recovery uses an exact directory target,
// optionally followed by the legacy cwd-relative --only filter.
//
// Replaces the misleading unconditional "If the parent directory is
// ignored, use --include" message that fired even when the parent was
// fully visible. See v0.6.4 ACTIVE_PLAN_natural_glob_targets_followup.md.
func globZeroMatchWarning(r *Resolver, pattern string, scopeIndex int, colors platform.Palette) string {
	prefix := longestLiteralPathPrefix(pattern)
	if prefix == "" || prefix == "." {
		return targetNotFoundWarning(pattern, scopeIndex, colors)
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
	filterPattern := recursiveGlobFilterPattern(pattern, suffix)

	return fmt.Sprintf("%sWarning:%s No files matched %s (scope %d).\n\n  %s%s checks %s directly in %s; target '*' does not cross folders.%s\n  %sTo search recursively below %s:%s\n    %scatclip %s --only %s%s",
		colors.Warn, colors.Reset, SingleQuoted(pattern), scopeIndex+1,
		colors.Dim, SingleQuoted(pattern), SingleQuoted(suffix), SingleQuoted(prefix+"/"), colors.Reset,
		colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
		colors.OK, ShellQuoteArg(prefix), ShellQuoteArg(filterPattern), colors.Reset)
}

func ignoredGlobZeroMatchWarning(pattern, prefix, suffix, source string, scopeIndex int, directoryShaped bool, colors platform.Palette) string {
	if directoryShaped {
		return fmt.Sprintf("%sWarning:%s %s is ignored by %s%s (scope %d).\n\n  %sTarget globs match files, not directory names; file paths do not end in '/'.%s\n  %sTo authorize and include everything below %s:%s\n    %scatclip %s --include %s%s",
			colors.Warn, colors.Reset, SingleQuoted(prefix+"/"), source, colors.Reset, scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
			colors.OK, ShellQuoteArg(prefix), ShellQuoteArg(prefix), colors.Reset)
	}
	filterPattern := recursiveGlobFilterPattern(pattern, suffix)

	return fmt.Sprintf("%sWarning:%s %s is ignored by %s%s (scope %d).\n\n  %s%s checks %s directly in %s; target '*' does not cross folders.%s\n  %sTo authorize it and search recursively:%s\n    %scatclip %s --include %s --only %s%s",
		colors.Warn, colors.Reset, SingleQuoted(prefix+"/"), source, colors.Reset, scopeIndex+1,
		colors.Dim, SingleQuoted(pattern), SingleQuoted(suffix), SingleQuoted(prefix+"/"), colors.Reset,
		colors.Dim, colors.Reset,
		colors.OK, ShellQuoteArg(prefix), ShellQuoteArg(prefix), ShellQuoteArg(filterPattern), colors.Reset)
}

func recursiveGlobFilterPattern(pattern, suffix string) string {
	if !strings.Contains(suffix, "/") {
		return suffix
	}
	return pattern
}

func targetNotFoundWarning(target string, scopeIndex int, colors platform.Palette) string {
	if strings.Contains(target, "/") {
		return fmt.Sprintf("%sWarning:%s Target %s not found (scope %d).\n\n  %sIf the parent directory is ignored, use --include to allow it first.%s\n  %sExample:%s %scatclip %s --include %s --only %s%s",
			colors.Warn, colors.Reset, SingleQuoted(target), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, SingleQuoted(path.Dir(target)), SingleQuoted(path.Dir(target)), SingleQuoted(path.Base(target)), colors.Reset)
	}
	if prefersDirectFileLookup(target) {
		return fmt.Sprintf("%sWarning:%s No file named %s found (scope %d).\n\n  %sDirect file targets use exact basenames first. Non-exact file shorthand is resolved by fzf across safe directories.%s\n\n  %sIf an ignored rule is hiding it, use --include to allow that blocked file or directory first.%s",
			colors.Warn, colors.Reset, SingleQuoted(target), scopeIndex+1,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset)
	}
	return fmt.Sprintf("%sWarning:%s No file or directory %s found (scope %d).\n\n  %sDirectory shorthand is resolved by fzf. File targets use exact basenames first, then fzf across safe directories.%s\n\n  %sIf the thing you want is ignored, use --include to browse blocked targets for this scope.%s",
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
func scopeTargetsForHint(relTarget string, scopeTargets []string) string {
	if len(scopeTargets) == 0 {
		return relTarget
	}
	parts := make([]string, 0, len(scopeTargets))
	for _, t := range scopeTargets {
		if strings.TrimSpace(t) == "" {
			continue
		}
		parts = append(parts, t)
	}
	if len(parts) == 0 {
		return relTarget
	}
	return strings.Join(parts, " ")
}

func ignoredDirMessage(relTarget, source string, includesActive bool, includedDescendants []string, scopeTargets []string, colors platform.Palette) string {
	scopeTail := scopeTargetsForHint(relTarget, scopeTargets)
	// Most actionable case: the user passed `--include <path>` where the
	// include path lives inside the target (so the include is a
	// descendant, not the ancestor that --include needs). Show them the
	// two correct shapes instead of the generic "your --include does not
	// cover this target" line.
	if len(includedDescendants) > 0 {
		descendant := includedDescendants[0]
		return fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %s--include %s points inside %s — it doesn't authorize %s itself.%s\n  %s--include must name the gitignored target, or an ancestor of it.%s\n\n  %sTo open %s and narrow to %s:%s\n    %scatclip %s --include %s --only %s%s\n  %sTo open %s directly:%s\n    %scatclip %s --include %s%s",
			colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
			colors.Dim, SingleQuoted(descendant), SingleQuoted(relTarget), SingleQuoted(relTarget), colors.Reset,
			colors.Dim, colors.Reset,
			colors.Dim, SingleQuoted(relTarget), SingleQuoted(descendant), colors.Reset,
			colors.OK, relTarget, SingleQuoted(relTarget), SingleQuoted(descendant), colors.Reset,
			colors.Dim, SingleQuoted(descendant), colors.Reset,
			colors.OK, descendant, SingleQuoted(relTarget), colors.Reset,
		) + IgnoreRemovalHint(source, colors)
	}
	if includesActive {
		return fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %sYour --include does not cover this target. Add it directly:%s\n  %sExample:%s %scatclip --include %s%s",
			colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset, colors.OK, SingleQuoted(relTarget), colors.Reset,
		) + IgnoreRemovalHint(source, colors)
	}
	// Canonical double-syntax hint: `catclip <scope-targets> --include
	// <path>`. Preserves ALL of the user's positional targets so a run
	// like `catclip cmd docs` gets `catclip cmd docs --include docs`,
	// not the misleading `catclip docs --include docs` (which drops
	// cmd). Matches the effect-5 error's suggestion.
	return fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %sUse --include to authorize %s for this run.%s\n  %sExample:%s %scatclip %s --include %s%s\n  %sTo narrow inside it:%s   %scatclip %s --include %s --only \"*.ext\"%s",
		colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
		colors.Dim, SingleQuoted(relTarget), colors.Reset,
		colors.Dim, colors.Reset, colors.OK, scopeTail, SingleQuoted(relTarget), colors.Reset,
		colors.Dim, colors.Reset, colors.OK, scopeTail, SingleQuoted(relTarget), colors.Reset,
	) + IgnoreRemovalHint(source, colors)
}

func ignoredFileMessage(relTarget, source string, fromChained, includesActive bool, scopeTargets []string, colors platform.Palette) string {
	scopeTail := scopeTargetsForHint(relTarget, scopeTargets)
	if includesActive {
		message := fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %sYour --include does not cover this target. Add it directly:%s\n  %sExample:%s %scatclip %s --include %s%s",
			colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset, colors.OK, scopeTail, SingleQuoted(relTarget), colors.Reset)
		if fromChained {
			return message
		}
		return message + fmt.Sprintf("\n  %sTo remove permanently:%s   %scatclip --hiss%s %s(delete the rule)%s",
			colors.Dim, colors.Reset, colors.OK, colors.Reset, colors.Dim, colors.Reset)
	}
	// Canonical double-syntax hint preserving all scope targets so
	// `catclip cmd docs/blocked.md` renders `catclip cmd docs/blocked.md
	// --include docs/blocked.md`, not the target-dropping shorter form.
	message := fmt.Sprintf("\n%s%sError:%s%s %s is ignored by %s%s\n\n  %sUse --include to authorize %s for this run.%s\n  %sExample:%s %scatclip %s --include %s%s",
		colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(relTarget), source, colors.Reset,
		colors.Dim, SingleQuoted(relTarget), colors.Reset,
		colors.Dim, colors.Reset, colors.OK, scopeTail, SingleQuoted(relTarget), colors.Reset)
	if fromChained {
		return message
	}
	return message + IgnoreRemovalHint(source, colors)
}

func includeQueryNeedsSelectionMessage(query string, colors platform.Palette) string {
	return fmt.Sprintf("\n%sError: %s needs an ignored-target selection.%s\n\n  %sUse --include with an exact ignored path, or run it in a TTY so catclip can open the ignored picker.%s",
		colors.Err, SingleQuoted(query), colors.Reset,
		colors.Dim, colors.Reset)
}

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
	// Hint form uses `<target> --include <path>` so users copy the
	// canonical double-syntax shape ignoredDirMessage and effect-5
	// both point at. Bare `catclip --include <path>` errors under
	// effect-5 in strict mode; teaching users a form that fails is
	// worse than showing the working shape.
	if _, err := fmt.Fprintf(stderr, "\n  %sTry: catclip --all-ignore-rules             # list every active ignore rule (.hiss + .gitignore)%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s     catclip <target> --include <path>      # authorize a gitignored path for this run%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stderr, "  %s     catclip --hiss                         # edit catclip's own ignore rules%s\n", colors.Dim, colors.Reset)
	return err
}
