package catclip

// =============================================================================
// catclip — Context Gatherer for LLMs
//
// This is the Go rewrite of the original Bash implementation.
//
// CURRENT LAYOUT:
//   - cmd/catclip/main.go is the thin binary entrypoint
//   - the implementation lives in package catclip at the module root
//   - files are split by responsibility, but most code still shares one package
//
// PRODUCT RULES:
//   1. Preview Must Be Truthful:
//      the tree and summary must reflect exactly what will be copied, not a
//      looser approximation
//   2. Current Paths Win:
//      the UI should show the current filesystem view and current working-tree
//      paths, even when Git metadata is simplified
//   3. Selectors Drive Status UI:
//      tree badges should optimize for `--changed`, `--staged`,
//      `--unstaged`, and `--untracked`, not raw porcelain fidelity
//   4. Specificity Grants Access:
//      safe discovery stays safe; ignored content only enters through explicit
//      `--include`
//   5. Exact Beats Fuzzy:
//      exact existing paths and exact basename hits should execute directly;
//      genuinely ambiguous fuzzy resolution belongs to fzf, not local
//      heuristics
//   6. One Scope, One Meaning:
//      targets come first, modifiers apply to that scope, and `--then` starts
//      a new scope; treat `--then` like starting a brand new catclip command
//      on the same line and unioning the results; scope grammar must stay
//      deterministic, and coverage is path-subtree based rather than
//      "conceptually related" (for example, selecting `src/vs` does not imply
//      that later selecting `src` is redundant)
//   7. Stage Order Is Semantic:
//      within one scope, `--only` and `--exclude` are sequential stages, not a
//      globally merged rule set; values inside one occurrence OR together, and
//      later occurrences run after earlier ones
//   8. Single Writer Integrity:
//      output order must stay deterministic, with one sink writer and loud
//      failure on read/write errors
//   9. Safe Path Is Fast Path:
//      normal visible discovery should stay on the optimized `.gitignore` +
//      `.hiss` path, using rg for Git-visible files and applying `.hiss` in
//      Go; slower ignored/bypass flows are acceptable off the hot path
//   10. Interactive Is a Convenience Layer:
//      catclip is both a scripting CLI and an interactive tool, so complete
//      deterministic commands must remain directly executable; startup `fzf`
//      only helps resolve ambiguity or unfinished human input
//  11. Classification Is Product Policy:
//      known text/binary allowlists are intentional behavior, not incidental
//      heuristics
//  12. No Silent Skips:
//      if catclip excludes something significant, that should remain a
//      deliberate product decision, not an accidental side effect
//  13. Diagnostics Must Be Actionable:
//      when catclip does show a diagnostic, it should tell the user what to do
//      next, not only what went wrong
//  14. Quiet Means Minimal UX, Not Different Semantics:
//      `-q` may suppress presentation, prompts, and tree output, but it should
//      not change what files are selected
//  15. Interactive Recovery State Must Be Reversible:
//      invalid interactive input must never poison the current scope state or
//      silently mutate the command being built
//  16. Same Payload, Different Sink:
//      stdout and clipboard modes may differ in transport cost, but they
//      should emit the same payload bytes for the same resolved selection
//  17. Bundled Tooling Is Part of the Product:
//      packaged installs must carry private fzf + ripgrep binaries; runtime
//      should not silently fall back to arbitrary PATH copies, because PATH
//      fallback reintroduces version drift, machine-specific behavior, weaker
//      install guarantees, and harder debugging/support
//  18. Picker Previews Stay POSIX-Free:
//      any command string handed to fzf — preview commands, `start:reload:` /
//      `change:reload:` bindings, future bind actions — must be a straight
//      pipeline of program invocations with placeholder substitution, never a
//      POSIX shell script. fzf forwards these to `cmd /s /c` on Windows by
//      default, so `set --`, `"$@"`, `if [ -n "$x" ]`, `for v in {+2}`, and
//      shell variable assignments (`name={N}`) all silently break previews
//      under cmd.exe. Push any conditional logic into Go-side `--internal-*`
//      subcommands instead, and verify the placeholders ({2}, {3}, {+2}, {q})
//      can be inlined directly. The working reference shapes are
//      `fzfPreviewCommand`, `recentPickerPreviewCommand`, and
//      `startupModifierCurrentScopePreviewCommand`.
//
// WHY THE ROOT PACKAGE IS STILL BROAD:
//   - resolver, discovery, ignore, git, and rendering still share many internal
//     types and evolve together
//   - tree/preview/rendering behavior is still changing, so freezing package
//     APIs now would create churn without reducing complexity
//   - extracting packages too early would force a lot of exports or awkward
//     shared "types" packages, which is usually a sign the boundary is not ready
//
// CURRENT FILE GROUPS:
//   - cli/help:          arg parsing, entry flow, prompts, help/version output
//   - command_spec:      declarative scope/flag parsing and validation
//   - flag_metadata:     flag specs, stage kinds, modifier boundary detection
//   - startup_picker:    startup fzf recovery, modifier chaining, resolved-command echo
//   - startup_preflight: pre-parse validation before full command spec
//   - startup_file_set_normalization: dedup redundant interactive file-set selections
//   - positional_glob_normalization: classify and pass through glob pattern targets
//   - resolver:          target resolution (path, glob, fzf) and file discovery
//   - discovery:         walking, visibility indexes, file classification
//   - content/ripgrep:   content filtering, snippet extraction, rg-backed helpers
//   - scope_stages:      stage execution (only, exclude, recent, depth, contains, etc.)
//   - scope_order_rules: ordering constraints and boundary policies between stages
//   - output_plan:       plan what to emit (full files, paths, snippets, diffs, raw)
//   - ignore/git:        .hiss loading, git-aware filtering, changed-file logic
//   - depth_stage/depth_picker: --depth filtering and interactive depth selection
//   - recent_stage/recent_picker: --recent sorting/limiting and interactive selection
//   - preview/emit:      preview tree, summaries, clipboard/stdout output
//   - validation_error:  structured validation failures with actionable messages
//   - spinner:           short-lived TTY loading indicators
//
// EXECUTION FLOW:
//   1. Parse args and build scopes (via CommandSpec / FlagSpec declarative model)
//   2. For interactive TTY runs, decide whether a token is already exact enough
//      to bypass fzf:
//      - exact existing paths win immediately
//      - exact basename file hits can also bypass the picker
//      - glob patterns (*, ?, [) bypass the picker and resolve directly
//      - only genuinely ambiguous / shorthand queries go to fzf
//   3. Resolve each target into either:
//      - an exact file
//      - an exact directory subtree
//      - a glob pattern matched against all visible files
//      - an fzf-backed fuzzy selection
//      - an --include-allowed ignored target
//   4. Discover files for resolved targets:
//      - rg is the primary engine for visible file enumeration
//      - exact visible directory targets also use rg-backed subtree discovery
//      - rg is also used for exact basename lookup and --contains matching
//      - Go walks are still used where directory objects matter:
//        ignored-target browsing and some exact ignored / include-allowed
//        directory cases
//      - symlinks are currently excluded everywhere by policy
//      - visible directory targets are derived from the visible file set rather
//        than a separate directory walk, so there is no standalone visible-dir
//        walk in the hot path; they inherit both rg/.gitignore visibility and
//        catclip's .hiss filtering
//      - consequence: empty directories, or directories with no surviving text
//        files, are intentionally excluded from the visible picker
//   5. Apply cheap file eligibility checks first:
//      - ignore rules
//      - --only / --exclude
//      - known binary basename/extension denylist
//      - known text basename/extension allowlist
//   6. Fall back to byte sniffing only for unknown file types:
//      - text sniffing is cached for the duration of the run
//      - the same file should only be sniffed once per command
//      - this is also why newer Linux repository runs include more assembly
//        source than older catclip runs: rg only enumerates candidates, but
//        `isLikelyTextFile(...)` now admits `.S` / `.lds.S` through the known
//        text allowlist because extension matching is shell-style and
//        case-insensitive (`.S` normalizes to `s`), instead of relying on the
//        old byte-sniff fallback to recognize them as text
//   7. Keep ripgrep-backed candidate entries lightweight:
//      - picker/index candidates are stored with RelPath first
//      - AbsPath is materialized only when a file survives to real work
//        like --contains, preview sizing, snippets/diffs, or final emission
//   8. Apply scope stages in order (left to right within each scope):
//      - `--include` adds authorized ignored paths (must be first, once per scope)
//      - `--only` / `--exclude` run as sequential file-set stages
//      - `--recent N` sorts by mtime, keeps top N
//      - `--depth N` removes files deeper than N path segments from cwd
//      - `--contains` filters by content match (regex, rg-backed)
//      - `--snippet` extracts blank-line-bounded blocks matching regex
//      - `--lines [START [END]]` slices each surviving file to that 1-based
//        line range
//      - git selectors (`--changed`, `--staged`, `--unstaged`, `--untracked`)
//      - output shape: `--paths` (terminal), `--*-diff`, or default full-file
//      - `--only -`, `--exclude -`, `--include -` read exact paths from stdin
//      - interactive file-set selections are normalized before argv emission:
//        redundant literals covered by a selected pattern are dropped
//   9. Build preview metadata and render the tree/summary when needed:
//      - normal `-q` runs skip tree rendering and confirmation entirely
//      - `-q` therefore makes `-y` and `-t` redundant in normal non-preview
//        runs
//      - preview/tree-specific metadata such as git status is only collected
//        when a tree will actually be rendered
//      - preview Git badges are selector-aligned rather than rename-detailed:
//        `[S]`, `[M]`, `[?]`, and `[SM]` are the states that matter because
//        they map to `--staged`, `--unstaged`, `--untracked`, and files that
//        are both staged and unstaged; catclip does not currently show a
//        dedicated rename badge
//      - size/token summary is still computed even without a tree because the
//        Count / Size / Tokens disclaimer depends on it; token counting remains
//        a fast byte-based estimate (`bytes / 4`) on purpose, because exact
//        tokenizers would add noticeable work while the real hot cost here is
//        gathering file sizes, not formatting the final number
//  10. Emit output to stdout or the clipboard sink:
//      - default: full file contents in <file path="..."> wrappers
//      - `--paths`: bare relative paths, one per line
//      - `--snippet`: matched blocks in <file path="..." lines="L-L"> wrappers
//      - `--lines`: line-sliced bodies in <file path="..." lines="L-L"> wrappers
//      - `--*-diff`: unified diff patches in <file path="..." type="diff"> wrappers
//      - `--raw` (`-r`): bare file bodies, no wrappers; multi-file concatenates
//        contiguously like `cat a b`; with `--lines`, line-number prefixes are
//        stripped (numbered-but-unwrapped is unsafe across files)
//      - unresolvable targets: warn on stderr (even with `-q`), emit what
//        resolved, exit 1
//
// GIT / RG PERFORMANCE RULES:
//   - do not reintroduce git check-ignore into the normal visible-file hot path
//   - for safe visible discovery, trust rg's .gitignore handling and then apply
//     .hiss in Go
//   - git check-ignore is reserved for narrow cases only:
//     exact ignored-target diagnostics, ignored-target browsing, and other
//     explicit include / allow-by-include flows
//   - a previous git cat-file --batch fast-path experiment was benchmarked and
//     was substantially slower than direct working-tree streaming for catclip's
//     "wrap and emit many files" workload; do not assume Git blob batching is
//     an optimization here without new measurements
//   - preview/tree badge collection now narrows `git status --porcelain` to
//     selected roots/pathspecs when the path list is small enough, and only
//     falls back to repo-wide porcelain for broad/unsafe path sets or Git
//     command failure; this materially improved tree-enabled scoped runs
//   - drawback/tradeoff of the narrowed porcelain path:
//     boundary-crossing rename/copy cases can be less complete than repo-wide
//     status, but because the tree only cares about staged / unstaged /
//     untracked selector states today, and not a dedicated rename badge, that
//     trade is acceptable; future rename-specific UI should revisit these
//     fallback rules carefully
//
// OUTPUT PIPELINE RULES:
//   - full-file emission uses bounded read-side concurrency, but exactly one
//     goroutine writes to the sink
//   - the default read worker count is 2:
//     tracked-linux benchmarks showed large wins at 2/4/8 workers, but 2 is
//     the safer cross-machine default because it still overlaps reads while
//     being less likely to thrash spinning disks than 4 or 8
//   - benchmark takeaway:
//     2 workers delivered the major jump; 4 and 8 improved things further but
//     with much smaller gains, so higher defaults are harder to justify
//   - integrity rules for future changes:
//     multiple readers are fine, but preserve exactly one writer, complete
//     per-file buffers only, immutable handoff from worker to writer, ordered
//     commit, and loud failure on read error
//   - future output corruption risk comes from:
//     multiple sink writers, shared/reused mutable buffers, out-of-order commit,
//     silent skip/retry logic, or reading files that are being modified mid-run
//   - clipboard note:
//     on macOS, giant clipboard runs are now mostly limited by `pbcopy` /
//     pasteboard wait time, not catclip's own payload generation; this matters
//     for pathological full-repo copies like `catclip .` on Linux repository
//     checkouts, not "Linux the OS". At `vscode-main` scale the clipboard wait
//     was about 1.2s and effectively negligible for normal use.
//
// KNOWN REMAINING COSTS:
//   - exact basename lookup still has its own rg pass separate from the picker
//   - ignored-target browsing still uses a full Go walk by design
//   - preview tree runs still pay per-file size collection, and large or broad
//     tree requests can still fall back to repo-wide git status collection;
//     quiet/no-tree paths avoid that cost
//   - on large clean repos, output emission is currently the dominant cost, not
//     visible-file discovery

