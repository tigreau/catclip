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
	bad := func(s string) string { return colors.Err + s + colors.Reset }
	flag := func(s string) string { return colors.Prompt + s + colors.Reset }

	fmt.Fprintf(&b, "%scatclip v%s — Recursively copy code context for AI prompts%s\n\n", colors.Bold, version, colors.Reset)
	b.WriteString("Usage:  catclip [target ...] [option ...] [filter ...]\n\n")

	fmt.Fprintf(&b, "%s\n", bold("Quick Start:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip", Right: "Pick files or folders from a menu"},
		{Left: "catclip src", Right: "Copy a folder"},
		{Left: "catclip Button.tsx", Right: "Copy a file (finds it for you)"},
		{Left: "catclip btn", Right: "Fuzzy match — finds Button.tsx for you"},
		{Left: "catclip src lib docs", Right: "Copy multiple folders"},
		{Left: `catclip "*.go"`, Right: "All .go files in the project (glob pattern)"},
		{Left: `catclip src "*.go"`, Right: "Union: src/ files + all .go files"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Interactive mode (build commands from menus):"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip --", Right: "Pick files or folders, then choose filters from menus"},
		{Left: "catclip src --", Right: "Pick filters for src from a menu"},
		{Left: "catclip src -- --", Right: "Chain menus to build a full command"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Filtering:"))
	fmt.Fprintf(&b, "  Filters run left to right. Changing the order changes the result.\n\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: `catclip src --only "*.ts"`, Right: "Only TypeScript files"},
		{Left: `catclip src --exclude "*.css"`, Right: "Skip CSS files"},
		{Left: "catclip src --recent 3", Right: "Keep the 3 most recently modified files"},
		{Left: "catclip src --depth 2", Right: "Keep files up to path depth 2"},
		{Left: "catclip . --depth 1", Right: "Copy only the files in the project root"},
		{Left: "catclip src --paths", Right: "Emit bare relative paths, not file bodies"},
		{Left: "catclip . --paths --then src", Right: "Show repo structure, then copy full files from src"},
		{Left: "catclip src --contains TODO", Right: "Find files containing specific text"},
		{Left: "catclip src --snippet TODO", Right: "Only the matching blocks, not full files"},
		{Left: "catclip src --snippet TODO 3", Right: "Matching lines plus 3 lines around each match"},
		{Left: "catclip src --lines", Right: "Add line numbers to file output"},
		{Left: "catclip src --lines 400 450", Right: "Read lines 400-450 with line numbers"},
	})

	fmt.Fprintf(&b, "\n  You can give %s, %s, and %s more than one value.\n", flag("--only"), flag("--exclude"), flag("--include"))
	fmt.Fprintf(&b, "  Examples: %s   %s\n", flag(`--only "*.ts" "*.tsx"`), flag(`--exclude "*.css" "*.scss"`))
	fmt.Fprintf(&b, "\n  These two commands are different:\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: `catclip src --recent 10 --only "*.ts"`, Right: "Take the 10 newest files, then keep the .ts ones"},
		{Left: `catclip src --only "*.ts" --recent 10`, Right: "Keep .ts first, then take the 10 newest of that set"},
	})
	fmt.Fprintf(&b, "\n  One combined example:\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --contains "Button" --only "*.tsx" --exclude "Header.tsx" "Login.tsx" --recent 5`))
	fmt.Fprintf(&b, "    Start with files under src.\n")
	fmt.Fprintf(&b, "    Keep only the ones that mention \"Button\".\n")
	fmt.Fprintf(&b, "    From those, keep only .tsx files.\n")
	fmt.Fprintf(&b, "    Then remove Header.tsx and Login.tsx.\n")
	fmt.Fprintf(&b, "    Finally, take the 5 most recently modified files left.\n\n")

	fmt.Fprintf(&b, "\n%s\n", bold("Git Filters (requires a git repo):"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip src --changed", Right: "Only files changed in git"},
		{Left: "catclip --changed-diff", Right: "Show changes as patches instead of full files"},
	})
	fmt.Fprintf(&b, "  Other git filters: %s, %s, %s, %s, %s.\n", flag("--staged"), flag("--unstaged"), flag("--untracked"), flag("--staged-diff"), flag("--unstaged-diff"))

	fmt.Fprintf(&b, "\n%s\n", bold("--then (chain another catclip command):"))
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --only "*.ts" --then docs --recent 5`))
	fmt.Fprintf(&b, "    Keeps only the TS files from src, then adds the 5 most recent files from docs.\n")
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip . --paths --then src`))
	fmt.Fprintf(&b, "    First emits the repo file structure as paths, then adds full file bodies from src.\n")
	fmt.Fprintf(&b, "    Useful when an AI should see the whole structure but only read source files from src.\n")
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", bad(`catclip src docs --only "*.ts"`))
	fmt.Fprintf(&b, "    Bad here because it would also throw away every non-TS file in docs.\n")
	fmt.Fprintf(&b, "    Use %s when the next target should use different filters or output shape.\n", flag("--then"))

	fmt.Fprintf(&b, "\n%s\n", bold("Ignored Files:"))
	fmt.Fprintf(&b, "  catclip skips .gitignored paths and paths matched by %s (catclip's own ignore rules, on top of .gitignore).\n", flag(".hiss"))
	fmt.Fprintf(&b, "\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip --include tests", Right: "Allow an ignored folder for this run"},
		{Left: "catclip --hiss", Right: fmt.Sprintf("Edit catclip's ignore rules (%s)", flag(displayPath(globalHissPath())))},
	})
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s only adds ignored files found inside your targets.\n", flag("--include"))
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip . --include build --only src build`))
	fmt.Fprintf(&b, "    Works — '.' covers the whole project, %s keeps just src and build.\n", flag("--only"))
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", bad(`catclip src --include build`))
	fmt.Fprintf(&b, "    Doesn't find build/ because it's outside src/.\n")

	fmt.Fprintf(&b, "\n%s\n", bold("Piping:"))
	fmt.Fprintf(&b, "  Use %s, %s, or %s to read exact relative paths from stdin.\n", flag("--only -"), flag("--exclude -"), flag("--include -"))
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`git diff --name-only main | catclip . --only -`))
	fmt.Fprintf(&b, "    Copy every file that differs from the main branch.\n")
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip . --contains TODO --paths -p | catclip . --exclude -`))
	fmt.Fprintf(&b, "    Copy the project but skip any file that contains \"TODO\".\n")
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --paths -p | xargs vim`))
	fmt.Fprintf(&b, "    Open the matching source files in vim for editing (works with %s too).\n", cmd(`nano`))
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src -p > snapshot.txt`))
	fmt.Fprintf(&b, "    Save the output to a file instead of copying it to the clipboard.\n")

	fmt.Fprintf(&b, "\n%s\n", bold("Options:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "--preview", Right: "Size up files before reading them"},
		{Left: "-p, --print", Right: "Print to terminal instead of clipboard"},
		{Left: "-r, --raw", Right: "Bare file body — no wrappers, no line numbers"},
		{Left: "-q, --quiet", Right: "No prompts, decorations, or tree preview"},
		{Left: "--headless", Right: "Agent contract: stdout, quiet, no prompts (see --help-all)"},
		{Left: "-y, --yes", Right: "Skip confirmation for large copies"},
		{Left: "-t, --no-tree", Right: "Skip the file tree preview"},
		{Left: "--no-bundle", Right: "Force text clipboard; skip bundle for large output"},
		{Left: "-v, --verbose", Right: "Debug info and timings"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Tools:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "-h, --help", Right: "Show this help"},
		{Left: "--help-all", Right: "Agent reference manual (--headless contract, all flags)"},
		{Left: "--version", Right: "Show version"},
		{Left: "--hiss", Right: "Edit catclip's own ignore rules (applied on top of .gitignore)"},
		{Left: "--hiss-reset", Right: "Reset catclip's ignore rules to defaults"},
		{Left: "--all-ignore-rules", Right: "List every ignore rule in effect — your .gitignore(s) + .hiss, merged"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("For agents and full flag reference: catclip --help-all"))

	return b.String()
}

func fullHelpText(version string, colors colorPalette) string {
	var b strings.Builder

	fmt.Fprintf(&b, "catclip v%s — Agent Reference\n\n", version)
	b.WriteString("════════════════════════════════════════════════════════════════════\n")
	b.WriteString(" FOR AGENTS: ALWAYS PASS --headless\n")
	b.WriteString("════════════════════════════════════════════════════════════════════\n\n")
	b.WriteString("  --headless is the agent contract. It guarantees:\n")
	b.WriteString("    • stdout = payload, stderr = errors only (no decorations, no prompts)\n")
	b.WriteString("    • rejects ambiguous invocations (bare --, fuzzy ambiguity, token warns)\n")
	b.WriteString("    • requires explicit targets (no implicit `.` — pass `.` if you mean cwd)\n\n")
	b.WriteString("  Every example in this reference assumes --headless even if not shown.\n")
	b.WriteString("  Use it on every invocation from an agent or script.\n\n")
	b.WriteString("  Modifiers are scope-local and left-to-right, resetting at each --then.\n")
	b.WriteString("  Do not assume all flags are global — read this reference fully before\n")
	b.WriteString("  generating commands.\n\n")
	b.WriteString("════════════════════════════════════════════════════════════════════\n\n")

	// ── Operations ──────────────────────────────────────────────────────
	b.WriteString("OPERATIONS\n\n")
	b.WriteString("  List files:        catclip TARGET --paths\n")
	b.WriteString("  List all files:    catclip TARGET --include '*' --with-binaries --paths\n")
	b.WriteString("  Search files:      catclip TARGET --contains 'REGEX' --paths\n")
	b.WriteString("  Search blocks:     catclip TARGET --snippet 'REGEX'\n")
	b.WriteString("  Search context:    catclip TARGET --snippet 'REGEX' 3\n")
	b.WriteString("  Read files:        catclip TARGET\n")
	b.WriteString("  Read one raw:      catclip FILE -r\n")
	b.WriteString("  Read with lines:   catclip FILE --lines\n")
	b.WriteString("  Read line range:   catclip FILE --lines 400 450\n")
	b.WriteString("  Copy file content: catclip FILE -r > dest    (preserves exact bytes)\n")
	b.WriteString("  Read git changes:  catclip TARGET --changed-diff\n")
	b.WriteString("  Preview cost:      catclip TARGET --preview\n\n")
	b.WriteString("  Start with --paths to orient before searching or reading:\n")
	b.WriteString("    catclip . --paths                   # see what's in the project\n")
	b.WriteString("    catclip . --depth 2 --paths         # top-level structure only\n")
	b.WriteString("  Then narrow with --contains, --snippet, or full reads on specific targets.\n\n")
	b.WriteString("  --contains --paths is like ripgrep --files-with-matches (which files match?).\n")
	b.WriteString("  --snippet is like ripgrep with context (which blocks match?). By default it\n")
	b.WriteString("  returns blank-line-bounded blocks; add N for fixed +/- N line context.\n")
	b.WriteString("  Default output (no --paths/--snippet) returns full file contents.\n\n")
	b.WriteString("  --preview sizes up files before you read them: a per-file table of size, tokens,\n")
	b.WriteString("  git status, modified date, and shape — no contents, nothing copied. Use it to\n")
	b.WriteString("  spend context deliberately — read small files whole, snippet the large ones, skip\n")
	b.WriteString("  the rest — rather than reading blind. The # header labels the columns.\n\n")
	b.WriteString("  catclip replaces find + grep + cat pipelines with a single command.\n")
	b.WriteString("  One process handles discovery, filtering, content matching, and output —\n")
	b.WriteString("  no per-file fork overhead. Faster than per-file cat loops on large codebases.\n")
	b.WriteString("  Bundles its own ripgrep — no external dependency needed.\n\n")

	// ── Targeting ───────────────────────────────────────────────────────
	b.WriteString("TARGETING (where)\n\n")
	b.WriteString("  Navigate by path. Targets are relative paths from the current working directory.\n\n")
	b.WriteString("  catclip src                          All text files under src/\n")
	b.WriteString("  catclip src/components               Narrow to a subdirectory\n")
	b.WriteString("  catclip src/components/Button.tsx    One specific file\n")
	b.WriteString("  catclip src lib docs                 Multiple targets in one scope\n\n")
	b.WriteString("  Glob patterns are also valid targets:\n")
	b.WriteString("  catclip '*.go'                       All .go files in the project\n")
	b.WriteString("  catclip '*.go' '*.ts'                All .go and .ts files (union)\n")
	b.WriteString("  catclip src '*.go'                   src/ files + all .go files (union)\n\n")
	b.WriteString("  Glob targets match against all visible files in the project, not scoped to\n")
	b.WriteString("  sibling path targets. Modifiers apply to the full combined set:\n")
	b.WriteString("  catclip src '*.go' --exclude '*_test.go' --recent 5\n\n")
	b.WriteString("  To narrow to a subdirectory, use it as the target.\n")
	b.WriteString("  Do not use --only with path prefixes for navigation.\n\n")
	b.WriteString("  Absolute paths and paths above cwd (../) are not allowed.\n\n")

	// ── Filtering ───────────────────────────────────────────────────────
	b.WriteString("FILTERING (what kind of files)\n\n")
	b.WriteString("  --only and --exclude filter by filename or path subtree.\n")
	b.WriteString("  They are not for path navigation — use targets for that.\n\n")
	b.WriteString("  Globs match filenames:\n")
	b.WriteString("  --only \"*.ts\"                Keep only .ts files\n")
	b.WriteString("  --only \"*.ts\" \"*.tsx\"         Keep .ts and .tsx (values OR together)\n")
	b.WriteString("  --exclude \"*.test.*\"          Remove test files\n")
	b.WriteString("  --exclude \"*.css\" \"*.svg\"     Remove CSS and SVG files\n\n")
	b.WriteString("  Trailing slash matches path subtrees:\n")
	b.WriteString("  --only docs/                 Keep only files under docs/ directories\n")
	b.WriteString("  --exclude build/              Remove files under build/ directories\n\n")
	b.WriteString("  Bare names match directory segments:\n")
	b.WriteString("  --exclude tests              Remove files in any tests/ directory\n\n")
	b.WriteString("  --only and --exclude use shell globs (*, ?, [...]).\n")
	b.WriteString("  --contains and --snippet use PCRE2 regex (supports lookaround,\n")
	b.WriteString("  backreferences, atomic groups, named captures).\n")
	b.WriteString("  Patterns use smart-case matching. If your pattern is all lowercase,\n")
	b.WriteString("  matching is case-insensitive. If your pattern contains any uppercase\n")
	b.WriteString("  letter, matching is case-sensitive.\n\n")
	b.WriteString("    --contains 'config'      matches Config, config, CONFIG (all lowercase → insensitive)\n")
	b.WriteString("    --contains 'Config'      matches only Config            (uppercase C → sensitive)\n")
	b.WriteString("    --contains 'TODO'        matches only TODO              (uppercase → sensitive)\n")
	b.WriteString("    --contains 'todo'        matches TODO, Todo, todo       (all lowercase → insensitive)\n\n")
	b.WriteString("  This matches ripgrep's --smart-case behavior. The (?i) inline flag\n")
	b.WriteString("  still works if you need to force case-insensitive on a mixed-case\n")
	b.WriteString("  pattern: --contains '(?i)handleClick' matches HandleClick, handleclick, etc.\n\n")

	// ── Narrowing ───────────────────────────────────────────────────────
	b.WriteString("NARROWING (which ones)\n\n")
	b.WriteString("  --contains 'REGEX'    Keep files whose contents match the regex\n")
	b.WriteString("  --changed             Any git-modified file\n")
	b.WriteString("  --staged              Files in the git index\n")
	b.WriteString("  --unstaged            Tracked files with working-tree changes\n")
	b.WriteString("  --untracked           New files not yet tracked\n")
	b.WriteString("  --recent N            Keep N most recently modified files\n")
	b.WriteString("  --recent              Sort all files newest-first (no limit)\n")
	b.WriteString("  --depth N             Keep files at path depth N or shallower (from cwd)\n")
	b.WriteString("                        (README.md = 1, src/main.ts = 2, src/lib/util.ts = 3)\n\n")

	// ── Output shape ────────────────────────────────────────────────────
	b.WriteString("OUTPUT SHAPE (what to emit)\n\n")
	b.WriteString("  (default)             Full file contents in <file> wrappers\n")
	b.WriteString("  --paths               Bare relative paths, one per line\n")
	b.WriteString("  --snippet 'REGEX'     Only blank-line-bounded blocks matching regex\n")
	b.WriteString("  --snippet 'REGEX' N   Matching lines plus N lines before and after\n")
	b.WriteString("  --changed-diff        All changed files as unified diff patches\n")
	b.WriteString("  --staged-diff         Staged changes as unified diff\n")
	b.WriteString("  --unstaged-diff       Unstaged changes as unified diff\n")
	b.WriteString("  --lines               Add line numbers to output\n")
	b.WriteString("  --lines START         Line numbers from START to end of file\n")
	b.WriteString("  --lines START END     Line numbers for lines START through END\n")
	b.WriteString("  -r, --raw             Bare file body — no wrappers, no line numbers\n\n")
	b.WriteString("  --raw emits raw bytes — no <file> tags, no `cat -n`-style line numbers.\n")
	b.WriteString("  Multi-file raw concatenates contiguously with no separator (same as\n")
	b.WriteString("  `cat a b`). With --lines, raw drops the line numbers because numbered-\n")
	b.WriteString("  but-unwrapped output is the worst of both modes: not parseable for edits\n")
	b.WriteString("  (no file path) and not pipeable (numbers corrupt downstream tools). For\n")
	b.WriteString("  numbered, edit-targetable output, drop -r and use the wrapped form.\n\n")
	b.WriteString("  --snippet returns blank-line-bounded blocks around regex matches by default.\n")
	b.WriteString("  Add a context number for fixed rg/grep-style line windows: N=0 emits only\n")
	b.WriteString("  matching lines; N=3 emits each match plus three lines before and after.\n")
	b.WriteString("  Already focused — head/tail piping is usually unnecessary.\n\n")
	b.WriteString("  --lines START END reads a file slice directly. Use it instead of:\n")
	b.WriteString("    catclip FILE -r | sed -n '400,450p'\n")
	b.WriteString("    catclip FILE | head -450 | tail -50\n")
	b.WriteString("  The built-in slice is faster, numbered, and keeps file wrappers.\n\n")
	b.WriteString("  Diff modifiers already filter to their change set — no need to double-filter:\n")
	b.WriteString("    catclip . --only '*.go' --unstaged-diff             # correct\n")
	b.WriteString("    catclip . --unstaged --only '*.go' --unstaged-diff  # redundant --unstaged\n\n")

	// ── Pipeline model ──────────────────────────────────────────────────
	b.WriteString("PIPELINE MODEL\n\n")
	b.WriteString("  Stages run left to right. Each stage receives only what the previous stage kept.\n")
	b.WriteString("  The set can only shrink (except --include, which adds authorized ignored files).\n\n")
	b.WriteString("  [all discovered files under TARGET]\n")
	b.WriteString("    → --include PATH      adds authorized ignored files (must be first, once per scope)\n")
	b.WriteString("    → --only PATTERN      keeps files matching PATTERN; discards rest\n")
	b.WriteString("    → --exclude PATTERN   removes files matching PATTERN; keeps rest\n")
	b.WriteString("    → --recent N          sorts by mtime, keeps top N\n")
	b.WriteString("    → --depth N           removes files deeper than N segments\n")
	b.WriteString("    → --contains REGEX    removes files whose contents don't match\n")
	b.WriteString("    → --changed           removes files not changed in git\n")
	b.WriteString("    → output shape        --paths | --snippet REGEX | --*-diff\n\n")
	b.WriteString("  --include must be the first modifier so that all filters apply to the full set.\n")
	b.WriteString("  The same modifier can appear multiple times (except --include). Each occurrence\n")
	b.WriteString("  is a separate step.\n\n")
	b.WriteString("    catclip src --only \"*.ts\" --exclude \"*test*\" --recent 5\n")
	b.WriteString("    # 1. keep .ts files  2. remove *test*  3. keep 5 newest survivors\n\n")
	b.WriteString("  Order matters because the input set differs at each step:\n")
	b.WriteString("    --recent 10 --only \"*.ts\"    take 10 newest, then keep .ts ones\n")
	b.WriteString("    --only \"*.ts\" --recent 10    keep .ts first, then take 10 newest of those\n\n")
	b.WriteString("  Ordering constraints after output-shape modifiers:\n\n")
	b.WriteString("    --paths             nothing can follow (terminal)\n")
	b.WriteString("    --snippet REGEX [N] no --contains after (already filtered by content)\n")
	b.WriteString("    --*-diff            no --contains or git filters after (diff owns both)\n\n")
	b.WriteString("  Output modes cannot repeat or combine (--paths --snippet is an error).\n\n")

	// ── Scopes ──────────────────────────────────────────────────────────
	b.WriteString("SCOPES (--then)\n\n")
	b.WriteString("  --then starts a fresh scope with independent targets and modifiers.\n")
	b.WriteString("  Like running two catclip commands and unioning the results.\n\n")
	b.WriteString("  catclip src --only \"*.ts\" --then docs --recent 5\n")
	b.WriteString("  # Scope 1: .ts files under src  |  Scope 2: 5 newest under docs\n\n")
	b.WriteString("  catclip . --paths --then src\n")
	b.WriteString("  # Scope 1: full repo listing as paths  |  Scope 2: full file bodies from src\n\n")
	b.WriteString("  Without --then, all targets share the same modifiers.\n")
	b.WriteString("  Overlapping scopes are deduplicated by path in output.\n\n")

	// ── Authorization ───────────────────────────────────────────────────
	b.WriteString("AUTHORIZATION (--include)\n\n")
	b.WriteString("  catclip only discovers files visible to git (.gitignore) and its own .hiss config.\n")
	b.WriteString("  Ignored paths require explicit --include authorization.\n\n")
	b.WriteString("  There are two modes:\n\n")
	b.WriteString("  --include PATH    Authorize a specific ignored path.\n")
	b.WriteString("                    Names an ignored directory or file relative to cwd.\n")
	b.WriteString("                    Including a directory authorizes all descendants under it.\n")
	b.WriteString("                    Including a parent authorizes descendant targets:\n")
	b.WriteString("                      catclip blocked/sub --include blocked    (parent authorizes child)\n\n")
	b.WriteString("  --include '*'     Disable all ignore rules — discover everything.\n")
	b.WriteString("                    Equivalent to ripgrep's --no-ignore.\n")
	b.WriteString("                    Authorizes any target, even if gitignored.\n")
	b.WriteString("                    Pair with --paths for inventory; avoid combining with\n")
	b.WriteString("                    body emit on uncurated repos. On a project with a full\n")
	b.WriteString("                    node_modules/build tree this can mean megabytes of\n")
	b.WriteString("                    payload and thousands of files, exceeding context\n")
	b.WriteString("                    budgets or stalling the agent.\n\n")
	b.WriteString("  Rules:\n")
	b.WriteString("    1. --include must be the first modifier in a scope (before --only, --exclude, etc.)\n")
	b.WriteString("    2. Only one --include per scope — use --then for additional includes\n")
	b.WriteString("    3. --include is scoped to the target paths (not the whole project)\n\n")
	b.WriteString("  Examples:\n")
	b.WriteString("  catclip blocked-dir --include blocked-dir --paths\n")
	b.WriteString("  catclip blocked-dir/src --include blocked-dir --paths\n")
	b.WriteString("  catclip .env.local --include .env.local -r\n")
	b.WriteString("  catclip src --include '*' --paths    # all files under src/, nothing ignored\n\n")
	b.WriteString("  Cross-scope includes use --then:\n")
	b.WriteString("    catclip src --then node_modules --include node_modules --paths\n\n")
	fmt.Fprintf(&b, "  catclip's own ignore rules: %s (--hiss to edit; applied on top of .gitignore)\n\n", displayPath(globalHissPath()))

	// ── Composition ─────────────────────────────────────────────────────
	b.WriteString("COMPOSITION (stdin piping)\n\n")
	b.WriteString("  catclip has no content negation. To exclude files by content, use a two-pass pipe:\n")
	b.WriteString("  first pass finds matches with --contains --paths, second pass excludes them with --exclude -.\n\n")
	b.WriteString("  --only -, --exclude -, and --include - read exact relative paths from stdin.\n\n")
	b.WriteString("  # Exclude files containing a pattern:\n")
	b.WriteString("  catclip src --contains 'generated' --paths --headless | catclip src --exclude - --headless\n\n")
	b.WriteString("  # Keep only files containing a pattern:\n")
	b.WriteString("  catclip src --contains TODO --paths --headless | catclip src --only - --headless\n\n")
	b.WriteString("  # Compound: Go files not containing 'test', 5 most recent, function snippets:\n")
	b.WriteString("  catclip . --only '*.go' --contains test --paths --headless \\\n")
	b.WriteString("    | catclip . --only '*.go' --recent 5 --snippet func --exclude - --headless\n\n")

	// ── Unix tool integration ──────────────────────────────────────────
	b.WriteString("UNIX TOOL INTEGRATION\n\n")
	b.WriteString("  --paths --headless produces one path per line, compatible with xargs and shell loops.\n\n")
	b.WriteString("  # Line counts per file:\n")
	b.WriteString("  catclip src --only '*.go' --paths --headless | xargs wc -l | sort -rn\n\n")
	b.WriteString("  # Grep across selected files:\n")
	b.WriteString("  catclip src --only '*.ts' --paths --headless | xargs grep -n 'pattern'\n\n")
	b.WriteString("  # Git diff stats on changed files:\n")
	b.WriteString("  catclip . --changed --paths --headless | xargs git diff --stat --\n\n")
	b.WriteString("  # Bulk find-and-replace with sed:\n")
	b.WriteString("  catclip src --only '*.ts' --contains 'oldName' --paths --headless | xargs sed -i '' 's/oldName/newName/g'\n\n")
	b.WriteString("  # Open matched files in vim:\n")
	b.WriteString("  vim $(catclip src --contains TODO --paths --headless)\n\n")
	b.WriteString("  # Read a specific line range from a large file:\n")
	b.WriteString("  catclip FILE --lines 400 450 --headless        # built-in, with line numbers\n")
	b.WriteString("  catclip FILE -r --headless | sed -n '400,450p' # via sed, no line numbers\n\n")
	b.WriteString("  # Skip a license/header at the top of a file (read body only):\n")
	b.WriteString("  catclip FILE --snippet '(?i)copyright' --headless   # e.g. returns <file ... lines=\"1-4\">\n")
	b.WriteString("  catclip FILE --lines 5 --headless                   # read from line 5 (one past the license end)\n\n")
	b.WriteString("  # File count and payload size:\n")
	b.WriteString("  catclip src --only '*.go' --paths --headless | wc -l\n")
	b.WriteString("  catclip src --only '*.go' --headless | wc -c\n\n")

	// ── Output format ───────────────────────────────────────────────────
	b.WriteString("OUTPUT FORMAT\n\n")
	b.WriteString("  Full files (default):\n")
	b.WriteString("    <file path=\"src/main.ts\">\n")
	b.WriteString("    ...file contents...\n")
	b.WriteString("    </file>\n\n")
	b.WriteString("  Snippets (--snippet REGEX [N]):\n")
	b.WriteString("    <file path=\"src/main.ts\" lines=\"42-57\">\n")
	b.WriteString("    ...matched block...\n")
	b.WriteString("    </file>\n\n")
	b.WriteString("    With N set, lines=\"...\" is the fixed context window around the match.\n\n")
	b.WriteString("  Lines (--lines):\n")
	b.WriteString("    <file path=\"src/main.ts\">\n")
	b.WriteString("         1\timport express from 'express';\n")
	b.WriteString("         2\t...\n")
	b.WriteString("    </file>\n\n")
	b.WriteString("  Lines range (--lines 42 57):\n")
	b.WriteString("    <file path=\"src/main.ts\" lines=\"42-57\">\n")
	b.WriteString("        42\t  const app = express();\n")
	b.WriteString("        43\t  ...\n")
	b.WriteString("    </file>\n\n")
	b.WriteString("  Diff (--changed-diff, --staged-diff, --unstaged-diff):\n")
	b.WriteString("    <file path=\"src/main.ts\" type=\"diff\">\n")
	b.WriteString("    ...unified diff...\n")
	b.WriteString("    </file>\n\n")
	b.WriteString("    <file path=\"new-file.ts\" type=\"untracked\">\n")
	b.WriteString("    ...full content for untracked files in changed-diff...\n")
	b.WriteString("    </file>\n\n")
	b.WriteString("  Paths (--paths):\n")
	b.WriteString("    src/main.ts\n")
	b.WriteString("    src/utils.ts\n")
	b.WriteString("    (bare relative paths, one per line)\n\n")
	b.WriteString("  Raw (-r with -p):\n")
	b.WriteString("    ...file body without any wrapper tags...\n")
	b.WriteString("    (requires exactly one surviving full-file item)\n\n")
	b.WriteString("  Diff type attributes: type=\"diff\", type=\"staged-diff\", type=\"unstaged-diff\", type=\"untracked\"\n\n")

	// ── Clipboard delivery ──────────────────────────────────────────────
	b.WriteString("CLIPBOARD DELIVERY\n\n")
	b.WriteString("  catclip auto-selects the clipboard delivery mode based on payload size:\n\n")
	b.WriteString("    < 4096 bytes   text clipboard (pbcopy / xclip / wl-copy / clip.exe)\n")
	b.WriteString("                   pastes anywhere — terminals, editors, web UIs, etc.\n")
	b.WriteString("    ≥ 4096 bytes   bundle file at {Documents}/catclip/{project}-{HHMMSS}.txt\n")
	b.WriteString("                   placed on the clipboard as a file reference; pastes as\n")
	b.WriteString("                   an attachment in web UIs (Claude, ChatGPT, etc.) and\n")
	b.WriteString("                   as a file in file managers (Finder, Explorer). Does NOT\n")
	b.WriteString("                   paste as text in terminals or editors.\n\n")
	b.WriteString("  --no-bundle      Force text clipboard regardless of size. Use when you\n")
	b.WriteString("                   want to paste raw text into a terminal or editor and\n")
	b.WriteString("                   the output would otherwise exceed 4KB.\n\n")
	b.WriteString("  Bundle file contents are byte-identical to -p (stdout) output.\n")
	b.WriteString("  CATCLIP_BUNDLE_DIR overrides the bundle directory.\n")
	b.WriteString("  --headless implies stdout, so bundling never applies in headless mode.\n\n")

	// ── Exit codes ──────────────────────────────────────────────────────
	b.WriteString("EXIT CODES\n\n")
	b.WriteString("  0    Success — all targets resolved, output was emitted\n")
	b.WriteString("  1    Partial or no results — one or more targets not found, or runtime error\n")
	b.WriteString("  2    Usage error — invalid flags, arguments, or validation failure\n\n")
	b.WriteString("  When some targets resolve and others don't, catclip emits output for the\n")
	b.WriteString("  resolved targets, warns on stderr (even with -q), and exits 1.\n")
	b.WriteString("  This matches cat/ls/grep behavior: process what you can, warn, exit non-zero.\n\n")

	// ── Common errors ───────────────────────────────────────────────────
	b.WriteString("COMMON ERRORS\n\n")
	b.WriteString("  \"No text files found matching your criteria.\"\n")
	b.WriteString("    → Target may be ignored. Add --include TARGET.\n")
	b.WriteString("    → Target may be empty or contain only binary files.\n")
	b.WriteString("    → Check for typos in the target path.\n\n")
	b.WriteString("  \"no files at depth N\"\n")
	b.WriteString("    → Depth counts path segments from cwd, not from the target.\n")
	b.WriteString("    → The error shows the actual depth range. Use the suggested value.\n\n")
	b.WriteString("  \"positional targets must come before modifiers.\"\n")
	b.WriteString("    → Move targets to the left of --only/--exclude/etc.\n")
	b.WriteString("    → Use --then for a new scope with different targets.\n\n")
	b.WriteString("  \"Warning: ... not found (scope N).\"\n")
	b.WriteString("    → Target could not be resolved. Output for other targets still emitted.\n")
	b.WriteString("    → Exit code is 1 (not 0) even when other targets succeed.\n")
	b.WriteString("    → Warnings print to stderr even with -q or --headless.\n\n")

	// ── Limitations ────────────────────────────────────────────────────
	b.WriteString("LIMITATIONS\n\n")
	b.WriteString("  By default catclip skips binary files and ignored paths.\n")
	b.WriteString("  Use --with-binaries to include binaries, --include '*' to include ignored paths,\n")
	b.WriteString("  or both for a complete inventory equivalent to find:\n\n")
	b.WriteString("    catclip TARGET --include '*' --with-binaries --paths --headless\n\n")
	b.WriteString("  Path semantics:\n")
	b.WriteString("    • Absolute paths are not accepted. Run catclip from the project root\n")
	b.WriteString("      and pass relative paths.\n")
	b.WriteString("    • Symlinks are not followed.\n")
	b.WriteString("    • Path case sensitivity follows the target filesystem/volume. On\n")
	b.WriteString("      case-insensitive filesystems (default macOS, default Windows), repos\n")
	b.WriteString("      with case-colliding paths in the git index may have git selectors\n")
	b.WriteString("      (--changed, --staged, etc.) miss entries whose index spelling differs\n")
	b.WriteString("      from the materialized path. Use Linux or a case-sensitive volume for\n")
	b.WriteString("      exact git selector behavior on those repos.\n\n")

	// ── Reference table ─────────────────────────────────────────────────
	b.WriteString("MODIFIER REFERENCE\n\n")
	b.WriteString("  Scope modifiers (per-scope, left to right):\n")
	b.WriteString("    --include VALUE...     Authorize ignored paths (must be first, once per scope)\n")
	b.WriteString("    --only VALUE...        Filename filter — keep matches (shell globs)\n")
	b.WriteString("    --exclude VALUE...     Filename filter — remove matches (shell globs)\n")
	b.WriteString("    --recent [N]           Sort by mtime; optional top-N\n")
	b.WriteString("    --depth N              Max path depth\n")
	b.WriteString("    --contains REGEX       Content filter\n")
	b.WriteString("    --snippet REGEX [N]    Extract matching blocks, or +/- N line context\n")
	b.WriteString("    --lines [START [END]]  Line numbers; optional range slice\n")
	b.WriteString("    --paths                Emit bare paths instead of file bodies\n")
	b.WriteString("    --changed              Git-modified files\n")
	b.WriteString("    --staged               Git index files\n")
	b.WriteString("    --unstaged             Tracked working-tree changes\n")
	b.WriteString("    --untracked            New untracked files\n")
	b.WriteString("    --changed-diff         Changed files as unified diff\n")
	b.WriteString("    --staged-diff          Staged changes as unified diff\n")
	b.WriteString("    --unstaged-diff        Unstaged changes as unified diff\n")
	b.WriteString("    --then                 Start a new scope\n\n")
	b.WriteString("  Global flags:\n")
	b.WriteString("    --headless             Agent contract: stdout output, quiet stderr, no prompts\n")
	b.WriteString("                           (rejects bare --, fuzzy ambiguity, token-warn prompt)\n")
	b.WriteString("    -q, --quiet            No prompts, decorations, or tree preview\n")
	b.WriteString("    -p, --print            Stdout instead of clipboard\n")
	b.WriteString("    -r, --raw              Bare file body, no wrappers or numbers\n")
	b.WriteString("    -y, --yes              Skip confirmation\n")
	b.WriteString("    -t, --no-tree          Skip tree preview\n")
	b.WriteString("    --no-bundle            Force text clipboard; skip bundle file for ≥4KB output\n")
	b.WriteString("    -v, --verbose          Debug info and timings\n")
	b.WriteString("    --preview              Size up files before reading them\n")
	b.WriteString("    --with-binaries        Include binary files in discovery\n")
	b.WriteString("    --hiss                 Edit catclip's own ignore rules (on top of .gitignore)\n")
	b.WriteString("    --hiss-reset           Reset catclip's ignore rules to defaults\n")
	b.WriteString("    --all-ignore-rules     List every ignore rule in effect — .gitignore(s) + .hiss, merged\n")

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
