package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

const internalDiffHighlightPath = "diff"

// internalContainsPreviewEmptyHint is rendered in the preview pane when the
// content match picker opens for --contains and the user hasn't typed a
// regex yet (or whenever the live query is empty). It shows the kind of
// regexes people actually run against file contents.
// The smart-case note must match isSmartCaseInsensitive in ripgrep.go:
// all-lowercase -> case-insensitive, any uppercase -> exact case.
const internalContainsPreviewEmptyHint = `Content search — PCRE2 regex (smart-case)

  Lists every matching line across the file set. Type a pattern to begin.

  Smart-case:
    todo          → case-insensitive (matches TODO, Todo, todo)
    TODO          → case-sensitive   (matches only TODO)

  Everyday searches:
    TODO|FIXME|HACK|XXX          → work markers
    func\s+\w+|def\s+\w+         → function definitions
    class\s+\w+                  → class / type definitions
    import\s+.*from              → ES module imports
    console\.(log|warn|error)    → debug leftovers
    throw\s+new\s+\w+            → thrown errors
    process\.env\.\w+            → environment-variable reads

  Spot risky code:
    password|secret|api[_-]?key  → possible credentials
    https?://[^\s"'<>]+          → hardcoded URLs
    <<<<<<<|=======|>>>>>>>      → merge-conflict markers
    [ \t]+$                      → trailing whitespace
    .{120,}                      → lines over 120 chars

  Symbols:
    .             → any single character
    *             → 0 or more of the previous   (ab* matches a, ab, abb)
    +             → 1 or more of the previous   (ab+ matches ab; not a)
    ?             → optional, 0 or 1            (https? matches http, https)
    ^             → start of line               (^TODO matches line-initial)
    $             → end of line                 (\.js$ matches paths ending .js)
    \.            → literal dot                 (plain . matches any char)
    \s            → any whitespace
    \b            → word boundary               (\berror\b skips handleError)
    [abc]         → any one of these chars      ([tj]s matches ts or js)
    a|b           → either a or b               (cat|dog matches cat or dog)
    {n,}          → n or more of the previous   (.{120,} matches 120+ chars)

  Focus a file on the left to preview it; matches are highlighted.`

// internalSnippetPreviewEmptyHint is the snippet-mode equivalent. Because
// --snippet returns the blank-line-separated block around each match, the
// examples here target signature lines (func/def/class/type) so the whole
// block comes back as one focused snippet. Smart-case matches ripgrep.go.
const internalSnippetPreviewEmptyHint = `Snippet search — PCRE2 regex (smart-case)

  Returns the blank-line-separated block around each match — focused
  code blocks, not whole files. Match a line inside the block you want
  (usually its signature) and the whole block comes back.

  Smart-case:
    todo          → case-insensitive (matches TODO, Todo, todo)
    Config        → case-sensitive   (matches only Config)

  Grab a definition:
    func\s+HandleLogin          → that function's block
    def\s+process_\w+           → matching Python functions
    class\s+\w+Controller       → controller classes
    type\s+\w+\s+struct         → Go struct definitions
    interface\s+\w+             → interface blocks

  Grab by role:
    describe\(|it\(|test\(      → test blocks
    @app\.route|@router\.       → route handlers
    throw\s+new|raise\s+\w+     → error-handling blocks
    TODO|FIXME                  → comment blocks with markers

  Symbols:
    .             → any single character
    *             → 0 or more of the previous   (ab* matches a, ab, abb)
    +             → 1 or more of the previous   (ab+ matches ab; not a)
    ?             → optional, 0 or 1            (https? matches http, https)
    ^             → start of line               (^TODO matches line-initial)
    $             → end of line                 (\.js$ matches paths ending .js)
    \.            → literal dot                 (plain . matches any char)
    \s            → any whitespace
    \b            → word boundary               (\berror\b skips handleError)
    [abc]         → any one of these chars      ([tj]s matches ts or js)
    a|b           → either a or b               (cat|dog matches cat or dog)
    {n,}          → n or more of the previous   (.{120,} matches 120+ chars)

  Focus a file on the left to preview matching blocks.`

type filePreviewConfig struct {
	WorkingDir     string
	FilePath       string
	SearchingHint  bool
	FocusedLabel   string
	Scopes         []command.ExecutionScope
	CheckpointPath string
	Invocation     command.Invocation
	Render         RenderConfig
}