// PATH / PICKER RULES:
//   - in an interactive TTY, bare `catclip` opens the target selector with
//     `[select all files]` first; non-interactive runs still default to `.`
//   - exact existing targets like `.`, `src`, or `dir/file` should run
//     directly instead of opening fzf
//   - slashless shorthand like `common`, `btn`, or `node` is picker territory
//   - trailing slash has no special picker meaning:
//     overloading `dir/` as "directories only" or treating `src/`
//     differently from `src` was a bad plan and is intentionally rejected;
//     we also rejected the idea that `dir/` should scope the picker to "all
//     files under that dir" as a special mode; exact paths should
//     stay exact, scoped path targets like `layout/Footer.tsx` still use
//     normal resolution, and fuzzy file/directory discovery should be handled
//     by fzf rather than by slash punctuation; if directory-only or
//     directory-first picker modes return later, they should use explicit
//     flags or picker toggles instead of path punctuation
//   - the normal picker is visible-only
//   - ignored targets require explicit `--include` authorization; in the picker
//     flow they are reached through the ignored-target path rather than mixed
//     into the safe list by default
//   - packaged installs are expected to resolve fzf/rg from app-private paths;
//     env overrides remain for tests and developer runs, but there is no normal
//     user-facing PATH fallback
//   - bare `--include` opens ignored-target selection for the current scope
//   - `.` means "all safe targets" and suppresses further safe-target picking,
//     but it must not suppress ignored-target browsing
//   - scope coverage is literal and subtree-based:
//     selecting `src/vs` covers only `src/vs/...`, not all of `src/...`, so a
//     later `--then src` is valid and should remain available
//   - exact overlapping scopes are allowed in scripting mode even when a later
//     scope is already covered by an earlier one; final payload is still
//     deduped by path, but the command should keep the user's literal scope
//     structure
//   - `--then` is a true fresh scope boundary:
//     treat it like starting a brand new catclip command on the same line, then
//     unioning the final file sets; interactive recovery must not turn it into
//     "remaining files only"
//   - current interactive continuation exclusion is target-based, not
//     result-set-based, within the current scope:
//     later pickers in the same scope exclude previously selected target
//     paths/subtrees, but do not evaluate prior modifiers like `--only`,
//     `--exclude`, `--contains`, `--changed`, `--snippet`, or `--diff` before
//     deciding what counts as "already covered"
//   - consequence of that simplification within one scope:
//     `src --only "*.ts"` still makes later same-scope picker logic treat all
//     of `src` as covered, and prior `.` still means "all safe targets are
//     covered" for same-scope continuation purposes, even if that scope would
//     later be narrowed by modifiers
//   - bare value-taking modifiers can recover interactively in a TTY:
//     `--include`, `--only`, `--exclude`, `--contains`, and bare `--`
//   - repeated bare `--` placeholders are allowed and each inserts one more
//     modifier stage
//   - `--headless` is the explicit no-prompt switch:
//     it forbids any interactive picker (startup recovery, target picker,
//     modifier value pickers) and requires explicit targets up front; agents
//     and scripts pass it to guarantee fzf never opens, and any code path
//     that reaches a prompt under `--headless` is a bug, not a fallback.
//     `-q` is independent and does not by itself disable prompts
//   - git selectors chosen from startup recovery may open follow-up file-set
//     pickers, but the canonical resolved command still compiles back to normal
//     CLI syntax
//
// NOTES FOR FUTURE CODEX/CLAUDE PASSES:
//   - preserve user-facing semantics over "cleaner" abstractions; if a refactor
//     changes target meaning, preview truthfulness, or startup recovery
//     behavior, it is
//     probably the wrong refactor
//   - only extract an internal package when it can own a small API without
//     exporting half the app's internals; if a split mainly moves files around,
//     it is premature
//   - if tree/render UX is still in flux, keep it close to the rest of the app
//     until the behavior settles
//   - benchmark explicit binaries, not just whatever `catclip` in PATH points
//     to; old installed binaries can silently invalidate before/after results
//   - benchmark the path you actually changed: tree/porcelain optimizations
//     must be measured with tree-enabled commands, not `-q -t` runs that skip
//     that work entirely
//   - when file counts or tree contents change, investigate selection and
//     classification first; output-path changes should not change what files
//     are selected
//   - for Git-performance work, use real tracked clones as the primary testbed;
//     odd, partially detached, or effectively untracked trees can hide or
//     distort Git costs
//   - before adding a Git-based "optimization", compare it against the current
//     rg + direct-filesystem baseline on a real tracked clone, not only on odd
//     working trees; previous `git cat-file --batch` experiments lost badly
//   - clipboard benchmarks must separate catclip generation time from clipboard
//     backend wait; large macOS runs were dominated by `pbcopy` wait, not by
//     catclip's own payload generation
//   - interactive input must be validated on a candidate copy before mutating
//     startup state; invalid choices should surface a clear error without
//     poisoning the current scope or command
//   - if startup recovery grows further, preserve deterministic CLI semantics,
//     file-set stage ordering, and the rule that `--then` behaves like a fresh
//     command boundary rather than a subtraction operator
// =============================================================================

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// =============================================================================
// Constants and types
// =============================================================================

