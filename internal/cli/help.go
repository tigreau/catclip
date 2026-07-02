package cli

import (
	"fmt"
	"strings"

	"github.com/tigreau/catclip/internal/platform"
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

func ShortHelpText(version, hissDisplayPath string, colors platform.Palette) string {
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
		{Left: `catclip "internal/*.go"`, Right: "Only .go files directly inside internal/"},
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
		{Left: "catclip src --size 0 100", Right: "Keep files up to 100 KiB, largest first"},
		{Left: "catclip src --depth 2", Right: "Keep files up to path depth 2"},
		{Left: "catclip . --depth 1", Right: "Copy only the files in the project root"},
		{Left: "catclip src --paths", Right: "Emit bare relative paths, not file bodies"},
		{Left: "catclip . --paths --then src", Right: "Show repo structure, then copy full files from src"},
		{Left: "catclip src --contains TODO", Right: "Find files containing specific text"},
		{Left: "catclip src --not-contains TODO", Right: "Drop files containing TODO; keep the rest"},
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
		{Left: "catclip tests --include tests", Right: "Allow an ignored folder for this run"},
		{Left: "catclip --hiss", Right: fmt.Sprintf("Edit catclip's ignore rules (%s)", flag(hissDisplayPath))},
	})
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s authorizes gitignored paths within your walk scope.\n", flag("--include"))
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
		{Left: "--headless", Right: "Script mode: stdout, quiet, no prompts (see --help-all)"},
		{Left: "-y, --yes", Right: "Skip confirmation for large copies"},
		{Left: "-t, --no-tree", Right: "Skip the file tree preview"},
		{Left: "--no-bundle", Right: "Force text clipboard; skip bundle for large output"},
		{Left: "-v, --verbose", Right: "Debug info and timings"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Tools:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "-h, --help", Right: "Show this help"},
		{Left: "--help-all", Right: "Full reference manual (every flag, every rule)"},
		{Left: "--version", Right: "Show version"},
		{Left: "--hiss", Right: "Edit catclip's own ignore rules (applied on top of .gitignore)"},
		{Left: "--hiss-reset", Right: "Reset catclip's ignore rules to defaults"},
		{Left: "--all-ignore-rules", Right: "List every ignore rule in effect — your .gitignore(s) + .hiss, merged"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Full reference manual: catclip --help-all"))

	return b.String()
}