func FilePreviewConfigFromParsedCommand(cfg command.Parsed) filePreviewConfig {
	return filePreviewConfig{
		WorkingDir:     cfg.WorkingDir,
		FilePath:       cfg.FilePath,
		SearchingHint:  cfg.FileSearchingPreview,
		FocusedLabel:   cfg.TreeTarget,
		Scopes:         command.ExecutionScopesFromSpec(cfg.Command),
		CheckpointPath: cfg.PrediscoveredPath,
		Invocation:     command.InvocationFromParsed(cfg),
		Render:         RenderConfigFromParsedCommand(cfg),
	}
}

// RunInternalFilePreview serves the content-match picker's preview pane.
// It dispatches on four states the picker can be in:
//
//  1. Empty pattern (fzf passed `--contains ""` or `--snippet ""`, the
//     user hasn't typed yet). Emits a static hint document teaching
//     smart-case behavior and useful patterns. Fires regardless of which
//     row is focused on the left.
//
//  2. Non-empty pattern + empty focused path + no focused row yet. Emits the
//     "searching" document, including the Windows Defender explanation. This
//     is the state between a query change and the reload producing rows.
//
//  3. Non-empty pattern + empty focused path (the `[all current matches]`
//     row, whose TSV field 3 is empty). When a prediscovered checkpoint
//     is attached, emits the full scope tree from the checkpoint — same
//     shape as --only / --exclude's `[all files]` preview. Without a
//     checkpoint, emits nothing (the legacy behavior).
//
//  4. Non-empty pattern + non-empty focused path. The existing per-file
//     preview path: load the file, render with optional match
//     highlighting / snippet extraction / diff.
//
// The shared preview command (a single string set when the picker opens)
// embeds `--internal-file-path {3}`, `--internal-tree-target {1}`, and
// `--internal-prediscovered <path>`
// so this one handler can dispatch these states without per-state
// shell branching in the command string. See fzfContentPreviewCommand for
// the command builder.
func RunInternalFilePreview(cfg filePreviewConfig, stdout io.Writer) error {
	// Before any work: terminate the prior focus-change's child if it's
	// still alive. The checkpoint_tree mode (empty FilePath + non-empty
	// checkpoint) ran 5–9 s of rg.matches + build_plan in v0.6.2 traces,
	// piling up against new previews on rapid focus moves. Separate
	// bucket from content-match-list so they don't kill each other's
	// children (both spawn from the same picker tmpdir).
	killSupersededPredecessor(cfg.CheckpointPath, predecessorBucketFilePreview)
	finishBench := platform.InternalBenchSpan("ui.internal.file_preview",
		"checkpoint", fmt.Sprint(cfg.CheckpointPath != ""),
		"file_path_empty", fmt.Sprint(strings.TrimSpace(cfg.FilePath) == ""),
		"scopes", platform.InternalBenchInt(len(cfg.Scopes)),
	)
	s := internalPreviewScope(cfg)

	if internalPreviewPatternIsEmpty(s) {
		finishHintBench := platform.InternalBenchSpan("ui.internal.file_preview.hint")
		err := renderTreeDocument(stdout, buildInternalContentHintDocument(s), FzfFilterTreeRenderOptions(), platform.ANSIPalette())
		finishHintBench("err", platform.InternalBenchError(err))
		finishBench("err", platform.InternalBenchError(err), "mode", "hint")
		return err
	}

	// Non-empty pattern + no focused row + the content picker explicitly
	// requesting the "searching" document: this is the phase between a
	// keystroke and the reload command producing rows. The focused label
	// separates true no-row state from the [all current matches] sentinel,
	// which also has an empty file path. Empty-pattern regex hints still win
	// above; once reload emits rows, sentinel rows render the checkpoint tree
	// and real file rows render per-file previews.
	if cfg.SearchingHint && strings.TrimSpace(cfg.FilePath) == "" && strings.TrimSpace(cfg.FocusedLabel) != contentMatchAllMatchesLabel {
		finishSearchBench := platform.InternalBenchSpan("ui.internal.file_preview.searching_hint")
		err := renderTreeDocument(stdout, buildInternalSearchingHintDocument(runtime.GOOS), FzfFilterTreeRenderOptions(), platform.ANSIPalette())
		finishSearchBench("err", platform.InternalBenchError(err))
		finishBench("err", platform.InternalBenchError(err), "mode", "searching_hint")
		return err
	}

	// Compatibility fallback: if a future/alternate fzf reports a zero-row
	// list to preview helpers, render the same searching document. The main
	// content picker no longer depends on this; it explicitly calls the
	// cfg.SearchingHint path before each reload.
	if strings.TrimSpace(cfg.FilePath) == "" && os.Getenv("FZF_MATCH_COUNT") == "0" {
		finishSearchBench := platform.InternalBenchSpan("ui.internal.file_preview.searching_hint")
		err := renderTreeDocument(stdout, buildInternalSearchingHintDocument(runtime.GOOS), FzfFilterTreeRenderOptions(), platform.ANSIPalette())
		finishSearchBench("err", platform.InternalBenchError(err))
		finishBench("err", platform.InternalBenchError(err), "mode", "searching_hint")
		return err
	}

	// The content picker's [all current matches] row passes an empty
	// `{3}` (FilePath). Detect that BEFORE consulting
	// internalPreviewRelPath — that helper falls back to scope targets
	// when FilePath is empty, which would surface "src" (the scope
	// target) as the focused file instead of the [all matches] sentinel.
	// When a checkpoint is wired in, emit the scope tree; otherwise stay
	// silent (matches pre-v0.5.2 behavior).
	if strings.TrimSpace(cfg.FilePath) == "" && cfg.CheckpointPath != "" {
		finishTreeBench := platform.InternalBenchSpan("ui.internal.file_preview.checkpoint_tree")
		err := runInternalContentCheckpointTreePreview(cfg, stdout)
		finishTreeBench("err", platform.InternalBenchError(err))
		finishBench("err", platform.InternalBenchError(err), "mode", "checkpoint_tree")
		return err
	}

	relPath := internalPreviewRelPath(cfg)
	if relPath == "" || relPath == "." {
		finishBench("err", "false", "mode", "empty_path")
		return nil
	}

	gitCtx := git.Detect(cfg.WorkingDir)
	finishBuildBench := platform.InternalBenchSpan("ui.internal.file_preview.build_document")
	doc, ok := buildInternalPreviewDocument(cfg, gitCtx, relPath)
	finishBuildBench("ok", fmt.Sprint(ok))
	if !ok {
		finishBench("err", "false", "mode", "document_empty")
		return nil
	}
	finishRenderBench := platform.InternalBenchSpan("ui.internal.file_preview.render_document")
	err := renderTreeDocument(stdout, doc, FzfFilterTreeRenderOptions(), platform.ANSIPalette())
	finishRenderBench("err", platform.InternalBenchError(err))
	finishBench("err", platform.InternalBenchError(err), "mode", string(s.OutputMode()))
	return err
}