type action string

const (
	actionRun       action = "run"
	actionHelp      action = "help"
	actionHelpAll   action = "help-all"
	actionVersion   action = "version"
	actionEditHiss  action = "hiss"
	actionResetHiss action = "hiss-reset"
)

type outputMode string

const (
	outputModeClipboard outputMode = "clipboard"
	outputModeStdout    outputMode = "stdout"
)

type runConfig struct {
	Action     action
	Version    string
	Platform   string
	WorkingDir string
	OutputMode outputMode

	Verbose          bool
	Quiet            bool
	Headless         bool
	WithBinaries     bool
	Yes              bool
	Raw              bool
	Preview          bool
	NoTree           bool
	TreePayload      bool
	TreeTarget       string
	TreeKind         string
	TreeState        string
	FilePreview      bool
	FilePath         string
	ContentMatchList bool
	RecentPreview    bool
	RecentData       string
	RecentSelect     string

	Command commandSpec

	Warnings []string
}

type executionScope struct {
	Targets         []string
	IncludedTargets []string
	Only            []string
	Exclude         []string
	Contains        string
	SnippetPattern  string
	Lines           bool
	LinesStart      int
	LinesEnd        int
	Stages          []scopeStage
	Paths           bool
	Snippet         bool
	Changed         bool
	Staged          bool
	Unstaged        bool
	Untracked       bool
	Diff            bool
}

