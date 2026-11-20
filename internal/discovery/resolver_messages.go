package discovery

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
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
	return globZeroMatchFormatter(r, pattern, colors)(scopeIndex)
}

func globZeroMatchFormatter(r *Resolver, pattern string, colors platform.Palette) func(int) string {
	// The target-local classification already proved that matching binaries
	// were excluded. Do not probe prefixes or ignore membership to explain it.
	if !r.WithBinaries && r.lastClassificationExcludedBinary {
		return func(scopeIndex int) string {
			return fmt.Sprintf("%sWarning:%s No eligible files matched %s%s.\n%s",
				colors.Warn, colors.Reset, SingleQuoted(pattern), diagnosticScopeSuffix(scopeIndex),
				binaryTargetInclusionHint(pattern, r.NoIgnore, colors))
		}
	}
	prefix := longestLiteralPathPrefix(pattern)
	if prefix == "" || prefix == "." {
		return func(scopeIndex int) string { return globWithoutLiteralPrefixWarning(pattern, scopeIndex, colors) }
	}
	suffix := strings.TrimPrefix(pattern, prefix+"/")
	if suffix == pattern {
		// prefix did not consume any portion of pattern; treat as missing
		return func(scopeIndex int) string { return targetNotFoundWarning(pattern, scopeIndex, colors) }
	}
	isDir, err := r.targetPathIsDirectory(prefix)
	if err != nil || !isDir {
		return func(scopeIndex int) string {
			return unresolvedGlobPrefixWarning(pattern, prefix, suffix, scopeIndex, colors)
		}
	}

	directoryShaped := strings.HasSuffix(strings.ReplaceAll(pattern, "\\", "/"), "/")
	block, _ := r.dirBlockedBy(prefix)
	if block != nil && block.Source != "" {
		return func(scopeIndex int) string {
			return ignoredGlobZeroMatchWarning(pattern, prefix, suffix, block.Source, scopeIndex, directoryShaped, colors)
		}
	}
	return func(scopeIndex int) string {
		return visibleGlobZeroMatchWarning(pattern, prefix, suffix, scopeIndex, directoryShaped, colors)
	}
}

// binaryTargetInclusionHint only formats the target and known scope policy.
// It must never discover files or classify content for a suggested command.
func binaryTargetInclusionHint(target string, noIgnore bool, colors platform.Palette) string {
	args := ShellQuoteArg(target)
	if noIgnore {
		args += " --no-ignore"
	}
	return fmt.Sprintf("  %sBinary files were excluded. Include them with -wb (--with-binaries):%s\n    %scatclip %s -wb%s\n\n  %sOr name a binary file by its complete relative path.%s",
		colors.Dim, colors.Reset, colors.OK, args, colors.Reset, colors.Dim, colors.Reset)
}

func globWithoutLiteralPrefixWarning(pattern string, scopeIndex int, colors platform.Palette) string {
	if core, ok := outerStarFuzzyCore(pattern); ok {
		return fmt.Sprintf("%sWarning:%s No files matched %s%s.\n\n  %sTarget globs select files and '*' does not cross folders.%s\n  %sFor fuzzy file and folder navigation:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(pattern), diagnosticScopeSuffix(scopeIndex),
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, ShellQuoteArg(core), colors.Reset)
	}

	return fmt.Sprintf("%sWarning:%s No files matched %s%s.\n\n  %sTarget globs match complete filenames and cwd-relative file paths; they do not fall back to fuzzy navigation.%s",
		colors.Warn, colors.Reset, SingleQuoted(pattern), diagnosticScopeSuffix(scopeIndex),
		colors.Dim, colors.Reset)
}