// runInternalContentCheckpointTreePreview re-uses the prediscovered
// tree-preview handler by re-packaging the filePreviewConfig into the
// shape that handler expects. The two configs carry overlapping data
// (working dir, scopes, render/invocation configs) — this is a shape
// adapter, not new logic.
func runInternalContentCheckpointTreePreview(cfg filePreviewConfig, stdout io.Writer) error {
	prediscoveredCfg := prediscoveredCommandConfig{
		CheckpointPath: cfg.CheckpointPath,
		Invocation:     cfg.Invocation,
		Render:         cfg.Render,
		Scopes:         cfg.Scopes,
	}
	return RunInternalPrediscoveredTreePreview(prediscoveredCfg, stdout)
}

func internalPreviewRelPath(cfg filePreviewConfig) string {
	relPath := normalizeRelPath(cfg.FilePath)
	if relPath == "" {
		if len(cfg.Scopes) == 1 {
			targets := cfg.Scopes[0].Targets
			if len(targets) == 1 {
				relPath = normalizeRelPath(targets[0])
			}
		}
	}
	return relPath
}

func buildInternalPreviewDocument(cfg filePreviewConfig, gitCtx git.Context, relPath string) (treeDocument, bool) {
	absPath := filepath.Join(cfg.WorkingDir, filepath.FromSlash(relPath))
	s := internalPreviewScope(cfg)

	switch s.OutputMode() {
	case command.EntryModeSnippet:
		return buildInternalSnippetPreviewDocument(relPath, absPath, s.SnippetPattern, output.SnippetOptionsFor(s.SnippetContextSet, s.SnippetContextLines))
	case command.EntryModeDiff:
		return buildInternalDiffPreviewDocument(relPath, absPath, gitCtx, s)
	default:
		return buildInternalFullFilePreviewDocument(relPath, absPath, s.Contains)
	}
}