func executionScopeOutputMode(s executionScope) entryMode {
	if s.Diff {
		return entryModeDiff
	}
	if s.Snippet {
		return entryModeSnippet
	}
	if s.Lines {
		return entryModeLines
	}
	return entryModeFull
}

func executionScopeHasGitSelection(s executionScope) bool {
	return s.Changed || s.Staged || s.Unstaged || s.Untracked
}

type executionScopeBuilder struct {
	executionScope
	explicitTargets int
}

type ignoreRuleKind string

const (
	ignoreRuleFile ignoreRuleKind = "file"
	ignoreRuleDir  ignoreRuleKind = "dir"
)

type ignoreRule struct {
	Raw     string
	Kind    ignoreRuleKind
	Pattern string
}

type compiledGlob struct {
	raw string
	re  *regexp.Regexp
}

type compiledDirRule struct {
	raw      string
	segments []*regexp.Regexp
}

type scopeMatcher struct {
	ignoreFiles []compiledGlob
	ignoreDirs  []compiledDirRule
}

type scopeStageKind string

const (
	scopeStageInclude      scopeStageKind = "include"
	scopeStageOnly         scopeStageKind = "only"
	scopeStageExclude      scopeStageKind = "exclude"
	scopeStageRecent       scopeStageKind = "recent"
	scopeStageDepth        scopeStageKind = "depth"
	scopeStageContains     scopeStageKind = "contains"
	scopeStageChanged      scopeStageKind = "changed"
	scopeStageStaged       scopeStageKind = "staged"
	scopeStageUnstaged     scopeStageKind = "unstaged"
	scopeStageUntracked    scopeStageKind = "untracked"
	scopeStagePaths        scopeStageKind = "paths"
	scopeStageDiff         scopeStageKind = "diff"
	scopeStageSnippet      scopeStageKind = "snippet"
	scopeStageChangedDiff  scopeStageKind = "changed-diff"
	scopeStageStagedDiff   scopeStageKind = "staged-diff"
	scopeStageUnstagedDiff scopeStageKind = "unstaged-diff"
	scopeStageLines        scopeStageKind = "lines"
)

