package catclip

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type helpRow struct {
	Left  string
	Right string
}

func writeAlignedHelpRows(b *strings.Builder, indent string, style func(string) string, rows []helpRow) {
	max := 0
	for _, row := range rows {
		if len(row.Left) > max {
			max = len(row.Left)
		}
	}
	for _, row := range rows {
		b.WriteString(indent)
		b.WriteString(style(row.Left))
		b.WriteString(strings.Repeat(" ", max-len(row.Left)+2))
		b.WriteString(row.Right)
		b.WriteByte('\n')
	}
}

func shortHelpText(version string, colors colorPalette) string {
	var b strings.Builder
	cmd := func(s string) string { return colors.OK + s + colors.Reset }
	bold := func(s string) string { return colors.Bold + s + colors.Reset }
	dim := func(s string) string { return colors.Dim + s + colors.Reset }
	bad := func(s string) string { return colors.Err + s + colors.Reset }

	fmt.Fprintf(&b, "%scatclip v%s — Recursively copy code context for AI prompts%s\n\n", colors.Bold, version, colors.Reset)
	b.WriteString("Usage:  catclip [target ...] [filters ...]\n\n")

	fmt.Fprintf(&b, "%s\n", bold("Quick Start:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip", Right: "Pick files or folders from a menu"},
		{Left: "catclip src", Right: "Copy a folder"},
		{Left: "catclip Button.tsx", Right: "Copy a file (finds it for you)"},
		{Left: "catclip btn", Right: "Fuzzy match — finds Button.tsx for you"},
		{Left: "catclip src lib docs", Right: "Copy multiple folders"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Don't remember the flags? Use menus:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip --", Right: "Pick files or folders, then a filter from menus"},
		{Left: "catclip src --", Right: "Pick filters for src from a menu"},
		{Left: "catclip src -- --", Right: "Chain menus to build a full command"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Filtering:"))
	fmt.Fprintf(&b, "  Filters run left to right. Changing the order changes the result.\n\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: `catclip src --only "*.ts"`, Right: "Only TypeScript files"},
		{Left: `catclip src --exclude "*.css"`, Right: "Skip CSS files"},
		{Left: "catclip src --recent 3", Right: "Keep the 3 most recently modified files"},
		{Left: "catclip src --contains TODO", Right: "Find files containing specific text"},
		{Left: "catclip src --snippet TODO", Right: "Only the matching blocks, not full files"},
	})

	fmt.Fprintf(&b, "\n  %s\n", dim(`You can give --only, --exclude, and --include more than one value.`))
	fmt.Fprintf(&b, "  %s\n", dim(`Examples: --only "*.ts" "*.tsx"   --exclude "*.css" "*.scss"`))
	fmt.Fprintf(&b, "\n  These two commands are different:\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: `catclip src --recent 10 --only "*.ts"`, Right: "Take the 10 newest files, then keep the .ts ones"},
		{Left: `catclip src --only "*.ts" --recent 10`, Right: "Keep .ts first, then take the 10 newest of that set"},
	})
	fmt.Fprintf(&b, "\n  One combined example:\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --contains "Button" --only "*.tsx" --exclude "Header.tsx" "Login.tsx" --recent 5`))
	fmt.Fprintf(&b, "    %s\n", dim(`Start with files under src.`))
	fmt.Fprintf(&b, "    %s\n", dim(`Keep only the ones that mention "Button".`))
	fmt.Fprintf(&b, "    %s\n", dim(`From those, keep only .tsx files.`))
	fmt.Fprintf(&b, "    %s\n", dim(`Then remove Header.tsx and Login.tsx.`))
	fmt.Fprintf(&b, "    %s\n\n", dim(`Finally, take the 5 most recently modified files left.`))

	fmt.Fprintf(&b, "\n%s\n", bold("Git Filters (requires a git repo):"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip src --changed", Right: "Only files changed in git"},
		{Left: "catclip --changed-diff", Right: "Show changes as patches instead of full files"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("--then (chain another catclip command):"))
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --only "*.ts" --then docs --recent 5`))
	fmt.Fprintf(&b, "    %s\n", dim("Like running two catclip commands and combining the results."))
	fmt.Fprintf(&b, "    %s\n", dim(`This keeps only the TS files from src, then adds the 5 most recent files from docs.`))
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", bad(`catclip src docs --only "*.ts"`))
	fmt.Fprintf(&b, "    %s\n", dim(`Bad here because it would also throw away every non-TS file in docs.`))
	fmt.Fprintf(&b, "    %s\n", dim("Use --then when the next target should use different filters."))

	fmt.Fprintf(&b, "\n%s\n", bold("Ignored Files:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip --include tests", Right: "Allow an ignored folder for this run"},
		{Left: "catclip --hiss", Right: fmt.Sprintf("Edit your ignore rules (%s)", displayPath(globalHissPath()))},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Options:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "-h, --help", Right: "Show this help"},
		{Left: "--help-all", Right: "Full reference manual"},
		{Left: "--version", Right: "Show version"},
		{Left: "--preview", Right: "See what would be copied without copying"},
		{Left: "-p, --print", Right: "Print to terminal instead of clipboard"},
		{Left: "-q, --quiet", Right: "No prompts, no decoration"},
		{Left: "-y, --yes", Right: "Skip confirmation for large copies"},
		{Left: "-t, --no-tree", Right: "Skip the file tree preview"},
		{Left: "--hiss-reset", Right: "Restore ignore rules to defaults"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("More examples and advanced flags: see --help-all"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "--staged", Right: "Staged files only"},
		{Left: "--unstaged", Right: "Unstaged changes only"},
		{Left: "--untracked", Right: "New untracked files only"},
		{Left: "--verbose", Right: "Debug info and timings"},
	})

	return b.String()
}

func fullHelpText(version string, colors colorPalette) string {
	var b strings.Builder
	cmd := func(s string) string { return colors.OK + s + colors.Reset }
	bold := func(s string) string { return colors.Bold + s + colors.Reset }
	dim := func(s string) string { return colors.Dim + s + colors.Reset }
	errText := func(s string) string { return colors.Err + s + colors.Reset }

	b.WriteString(shortHelpText(version, colors))
	fmt.Fprintf(&b, "\n\n%s\n\n", bold("━━━ Full Manual ━━━"))

	fmt.Fprintf(&b, "%s\n", bold("All Scope Modifiers:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "--only VALUE...", Right: "Add one only stage (values OR together within that stage)"},
		{Left: "--exclude VALUE...", Right: "Add one exclude stage (values OR together; trailing / = directory)"},
		{Left: "--recent [N]", Right: "Sort current files newest-first; optional top-N limit"},
		{Left: "--changed", Right: "Only git-modified files"},
		{Left: "--staged", Right: "Only staged files (git index)"},
		{Left: "--unstaged", Right: "Only unstaged tracked modifications"},
		{Left: "--untracked", Right: "Only new untracked files"},
		{Left: "--changed-diff", Right: "Changed files, emitted as unified diff"},
		{Left: "--staged-diff", Right: "Staged files, emitted as unified diff"},
		{Left: "--unstaged-diff", Right: "Unstaged files, emitted as unified diff"},
		{Left: "--contains PATTERN", Right: "Only files whose contents match regex pattern"},
		{Left: "--snippet PATTERN", Right: "Only blank-line-bounded blocks whose contents match regex"},
		{Left: "--include VALUE...", Right: "Allow one or more ignored targets for this scope"},
		{Left: "--then", Right: "Start a new scope (separate targets with different modifiers)"},
	})
	fmt.Fprintf(&b, "\n  %s\n\n", dim("Filename filters use shell globs (*, ?, [...]); --contains uses regex."))

	fmt.Fprintf(&b, "%s\n", bold("Scope System:"))
	fmt.Fprintf(&b, "  Use %s to separate scopes with different modifiers.\n", cmd("--then"))
	fmt.Fprintf(&b, "  Layout: %s\n\n", dim("TARGETS [MODIFIERS...] --then TARGETS [MODIFIERS...] ..."))
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --only "*.ts" --exclude "*.test.ts" --then features --only "*.tsx"`))
	fmt.Fprintf(&b, "  %s\n", dim("  Scope 1: src — TypeScript files, skipping tests"))
	fmt.Fprintf(&b, "  %s\n\n", dim("  Scope 2: features — TSX files only"))
	fmt.Fprintf(&b, "  %s\n", dim("  Overlapping scopes are allowed; final copied files are deduplicated by path."))
	fmt.Fprintf(&b, "  %s\n", dim("  If scopes overlap, a duplicate file keeps the position of its first scope occurrence."))
	fmt.Fprintf(&b, "  %s\n", dim("  A bare --then keeps both scopes unresolved in interactive startup: catclip first asks for scope 1 targets, then scope 2 targets."))
	fmt.Fprintf(&b, "  %s\n", dim("  Think of --then as the one-command equivalent of running multiple catclip scope commands and unioning their results."))
	fmt.Fprintf(&b, "  %s\n", dim("  That also applies to --recent: it ranks within each scope, not across the final union."))
	fmt.Fprintf(&b, "  %s\n", dim("  Within one scope, modifiers run left to right, and changing the order can change the result."))
	fmt.Fprintf(&b, "  Without %s, all targets share the same modifiers:\n", cmd("--then"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip src lib", Right: dim("Both use default rules")},
		{Left: `catclip src lib --only "*.ts"`, Right: dim("Both filtered to .ts files")},
	})
	b.WriteByte('\n')

	fmt.Fprintf(&b, "%s\n", bold("Target Resolution:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip", Right: "Open the safe-target picker (interactive terminal)"},
		{Left: "catclip auth", Right: "Directory shorthand; resolves directly when unique, otherwise fzf"},
		{Left: "catclip Button.tsx", Right: "Near-instant exact basename lookup across safe files"},
		{Left: "catclip layout/Footer.tsx", Right: "Scoped shorthand; resolves directly when unique"},
		{Left: "catclip btn.tsx", Right: "File shorthand; resolves directly when unique, otherwise fzf"},
		{Left: "catclip src/components/ui/Button.tsx", Right: "Exact nested file path"},
	})
	fmt.Fprintf(&b, "  %s\n", dim("Plain targets stay independent: 'catclip src Button.tsx docs' does not bind Button.tsx to src."))
	fmt.Fprintf(&b, "  %s\n", dim("Visible targets can resolve by exact path, unique basename, or unique directory segment before falling back to fzf."))
	fmt.Fprintf(&b, "  %s\n", dim("In multi-select pickers, Tab marks items. Toggle-all uses Alt-A on Linux/Windows and Ctrl-A on macOS."))
	fmt.Fprintf(&b, "  %s\n", dim("In dynamic-set pickers like --changed and --contains, keeping the whole set stays plain (--changed / --contains ...) instead of expanding to --only ..."))
	fmt.Fprintf(&b, "  %s\n\n", dim("catclip handles exact targets directly; bundled fzf is only used for shorthand and fuzzy disambiguation when needed."))

	fmt.Fprintf(&b, "%s\n", bold("Safe By Default:"))
	b.WriteString("  Default discovery stays safe and respects local .gitignore + .hiss.\n")
	b.WriteString("  Ignored files and directories from either source require --include authorization.\n")
	b.WriteString("  --include is stricter than normal visible target resolution: it only auto-accepts exact ignored paths relative to the current cwd.\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: `catclip --include tests`, Right: "Allow tests/ for this run"},
		{Left: `catclip --include .env.production`, Right: "Allow a blocked filename"},
		{Left: `catclip --include coverage --only "*.json"`, Right: "Allow via include, then narrow"},
		{Left: `catclip index.js`, Right: "Safe basename search; blocked dir hits are skipped"},
	})
	b.WriteByte('\n')

	fmt.Fprintf(&b, "%s\n", bold("Ignore System:"))
	fmt.Fprintf(&b, "  Global config: %s (gitignore-inspired syntax)\n", cmd(displayPath(globalHissPath())))
	fmt.Fprintf(&b, "  First run is still safe: catclip creates the default %s and applies it immediately.\n", cmd(".hiss"))
	fmt.Fprintf(&b, "  Local project %s still applies alongside the global %s.\n\n", cmd(".gitignore"), cmd(".hiss"))

	fmt.Fprintf(&b, "%s\n", bold("Example .hiss:"))
	fmt.Fprintf(&b, "  %s\n", dim("# Ignore build output"))
	fmt.Fprintf(&b, "  %s\n", dim("dist/"))
	fmt.Fprintf(&b, "  %s\n", dim("*.min.js"))
	fmt.Fprintf(&b, "  %s\n", dim("# Ignore specific file"))
	fmt.Fprintf(&b, "  %s\n\n", dim("test/fixtures.json"))

	fmt.Fprintf(&b, "%s\n", bold("Editing Ignore Rules:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip --hiss", Right: "Open ignore config in editor"},
		{Left: "catclip --hiss-reset", Right: "Restore defaults"},
	})
	b.WriteByte('\n')

	fmt.Fprintf(&b, "%s\n", bold("Pattern Matching:"))
	writeAlignedHelpRows(&b, "  ", dim, []helpRow{
		{Left: "--only, --exclude", Right: "Shell globs (*, ?, [...]) match filenames and paths"},
		{Left: "--contains", Right: "Regex syntax matches file contents"},
	})
	fmt.Fprintf(&b, "  %s\n", dim("Globs can match against both basename and full path."))
	fmt.Fprintf(&b, "  %s\n\n", dim("Examples: *.ts   test/*.ts   **/test/*.ts"))

	fmt.Fprintf(&b, "%s\n", bold("--exclude (add rules):"))
	b.WriteString("  Adds temporary skip rules for this run only.\n")
	b.WriteString("  One stage per occurrence. Values in the same occurrence OR together. Trailing / = directory.\n")
	b.WriteString("  Bare names like build match a file named build or a directory named build. Use build/ for the explicit directory-only form.\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: `--exclude "*.css"`, Right: "Skip CSS files"},
		{Left: `--exclude "*.css" "*.svg"`, Right: "Skip CSS and SVG files"},
		{Left: `--exclude "build/"`, Right: "Skip build directory"},
	})
	b.WriteByte('\n')

	fmt.Fprintf(&b, "%s\n", bold("--contains (content search):"))
	b.WriteString("  Filters to files whose contents match a regex pattern.\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "--contains TODO", Right: `Files containing "TODO"`},
		{Left: `--contains "useState|useEffect"`, Right: "Files matching either hook"},
		{Left: `--contains '\$store'`, Right: "Escaped special characters"},
	})
	fmt.Fprintf(&b, "  %s\n", dim("Interactive content-match pickers show [all current matches]. If your selection already covers every regex match in scope, catclip keeps the current regex command plain instead of appending --only."))
	fmt.Fprintf(&b, "  %s\n\n", dim("Plain text works for most searches. Use single quotes for special chars."))

	fmt.Fprintf(&b, "%s\n", bold("--snippet (block extraction):"))
	b.WriteString("  Takes its own regex. Instead of the full file, emits only the semantic blocks\n")
	b.WriteString("  (blank-line-bounded) surrounding each match. Dramatically reduces token usage.\n")
	b.WriteString("  If a matched block spans the whole file, snippet output can look identical to\n")
	b.WriteString("  full-file output for that file even though snippet mode is active.\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip src --snippet TODO", Right: "Blocks around each TODO"},
		{Left: `catclip . --snippet "useState"`, Right: "React hook call-sites only"},
	})
	fmt.Fprintf(&b, "  Output: %s\n\n", dim(`<file path="..." snippet="42-57">...block...</file>`))

	fmt.Fprintf(&b, "%s\n", bold("--recent (filesystem recency):"))
	b.WriteString("  Sorts the current file set by modification time (newest first).\n")
	b.WriteString("  Bare --recent keeps all files and changes payload order only.\n")
	b.WriteString("  --recent N keeps only the top N newest files after earlier stages.\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip --recent", Right: "All files, emitted newest-first"},
		{Left: "catclip src --recent 5", Right: "5 newest files under src"},
		{Left: `catclip src --only "*.ts" --recent 5`, Right: "Newest 5 after filtering to .ts"},
		{Left: `catclip src --recent 5 --then docs --recent 5`, Right: "Recent is evaluated per scope, then scopes union in order"},
	})
	fmt.Fprintf(&b, "  %s\n", dim(`Order matters: --recent 10 --only "*.ts" means "take the 10 newest files, then keep the .ts ones"; --only "*.ts" --recent 10 means "keep .ts first, then take the 10 newest of that set".`))
	fmt.Fprintf(&b, "  %s\n", dim(`In an interactive terminal, a bare trailing --recent opens a recent picker with [sort all by recent] plus numeric rows labeled with date text such as "Today at 9:30 AM" and "Mar 28 at 8:15 AM".`))
	fmt.Fprintf(&b, "  %s\n", dim("With --then, each scope behaves like a separate catclip command: recent ordering happens inside that scope before the final union."))
	fmt.Fprintf(&b, "  %s\n", dim("When overlapping scopes produce the same path, the first scope keeps that file's final position."))
	fmt.Fprintf(&b, "  %s\n\n", dim("Ties are broken by relative path so outputs stay deterministic across filesystems."))

	fmt.Fprintf(&b, "%s\n", bold("--staged / --unstaged / --untracked (git filters):"))
	b.WriteString("  Composable alternatives to --changed (which is shorthand for all three).\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "--staged", Right: "Files in the git index (staged for commit)"},
		{Left: "--unstaged", Right: "Tracked modifications in working tree"},
		{Left: "--untracked", Right: "New files not yet tracked by git"},
	})
	fmt.Fprintf(&b, "  %s\n", dim("Interactive git pickers show rows like [all changed files] / [all staged files]. Keeping the whole set stays plain instead of appending --only."))
	fmt.Fprintf(&b, "  %s\n\n", "  These flags imply --changed; they can be combined:")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip src --staged --untracked", Right: "Staged + new, skip WIP edits"},
	})
	b.WriteByte('\n')

	fmt.Fprintf(&b, "%s\n", bold("*-diff (unified diff output):"))
	b.WriteString("  Diff output is requested directly through --changed-diff, --staged-diff,\n")
	b.WriteString("  or --unstaged-diff.\n")
	b.WriteString("  Those commands emit unified git diff instead of full file contents.\n")
	b.WriteString("  Changed diff may still include untracked files as full content (type=\"untracked\").\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip --changed-diff", Right: "All modified files as patches"},
		{Left: "catclip --staged-diff", Right: "Staged changes only — ideal for commit review"},
		{Left: "catclip --unstaged-diff", Right: "WIP edits — what you're actively changing"},
	})
	fmt.Fprintf(&b, "  Output types: %s %s %s %s\n\n", dim(`type="staged-diff"`), dim(`type="unstaged-diff"`), dim(`type="diff"`), dim(`type="untracked"`))

	fmt.Fprintf(&b, "%s %s for headless stdout output (no prompts, no stderr hints, no clipboard writes)\n", bold("Machine mode:"), cmd("-q -p"))
	fmt.Fprintf(&b, "%s\n", dim("In normal copy runs, -q already behaves like a non-interactive yes-all and does not start tree rendering."))
	fmt.Fprintf(&b, "%s\n\n", dim("Exception: with --preview, -t still matters because preview mode can render the tree even when -q is set."))

	fmt.Fprintf(&b, "%s\n", bold("Evaluation Order:"))
	fmt.Fprintf(&b, "  %s\n", dim("Per scope:"))
	for i, line := range []string{
		"Load .hiss and resolve scope targets",
		"Discover candidate files, applying binary exclusion and text classification during discovery",
		"Apply per-scope stages left to right, in the order written",
		"Choose output mode (full file, snippet, or diff)",
	} {
		fmt.Fprintf(&b, "  %s %s\n", dim(fmt.Sprintf("%d.", i+1)), line)
	}
	fmt.Fprintf(&b, "\n  %s\n", dim("After all scopes:"))
	for i, line := range []string{
		"Merge and dedupe the final file set",
		"Build the tree and summary from that final selected file set when tree output is enabled",
		"Emit output",
	} {
		fmt.Fprintf(&b, "  %s %s\n", dim(fmt.Sprintf("%d.", i+5)), line)
	}
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "%s\n", bold("Output Format:"))
	fmt.Fprintf(&b, "  Each file is wrapped in %s\n\n", dim(`<file path="path/to/file">`))

	fmt.Fprintf(&b, "%s\n", errText("Not Allowed:"))
	writeAlignedHelpRows(&b, "  ", dim, []helpRow{
		{Left: "catclip ../parent", Right: "Cannot go above working directory"},
		{Left: "catclip /abs/path", Right: "Absolute paths not allowed"},
	})
	b.WriteByte('\n')

	fmt.Fprintf(&b, "%s %s  %s\n", bold("Config:"), dim(displayPath(globalHissPath())), dim("(catclip --hiss to edit)"))
	return b.String()
}

func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(p, home+string(filepath.Separator))
	}
	return p
}

func detectPlatform() string {
	procVersion := ""
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/version"); err == nil {
			procVersion = string(data)
		}
	}
	return detectPlatformForGOOS(runtime.GOOS, procVersion)
}

func detectPlatformForGOOS(goos, procVersion string) string {
	switch goos {
	case "darwin":
		return "macos"
	case "linux":
		if isWSLProcVersion(procVersion) {
			return "wsl"
		}
		return "linux"
	case "windows":
		return "windows"
	default:
		return goos
	}
}

func isWSLProcVersion(procVersion string) bool {
	version := strings.ToLower(procVersion)
	return strings.Contains(version, "microsoft") || strings.Contains(version, "wsl")
}

func loadVersion() string {
	const fallback = "dev"

	candidates := []string{"VERSION"}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "VERSION"))
	}
	for _, dir := range executableCandidateDirs() {
		candidates = append(candidates,
			filepath.Join(dir, "VERSION"),
			filepath.Join(dir, "..", "share", "catclip", "VERSION"),
		)
	}

	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		version := strings.TrimSpace(string(data))
		if version != "" {
			return version
		}
	}

	return fallback
}