func internalPreviewScope(cfg filePreviewConfig) command.ExecutionScope {
	if len(cfg.Scopes) == 0 {
		return command.ExecutionScope{}
	}
	return cfg.Scopes[len(cfg.Scopes)-1]
}

func buildInternalFullFilePreviewDocument(relPath, absPath, matchPattern string) (treeDocument, bool) {
	finishBench := platform.InternalBenchSpan("ui.internal.file_preview.full_document")
	snapshot, err := output.LoadTextSnapshot(absPath, relPath)
	if err != nil || !snapshot.IsText {
		finishBench("err", platform.InternalBenchError(err), "is_text", fmt.Sprint(snapshot.IsText))
		return treeDocument{}, false
	}
	finishBench("err", "false", "is_text", "true")
	return buildTreeFilePreviewDocument(relPath, "", snapshot.PreviewText(), matchPattern, false, nil), true
}

func buildInternalSnippetPreviewDocument(relPath, absPath, pattern string, opts output.SnippetOptions) (treeDocument, bool) {
	// Opt-in diagnostics for the --snippet preview hot path. On Windows this
	// path can be dominated by repeated child-process startup from fzf previews,
	// so keep separate timings for snapshot load, rg matching, and snippet math.
	finishBench := platform.InternalBenchSpan("ui.internal.file_preview.snippet_document")
	if strings.TrimSpace(pattern) == "" {
		finishBench("err", "false", "mode", "hint")
		return buildInternalSnippetHintDocument(), true
	}

	finishLoadBench := platform.InternalBenchSpan("ui.internal.file_preview.snippet_load_snapshot")
	snapshot, err := output.LoadTextSnapshot(absPath, relPath)
	finishLoadBench("err", platform.InternalBenchError(err), "is_text", fmt.Sprint(snapshot.IsText))
	if err != nil || !snapshot.IsText {
		finishBench("err", platform.InternalBenchError(err), "is_text", fmt.Sprint(snapshot.IsText))
		return treeDocument{}, false
	}
	finishMatchBench := platform.InternalBenchSpan("ui.internal.file_preview.snippet_rg_match_lines")
	matches, err := search.RunRipgrepMatchLines(pattern, []string{absPath})
	finishMatchBench(
		"err", platform.InternalBenchError(err),
		"matched_files", platform.InternalBenchInt(len(matches)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return treeDocument{}, false
	}
	finishResolveBench := platform.InternalBenchSpan("ui.internal.file_preview.snippet_resolve")
	snippet, err := output.ResolveSnippetFromSnapshot(snapshot, matches[absPath], opts)
	finishResolveBench(
		"err", platform.InternalBenchError(err),
		"ranges", platform.InternalBenchInt(len(snippet.Ranges)),
	)
	if err != nil || len(snippet.Ranges) == 0 {
		finishBench("err", platform.InternalBenchError(err), "ranges", platform.InternalBenchInt(len(snippet.Ranges)))
		return treeDocument{}, false
	}

	content, focusLines := buildInternalSnippetPreviewContent(snippet.Ranges, snippet.Lines)
	finishBench("err", "false", "ranges", platform.InternalBenchInt(len(snippet.Ranges)))
	return buildTreeFilePreviewDocument(relPath, "", content, pattern, false, focusLines), true
}

// buildInternalSearchingHintDocument renders the preview shown while a
// typed pattern has zero matches so far (search still running, or a
// genuine zero-match). goos is a parameter for testability; only Windows
// gets the antivirus paragraph — the once-per-boot cold toll is a
// Defender phenomenon (rg reads every candidate through the on-access
// filter on first touch after boot; later searches hit the per-boot
// scan cache and are fast).
func buildInternalSearchingHintDocument(goos string) treeDocument {
	var b strings.Builder
	b.WriteString("Searching — no matches for this pattern yet.\n")
	b.WriteString("\nMatches appear here when the search completes;\nrefining the pattern restarts it.\n")
	if goos == "windows" {
		b.WriteString("\nFirst content search after a reboot is slow on Windows:\n")
		b.WriteString("the antivirus scans every file the search reads, once per\n")
		b.WriteString("boot. This can take ~30s on large projects. Searches after\n")
		b.WriteString("this one reuse the antivirus cache and finish in seconds —\n")
		b.WriteString("the wait will not recur until the next reboot.\n")
	}
	// HighlightPath "hint.txt" keeps the document plain (no lexer match).
	return buildTreeFilePreviewDocument("", "hint.txt", b.String(), "", false, nil)
}

func buildInternalSnippetHintDocument() treeDocument {
	// HighlightPath "hint.go" forces a deterministic Go lexer (it is used only
	// for highlighting, never displayed), so the cheat-sheet colors consistently
	// instead of depending on chroma's content sniffing — which colored the
	// contains hint (detected as Go) but left this one plain.
	return buildTreeFilePreviewDocument("", "hint.go", internalSnippetPreviewEmptyHint, "", false, nil)
}

// internalPreviewPatternIsEmpty reports whether the content-picker preview
// is being invoked in content mode (--contains or --snippet) with an empty
// or whitespace-only query. The hint document fires in this case, regardless
// of which file (if any) is focused on the left.
//
// Returns false when the scope is not in content mode (e.g., the preview is
// serving a non-content picker) so the existing behavior is preserved.
func internalPreviewPatternIsEmpty(s command.ExecutionScope) bool {
	if s.Snippet || s.HasStage(command.StageSnippet) {
		return strings.TrimSpace(s.SnippetPattern) == ""
	}
	if s.HasStage(command.StageContains) {
		return strings.TrimSpace(s.Contains) == ""
	}
	if s.HasStage(command.StageNotContains) {
		// --not-contains live mode: empty pattern means the user
		// hasn't typed anything yet — show the hint instead of trying
		// to prune the file set with no regex.
		if len(s.NotContains) == 0 {
			return true
		}
		return strings.TrimSpace(s.NotContains[len(s.NotContains)-1]) == ""
	}
	return false
}

// buildInternalContentHintDocument returns the appropriate hint document
// for the current content picker mode. Snippet mode gets the snippet hint
// (which already shipped); --contains gets the new richer hint.
func buildInternalContentHintDocument(s command.ExecutionScope) treeDocument {
	if s.Snippet || s.HasStage(command.StageSnippet) {
		return buildInternalSnippetHintDocument()
	}
	return buildTreeFilePreviewDocument("", "hint.go", internalContainsPreviewEmptyHint, "", false, nil)
}

func buildInternalDiffPreviewDocument(relPath, absPath string, gitCtx git.Context, s command.ExecutionScope) (treeDocument, bool) {
	entry := discovery.Entry{
		AbsPath:          absPath,
		RelPath:          relPath,
		DiffWantStaged:   s.Staged,
		DiffWantUnstaged: s.Unstaged,
	}

	content, _, tracked, err := output.DiffEntryOutput(gitCtx, entry)
	if err != nil {
		return treeDocument{}, false
	}
	if !tracked {
		return buildInternalFullFilePreviewDocument(relPath, absPath, "")
	}
	if strings.TrimSpace(content) == "" {
		return treeDocument{}, false
	}

	return buildTreeFilePreviewDocument(relPath, internalDiffHighlightPath, content, "", false, nil), true
}

func buildInternalSnippetPreviewContent(ranges []output.SnippetRange, lines []string) (string, []int) {
	previewLines := make([]string, 0, len(lines))
	for idx, r := range ranges {
		if idx > 0 && len(previewLines) > 0 {
			previewLines = append(previewLines, "")
		}
		previewLines = append(previewLines, fmt.Sprintf("[lines %d-%d]", r.Start, r.End))
		for i := r.Start - 1; i < r.End; i++ {
			previewLines = append(previewLines, lines[i])
		}
	}
	// The range headers make the selected snippet extent explicit. Returning no
	// focus lines leaves the reverse-video emphasis exclusively on regex spans,
	// matching the --contains preview instead of painting the whole snippet.
	return strings.Join(previewLines, "\n"), nil
}