type scopeStage struct {
	Kind        scopeStageKind
	Values      []string
	Limit       *int
	ExactValues bool
}

type fileEntry struct {
	AbsPath          string
	RelPath          string
	ModTime          time.Time
	TargetRoot       string
	GitVisible       bool
	Mode             entryMode
	SnippetPattern   string
	Lines            bool
	LinesStart       int
	LinesEnd         int
	DiffWantStaged   bool
	DiffWantUnstaged bool
	AllowedByInclude bool
	BlockRule        string
	BlockSource      string
}

type gitContext struct {
	Enabled    bool
	Root       string
	WorkPrefix string
	HasHead    bool
}

type colorPalette struct {
	Reset  string
	Bold   string
	Dim    string
	OK     string
	Err    string
	Warn   string
	Dir    string
	Label  string
	Value  string
	Tree   string
	Prompt string
	Git    string
}

type outputReport struct {
	sizes     map[string]int64
	statuses  map[string]string
	modeTags  map[string]string
	humanSize string
	tokens    int64
	countWord string
	notices   []string
}

type visibleDirIndex struct {
	dirs        []string
	set         map[string]struct{}
	symlinkDirs []string
}

type visibleFileIndex struct {
	byBase        map[string][]fileEntry
	skippedByBase map[string][]skippedMatch
}