func unresolvedGlobPrefixWarning(pattern, prefix, suffix string, scopeIndex int, colors platform.Palette) string {
	commandPrefix := ShellQuoteArg(prefix)
	normalizedSuffix := strings.TrimSuffix(strings.ReplaceAll(suffix, "\\", "/"), "/")
	allStars := normalizedSuffix != "" && strings.Trim(normalizedSuffix, "*") == ""
	if !allStars && !strings.Contains(normalizedSuffix, "/") {
		filter := ShellQuoteArg(normalizedSuffix)
		return fmt.Sprintf("%sWarning:%s No files matched %s%s.\n\n  %sNo cwd-relative directory %s exists; target globs do not fuzzy-match directory segments.%s\n  %sTo choose a directory matching %s and search recursively:%s\n    %scatclip %s --only %s%s\n  %sTo keep only direct matches:%s\n    %scatclip %s --depth 1 --only %s%s",
			colors.Warn, colors.Reset, SingleQuoted(pattern), diagnosticScopeSuffix(scopeIndex),
			colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
			colors.Dim, SingleQuoted(prefix), colors.Reset,
			colors.OK, commandPrefix, filter, colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, commandPrefix, filter, colors.Reset)
	}

	return fmt.Sprintf("%sWarning:%s No files matched %s%s.\n\n  %sNo cwd-relative directory %s exists; target globs do not fuzzy-match directory segments.%s\n  %sTo choose a directory matching %s and include everything below it:%s\n    %scatclip %s%s\n  %sTo keep only its direct files:%s\n    %scatclip %s --depth 1%s",
		colors.Warn, colors.Reset, SingleQuoted(pattern), diagnosticScopeSuffix(scopeIndex),
		colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
		colors.Dim, SingleQuoted(prefix), colors.Reset,
		colors.OK, commandPrefix, colors.Reset,
		colors.Dim, colors.Reset,
		colors.OK, commandPrefix, colors.Reset)
}

func visibleGlobZeroMatchWarning(pattern, prefix, suffix string, scopeIndex int, directoryShaped bool, colors platform.Palette) string {
	if directoryShaped {
		return fmt.Sprintf("%sWarning:%s No files matched %s%s.\n\n  %sTarget globs match files, not directory names; file paths do not end in '/'.%s\n  %sTo include everything below %s:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(pattern), diagnosticScopeSuffix(scopeIndex),
			colors.Dim, colors.Reset,
			colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
			colors.OK, ShellQuoteArg(prefix), colors.Reset)
	}
	if globSuffixIsOnlyStars(suffix) {
		return fmt.Sprintf("%sWarning:%s No files matched %s%s.\n\n  %s%s checks direct files in %s; target '*' does not cross folders.%s\n  %sTo include everything below %s:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(pattern), diagnosticScopeSuffix(scopeIndex),
			colors.Dim, SingleQuoted(pattern), SingleQuoted(prefix+"/"), colors.Reset,
			colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
			colors.OK, ShellQuoteArg(prefix), colors.Reset)
	}
	filterPattern := recursiveGlobFilterPattern(pattern, suffix)

	return fmt.Sprintf("%sWarning:%s No files matched %s%s.\n\n  %s%s checks %s directly in %s; target '*' does not cross folders.%s\n  %sTo search recursively below %s:%s\n    %scatclip %s --only %s%s",
		colors.Warn, colors.Reset, SingleQuoted(pattern), diagnosticScopeSuffix(scopeIndex),
		colors.Dim, SingleQuoted(pattern), SingleQuoted(suffix), SingleQuoted(prefix+"/"), colors.Reset,
		colors.Dim, SingleQuoted(prefix+"/"), colors.Reset,
		colors.OK, ShellQuoteArg(prefix), ShellQuoteArg(filterPattern), colors.Reset)
}

