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
	b.WriteString("Examples use one project: React TypeScript in src/, Go in cmd/ and internal/, and Markdown in docs/.\n\n")

	fmt.Fprintf(&b, "%s\n", bold("Quick Start:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip", Right: "Pick files or folders from a menu"},
		{Left: "catclip src", Right: "Copy a folder"},
		{Left: "catclip Button.tsx", Right: "Copy a file (finds it for you)"},
		{Left: "catclip btn", Right: "Fuzzy picker (Button.tsx ranks first)"},
		{Left: "catclip src internal docs", Right: "Copy multiple folders"},
		{Left: `catclip "*.go"`, Right: "All Go files in the project (glob pattern)"},
		{Left: `catclip src "*.go"`, Right: "Union: src/ files + all Go files"},
		{Left: `catclip "src/*.tsx"`, Right: "React entry files directly inside src/"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Interactive mode (build commands from menus):"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip --", Right: "Pick files or folders, then choose filters from menus"},
		{Left: "catclip src --", Right: "Pick filters for src from a menu"},
		{Left: "catclip src -- --", Right: "Chain menus to build a full command"},
	})

	fmt.Fprintf(&b, "\n%s\n", bold("Filtering:"))
	fmt.Fprintf(&b, "  Targets choose where catclip looks; filters narrow the files found there.\n")
	fmt.Fprintf(&b, "  Filters run left to right. Changing the order changes the result.\n\n")
	fmt.Fprintf(&b, "  When the result is the same, narrow by file name before searching file contents.\n\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: `catclip src --only "*.tsx"`, Right: "Only TSX files"},
		{Left: `catclip src --exclude "*.css"`, Right: "Skip CSS files"},
		{Left: `catclip internal --only "handler/"`, Right: "Only files under handler/ directories"},
		{Left: `catclip internal --exclude "handler/"`, Right: "Skip files under handler/ directories"},
		{Left: "catclip src --recent 3", Right: "Keep the 3 most recently modified files"},
		{Left: "catclip src --size 0 100", Right: "Keep files up to 100 KiB, largest first"},
		{Left: "catclip src --depth 1", Right: "Just the top level of src"},
		{Left: "catclip . --depth 1", Right: "Copy only the files in the project root"},
		{Left: "catclip src --paths", Right: "Emit bare relative paths, not file bodies"},
		{Left: "catclip . --paths --then src", Right: "Show project structure, then copy React files"},
		{Left: "catclip src --contains TODO", Right: "Find files containing specific text"},
		{Left: "catclip src --not-contains TODO", Right: "Drop files containing TODO; keep the rest"},
		{Left: "catclip src --snippet useAuth", Right: "Smart block around each match"},
		{Left: "catclip src --snippet useAuth 3", Right: "Matching lines plus 3 lines around each match"},
		{Left: "catclip internal/handler/user.go --lines", Right: "Add line numbers to one Go file"},
		{Left: "catclip internal/handler/user.go --lines 40 80", Right: "Read lines 40-80 with line numbers"},
	})

	fmt.Fprintf(&b, "\n  You can give %s, %s, and %s more than one value.\n", flag("--only"), flag("--exclude"), flag("--include"))
	fmt.Fprintf(&b, "  Examples: %s   %s\n", flag(`--only "*.ts" "*.tsx"`), flag(`--exclude "*.css" "*.scss"`))
	fmt.Fprintf(&b, "  In %s and %s, filename patterns and single directory names match anywhere below the targets:\n", flag("--only"), flag("--exclude"))
	fmt.Fprintf(&b, "    %s keeps TSX files in nested folders too.\n", flag(`--only "*.tsx"`))
	fmt.Fprintf(&b, "    %s keeps files below every directory named handler.\n", flag(`--only "handler/"`))
	fmt.Fprintf(&b, "  A value containing a folder path starts from the directory where you run catclip:\n")
	fmt.Fprintf(&b, "    %s, not %s\n", cmd(`catclip src --only "src/components/*"`), flag(`--only "components/*"`))
	fmt.Fprintf(&b, "\n  These two commands are different:\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: `catclip src --recent 10 --only "*.tsx"`, Right: "Take the 10 newest files, then keep the .tsx ones"},
		{Left: `catclip src --only "*.tsx" --recent 10`, Right: "Keep .tsx first, then take the 10 newest of that set"},
	})
	fmt.Fprintf(&b, "\n  One combined example:\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --only "*.tsx" --exclude "*.test.tsx" --contains "Button" --recent 3`))
	fmt.Fprintf(&b, "    Start with files under src.\n")
	fmt.Fprintf(&b, "    Keep only .tsx files.\n")
	fmt.Fprintf(&b, "    Remove test files.\n")
	fmt.Fprintf(&b, "    Then keep only the ones that mention \"Button\".\n")
	fmt.Fprintf(&b, "    Finally, take the 3 most recently modified files left.\n\n")

	fmt.Fprintf(&b, "\n%s\n", bold("Git Filters (requires a git repo):"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip src --changed", Right: "Only changed React files"},
		{Left: "catclip --changed-diff", Right: "Show changes as patches instead of full files"},
	})
	fmt.Fprintf(&b, "  Other git filters: %s, %s, %s, %s, %s.\n", flag("--staged"), flag("--unstaged"), flag("--untracked"), flag("--staged-diff"), flag("--unstaged-diff"))

	fmt.Fprintf(&b, "\n%s\n", bold("--then (chain another catclip command):"))
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --only "*.tsx" --then docs --recent 5`))
	fmt.Fprintf(&b, "    Keeps only TSX files from src, then adds the 5 most recent files from docs.\n")
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip . --paths --then src`))
	fmt.Fprintf(&b, "    First emits the project structure as paths, then adds full files from src.\n")
	fmt.Fprintf(&b, "    Useful when an AI should see the whole structure but only read the React app.\n")
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", bad(`catclip src docs --only "*.tsx"`))
	fmt.Fprintf(&b, "    Bad here because it would also throw away every non-TSX file in docs.\n")
	fmt.Fprintf(&b, "    Use %s when the next target should use different filters or output shape.\n", flag("--then"))

	fmt.Fprintf(&b, "\n%s\n", bold("Ignored Files:"))
	fmt.Fprintf(&b, "  catclip skips .gitignored paths and paths matched by %s (catclip's own ignore rules, on top of .gitignore).\n", flag(".hiss"))
	fmt.Fprintf(&b, "\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip dist --include dist", Right: "Allow the ignored React build output for this run"},
		{Left: "catclip --hiss", Right: fmt.Sprintf("Edit catclip's ignore rules (%s)", flag(hissDisplayPath))},
	})
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s authorizes gitignored paths within your walk scope.\n", flag("--include"))
	b.WriteString("  Write specific include paths from the directory where you run catclip; targets do not shorten them.\n")
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip . --include dist --only src dist`))
	fmt.Fprintf(&b, "    Works — '.' covers the whole project, %s keeps src and dist.\n", flag("--only"))
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  %s\n", bad(`catclip internal --include dist`))
	fmt.Fprintf(&b, "    Doesn't find dist because it's outside internal/.\n")

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

	head("EXAMPLE PROJECT")
	b.WriteString("  Concrete examples use one React + Go project:\n\n")
	b.WriteString("    src/main.tsx\n")
	b.WriteString("    src/App.tsx\n")
	b.WriteString("    src/components/Button.tsx\n")
	b.WriteString("    src/components/Button.test.tsx\n")
	b.WriteString("    src/components/Header.tsx\n")
	b.WriteString("    src/pages/Login.tsx\n")
	b.WriteString("    src/pages/Profile.tsx          (untracked)\n")
	b.WriteString("    src/lib/api.ts\n")
	b.WriteString("    src/hooks/useAuth.ts\n")
	b.WriteString("    src/styles/globals.css\n")
	b.WriteString("    cmd/api/main.go\n")
	b.WriteString("    internal/handler/user.go\n")
	b.WriteString("    internal/store/store.go\n")
	b.WriteString("    docs/api.md\n")
	b.WriteString("    .env.example                (ignored by .hiss)\n")
	b.WriteString("    dist/index.html             (gitignored)\n")
	b.WriteString("    dist/assets/index.js        (gitignored)\n")
	b.WriteString("    node_modules/react/index.js (gitignored)\n\n")

	// ── Common tasks ────────────────────────────────────────────────────
	head("COMMON TASKS")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip TARGET --paths", Right: "List files"},
		{Left: "catclip TARGET --include '*' --with-binaries --paths", Right: "List everything, ignored and binary included"},
		{Left: "catclip TARGET --contains 'REGEX' --paths", Right: "Which files mention this?"},
		{Left: "catclip TARGET --not-contains 'REGEX'", Right: "Everything except files mentioning this"},
		{Left: "catclip TARGET --snippet 'REGEX'", Right: "Smart block: smallest enclosing unit"},
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
	fmt.Fprintf(&b, "    %s         # files at the root and one directory below\n\n", cmd("catclip . --depth 2 --paths"))
	fmt.Fprintf(&b, "  If you know ripgrep: %s is like rg --files-with-matches\n", flag("--contains --paths"))
	fmt.Fprintf(&b, "  (which files match?), and %s is like rg with context (which blocks\n", flag("--snippet"))
	b.WriteString("  match?). By default --snippet returns the smart block: the complete unit\n")
	b.WriteString("  around each match, a whole function/class/method, an XML element, or a\n")
	b.WriteString("  config section; add N for fixed +/- N line windows. With neither, you get\n")
	b.WriteString("  full file contents.\n\n")
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
		{Left: "catclip src internal docs", Right: "Multiple targets in one scope"},
	})
	b.WriteString("\n  Plain non-exact targets use the fuzzy file/folder picker. A target containing\n")
	b.WriteString("  *, ?, or [ is a deterministic file glob and never opens that picker:\n\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip Button", Right: "Fuzzy file/folder navigation"},
		{Left: "catclip '*Button*'", Right: "Every visible file matching *Button*"},
	})
	b.WriteString("\n  Glob patterns are also valid targets:\n\n")
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: "catclip '*.tsx'", Right: "All TSX files in the project"},
		{Left: "catclip '*.go' '*.tsx'", Right: "All Go and TSX files (union)"},
		{Left: "catclip src '*.go'", Right: "src/ files + all Go files (union)"},
		{Left: "catclip 'src/*.tsx'", Right: "React entry files directly inside src/"},
	})
	b.WriteString("\n  Keep glob targets quoted so the shell passes the pattern to catclip.\n")
	b.WriteString("  CLI patterns do not support **. Since filter * already crosses folders,\n")
	b.WriteString("  use a directory target plus a single-star recursive filter:\n")
	fmt.Fprintf(&b, "  %s\n", cmd("catclip src --only '*.tsx'"))
	b.WriteString("\n  Glob targets match against all visible files in the project, not scoped to\n")
	b.WriteString("  sibling path targets. Modifiers apply to the full combined set:\n")
	fmt.Fprintf(&b, "  %s\n\n", cmd("catclip src '*.go' --exclude '*_test.go' --recent 5"))
	b.WriteString("  Use targets to choose where catclip looks. Use --only and --exclude to\n")
	b.WriteString("  filter the files found there.\n\n")
	b.WriteString("  Absolute paths and paths above cwd (../) are not allowed.\n\n")

	// ── Filtering ───────────────────────────────────────────────────────
	head("FILTERING (which files)")
	fmt.Fprintf(&b, "  %s and %s keep or remove files by name, path, subtree, or glob.\n", flag("--only"), flag("--exclude"))
	b.WriteString("  Targets choose where catclip looks; these filters act on the files found there.\n\n")
	fmt.Fprintf(&b, "  %s\n", bold("Glob values:"))
	writeAlignedHelpRows(&b, "  ", flag, []helpRow{
		{Left: `--only "*.ts"`, Right: "Keep only .ts files"},
		{Left: `--only "*.ts" "*.tsx"`, Right: "Keep .ts and .tsx (values OR together)"},
		{Left: `--exclude "*.test.*"`, Right: "Remove test files"},
		{Left: `--exclude "*.css" "*.svg"`, Right: "Remove CSS and SVG files"},
	})
	fmt.Fprintf(&b, "\n  %s\n", bold("A literal directory name ending in / selects its subtree:"))
	writeAlignedHelpRows(&b, "  ", cmd, []helpRow{
		{Left: `catclip internal --only "handler/"`, Right: "Only files under handler/ directories"},
		{Left: `catclip internal --exclude "handler/"`, Right: "Skip files under handler/ directories"},
	})
	b.WriteString("  A glob ending in / (such as \"*/\") cannot match file paths.\n")
	fmt.Fprintf(&b, "\n  %s\n", bold("Bare names match directory segments:"))
	writeAlignedHelpRows(&b, "  ", flag, []helpRow{
		{Left: "--exclude handler", Right: "Remove a file named handler or files beneath handler/ directories"},
	})
	b.WriteString("\n")
	b.WriteString("  Filter globs support *, ?, and [...]; * can cross folder boundaries.\n")
	b.WriteString("  ** is rejected in CLI targets and filters; it remains valid in ignore files.\n")
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
		{Left: "--depth N", Right: "Keep files N levels or fewer below each target"},
	})
	b.WriteString("                         (README.md = 1, src/lib/api.ts = 3, internal/handler/user.go = 3)\n\n")

	// ── Output shape ────────────────────────────────────────────────────
	head("OUTPUT SHAPE (what to emit)")
	writeAlignedHelpRows(&b, "  ", flag, []helpRow{
		{Left: "(default)", Right: "Full file contents in <file> wrappers"},
		{Left: "--paths", Right: "Bare relative paths, one per line"},
		{Left: "--preview", Right: "Per-file table (size, tokens, git, modified, shape); no contents"},
		{Left: "--snippet 'REGEX'", Right: "Smart block around each regex match"},
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
	fmt.Fprintf(&b, "  %s defaults to the smart block: the complete unit containing each match.\n", flag("--snippet"))
	b.WriteString("  In code that is the whole function, class, or method; in XML/HTML the\n")
	b.WriteString("  enclosing <tag> element; in INI/TOML the [section]. Unrecognized syntax\n")
	b.WriteString("  falls back to the surrounding paragraph.\n")
	b.WriteString("  Add a context number for fixed rg/grep-style line windows: N=0 emits only\n")
	b.WriteString("  matching lines; N=3 emits each match plus three lines before and after.\n")
	b.WriteString("  Already focused — head/tail piping is usually unnecessary.\n\n")
	fmt.Fprintf(&b, "  %s reads a file slice directly. Use it instead of:\n", flag("--lines START END"))
	fmt.Fprintf(&b, "    %s\n", bad("catclip FILE -r | sed -n '400,450p'"))
	fmt.Fprintf(&b, "    %s\n", bad("catclip FILE | head -450 | tail -50"))
	b.WriteString("  The built-in slice is faster, numbered, and keeps file wrappers.\n\n")
	b.WriteString("  Diff modifiers already filter to their change set — no need to double-filter:\n")
	fmt.Fprintf(&b, "    %s             # correct\n", cmd("catclip . --only '*.tsx' --unstaged-diff"))
	fmt.Fprintf(&b, "    %s  # redundant --unstaged\n\n", bad("catclip . --unstaged --only '*.tsx' --unstaged-diff"))

	// ── Pipeline model ──────────────────────────────────────────────────
	head("PIPELINE MODEL")
	b.WriteString("  Stages run left to right. Each stage receives only what the previous stage kept.\n")
	fmt.Fprintf(&b, "  The set can only shrink (except %s, which adds authorized ignored files).\n\n", flag("--include"))
	b.WriteString("  When the result is the same, narrow by file name, path, or depth before\n")
	b.WriteString("  searching file contents, so fewer files need to be read.\n\n")
	fmt.Fprintf(&b, "  %s\n", bold("[all discovered files under TARGET]"))
	fmt.Fprintf(&b, "    → %s      adds authorized ignored files (must be first, once per scope)\n", flag("--include PATH"))
	fmt.Fprintf(&b, "    → %s      keeps files matching PATTERN; discards rest\n", flag("--only PATTERN"))
	fmt.Fprintf(&b, "    → %s   removes files matching PATTERN; keeps rest\n", flag("--exclude PATTERN"))
	fmt.Fprintf(&b, "    → %s          sorts by mtime, keeps top N\n", flag("--recent N"))
	fmt.Fprintf(&b, "    → %s      filters by KiB size, sorts largest-first\n", flag("--size MIN MAX"))
	fmt.Fprintf(&b, "    → %s           removes files more than N levels below each target\n", flag("--depth N"))
	fmt.Fprintf(&b, "    → %s    removes files whose contents don't match\n", flag("--contains REGEX"))
	fmt.Fprintf(&b, "    → %s removes files whose contents DO match\n", flag("--not-contains REGEX"))
	fmt.Fprintf(&b, "    → %s           removes files not changed in git\n", flag("--changed"))
	fmt.Fprintf(&b, "    → output shape        %s | %s | %s\n\n", flag("--paths"), flag("--snippet REGEX"), flag("--*-diff"))
	fmt.Fprintf(&b, "  %s must be the first modifier so that all filters apply to the full set.\n", flag("--include"))
	b.WriteString("  The same modifier can appear multiple times (except --include). Each occurrence\n")
	b.WriteString("  is a separate step.\n\n")
	fmt.Fprintf(&b, "    %s\n", cmd(`catclip src --only "*.tsx" --exclude "*.test.tsx" --recent 3`))
	b.WriteString("    # 1. keep .tsx files  2. remove tests  3. keep 3 newest survivors\n\n")
	fmt.Fprintf(&b, "  %s\n", bold("Order matters because the input set differs at each step:"))
	writeAlignedHelpRows(&b, "    ", flag, []helpRow{
		{Left: `--recent 10 --only "*.tsx"`, Right: "take 10 newest, then keep .tsx ones"},
		{Left: `--only "*.tsx" --recent 10`, Right: "keep .tsx first, then take 10 newest of those"},
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
	fmt.Fprintf(&b, "  %s\n", cmd(`catclip src --only "*.tsx" --then docs --recent 5`))
	b.WriteString("  # Scope 1: .tsx files under src  |  Scope 2: 5 newest under docs\n\n")
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
	fmt.Fprintf(&b, "                      %s    (parent authorizes child)\n\n", cmd("catclip dist/assets --include dist"))
	fmt.Fprintf(&b, "  %s     Disable ignore rules within the selected targets.\n", flag("--include '*'"))
	b.WriteString("                    Uses target-bounded no-ignore discovery.\n")
	b.WriteString("                    Text/binary filtering still applies unless --with-binaries is set.\n")
	b.WriteString("                    Authorizes any selected target, even if gitignored.\n")
	fmt.Fprintf(&b, "                    Pair with %s for inventory; avoid combining with\n", flag("--paths"))
	b.WriteString("                    body emit on uncurated repos. On a project with a full\n")
	b.WriteString("                    node_modules/build tree this can mean megabytes of\n")
	b.WriteString("                    payload and thousands of files — far more than you want\n")
	b.WriteString("                    on a clipboard or in a prompt.\n\n")
	fmt.Fprintf(&b, "  %s\n", bold("Rules:"))
	fmt.Fprintf(&b, "    1. %s must be the first modifier in a scope (before --only, --exclude, etc.)\n", flag("--include"))
	fmt.Fprintf(&b, "    2. Only one %s per scope — use %s for additional includes\n", flag("--include"), flag("--then"))
	fmt.Fprintf(&b, "    3. %s is scoped to the target paths (not the whole project)\n", flag("--include"))
	b.WriteString("    4. Specific paths are written from cwd; targets never rebase them\n")
	fmt.Fprintf(&b, "    5. %s is the broad form; %s is not a target-root alias\n\n", flag("--include '*'"), bad("--include ."))
	fmt.Fprintf(&b, "  %s\n", bold("Examples:"))
	fmt.Fprintf(&b, "  %s\n", cmd("catclip dist --include dist --paths"))
	fmt.Fprintf(&b, "  %s\n", cmd("catclip dist/assets --include dist --paths"))
	fmt.Fprintf(&b, "  %s\n", cmd("catclip .env.example --include .env.example -r"))
	fmt.Fprintf(&b, "  %s    # all text files under src/, ignore rules disabled\n\n", cmd("catclip src --include '*' --paths"))
	b.WriteString("  In a TTY, an unresolved include query may open the ignored-path picker.\n")
	b.WriteString("  Headless runs keep it exact and may suggest complete paths without choosing one.\n\n")
	fmt.Fprintf(&b, "  Cross-scope includes use %s:\n", flag("--then"))
	fmt.Fprintf(&b, "    %s\n\n", cmd("catclip src --then node_modules/react --include node_modules --paths"))
	fmt.Fprintf(&b, "  catclip's own ignore rules: %s (%s to edit; applied on top of .gitignore)\n\n", flag(hissDisplayPath), flag("--hiss"))

	// ── Pipelines ───────────────────────────────────────────────────────
	head("PIPELINES")
	fmt.Fprintf(&b, "  %s produces one path per line on stdout.\n", flag("--paths --headless"))
	fmt.Fprintf(&b, "  %s, %s, and %s read exact relative paths from stdin.\n", flag("--only -"), flag("--exclude -"), flag("--include -"))
	b.WriteString("  Combined, catclip plugs into either side of a shell pipeline.\n\n")
	fmt.Fprintf(&b, "  %s\n\n", bold("Catclip as input source (stdout → tool):"))
	b.WriteString("    # Line counts per file, biggest first:\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("catclip src --only '*.tsx' --paths --headless | xargs wc -l | sort -rn"))
	b.WriteString("    # Bulk find-and-replace (macOS sed -i '' syntax):\n")
	fmt.Fprintf(&b, "    %s\n", cmd("catclip src --only '*.tsx' --contains 'oldName' --paths --headless \\"))
	fmt.Fprintf(&b, "      %s\n\n", cmd("| xargs sed -i '' 's/oldName/newName/g'"))
	b.WriteString("    # Open matched files in vim:\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("vim $(catclip src --contains TODO --paths --headless)"))
	b.WriteString("    # File count and payload size of your current selection:\n")
	fmt.Fprintf(&b, "    %s\n", cmd("catclip src --only '*.tsx' --paths --headless | wc -l"))
	fmt.Fprintf(&b, "    %s\n\n", cmd("catclip src --only '*.tsx' --headless | wc -c"))
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
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"src/lib/api.ts\">"))
	b.WriteString("    ...file contents...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Snippets"), flag("--snippet REGEX [N]"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"src/components/Button.tsx\" lines=\"42-57\">"))
	b.WriteString("    ...matched block...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	b.WriteString("    With N set, lines=\"...\" is the fixed context window around the match.\n\n")
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Lines"), flag("--lines"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"cmd/api/main.go\">"))
	b.WriteString("         1\tpackage main\n")
	b.WriteString("         2\t...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Lines range"), flag("--lines 42 57"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"internal/handler/user.go\" lines=\"42-57\">"))
	b.WriteString("        42\tfunc main() {\n")
	b.WriteString("        43\t  ...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "    %s output terminates each emitted line with a newline, even if the\n", flag("--lines"))
	b.WriteString("    source file's final line has none.\n\n")
	fmt.Fprintf(&b, "  %s (%s, %s, %s):\n", bold("Diff"), flag("--changed-diff"), flag("--staged-diff"), flag("--unstaged-diff"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"src/lib/api.ts\" type=\"diff\">"))
	b.WriteString("    ...unified diff...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "    %s\n", cmd("<file path=\"src/pages/Profile.tsx\" type=\"untracked\">"))
	b.WriteString("    ...full content for untracked files in changed-diff...\n")
	fmt.Fprintf(&b, "    %s\n\n", cmd("</file>"))
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Paths"), flag("--paths"))
	b.WriteString("    src/components/Button.tsx\n")
	b.WriteString("    src/components/Header.tsx\n")
	b.WriteString("    (bare relative paths, one per line)\n\n")
	fmt.Fprintf(&b, "  %s (%s with %s):\n", bold("Raw"), flag("-r"), flag("-p"))
	b.WriteString("    ...file body without any wrapper tags...\n")
	b.WriteString("    (requires exactly one surviving full-file item)\n\n")
	fmt.Fprintf(&b, "  %s (%s):\n", bold("Preview"), flag("--preview"))
	b.WriteString("    # line 1: relative path\n")
	b.WriteString("    # line 2: size, tokens, git, modified, shape\n")
	b.WriteString("    #   git:   M=modified S=staged SM=staged+modified ?=untracked -=none\n")
	b.WriteString("    #   shape: full | snippet | lines | diff | path-only\n")
	b.WriteString("    src/components/Button.tsx\n")
	b.WriteString("      2.14KB    ~547    [M]  Today at 2:58 AM  full\n")
	b.WriteString("    src/components/Header.tsx\n")
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
	b.WriteString("    → Depth counts path segments below each target (like rg --max-depth).\n")
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
		{Left: "--only VALUE...", Right: "Keep matches by name, path, subtree, or glob"},
		{Left: "--exclude VALUE...", Right: "Remove matches by name, path, subtree, or glob"},
		{Left: "--recent [N]", Right: "Sort by mtime; optional top-N"},
		{Left: "--size [MIN [MAX]]", Right: "Sort/filter by file size in KiB"},
		{Left: "--depth N", Right: "Max path depth"},
		{Left: "--contains REGEX", Right: "Content filter — keep files matching REGEX"},
		{Left: "--not-contains REGEX", Right: "Content filter — drop files matching REGEX (repeatable)"},
		{Left: "--snippet REGEX [N]", Right: "Smart block around matches, or +/- N line context"},
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