type blockInfo struct {
	Rule   string
	Source string
}

type skippedMatch struct {
	RelPath     string
	BlockRule   string
	BlockSource string
	BlockKind   string
}

type gitIgnoreMatch struct {
	Rule    string
	DirRule bool
}

type targetMatch struct {
	Path         string
	Kind         string
	State        string
	Ignored      bool
	IgnoreSource string
}

type usageError struct {
	message string
}

type exitError struct {
	message string
	code    int
}

// Diagnostics are collected in encounter order so stderr matches the shell's
// target-by-target flow. Some of them are real "Error:" blocks that should
// still print under --quiet even when soft warnings are suppressed.
type diagnostic struct {
	message          string
	isError          bool
	isTargetNotFound bool
}

type entryMode string

const (
	entryModeFull    entryMode = "full"
	entryModeLines   entryMode = "lines"
	entryModeSnippet entryMode = "snippet"
	entryModeDiff    entryMode = "diff"
)

const tokenWarnThreshold = 100000

func (e usageError) Error() string {
	return e.message
}

func (e exitError) Error() string {
	return e.message
}

func newUsageError(format string, args ...any) error {
	return usageError{message: fmt.Sprintf(format, args...)}
}

func newExitError(code int, message string) error {
	return exitError{message: message, code: code}
}

// =============================================================================
// Main entrypoint
// =============================================================================

// Main parses the CLI and runs the selected action.
func Main() {
	if err := ensureRequiredTools(os.Stderr); err != nil {
		os.Exit(1)
		return
	}
	args := os.Args[1:]
	normResult, err := normalizePositionalGlobArgs(args, positionalGlobArgsQuiet(args))
	if err != nil {
		exitWithError(err, os.Stderr)
		return
	}
	args = normResult.Args
	startupResult := startupPickerResult{Args: args}
	handled := false
	startupResult, handled, err = maybeResolveStartupPickerArgs(args)
	if err != nil {
		exitWithError(err, os.Stderr)
		return
	} else if handled {
		if startupResult.Args == nil {
			return
		}
		args = startupResult.Args
	}

	cfg, err := parseArgs(args)
	if err != nil {
		exitWithError(err, os.Stderr)
		return
	}
	if !cfg.Quiet {
		for _, hint := range normResult.Hints {
			if _, err := fmt.Fprintln(os.Stderr, hint); err != nil {
				exitWithError(err, os.Stderr)
				return
			}
		}
	}
	if startupResult.UsedFzf && !cfg.Quiet {
		if err := writeResolvedStartupCommand(os.Stderr, args); err != nil {
			exitWithError(err, os.Stderr)
			return
		}
	}

	if err := run(cfg, os.Stdout, os.Stderr); err != nil {
		exitWithError(err, os.Stderr)
	}
}