func ignoredGlobZeroMatchWarning(pattern, prefix, suffix, source string, scopeIndex int, directoryShaped bool, colors platform.Palette) string {
	if directoryShaped {
		return fmt.Sprintf("%sWarning:%s %s is ignored by %s%s%s.\n\n  %sTarget globs match files, not directory names; file paths do not end in '/'.%s\n  %sName the ignored directory directly:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(prefix+"/"), source, colors.Reset, diagnosticScopeSuffix(scopeIndex),
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, ShellQuoteArg(prefix), colors.Reset)
	}
	if globSuffixIsOnlyStars(suffix) {
		return fmt.Sprintf("%sWarning:%s %s is ignored by %s%s%s.\n\n  %s%s checks direct files in %s; target '*' does not cross folders.%s\n  %sName the ignored directory directly:%s\n    %scatclip %s%s",
			colors.Warn, colors.Reset, SingleQuoted(prefix+"/"), source, colors.Reset, diagnosticScopeSuffix(scopeIndex),
			colors.Dim, SingleQuoted(pattern), SingleQuoted(prefix+"/"), colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, ShellQuoteArg(prefix), colors.Reset)
	}
	filterPattern := recursiveGlobFilterPattern(pattern, suffix)

	return fmt.Sprintf("%sWarning:%s %s is ignored by %s%s%s.\n\n  %s%s checks %s directly in %s; target '*' does not cross folders.%s\n  %sName the ignored directory, then filter it:%s\n    %scatclip %s --only %s%s",
		colors.Warn, colors.Reset, SingleQuoted(prefix+"/"), source, colors.Reset, diagnosticScopeSuffix(scopeIndex),
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
	return fmt.Sprintf("%sWarning:%s %s pattern %s cannot match file paths%s.\n\n  %sGlob patterns ending in '/' look for a file path ending in '/', but file paths do not have that ending.%s\n  %sTo select a directory subtree, use a literal directory name such as %s %s.%s",
		colors.Warn, colors.Reset, flag, SingleQuoted(value), diagnosticScopeSuffix(scopeIndex),
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
	// A path that is not on disk at all (often guessed, e.g. by a chat agent
	// asking for code) is not an ignore problem: say what exists instead.
	if strings.Contains(target, "/") && !pathExists(target) {
		return missingPathWarning(target, scopeIndex, colors)
	}
	if strings.Contains(target, "/") {
		return fmt.Sprintf("%sWarning:%s Target %s not found%s.\n\n  %sIgnored paths must be named by their complete relative path, or discovered with --no-ignore.%s\n  %sTry the same target with ignore rules disabled:%s\n    %scatclip %s --no-ignore%s",
			colors.Warn, colors.Reset, SingleQuoted(target), diagnosticScopeSuffix(scopeIndex),
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset, colors.OK, ShellQuoteArg(target), colors.Reset)
	}
	if prefersDirectFileLookup(target) {
		return fmt.Sprintf("%sWarning:%s No file named %s found%s.\n\n  %sDirect file targets use exact basenames first. Non-exact file shorthand is resolved by fzf across visible directories.%s\n\n  %sFor ignored files, name the complete relative path or add --no-ignore.%s",
			colors.Warn, colors.Reset, SingleQuoted(target), diagnosticScopeSuffix(scopeIndex),
			colors.Dim, colors.Reset,
			colors.Dim, colors.Reset)
	}
	return fmt.Sprintf("%sWarning:%s No file or directory %s found%s.\n\n  %sDirectory shorthand is resolved by fzf. File targets use exact basenames first, then fzf across visible directories.%s\n\n  %sFor ignored paths, name the complete relative path or add --no-ignore.%s",
		colors.Warn, colors.Reset, SingleQuoted(target), diagnosticScopeSuffix(scopeIndex),
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

		if _, err := fmt.Fprintf(stderr, "%sNo files matched the requested targets and filters.%s\n", colors.Warn, colors.Reset); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stderr, "  %sGit selection requested: %s.%s\n  %sTargets and other filters also limit the result.%s\n  %sCheck changes with: git status --short%s\n",
			colors.Dim, flags, colors.Reset, colors.Dim, colors.Reset, colors.Dim, colors.Reset); err != nil {
			return err
		}
		return writeEmptyResultIgnoreHints(stderr, colors)
	}

	// --snippet that found nothing: the files exist, the pattern matched no
	// line. Name the pattern instead of suggesting empty or ignored folders.
	var snippetPatterns []string
	for _, s := range scopes {
		if s.Snippet && s.SnippetPattern != "" {
			snippetPatterns = append(snippetPatterns, "'"+s.SnippetPattern+"'")
		}
	}
	if len(snippetPatterns) > 0 {
		if _, err := fmt.Fprintf(stderr, "\n%sNo lines matched --snippet %s.%s\n", colors.Warn, strings.Join(snippetPatterns, ", "), colors.Reset); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stderr, "\n  %sThe pattern is a regular expression (smart-case: uppercase makes it case-sensitive).%s\n"+
			"  %sEscape ( ) [ ] { } . * + ? | \\ to match them literally, e.g. 'Main\\(' for \"Main(\".%s\n"+
			"  %sTargets and other filters also limit which files are searched.%s\n",
			colors.Dim, colors.Reset, colors.Dim, colors.Reset, colors.Dim, colors.Reset); err != nil {
			return err
		}
		// No ignore-rule tips: the files were found, so ignore rules are not
		// why nothing matched, and the tips sent readers the wrong way.
		return nil
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
	if _, err := fmt.Fprintf(stderr, "  %s  3. Targets or filters exclude every file%s\n", colors.Dim, colors.Reset); err != nil {
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
		if _, err := fmt.Fprintf(stderr, "  %s  4. Pattern contains uppercase letters (smart-case: uppercase = case-sensitive)%s\n", colors.Dim, colors.Reset); err != nil {
			return err
		}
	}
	return writeEmptyResultIgnoreHints(stderr, colors)
}

func writeEmptyResultIgnoreHints(stderr io.Writer, colors platform.Palette) error {
	if _, err := fmt.Fprintf(stderr, "\n  %sTry: catclip --all-ignore-rules             # list every active ignore rule (.hiss + .gitignore)%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "  %s     catclip <ignored-path>                 # name an ignored path directly%s\n", colors.Dim, colors.Reset); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stderr, "  %s     catclip --hiss                         # edit catclip's own ignore rules%s\n", colors.Dim, colors.Reset)
	return err
}

func diagnosticScopeSuffix(index int) string {
	if index < 0 {
		return ""
	}
	return fmt.Sprintf(" (scope %d)", index+1)
}

// Build both presentations from already captured facts, never by replaying a
// resolver. Paths and regexes may themselves contain literal "(scope N)" text.
func scopedDiagnostic(scope int, format func(int) string) Diagnostic {
	return Diagnostic{ScopeIndex: scope, Message: format(scope), GroupedMessage: format(-1)}
}

func pathExists(target string) bool {
	_, err := os.Lstat(filepath.FromSlash(strings.TrimSuffix(target, "/")))
	return err == nil
}

// missingPathFolderLimit keeps the listing of a folder's files short.
const missingPathFolderLimit = 8

// missingPathWarning explains a path that does not exist: it lists the
// files of its folder when that folder exists, and otherwise suggests a bare
// file name or a folder, which catclip finds anywhere in the project.
func missingPathWarning(target string, scopeIndex int, colors platform.Palette) string {
	clean := strings.TrimSuffix(target, "/")
	dir, base := path.Split(clean)
	dir = strings.TrimSuffix(dir, "/")
	var b strings.Builder
	fmt.Fprintf(&b, "%sWarning:%s Target %s not found%s.\n", colors.Warn, colors.Reset, SingleQuoted(target), diagnosticScopeSuffix(scopeIndex))
	if dir != "" && pathExists(dir) {
		if entries, err := os.ReadDir(filepath.FromSlash(dir)); err == nil {
			var names []string
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") {
					continue
				}
				name := e.Name()
				if e.IsDir() {
					name += "/"
				}
				names = append(names, name)
			}
			if len(names) > 0 {
				shown := names
				more := ""
				if len(shown) > missingPathFolderLimit {
					shown = shown[:missingPathFolderLimit]
					more = fmt.Sprintf(", and %d more", len(names)-missingPathFolderLimit)
				}
				fmt.Fprintf(&b, "\n  %s%s/ has: %s%s%s\n", colors.Dim, dir, strings.Join(shown, ", "), more, colors.Reset)
				fmt.Fprintf(&b, "  %sCopy the whole folder with:%s %scatclip %s%s", colors.Dim, colors.Reset, colors.OK, ShellQuoteArg(dir), colors.Reset)
				return b.String()
			}
		}
	}
	fmt.Fprintf(&b, "\n  %sA file name alone or a folder is found anywhere in the project:%s %scatclip %s%s", colors.Dim, colors.Reset, colors.OK, ShellQuoteArg(base), colors.Reset)
	return b.String()
}