func FullHelpText(version, hissDisplayPath string, colors platform.Palette) string {
	var b strings.Builder
	cmd := func(s string) string { return colors.OK + s + colors.Reset }
	bold := func(s string) string { return colors.Bold + s + colors.Reset }
	bad := func(s string) string { return colors.Err + s + colors.Reset }
	flag := func(s string) string { return colors.Prompt + s + colors.Reset }
	head := func(s string) { fmt.Fprintf(&b, "%s\n\n", bold(s)) }

	fmt.Fprintf(&b, "%scatclip v%s — Reference Manual%s\n\n", colors.Bold, version, colors.Reset)
	fmt.Fprintf(&b, "Everything catclip does, in one place. For the short tour, run %s.\n\n", cmd("catclip --help"))
	fmt.Fprintf(&b, "One rule underpins the whole tool: modifiers are scope-local and run left\n")
	fmt.Fprintf(&b, "to right, resetting at each %s. Not every flag is global — the Modifier\n", flag("--then"))
	b.WriteString("Reference at the end lists which is which.\n\n")

	// ── Common tasks ────────────────────────────────────────────────────
	head("COMMON TASKS")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip TARGET --paths", Right: "List files"},
		{Left: "catclip TARGET --include '*' --with-binaries --paths", Right: "List everything, ignored and binary included"},
		{Left: "catclip TARGET --contains 'REGEX' --paths", Right: "Which files mention this?"},
		{Left: "catclip TARGET --not-contains 'REGEX'", Right: "Everything except files mentioning this"},
		{Left: "catclip TARGET --snippet 'REGEX'", Right: "Just the matching blocks"},
		{Left: "catclip TARGET --snippet 'REGEX' 3", Right: "Matches with 3 lines of context"},
		{Left: "catclip TARGET", Right: "Read full files"},
		{Left: "catclip FILE -r", Right: "One file, raw bytes"},
		{Left: "catclip FILE --lines", Right: "One file, numbered"},
		{Left: "catclip FILE --lines 400 450", Right: "A numbered slice"},
		{Left: "catclip FILE -r > dest", Right: "Copy a file byte-exact"},
		{Left: "catclip TARGET --changed-diff", Right: "Git changes as patches"},
		{Left: "catclip TARGET --preview", Right: "Size things up before reading"},
	})
	b.WriteString("\n")
	fmt.Fprintf(&b, "  A workflow that scales: start with %s to see what's there,\n", flag("--paths"))
	b.WriteString("  then narrow with content filters or read specific targets in full:\n\n")
	fmt.Fprintf(&b, "    %s                   # see what's in the project\n", cmd("catclip . --paths"))
	fmt.Fprintf(&b, "    %s         # top-level structure only\n\n", cmd("catclip . --depth 2 --paths"))
	fmt.Fprintf(&b, "  If you know ripgrep: %s is like rg --files-with-matches\n", flag("--contains --paths"))
	fmt.Fprintf(&b, "  (which files match?), and %s is like rg with context (which blocks\n", flag("--snippet"))
	b.WriteString("  match?). By default --snippet returns blank-line-bounded blocks; add N for\n")
	b.WriteString("  fixed +/- N line windows. With neither, you get full file contents.\n\n")
	fmt.Fprintf(&b, "  %s sizes up files before you read them: a per-file table of size,\n", flag("--preview"))
	b.WriteString("  tokens, git status, modified date, and shape — no contents, nothing copied.\n")
	b.WriteString("  Read small files whole, snippet the large ones, skip the rest, instead of\n")
	b.WriteString("  reading blind. The # header labels the columns.\n\n")
	b.WriteString("  catclip replaces find + grep + cat pipelines with a single command.\n")
	b.WriteString("  One process handles discovery, filtering, content matching, and output —\n")
	b.WriteString("  no per-file fork overhead. Faster than per-file cat loops on large codebases.\n")
	b.WriteString("  Bundles its own ripgrep — no external dependency needed.\n\n")

	// ── Targeting ───────────────────────────────────────────────────────
	head("TARGETING (where)")
	b.WriteString("  Navigate by path. Targets are relative paths from the current working directory.\n\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip src", Right: "All text files under src/"},
		{Left: "catclip src/components", Right: "Narrow to a subdirectory"},
		{Left: "catclip src/components/Button.tsx", Right: "One specific file"},
		{Left: "catclip src lib docs", Right: "Multiple targets in one scope"},
	})
	b.WriteString("\n  Glob patterns are also valid targets:\n\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip '*.go'", Right: "All .go files in the project"},
		{Left: "catclip '*.go' '*.ts'", Right: "All .go and .ts files (union)"},
		{Left: "catclip src '*.go'", Right: "src/ files + all .go files (union)"},
		{Left: "catclip 'internal/*.go'", Right: "Only .go files directly inside internal/"},
	})
	b.WriteString("\n  Glob targets match against all visible files in the project, not scoped to\n")
	b.WriteString("  sibling path targets. Modifiers apply to the full combined set:\n")
	fmt.Fprintf(&b, "  %s\n\n", cmd("catclip src '*.go' --exclude '*_test.go' --recent 5"))
	b.WriteString("  To narrow to a subdirectory, use it as the target.\n")
	fmt.Fprintf(&b, "  Do not use %s with path prefixes for navigation.\n\n", flag("--only"))
	b.WriteString("  Absolute paths and paths above cwd (../) are not allowed.\n\n")

	// ── Filtering ───────────────────────────────────────────────────────
	head("FILTERING (what kind of files)")
	fmt.Fprintf(&b, "  %s and %s filter by filename or path subtree.\n", flag("--only"), flag("--exclude"))
	b.WriteString("  They are not for path navigation — use targets for that.\n\n")
	fmt.Fprintf(&b, "  %s\n", bold("Globs match filenames:"))
	writeAlignedHelpRows(&b, "  ", flag, []helpRow{
		{Left: `--only "*.ts"`, Right: "Keep only .ts files"},
		{Left: `--only "*.ts" "*.tsx"`, Right: "Keep .ts and .tsx (values OR together)"},
		{Left: `--exclude "*.test.*"`, Right: "Remove test files"},
		{Left: `--exclude "*.css" "*.svg"`, Right: "Remove CSS and SVG files"},
	})
	fmt.Fprintf(&b, "\n  %s\n", bold("Trailing slash matches path subtrees:"))
	writeAlignedHelpRows(&b, "  ", flag, []helpRow{
		{Left: "--only docs/", Right: "Keep only files under docs/ directories"},
		{Left: "--exclude build/", Right: "Remove files under build/ directories"},
	})
	fmt.Fprintf(&b, "\n  %s\n", bold("Bare names match directory segments:"))
	writeAlignedHelpRows(&b, "  ", flag, []helpRow{
		{Left: "--exclude tests", Right: "Remove files in any tests/ directory"},
	})
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %s and %s use shell globs (*, ?, [...]).\n", flag("--only"), flag("--exclude"))
	fmt.Fprintf(&b, "  %s and %s use PCRE2 regex (supports lookaround,\n", flag("--contains"), flag("--snippet"))
	b.WriteString("  backreferences, atomic groups, named captures).\n\n")
	b.WriteString("  Patterns use smart-case matching. If your pattern is all lowercase,\n")
	b.WriteString("  matching is case-insensitive. If your pattern contains any uppercase\n")
	b.WriteString("  letter, matching is case-sensitive.\n\n")
	writeAlignedHelpRows(&b, "    ", flag, []helpRow{
		{Left: "--contains 'config'", Right: "matches Config, config, CONFIG (all lowercase → insensitive)"},
		{Left: "--contains 'Config'", Right: "matches only Config            (uppercase C → sensitive)"},
		{Left: "--contains 'TODO'", Right: "matches only TODO              (uppercase → sensitive)"},
		{Left: "--contains 'todo'", Right: "matches TODO, Todo, todo       (all lowercase → insensitive)"},
	})
	b.WriteString("\n  This matches ripgrep's --smart-case behavior. The (?i) inline flag\n")
	b.WriteString("  still works if you need to force case-insensitive on a mixed-case\n")
	fmt.Fprintf(&b, "  pattern: %s matches HandleClick, handleclick, etc.\n\n", flag("--contains '(?i)handleClick'"))

	// ── Narrowing ───────────────────────────────────────────────────────
	head("NARROWING (which ones)")
	writeAlignedHelpRows(&b, "  ", flag, []helpRow{
		{Left: "--contains 'REGEX'", Right: "Keep files whose contents match the regex"},
		{Left: "--not-contains 'REGEX'", Right: "Drop files whose contents match the regex"},
		{Left: "--changed", Right: "Any git-modified file"},
		{Left: "--staged", Right: "Files in the git index"},
		{Left: "--unstaged", Right: "Tracked files with working-tree changes"},
		{Left: "--untracked", Right: "New files not yet tracked"},
		{Left: "--recent N", Right: "Keep N most recently modified files"},
		{Left: "--recent", Right: "Sort all files newest-first (no limit)"},
		{Left: "--size", Right: "Sort all files largest-first"},
		{Left: "--size MIN", Right: "Keep files at least MIN KiB, largest-first"},
		{Left: "--size MIN MAX", Right: "Keep files between MIN and MAX KiB inclusive"},
		{Left: "--depth N", Right: "Keep files at path depth N or shallower (from cwd)"},
	})
	b.WriteString("                         (README.md = 1, src/main.ts = 2, src/lib/util.ts = 3)\n\n")

	// ── Output shape ────────────────────────────────────────────────────
	head("OUTPUT SHAPE (what to emit)")
	writeAlignedHelpRows(&b, "  ", flag, []helpRow{
		{Left: "(default)", Right: "Full file contents in <file> wrappers"},
		{Left: "--paths", Right: "Bare relative paths, one per line"},
		{Left: "--preview", Right: "Per-file table (size, tokens, git, modified, shape); no contents"},
		{Left: "--snippet 'REGEX'", Right: "Only blank-line-bounded blocks matching regex"},
		{Left: "--snippet 'REGEX' N", Right: "Matching lines plus N lines before and after"},
		{Left: "--changed-diff", Right: "All changed files as unified diff patches"},
		{Left: "--staged-diff", Right: "Staged changes as unified diff"},
		{Left: "--unstaged-diff", Right: "Unstaged changes as unified diff"},
		{Left: "--lines", Right: "Add line numbers to output"},
		{Left: "--lines START", Right: "Line numbers from START to end of file"},
		{Left: "--lines START END", Right: "Line numbers for lines START through END"},
		{Left: "-r, --raw", Right: "Bare file body — no wrappers, no line numbers"},
	})
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %s emits raw bytes — no <file> tags, no `cat -n`-style line numbers.\n", flag("--raw"))
	b.WriteString("  Multi-file raw concatenates contiguously with no separator (same as\n")
	fmt.Fprintf(&b, "  `cat a b`). With %s, raw drops the line numbers because numbered-\n", flag("--lines"))
	b.WriteString("  but-unwrapped output is the worst of both modes: not parseable for edits\n")
	b.WriteString("  (no file path) and not pipeable (numbers corrupt downstream tools). For\n")
	b.WriteString("  numbered, edit-targetable output, drop -r and use the wrapped form.\n\n")
	fmt.Fprintf(&b, "  %s returns blank-line-bounded blocks around regex matches by default.\n", flag("--snippet"))
	b.WriteString("  Add a context number for fixed rg/grep-style line windows: N=0 emits only\n")
	b.WriteString("  matching lines; N=3 emits each match plus three lines before and after.\n")
	b.WriteString("  Already focused — head/tail piping is usually unnecessary.\n\n")
	fmt.Fprintf(&b, "  %s reads a file slice directly. Use it instead of:\n", flag("--lines START END"))
	fmt.Fprintf(&b, "    %s\n", bad("catclip FILE -r | sed -n '400,450p'"))
	fmt.Fprintf(&b, "    %s\n", bad("catclip FILE | head -450 | tail -50"))
	b.WriteString("  The built-in slice is faster, numbered, and keeps file wrappers.\n\n")
	b.WriteString("  Diff modifiers already filter to their change set — no need to double-filter:\n")
	fmt.Fprintf(&b, "    %s             # correct\n", cmd("catclip . --only '*.go' --unstaged-diff"))
	fmt.Fprintf(&b, "    %s  # redundant --unstaged\n\n", bad("catclip . --unstaged --only '*.go' --unstaged-diff"))

	// ── Pipeline model ──────────────────────────────────────────────────
	head("PIPELINE MODEL")
	b.WriteString("  Stages run left to right. Each stage receives only what the previous stage kept.\n")
	fmt.Fprintf(&b, "  The set can only shrink (except %s, which adds authorized ignored files).\n\n", flag("--include"))
	fmt.Fprintf(&b, "  %s\n", bold("[all discovered files under TARGET]"))
	fmt.Fprintf(&b, "    → %s      adds authorized ignored files (must be first, once per scope)\n", flag("--include PATH"))
	fmt.Fprintf(&b, "    → %s      keeps files matching PATTERN; discards rest\n", flag("--only PATTERN"))
	fmt.Fprintf(&b, "    → %s   removes files matching PATTERN; keeps rest\n", flag("--exclude PATTERN"))
	fmt.Fprintf(&b, "    → %s          sorts by mtime, keeps top N\n", flag("--recent N"))
	fmt.Fprintf(&b, "    → %s      filters by KiB size, sorts largest-first\n", flag("--size MIN MAX"))
	fmt.Fprintf(&b, "    → %s           removes files deeper than N segments\n", flag("--depth N"))
	fmt.Fprintf(&b, "    → %s    removes files whose contents don't match\n", flag("--contains REGEX"))
	fmt.Fprintf(&b, "    → %s removes files whose contents DO match\n", flag("--not-contains REGEX"))
	fmt.Fprintf(&b, "    → %s           removes files not changed in git\n", flag("--changed"))
	fmt.Fprintf(&b, "    → output shape        %s | %s | %s\n\n", flag("--paths"), flag("--snippet REGEX"), flag("--*-diff"))
	fmt.Fprintf(&b, "  %s must be the first modifier so that all filters apply to the full set.\n", flag("--include"))
	b.WriteString("  The same modifier can appear multiple times (except --include). Each occurrence\n")
	b.WriteString("  is a separate step.\n\n")
	fmt.Fprintf(&b, "    %s\n", cmd(`catclip src --only "*.ts" --exclude "*test*" --recent 5`))
	b.WriteString("    # 1. keep .ts files  2. remove *test*  3. keep 5 newest survivors\n\n")
	fmt.Fprintf(&b, "  %s\n", bold("Order matters because the input set differs at each step:"))
	writeAlignedHelpRows(&b, "    ", flag, []helpRow{
		{Left: `--recent 10 --only "*.ts"`, Right: "take 10 newest, then keep .ts ones"},
		{Left: `--only "*.ts" --recent 10`, Right: "keep .ts first, then take 10 newest of those"},
	})
	fmt.Fprintf(&b, "\n  %s\n\n", bold("Ordering constraints after output-shape modifiers:"))
	writeAlignedHelpRows(&b, "    ", flag, []helpRow{
		{Left: "--paths", Right: "nothing can follow (terminal)"},
		{Left: "--snippet REGEX [N]", Right: "no --contains or --not-contains after (already content-filtered)"},
		{Left: "--*-diff", Right: "no --contains, --not-contains, or git filters after (diff owns both)"},
	})
	fmt.Fprintf(&b, "\n  Output modes cannot repeat or combine (%s is an error).\n\n", bad("--paths --snippet"))

	// ── Scopes ──────────────────────────────────────────────────────────
	head("SCOPES (--then)")
	fmt.Fprintf(&b, "  %s starts a fresh scope with independent targets and modifiers.\n", flag("--then"))
	b.WriteString("  Like running two catclip commands and unioning the results.\n\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --only "*.ts" --then docs --recent 5`))
	b.WriteString("  # Scope 1: .ts files under src  |  Scope 2: 5 newest under docs\n\n")
	fmt.Fprintf(&b, "  %s\n", cmd("catclip . --paths --then src"))
	b.WriteString("  # Scope 1: full repo listing as paths  |  Scope 2: full file bodies from src\n\n")
	fmt.Fprintf(&b, "  Without %s, all targets share the same modifiers.\n", flag("--then"))
	b.WriteString("  Overlapping scopes are deduplicated by path in output.\n\n")

	// ── Authorization ───────────────────────────────────────────────────
	head("AUTHORIZATION (--include)")
	b.WriteString("  catclip only discovers files visible to git (.gitignore) and its own .hiss config.\n")
	fmt.Fprintf(&b, "  Ignored paths require explicit %s authorization.\n\n", flag("--include"))
	fmt.Fprintf(&b, "  %s\n\n", bold("There are two modes:"))
	fmt.Fprintf(&b, "  %s    Authorize a specific ignored path.\n", flag("--include PATH"))
	b.WriteString("                    Names an ignored directory or file relative to cwd.\n")
	b.WriteString("                    Including a directory authorizes all descendants under it.\n")
	b.WriteString("                    Including a parent authorizes descendant targets:\n")
	fmt.Fprintf(&b, "                      %s    (parent authorizes child)\n\n", cmd("catclip blocked/sub --include blocked"))
	fmt.Fprintf(&b, "  %s     Disable all ignore rules — discover everything.\n", flag("--include '*'"))
	b.WriteString("                    Equivalent to ripgrep's --no-ignore.\n")
	b.WriteString("                    Authorizes any target, even if gitignored.\n")
	fmt.Fprintf(&b, "                    Pair with %s for inventory; avoid combining with\n", flag("--paths"))
	b.WriteString("                    body emit on uncurated repos. On a project with a full\n")
	b.WriteString("                    node_modules/build tree this can mean megabytes of\n")
	b.WriteString("                    payload and thousands of files — far more than you want\n")
	b.WriteString("                    on a clipboard or in a prompt.\n\n")
	fmt.Fprintf(&b, "  %s\n", bold("Rules:"))
	fmt.Fprintf(&b, "    1. %s must be the first modifier in a scope (before --only, --exclude, etc.)\n", flag("--include"))
	fmt.Fprintf(&b, "    2. Only one %s per scope — use %s for additional includes\n", flag("--include"), flag("--then"))
	fmt.Fprintf(&b, "    3. %s is scoped to the target paths (not the whole project)\n\n", flag("--include"))
	fmt.Fprintf(&b, "  %s\n", bold("Examples:"))
	fmt.Fprintf(&b, "  %s\n", cmd("catclip blocked-dir --include blocked-dir --paths"))
	fmt.Fprintf(&b, "  %s\n", cmd("catclip blocked-dir/src --include blocked-dir --paths"))
	fmt.Fprintf(&b, "  %s\n", cmd("catclip .env.local --include .env.local -r"))
	fmt.Fprintf(&b, "  %s    # all files under src/, nothing ignored\n\n", cmd("catclip src --include '*' --paths"))
	fmt.Fprintf(&b, "  Cross-scope includes use %s:\n", flag("--then"))
	fmt.Fprintf(&b, "    %s\n\n", cmd("catclip src --then node_modules --include node_modules --paths"))
	fmt.Fprintf(&b, "  catclip's own ignore rules: %s (%s to edit; applied on top of .gitignore)\n\n", flag(hissDisplayPath), flag("--hiss"))

	// ── Pipelines ───────────────────────────────────────────────────────
	head("PIPELINES")
	fmt.Fprintf(&b, "  %s produces one path per line on stdout.\n", flag("--paths --headless"))
	fmt.Fprintf(&b, "  %s, %s, and %s read exact relative paths from stdin.\n", flag("--only -"), flag("--exclude -"), flag("--include -"))
	b.WriteString("  Combined, catclip plugs into either side of a shell pipeline.\n\n")
	fmt.Fprintf(&b, "  %s\n\n", bold("Catclip as input source (stdout → tool):"))
	b.WriteString("    # Line counts per file, biggest first:\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("catclip src --only '*.go' --paths --headless | xargs wc -l | sort -rn"))
	b.WriteString("    # Bulk find-and-replace (macOS sed -i '' syntax):\n")
	fmt.Fprintf(&b, "    %s\n", cmd("catclip src --only '*.ts' --contains 'oldName' --paths --headless \\"))
	fmt.Fprintf(&b, "      %s\n\n", cmd("| xargs sed -i '' 's/oldName/newName/g'"))
	b.WriteString("    # Open matched files in vim:\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("vim $(catclip src --contains TODO --paths --headless)"))
	b.WriteString("    # File count and payload size of your current selection:\n")
	fmt.Fprintf(&b, "    %s\n", cmd("catclip src --only '*.go' --paths --headless | wc -l"))
	fmt.Fprintf(&b, "    %s\n\n", cmd("catclip src --only '*.go' --headless | wc -c"))
	fmt.Fprintf(&b, "  %s\n\n", bold("Catclip as filter sink (tool → stdin):"))
	fmt.Fprintf(&b, "    # Copy files changed against an arbitrary git ref (%s is HEAD-only;\n", flag("--changed"))
	b.WriteString("    # this is the canonical way to scope a PR review or fork-diff copy):\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("git diff --name-only main | catclip . --only - --headless"))

	// ── Scripting mode ──────────────────────────────────────────────────
	head("SCRIPTING MODE (--headless)")
	fmt.Fprintf(&b, "  %s makes catclip safe to call from scripts, CI, and other tools:\n\n", flag("--headless"))
	fmt.Fprintf(&b, "    • %s (no decorations, no prompts)\n", bold("stdout = payload, stderr = errors only"))
	fmt.Fprintf(&b, "    • %s (bare --, fuzzy ambiguity, token warns)\n", bold("rejects ambiguous invocations"))
	fmt.Fprintf(&b, "    • %s (no implicit `.` — pass `.` if you mean cwd)\n\n", bold("requires explicit targets"))
	b.WriteString("  Anywhere a human would get a menu or a confirmation, --headless fails\n")
	b.WriteString("  loudly instead — a script can't answer a prompt. Use it on every\n")
	b.WriteString("  non-interactive invocation.\n\n")

	// ── Output format ───────────────────────────────────────────────────
	head("OUTPUT FORMAT")
	fmt.Fprintf(&b, "  %s (default):\n", bold("Full files"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"src/main.ts\">"))
	b.WriteString("    ...file contents...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Snippets"), flag("--snippet REGEX [N]"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"src/main.ts\" lines=\"42-57\">"))
	b.WriteString("    ...matched block...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	b.WriteString("    With N set, lines=\"...\" is the fixed context window around the match.\n\n")
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Lines"), flag("--lines"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"src/main.ts\">"))
	b.WriteString("         1\timport express from 'express';\n")
	b.WriteString("         2\t...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Lines range"), flag("--lines 42 57"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"src/main.ts\" lines=\"42-57\">"))
	b.WriteString("        42\t  const app = express();\n")
	b.WriteString("        43\t  ...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "  %s (%s, %s, %s):\n", bold("Diff"), flag("--changed-diff"), flag("--staged-diff"), flag("--unstaged-diff"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"src/main.ts\" type=\"diff\">"))
	b.WriteString("    ...unified diff...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"new-file.ts\" type=\"untracked\">"))
	b.WriteString("    ...full content for untracked files in changed-diff...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Paths"), flag("--paths"))
	b.WriteString("    src/main.ts\n")
	b.WriteString("    src/utils.ts\n")
	b.WriteString("    (bare relative paths, one per line)\n\n")
	fmt.Fprintf(&b, "  %s (%s with %s):\n", bold("Raw"), flag("-r"), flag("-p"))
	b.WriteString("    ...file body without any wrapper tags...\n")
	b.WriteString("    (requires exactly one surviving full-file item)\n\n")
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Preview"), flag("--preview"))
	b.WriteString("    # line 1: relative path\n")
	b.WriteString("    # line 2: size, tokens, git, modified, shape\n")
	b.WriteString("    #   git:   M=modified S=staged SM=staged+modified ?=untracked -=none\n")
	b.WriteString("    #   shape: full | snippet | lines | diff | path-only\n")
	b.WriteString("    src/main.ts\n")
	b.WriteString("      2.14KB    ~547    [M]  Today at 2:58 AM  full\n")
	b.WriteString("    src/utils.ts\n")
	b.WriteString("      1.05KB    ~256    [-]  Jun 3, 2026        full\n")
	b.WriteString("    (two lines per file — path, then metrics; no file contents; column-aligned)\n\n")
	fmt.Fprintf(&b, "  Diff type attributes: %s, %s, %s, %s\n\n",
		flag("type=\"diff\""), flag("type=\"staged-diff\""), flag("type=\"unstaged-diff\""), flag("type=\"untracked\""))

	// ── Clipboard delivery ──────────────────────────────────────────────
	head("CLIPBOARD DELIVERY")
	b.WriteString("  catclip auto-selects the clipboard delivery mode based on payload size:\n\n")
	fmt.Fprintf(&b, "    %s   text clipboard (pbcopy / wl-copy / clip.exe)\n", bold("< 4096 bytes"))
	b.WriteString("                   pastes anywhere — terminals, editors, web UIs, etc.\n")
	fmt.Fprintf(&b, "    %s   bundle file at {Documents}/catclip/{project}-{HHMMSS}.txt\n", bold("≥ 4096 bytes"))
	b.WriteString("                   placed on the clipboard as a file reference; pastes as\n")
	b.WriteString("                   an attachment in web UIs (Claude, ChatGPT, etc.) and\n")
	b.WriteString("                   as a file in file managers (Finder, Explorer). Does NOT\n")
	b.WriteString("                   paste as text in terminals or editors.\n\n")
	fmt.Fprintf(&b, "  %s      Force text clipboard regardless of size. Use when you\n", flag("--no-bundle"))
	b.WriteString("                   want to paste raw text into a terminal or editor and\n")
	b.WriteString("                   the output would otherwise exceed 4KB.\n\n")
	fmt.Fprintf(&b, "  Bundle file contents are byte-identical to %s (stdout) output.\n", flag("-p"))
	fmt.Fprintf(&b, "  %s overrides the bundle directory.\n", flag("CATCLIP_BUNDLE_DIR"))
	fmt.Fprintf(&b, "  %s implies stdout, so bundling never applies in headless mode.\n\n", flag("--headless"))

	// ── Exit codes ──────────────────────────────────────────────────────
	head("EXIT CODES")
	fmt.Fprintf(&b, "  %s    Success — all targets resolved, output was emitted\n", bold("0"))
	fmt.Fprintf(&b, "  %s    Partial or no results — one or more targets not found, or runtime error\n", bold("1"))
	fmt.Fprintf(&b, "  %s    Usage error — invalid flags, arguments, or validation failure\n\n", bold("2"))
	b.WriteString("  When some targets resolve and others don't, catclip emits output for the\n")
	fmt.Fprintf(&b, "  resolved targets, warns on stderr (even with %s), and exits 1.\n", flag("-q"))
	b.WriteString("  This matches cat/ls/grep behavior: process what you can, warn, exit non-zero.\n\n")

	// ── Common errors ───────────────────────────────────────────────────
	head("COMMON ERRORS")
	fmt.Fprintf(&b, "  %s\n", bad("\"No text files found matching your criteria.\""))
	fmt.Fprintf(&b, "    → Target may be ignored. Use the canonical form: %s.\n", cmd("catclip TARGET --include TARGET"))
	b.WriteString("    → Target may be empty or contain only binary files.\n")
	b.WriteString("    → Check for typos in the target path.\n\n")
	fmt.Fprintf(&b, "  %s\n", bad("\"no files at depth N\""))
	b.WriteString("    → Depth counts path segments from cwd, not from the target.\n")
	b.WriteString("    → The error shows the actual depth range. Use the suggested value.\n\n")
	fmt.Fprintf(&b, "  %s\n", bad("\"positional targets must come before modifiers.\""))
	b.WriteString("    → Move targets to the left of --only/--exclude/etc.\n")
	fmt.Fprintf(&b, "    → Use %s for a new scope with different targets.\n\n", flag("--then"))
	fmt.Fprintf(&b, "  %s\n", bad("\"Warning: ... not found (scope N).\""))
	b.WriteString("    → Target could not be resolved. Output for other targets still emitted.\n")
	b.WriteString("    → Exit code is 1 (not 0) even when other targets succeed.\n")
	fmt.Fprintf(&b, "    → Warnings print to stderr even with %s or %s.\n\n", flag("-q"), flag("--headless"))

	// ── Limitations ────────────────────────────────────────────────────
	head("LIMITATIONS")
	b.WriteString("  By default catclip skips binary files and ignored paths.\n")
	fmt.Fprintf(&b, "  Use %s to include binaries, %s to include ignored paths,\n", flag("--with-binaries"), flag("--include '*'"))
	b.WriteString("  or both for a complete inventory equivalent to find:\n\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("catclip TARGET --include '*' --with-binaries --paths --headless"))
	fmt.Fprintf(&b, "  %s\n", bold("Path semantics:"))
	b.WriteString("    • Absolute paths are not accepted. Run catclip from the project root\n")
	b.WriteString("      and pass relative paths.\n")
	b.WriteString("    • Symlinks are not followed.\n")
	b.WriteString("    • Path case sensitivity follows the target filesystem/volume. On\n")
	b.WriteString("      case-insensitive filesystems (default macOS, default Windows), repos\n")
	fmt.Fprintf(&b, "      with case-colliding paths in the git index may have git selectors\n")
	fmt.Fprintf(&b, "      (%s, %s, etc.) miss entries whose index spelling differs\n", flag("--changed"), flag("--staged"))
	b.WriteString("      from the materialized path. Use Linux or a case-sensitive volume for\n")
	b.WriteString("      exact git selector behavior on those repos.\n\n")

	// ── Reference table ─────────────────────────────────────────────────
	head("MODIFIER REFERENCE")
	fmt.Fprintf(&b, "  %s\n", bold("Scope modifiers (per-scope, left to right):"))
	writeAlignedHelpRows(&b, "    ", flag, []helpRow{
		{Left: "--include VALUE...", Right: "Authorize ignored paths (must be first, once per scope)"},
		{Left: "--only VALUE...", Right: "Filename filter — keep matches (shell globs)"},
		{Left: "--exclude VALUE...", Right: "Filename filter — remove matches (shell globs)"},
		{Left: "--recent [N]", Right: "Sort by mtime; optional top-N"},
		{Left: "--size [MIN [MAX]]", Right: "Sort/filter by file size in KiB"},
		{Left: "--depth N", Right: "Max path depth"},
		{Left: "--contains REGEX", Right: "Content filter — keep files matching REGEX"},
		{Left: "--not-contains REGEX", Right: "Content filter — drop files matching REGEX (repeatable)"},
		{Left: "--snippet REGEX [N]", Right: "Extract matching blocks, or +/- N line context"},
		{Left: "--lines [START [END]]", Right: "Line numbers; optional range slice"},
		{Left: "--paths", Right: "Emit bare paths instead of file bodies"},
		{Left: "--changed", Right: "Git-modified files"},
		{Left: "--staged", Right: "Git index files"},
		{Left: "--unstaged", Right: "Tracked working-tree changes"},
		{Left: "--untracked", Right: "New untracked files"},
		{Left: "--changed-diff", Right: "Changed files as unified diff"},
		{Left: "--staged-diff", Right: "Staged changes as unified diff"},
		{Left: "--unstaged-diff", Right: "Unstaged changes as unified diff"},
		{Left: "--then", Right: "Start a new scope"},
	})
	fmt.Fprintf(&b, "\n  %s\n", bold("Global flags:"))
	writeAlignedHelpRows(&b, "    ", flag, []helpRow{
		{Left: "--headless", Right: "Script mode: stdout output, quiet stderr, no prompts"},
		{Left: "", Right: "(rejects bare --, fuzzy ambiguity, token-warn prompt)"},
		{Left: "-q, --quiet", Right: "No prompts, decorations, or tree preview"},
		{Left: "-p, --print", Right: "Stdout instead of clipboard"},
		{Left: "-r, --raw", Right: "Bare file body, no wrappers or numbers"},
		{Left: "-y, --yes", Right: "Skip confirmation"},
		{Left: "-t, --no-tree", Right: "Skip tree preview"},
		{Left: "--no-bundle", Right: "Force text clipboard; skip bundle file for ≥4KB output"},
		{Left: "-v, --verbose", Right: "Debug info and timings"},
		{Left: "--preview", Right: "Size up files before reading them"},
		{Left: "--with-binaries", Right: "Include binary files in discovery"},
		{Left: "--hiss", Right: "Edit catclip's own ignore rules (on top of .gitignore)"},
		{Left: "--hiss-reset", Right: "Reset catclip's ignore rules to defaults"},
		{Left: "--all-ignore-rules", Right: "List every ignore rule in effect — .gitignore(s) + .hiss, merged"},
	})

	return b.String()
}