func rawArgsHasHeadless(args []string) bool {
	for _, arg := range args {
		if arg == "--headless" {
			return true
		}
	}
	return false
}

func rawArgsUseStdinPathValues(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--include", "--only", "--exclude":
			values, next := consumeModifierValues(args, i+1)
			if len(values) == 1 && values[0] == "-" {
				return true
			}
			i = next - 1
		}
	}
	return false
}

func writeResolvedStartupCommand(stderr io.Writer, args []string) error {
	if len(args) == 0 {
		return nil
	}
	_, err := fmt.Fprintf(stderr, "Resolved command:\n  %s\n", formatResolvedStartupCommand(args))
	return err
}

func formatResolvedStartupCommand(args []string) string {
	if cfg, err := parseArgsAllowImplicitDot(args); err == nil {
		return formatCanonicalResolvedCommand(args, cfg)
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "catclip")
	for _, arg := range args {
		parts = append(parts, shellQuoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func formatCanonicalResolvedCommand(rawArgs []string, cfg runConfig) string {
	parts := make([]string, 0, len(rawArgs)+4)
	parts = append(parts, "catclip")
	for _, arg := range resolvedCommandGlobalFlags(rawArgs) {
		parts = append(parts, shellQuoteArg(arg))
	}
	for i, scopeSpec := range configCommandScopes(cfg) {
		if i > 0 {
			parts = append(parts, "--then")
		}
		s := executionScopeFromCommandScopeSpec(scopeSpec)
		parts = append(parts, canonicalScopeArgs(s)...)
	}
	return strings.Join(parts, " ")
}

func canonicalScopeArgs(s executionScope) []string {
	parts := make([]string, 0, len(s.Targets)+len(s.Stages)*2)
	for _, target := range s.Targets {
		parts = append(parts, shellQuoteArg(target))
	}
	for _, stage := range s.Stages {
		switch stage.Kind {
		case scopeStageInclude:
			parts = append(parts, "--include")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case scopeStageOnly:
			parts = append(parts, "--only")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case scopeStageExclude:
			parts = append(parts, "--exclude")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case scopeStageRecent:
			parts = append(parts, "--recent")
			if stage.Limit != nil {
				parts = append(parts, shellQuoteArg(strconv.Itoa(*stage.Limit)))
			}
		case scopeStageDepth:
			parts = append(parts, "--depth")
			if stage.Limit != nil {
				parts = append(parts, shellQuoteArg(strconv.Itoa(*stage.Limit)))
			}
		case scopeStageContains:
			parts = append(parts, "--contains")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case scopeStagePaths:
			parts = append(parts, "--paths")
		case scopeStageSnippet:
			parts = append(parts, "--snippet")
			for _, value := range stage.Values {
				parts = append(parts, shellQuoteArg(value))
			}
		case scopeStageChanged:
			parts = append(parts, "--changed")
		case scopeStageStaged:
			parts = append(parts, "--staged")
		case scopeStageUnstaged:
			parts = append(parts, "--unstaged")
		case scopeStageUntracked:
			parts = append(parts, "--untracked")
		case scopeStageDiff:
			parts = append(parts, "--diff")
		case scopeStageChangedDiff:
			parts = append(parts, "--changed-diff")
		case scopeStageStagedDiff:
			parts = append(parts, "--staged-diff")
		case scopeStageUnstagedDiff:
			parts = append(parts, "--unstaged-diff")
		}
	}
	return parts
}

func resolvedCommandGlobalFlags(rawArgs []string) []string {
	out := make([]string, 0, 6)
	for _, arg := range rawArgs {
		switch arg {
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--preview", "--with-binaries":
			out = append(out, arg)
		}
	}
	return out
}
