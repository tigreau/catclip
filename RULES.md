# catclip — Architecture & Product Rules

catclip is a context gatherer for LLMs. This is the Go rewrite of the original Bash implementation.

## Layout

- `cmd/catclip/main.go` is the thin binary entrypoint.
- The implementation lives in package `catclip` at the module root.
- Files are split by responsibility, but most code still shares one package.

### File groups

Listed roughly in pipeline order (rule 21).

Parsing and validation:

- **cli / help**: arg parsing, entry flow, prompts, help/version output
- **command_spec**: declarative scope/flag parsing and validation
- **flag_metadata**: flag specs, stage kinds, modifier boundary detection
- **positional_glob_normalization**: classify and pass through glob pattern targets
- **startup_preflight**: pre-parse validation before full command spec

Interactive startup pipeline:

- **startup_picker**: startup fzf recovery, modifier chaining, resolved-command echo
- **startup_file_set_normalization**: dedup redundant interactive file-set selections and dynamic pattern inference (literal → wildcard collapse)
- **startup_sink_picker**: interactive output-sink picker (clipboard / text-clipboard / stdout / headless)
- **startup_undo**: Esc step-back through the picker chain
- **prepared_output**: prepared-output handoff between the startup picker and the run pipeline

Resolution and discovery:

- **resolver**: target resolution (path, glob, fzf) and file discovery
- **discovery**: walking, visibility indexes, file classification
- **ripgrep**: rg invocations (visible-file enumeration, NUL-byte text classification, content matching)
- **ignore / git**: `.hiss` loading, git-aware filtering, changed-file logic
- **bundled_tools**: app-private fzf/rg discovery and toolchain capability checks (PCRE2, `multi:refresh-preview`)

Scope and stages:

- **scope_stages**: stage execution (only, exclude, recent, depth, contains, etc.) via composable `stageApplierTable`
- **scope_order_rules**: ordering constraints and boundary policies between stages
- **content / snippet_resolution**: content filtering and snippet block extraction
- **depth_stage / depth_picker**: `--depth` filtering and interactive depth selection
- **recent_stage / recent_picker**: `--recent` sorting/limiting and interactive selection
- **lines_picker**: two-stage `--lines` (start → end) picker
- **contains_list / contains_usage**: `--contains` / `--snippet` picker support, regex hint surfaces, and ordering suggestions
- **internal_prediscovered**: SCC checkpoint serialization so picker previews never re-run `evaluateScope`

Pipeline coordination:

- **run_discovery**: drives `resolvedInvocation → discoveredInvocation` (rule 21)
- **output_plan**: plan what to emit (full files, paths, snippets, diffs, raw)
- **run_output**: drives `outputPlan → render → emit` (rule 21)

Rendering surfaces:

- **preview**: confirmation-flow tree summary, badges, size/token disclaimer
- **preview_table**: the `--preview` file table (path + size/tokens/git/modified/shape); replaces the tree on `--preview` only
- **date_format**: shared timestamp / duration / Finder-style date formatting (`--recent`, `--preview`, `--verbose`, bundle names)
- **file_preview**: per-file preview rendering for pickers
- **resolved_scope_view**: human-readable resolved-scope summary
- **text_snapshot**: load-once text snapshot used by snippet and lines rendering
- **tree_bridge / tree_payload_emit**: bridge to the `catclip-tree` subprocess that renders picker tree previews
- **command_render**: canonical "Resolved command:" rendering (rule 22)
- **fzf_theme**: fzf color theme parsing

Output and diagnostics:

- **emit**: clipboard/stdout writers, bounded-concurrency read path
- **fileclip/** (subpackage): file-reference clipboard module (bundles above 4 KB; Linux Wayland uses live `text/uri-list`, X11 returns `ErrX11Unsupported`)
- **diagnostic_policy**: classify which diagnostics print under `-q` vs default
- **validation_error**: structured validation failures with actionable messages
- **spinner**: short-lived TTY loading indicators

Subpackages and platform shims:

- **internal/picker, internal/tree**: package extractions broken out from the root package
- **cmd/catclip, cmd/catclip-tree**: thin binary entrypoints
- **editor_command / editor_command_nonwindows / editor_command_windows**: `--hiss` / `--hiss-reset` editor invocation
- **terminal_unix / terminal_windows**: platform TTY/terminal capability shims

### Why the root package is still broad

- resolver, discovery, ignore, git, and rendering still share many internal types and evolve together.
- tree/preview/rendering behavior is still changing, so freezing package APIs now would create churn without reducing complexity.
- extracting packages too early would force a lot of exports or awkward shared "types" packages, which is usually a sign the boundary is not ready.

## Product rules

1. **Preview must be truthful** — the tree, the `--preview` table, and the summary must reflect exactly what will be copied, not a looser approximation.

2. **Current paths win** — the UI should show the current filesystem view and current working-tree paths, even when Git metadata is simplified.

3. **Selectors drive status UI** — tree badges should optimize for `--changed`, `--staged`, `--unstaged`, and `--untracked`, not raw porcelain fidelity. The badges that matter today are `[S]`, `[M]`, `[?]`, and `[SM]`; there is no dedicated rename badge.

4. **Specificity grants access** — safe discovery stays safe; ignored content only enters through explicit `--include`.

5. **Exact beats fuzzy** — exact existing paths and exact basename hits should execute directly; genuinely ambiguous fuzzy resolution belongs to fzf, not local heuristics.

6. **One scope, one meaning** — targets come first, modifiers apply to that scope, and `--then` starts a new scope. Treat `--then` like starting a brand new catclip command on the same line and unioning the results. Scope grammar must stay deterministic, and coverage is path-subtree based rather than "conceptually related" (selecting `src/vs` does not imply that later selecting `src` is redundant).

7. **Stage order is semantic** — within one scope, `--only` and `--exclude` are sequential stages, not a globally merged rule set. Values inside one occurrence OR together, and later occurrences run after earlier ones.

8. **Single writer integrity** — output order must stay deterministic, with one sink writer and loud failure on read/write errors.

9. **Safe path is fast path** — normal visible discovery should stay on the optimized `.gitignore` + `.hiss` path: rg enumerates Git-visible files, and `.hiss` is applied via `rg --ignore-file`. Slower ignored/bypass flows are acceptable off the hot path.

10. **Interactive is a convenience layer** — catclip is both a scripting CLI and an interactive tool, so complete deterministic commands must remain directly executable; startup fzf only helps resolve ambiguity or unfinished human input.

11. **Classification is rg's job** — text/binary classification flows entirely through rg's NUL-byte detection (`rg --files-without-match --text -e '\x00'`). The rg answer is authoritative — no per-file Go byte-sniff fallback, and no Go-side name-based allowlist. See `docs/architecture/ACTIVE_NOTE_ripgrep_is_required.md`.

12. **No silent skips** — if catclip excludes something significant, that should remain a deliberate product decision, not an accidental side effect.

13. **Diagnostics must be actionable** — when catclip does show a diagnostic, it should tell the user what to do next, not only what went wrong.

14. **Quiet means minimal UX, not different semantics** — `-q` may suppress presentation, prompts, and tree output, but it should not change what files are selected.

15. **Interactive recovery state must be reversible** — invalid interactive input must never poison the current scope state or silently mutate the command being built.

16. **Same payload, different sink** — stdout and clipboard modes may differ in transport cost, but they should emit the same payload bytes for the same resolved selection.

17. **Bundled tooling is part of the product** — packaged installs must carry private fzf + ripgrep binaries; runtime should not silently fall back to arbitrary PATH copies. PATH fallback reintroduces version drift, machine-specific behavior, weaker install guarantees, and harder debugging/support.

18. **Picker previews stay POSIX-free** — any command string handed to fzf (preview commands, `start:reload:` / `change:reload:` bindings, future bind actions) must be a straight pipeline of program invocations with placeholder substitution, never a POSIX shell script. fzf forwards these to `cmd /s /c` on Windows by default, so `set --`, `"$@"`, `if [ -n "$x" ]`, `for v in {+2}`, and shell variable assignments (`name={N}`) all silently break previews under cmd.exe. Push any conditional logic into Go-side `--internal-*` subcommands instead, and verify the placeholders (`{2}`, `{3}`, `{+2}`, `{q}`) can be inlined directly.

    Reference shapes: `startupCheckpointFileSetPreviewCommand`, `fzfCheckpointContentMatchListCommand`, `buildDepthPickerPreview`, `recentPickerPreviewCommand`.

    A corollary that has regressed and been re-fixed four times (see `docs/versions/v0.5.0/reports/RESOLVED_BUG_windows_preview_posix_shell.md`). It has **two** parts, and both are load-bearing:

    1. **Standalone token.** Every fzf placeholder (`{2}`, `{+2}`, `{q}`, …) must be a whitespace-delimited token, never concatenated into a compound string (`dir/{2}.json`, `'{2}'.json`, `name={2}`).
    2. **Trivial value.** The value fzf substitutes per refresh must itself be trivial — a bare number/key with no spaces, backslashes, or shell metacharacters. The hazardous part (an absolute temp path: `%TEMP%` on Windows can contain spaces) is passed as a *fixed* argument that catclip quotes via `shellQuoteArg` at build time; only the trivial value goes through fzf's `cmd /s /c` substitution. This is why `catclip-tree` uses `--input-dir <fixed-quoted-dir> --input-stem {2}` and joins in Go — **not** `--input-file {N}` with a full-path column, which would push a spaced/backslashed path through fzf's cmd.exe quoting. Keep `--input-dir`/`--input-stem`; see `docs/versions/v0.6.0/reports/ACTIVE_PLAN_unify_preview_rendering.md` for why the `--input-file {N}` and combined-file alternatives were rejected.

    **Enforced (part 1 only):** `TestRunPipelineArchitectureGuards` (`requirePreviewPlaceholdersStayStandalone`) scans every command builder in `previewCommandBuilders` for embedded placeholders and POSIX shell fragments, and a meta-check fails the build if a new placeholder-using function in a builder file isn't classified as either a shell-command builder or an fzf-native exemption (`fzfPlaceholderExemptFuncs`, e.g. `--preview-window` offsets). An agent that reintroduces `dir/{2}.json` fails the test. **Part 2 (trivial value) is not statically enforceable** — whether a substituted value is trivial depends on runtime row data — so passing the guard means "not concatenated," *not* "safe to push any value through fzf." Keep substituted values trivial by design.

19. **Discovery is the parent's job, once** — every fzf modifier picker (`--depth`, `--recent`, `--only`, …) runs `evaluateScope` exactly once in the parent and writes the result to a temp file. The fzf preview command then reads that file and formats — nothing more.

    A preview command that invokes `catclip` with scope-bearing arguments in a free-form modifier hot path is broken by definition: fzf re-runs preview on every cursor move, so any path that ends up calling `evaluateScope` from there means hundreds of milliseconds of rg + classification + stage application per keystroke. Legacy commands may remain only as explicit fallback/exempt paths (target selection before a settled scope, per-file previews, static menu preview). The bug is invisible on small repos and crippling on large ones, so the rule has to be load-bearing at design time.

    `--recent` is the reference: parent serializes entries via `writeRecentPreviewData`, preview reads the TSV and formats. Pre-rendered tree payloads per bucket (one file per picker row) are the same pattern applied to richer rendering.

    **Enforced:** `TestRunPipelineArchitectureGuards` (`requireInternalRenderHandlersAvoidDerivation`) asserts the per-refresh render handlers — `runInternalPrediscoveredTreePayload`, `runInternalLinesPreview`, `runInternalPrediscoveredContentMatchList`, `runInternalFilePreview`, `runInternalContentCheckpointTreePayload`, `runInternalRecentPreview`, `runInternalSinkPreview`, `runInternalSinkToggle` — never call `evaluateScope` or `discoverInvocation`. Picker *drivers* (run once at picker open) and the content-match `change:reload:` search path legitimately discover and are deliberately exempt. This is the entry-point contract: render precomputed payloads, never derive. See `docs/versions/v0.5.5/reports/ACTIVE_PLAN_internal_picker_entrypoint_contract.md`.

20. **State lives in the args string** — modifier chaining is string concatenation; file sets are derived on demand via a functional `evaluateScope`. The chain a user has built up is fully represented by `currentArgs []string` carried across picker iterations — nothing mutable lives between them. Undo (Esc steps back through the picker chain) is shipped; redo, what-if branching, session replay, and serialization remain trivially implementable but unshipped.

    Caches keyed on *stable* inputs (workingDir, hissPath, scope targets — like `visibleFileSetCache` and `textFileSetCache`) are fine; their keys do not change with modifier chaining. What is forbidden is any cross-iteration cache whose key includes a modifier value or accumulated stage output — e.g., memoizing `(src + --only=*.go) → entries`. That kind of cache turns the modifier loop into a state machine that has to be invalidated on undo. If you find yourself wanting to "remember the entries from before the last stage was applied," you have broken the property — fix the design.

21. **Pipeline linearity** — every catclip flow runs the same stages in the same order: `parsedCommand → resolvedInvocation → discoveredInvocation → outputPlan → render → emit`. No god-object carries mutable state across stages, no stage reads back into an earlier one, and no shortcut path fuses two stages by skipping the boundary. The boundary types are the contract; new features extend a stage's input/output shape rather than reaching across. Picker preview commands (`--internal-*`) re-enter at the discovery or render boundary with prediscovered inputs — they do not re-derive the chain from scratch.

22. **Resolved command parity** — every command catclip prints as "Resolved command:" must produce an identical file selection when copy-pasted and re-run non-interactively. Startup pickers, modifier pickers, sink picker, and argv normalizations (e.g., dynamic pattern inference collapsing repeated literals into wildcards, redundant-literal removal) all participate in building this canonical argv; none of them may rely on hidden picker-frame state, RNG, or in-process side effects to reach their final selection. If a normalization rewrites the argv, the rewritten form must be a true equivalent — losing or gaining a file across the round trip is a bug, not a normalization.

23. **Modifier-stage previews are scope-local** — once a scope has a stage checkpoint (`entries[N]`), any modifier-stage picker that previews or tests a pending same-scope stage must start from that checkpoint: the surviving entries produced by the current scope's accepted stages, in order. It may apply exactly the candidate stage (`N+1`) to a copy for display/search. It must not rebuild a synthetic scope, re-evaluate from original targets, inspect sibling `--then` scopes, flatten the full command, or skip/backfill earlier stages. Target pickers before a settled scope, per-file previews, and final output/sink previews have their own contracts, but they must not be used to smuggle cross-scope knowledge into a modifier-stage preview. Cross-scope unioning and final dedupe happen only after scope evaluation, in output planning.

## Execution flow

1. Parse args and build scopes (via `CommandSpec` / `FlagSpec` declarative model).

2. For interactive TTY runs, decide whether a token is already exact enough to bypass fzf:
   - exact existing paths win immediately
   - exact basename file hits can also bypass the picker
   - glob patterns (`*`, `?`, `[`) bypass the picker and resolve directly
   - only genuinely ambiguous / shorthand queries go to fzf

3. Resolve each target into either:
   - an exact file
   - an exact directory subtree
   - a glob pattern matched against all visible files
   - an fzf-backed fuzzy selection
   - an `--include`-allowed ignored target

4. Discover files for resolved targets:
   - rg is the primary engine for visible file enumeration
   - exact visible directory targets also use rg-backed subtree discovery
   - rg is also used for exact basename lookup and `--contains` matching
   - Go walks are still used where directory objects matter: ignored-target browsing and some exact ignored / include-allowed directory cases
   - symlinks are currently excluded everywhere by policy
   - visible directory targets are derived from the visible file set rather than a separate directory walk, so there is no standalone visible-dir walk in the hot path; they inherit both rg/.gitignore visibility and catclip's `.hiss` filtering
   - consequence: empty directories, or directories with no surviving text files, are intentionally excluded from the visible picker

5. Apply cheap file eligibility checks first:
   - ignore rules
   - `--only` / `--exclude`
   - known binary basename/extension denylist

6. Classify text/binary via rg's NUL-byte text-file set (see rule 11). No per-file Go byte-sniff fallback, no Go-side name allowlist.

7. Keep ripgrep-backed candidate entries lightweight:
   - picker/index candidates are stored with `RelPath` first
   - `AbsPath` is materialized only when a file survives to real work like `--contains`, preview sizing, snippets/diffs, or final emission

8. Apply scope stages in order (left to right within each scope):
   - `--include` adds authorized ignored paths (must be first, once per scope)
   - `--only` / `--exclude` run as sequential file-set stages
   - `--recent N` sorts by mtime, keeps top N
   - `--depth N` removes files deeper than N path segments from cwd
   - `--contains` filters by content match (regex, rg-backed)
   - `--snippet` extracts blank-line-bounded blocks matching regex
   - `--lines [START [END]]` slices each surviving file to that 1-based line range
   - git selectors (`--changed`, `--staged`, `--unstaged`, `--untracked`)
   - output shape: `--paths` (terminal), `--*-diff`, or default full-file
   - `--only -`, `--exclude -`, `--include -` read exact paths from stdin
   - interactive file-set selections are normalized before argv emission: redundant literals covered by a selected pattern are dropped, and repeated exact selections collapse into a single wildcard when full current-scope coverage proves the shorter form equivalent (dynamic pattern inference). The rewritten argv must satisfy resolved-command parity (rule 22)

9. Build preview metadata and render the tree/summary when needed:
   - `--preview` renders a per-file table (path + size/tokens/git/modified/shape) instead of the tree; the confirmation flow, sink picker, and fzf pickers keep the tree. `--no-tree` governs only the confirmation tree, not `--preview`
   - normal `-q` runs skip tree rendering and confirmation entirely
   - `-q` therefore makes `-y` and `-t` redundant in normal non-preview runs
   - preview/tree-specific metadata such as git status is only collected when a tree will actually be rendered
   - preview Git badges are selector-aligned: `[S]`, `[M]`, `[?]`, `[SM]` — see rule 3
   - size/token summary is still computed even without a tree because the Count / Size / Tokens disclaimer depends on it; token counting remains a fast byte-based estimate (`bytes / 4`) on purpose, because exact tokenizers would add noticeable work while the real hot cost here is gathering file sizes, not formatting the final number

10. Emit output to stdout or the clipboard sink:
    - default: full file contents in `<file path="...">` wrappers
    - `--paths`: bare relative paths, one per line
    - `--snippet`: matched blocks in `<file path="..." lines="L-L">` wrappers
    - `--lines`: line-sliced bodies in `<file path="..." lines="L-L">` wrappers
    - `--*-diff`: unified diff patches in `<file path="..." type="diff">` wrappers
    - `--raw` (`-r`): bare file bodies, no wrappers; multi-file concatenates contiguously like `cat a b`; with `--lines`, line-number prefixes are stripped (numbered-but-unwrapped is unsafe across files)
    - unresolvable targets: warn on stderr (even with `-q`), emit what resolved, exit 1

## Git / rg performance rules

- rg owns ignore semantics. Both `.gitignore` and `.hiss` flow through ripgrep: `.gitignore` via rg's native engine (with `--no-require-git` so it activates outside repos), `.hiss` via `--ignore-file`. No Go-side gitignore matcher; no `git check-ignore` subprocess.
- Source attribution (`.hiss` vs `.gitignore`) comes from a two-call diff: `visibleAll` (gitignore-only) vs `visibleWithHiss` (gitignore + `.hiss`). Both calls are cached process-wide.
- A previous `git cat-file --batch` fast-path experiment was benchmarked and was substantially slower than direct working-tree streaming for catclip's "wrap and emit many files" workload; do not assume Git blob batching is an optimization here without new measurements.
- Preview/tree badge collection narrows `git status --porcelain` to selected roots/pathspecs when the path list is small enough, and only falls back to repo-wide porcelain for broad/unsafe path sets or Git command failure; this materially improved tree-enabled scoped runs.
- Tradeoff of the narrowed porcelain path: boundary-crossing rename/copy cases can be less complete than repo-wide status. Because the tree only cares about staged / unstaged / untracked selector states today, and not a dedicated rename badge, that trade is acceptable; future rename-specific UI should revisit these fallback rules carefully.

## Output pipeline rules

- Full-file emission uses bounded read-side concurrency, but exactly one goroutine writes to the sink.
- The default read worker count is 2: tracked-linux benchmarks showed large wins at 2/4/8 workers, but 2 is the safer cross-machine default because it still overlaps reads while being less likely to thrash spinning disks than 4 or 8.
- Benchmark takeaway: 2 workers delivered the major jump; 4 and 8 improved things further but with much smaller gains, so higher defaults are harder to justify.
- Integrity rules for future changes: multiple readers are fine, but preserve exactly one writer, complete per-file buffers only, immutable handoff from worker to writer, ordered commit, and loud failure on read error.
- Future output corruption risk comes from: multiple sink writers, shared/reused mutable buffers, out-of-order commit, silent skip/retry logic, or reading files that are being modified mid-run.
- Clipboard note: on macOS, giant clipboard runs are now mostly limited by `pbcopy` / pasteboard wait time, not catclip's own payload generation. This matters for pathological full-repo copies like `catclip .` on Linux repository checkouts, not "Linux the OS". At `vscode-main` scale the clipboard wait was about 1.2s and effectively negligible for normal use.

## Known remaining costs

- Exact basename lookup still has its own rg pass separate from the picker.
- Ignored-target browsing still uses a full Go walk by design.
- Preview tree runs still pay per-file size collection, and large or broad tree requests can still fall back to repo-wide git status collection; quiet/no-tree paths avoid that cost.
- On large clean repos, output emission is currently the dominant cost, not visible-file discovery.

## Profiling

`cmd/catclip/main.go` exposes opt-in profiling, off by default (no effect unless the env var is set, so it is safe in the shipped binary). Point each var at an output path:

- `CATCLIP_CPUPROFILE=cpu.prof` — `go tool pprof cpu.prof`. On-CPU time only.
- `CATCLIP_MEMPROFILE=mem.prof` — `go tool pprof -inuse_space mem.prof` (live memory) or `-alloc_space` (total allocations / GC churn). One file covers both via pprof's sample types.
- `CATCLIP_TRACE=trace.out` — `go tool trace trace.out`. The full timeline: syscalls, GC, scheduling, blocking.

Pick the tool by the question you are asking:

- **"What's the hot function?"** → CPU profile.
- **"Why is it slow / where does wall-clock time go?"** → trace. catclip runs are mostly *off* CPU — waiting on the rg subprocess, file-read syscalls, fzf, and GC (one investigated interactive run was only ~37% on-CPU). The CPU profile is blind to that other ~63%; the trace shows it. Reach for the trace first on latency questions, the CPU profile second.
- **"Memory or allocation pressure?"** → mem profile (`-inuse_space` for retention — e.g. the one-body-at-a-time streaming guarantees; `-alloc_space` for the GC churn the benchmarks track).

Caveat: `catclip.Main()` returns normally on success but calls `os.Exit()` on error paths, and `os.Exit` bypasses deferred funcs — so a profile is flushed only when `Main()` returns (the success path, e.g. a completed interactive run, which is what we profile). Capturing error paths too would require `Main()` to return an exit code instead of calling `os.Exit()` internally; until then, profile a run that succeeds.

Profiling answers *where the time/memory goes within one process*. Pair it with the benchmarking conventions in "Notes for future passes": `hyperfine` for end-to-end per-invocation cost (it includes the spawn + runtime-init floor a profile cannot see), and `go test -bench` for pure hot functions below the spawn floor.

## Path / picker rules

- In an interactive TTY, bare `catclip` opens the target selector with `[select all files]` first; non-interactive runs still default to `.`.
- Exact existing targets like `.`, `src`, or `dir/file` should run directly instead of opening fzf.
- Slashless shorthand like `common`, `btn`, or `node` is picker territory.
- Trailing slash has no special picker meaning. Overloading `dir/` as "directories only", or treating `src/` differently from `src`, was a bad plan and is intentionally rejected; the idea that `dir/` should scope the picker to "all files under that dir" as a special mode was also rejected. Exact paths stay exact, scoped path targets like `layout/Footer.tsx` still use normal resolution, and fuzzy file/directory discovery is handled by fzf rather than by slash punctuation. If directory-only or directory-first picker modes return later, they should use explicit flags or picker toggles instead of path punctuation.
- The normal picker is visible-only.
- Ignored targets require explicit `--include` authorization; in the picker flow they are reached through the ignored-target path rather than mixed into the safe list by default.
- Packaged installs resolve fzf/rg from app-private paths; env overrides remain for tests and developer runs, but there is no normal user-facing PATH fallback.
- Bare `--include` opens ignored-target selection for the current scope.
- `.` means "all safe targets" and suppresses further safe-target picking, but it must not suppress ignored-target browsing.
- Scope coverage is literal and subtree-based: selecting `src/vs` covers only `src/vs/...`, not all of `src/...`, so a later `--then src` is valid and should remain available.
- Exact overlapping scopes are allowed in scripting mode even when a later scope is already covered by an earlier one; final payload is still deduped by path, but the command should keep the user's literal scope structure.
- `--then` is a true fresh scope boundary: treat it like starting a brand new catclip command on the same line, then unioning the final file sets. Interactive recovery must not turn it into "remaining files only".
- Current interactive continuation exclusion is target-based, not result-set-based, within the current scope: later pickers in the same scope exclude previously selected target paths/subtrees, but do not evaluate prior modifiers like `--only`, `--exclude`, `--contains`, `--changed`, `--snippet`, or `--diff` before deciding what counts as "already covered".
- Consequence of that simplification within one scope: `src --only "*.ts"` still makes later same-scope picker logic treat all of `src` as covered, and prior `.` still means "all safe targets are covered" for same-scope continuation purposes, even if that scope would later be narrowed by modifiers.
- Bare value-taking modifiers can recover interactively in a TTY: `--include`, `--only`, `--exclude`, `--contains`, and bare `--`. Repeated bare `--` placeholders are allowed and each inserts one more modifier stage.
- `--headless` is the explicit no-prompt switch: it forbids any interactive picker (startup recovery, target picker, modifier value pickers) and requires explicit targets up front. Agents and scripts pass it to guarantee fzf never opens, and any code path that reaches a prompt under `--headless` is a bug, not a fallback. `-q` is independent and does not by itself disable prompts.
- Git selectors chosen from startup recovery may open follow-up file-set pickers, but the canonical resolved command still compiles back to normal CLI syntax.

## Notes for future passes

- Preserve user-facing semantics over "cleaner" abstractions. If a refactor changes target meaning, preview truthfulness, or startup recovery behavior, it is probably the wrong refactor.
- Only extract an internal package when it can own a small API without exporting half the app's internals; if a split mainly moves files around, it is premature.
- If tree/render UX is still in flux, keep it close to the rest of the app until the behavior settles.
- Benchmark end-to-end CLI cost with `hyperfine`, not ad-hoc `time` loops: most of catclip's perf-relevant work is per-invocation (per-refresh preview commands, full runs), and the process-spawn + runtime-init floor (~26 ms on macOS) is part of what the user pays — hyperfine captures it and handles warmup, repeats, and mean ± σ. Conventions: explicit binary paths (not PATH); set `CATCLIP_RG`/`CATCLIP_FZF` so `ensureRequiredTools` takes its real (non-error) path; `-N` to skip the intermediate shell, `-w` warmup, `-M` run cap. Use `go test -bench` instead only to isolate a *pure hot function* below the spawn floor (e.g. `PreviewModeTags`, stage application) — hyperfine would drown a µs function in the spawn cost. Caveat: hyperfine measures **warm** steady-state; cold-cache / first-touch cost is not captured (macOS `purge` needs root), so reason about cold paths separately. hyperfine tells you *how long*; to see *where* that time goes inside one process, use the profiling env vars in the `## Profiling` section (trace especially, since most of the cost is off-CPU).
- Benchmark explicit binaries, not just whatever `catclip` in PATH points to; old installed binaries can silently invalidate before/after results.
- Benchmark the path you actually changed: tree/porcelain optimizations must be measured with tree-enabled commands, not `-q -t` runs that skip that work entirely.
- For preview/content performance work, include a large-entry validation against a local directory that collects several large real-world project checkouts under one folder (e.g. clones of big repos like linux, vscode, or chromium), when you have one set up. Small fixtures catch correctness, but 100k+ file corpora expose O(N), repeated-rg, JSON decode, and file-body scan regressions that are invisible at toy sizes. The location of that directory is machine-specific and not stored in the repo — if you don't already have it in context, ask the user where it is rather than guessing a path.
- When file counts or tree contents change, investigate selection and classification first; output-path changes should not change what files are selected.
- For Git-performance work, use real tracked clones as the primary testbed; odd, partially detached, or effectively untracked trees can hide or distort Git costs.
- Before adding a Git-based "optimization", compare it against the current rg + direct-filesystem baseline on a real tracked clone, not only on odd working trees; previous `git cat-file --batch` experiments lost badly.
- Clipboard benchmarks must separate catclip generation time from clipboard backend wait; large macOS runs were dominated by `pbcopy` wait, not by catclip's own payload generation.
- Interactive input must be validated on a candidate copy before mutating startup state; invalid choices should surface a clear error without poisoning the current scope or command.
- If startup recovery grows further, preserve deterministic CLI semantics, file-set stage ordering, and the rule that `--then` behaves like a fresh command boundary rather than a subtraction operator.
