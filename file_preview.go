package catclip

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
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
	Scopes         []executionScope
	CheckpointPath string
	Invocation     invocationConfig
	Render         renderConfig
}

func filePreviewConfigFromParsedCommand(cfg parsedCommand) filePreviewConfig {
	return filePreviewConfig{
		WorkingDir:     cfg.WorkingDir,
		FilePath:       cfg.FilePath,
		Scopes:         executionScopesFromCommandSpec(cfg.Command),
		CheckpointPath: cfg.PrediscoveredPath,
		Invocation:     invocationConfigFromParsedCommand(cfg),
		Render:         renderConfigFromParsedCommand(cfg),
	}
}

// runInternalFilePreview serves the content-match picker's preview pane.
// It dispatches on three states the picker can be in:
//
//  1. Empty pattern (fzf passed `--contains ""` or `--snippet ""`, the
//     user hasn't typed yet). Emits a static hint document teaching
//     smart-case behavior and useful patterns. Fires regardless of which
//     row is focused on the left.
//
//  2. Non-empty pattern + empty focused path (the `[all current matches]`
//     row, whose TSV field 3 is empty). When a prediscovered checkpoint
//     is attached, emits the full scope tree from the checkpoint — same
//     shape as --only / --exclude's `[all files]` preview. Without a
//     checkpoint, emits nothing (the legacy behavior).
//
//  3. Non-empty pattern + non-empty focused path. The existing per-file
//     preview path: load the file, render with optional match
//     highlighting / snippet extraction / diff.
//
// The shared preview command (a single string set when the picker opens)
// embeds both `--internal-file-path {3}` and `--internal-prediscovered <path>`
// so this one handler can dispatch all three states without per-state
// shell branching in the command string. See fzfContentPreviewCommand for
// the command builder.
func runInternalFilePreview(cfg filePreviewConfig, stdout io.Writer) error {
	s := internalPreviewScope(cfg)

	if internalPreviewPatternIsEmpty(s) {
		return encodeTreePayload(stdout, buildInternalContentHintDocument(s))
	}

	// The content picker's [all current matches] row passes an empty
	// `{3}` (FilePath). Detect that BEFORE consulting
	// internalPreviewRelPath — that helper falls back to scope targets
	// when FilePath is empty, which would surface "src" (the scope
	// target) as the focused file instead of the [all matches] sentinel.
	// When a checkpoint is wired in, emit the scope tree; otherwise stay
	// silent (matches pre-v0.5.2 behavior).
	if strings.TrimSpace(cfg.FilePath) == "" && cfg.CheckpointPath != "" {
		return runInternalContentCheckpointTreePayload(cfg, stdout)
	}

	relPath := internalPreviewRelPath(cfg)
	if relPath == "" || relPath == "." {
		return nil
	}

	gitCtx := detectGitContext(cfg.WorkingDir)
	doc, ok := buildInternalPreviewDocument(cfg, gitCtx, relPath)
	if !ok {
		return nil
	}
	return encodeTreePayload(stdout, doc)
}

// runInternalContentCheckpointTreePayload re-uses the prediscovered
// tree-payload handler by re-packaging the filePreviewConfig into the
// shape that handler expects. The two configs carry overlapping data
// (working dir, scopes, render/invocation configs) — this is a shape
// adapter, not new logic.
func runInternalContentCheckpointTreePayload(cfg filePreviewConfig, stdout io.Writer) error {
	prediscoveredCfg := prediscoveredCommandConfig{
		CheckpointPath: cfg.CheckpointPath,
		Invocation:     cfg.Invocation,
		Render:         cfg.Render,
		Scopes:         cfg.Scopes,
	}
	return runInternalPrediscoveredTreePayload(prediscoveredCfg, stdout)
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

func buildInternalPreviewDocument(cfg filePreviewConfig, gitCtx gitContext, relPath string) (treeDocument, bool) {
	absPath := filepath.Join(cfg.WorkingDir, filepath.FromSlash(relPath))
	s := internalPreviewScope(cfg)

	switch executionScopeOutputMode(s) {
	case entryModeSnippet:
		return buildInternalSnippetPreviewDocument(relPath, absPath, s.SnippetPattern, snippetOptionsFor(s.SnippetContextSet, s.SnippetContextLines))
	case entryModeDiff:
		return buildInternalDiffPreviewDocument(relPath, absPath, gitCtx, s)
	default:
		return buildInternalFullFilePreviewDocument(relPath, absPath, s.Contains)
	}
}

func internalPreviewScope(cfg filePreviewConfig) executionScope {
	if len(cfg.Scopes) == 0 {
		return executionScope{}
	}
	return cfg.Scopes[len(cfg.Scopes)-1]
}

func buildInternalFullFilePreviewDocument(relPath, absPath, matchPattern string) (treeDocument, bool) {
	snapshot, err := loadTextSnapshot(absPath, relPath)
	if err != nil || !snapshot.IsText {
		return treeDocument{}, false
	}
	return buildTreeFilePreviewDocument(relPath, "", snapshot.PreviewText(), matchPattern, false, nil), true
}

func buildInternalSnippetPreviewDocument(relPath, absPath, pattern string, opts snippetOptions) (treeDocument, bool) {
	if strings.TrimSpace(pattern) == "" {
		return buildInternalSnippetHintDocument(), true
	}

	snapshot, err := loadTextSnapshot(absPath, relPath)
	if err != nil || !snapshot.IsText {
		return treeDocument{}, false
	}
	matches, err := runRipgrepMatchLines(pattern, []string{absPath})
	if err != nil {
		return treeDocument{}, false
	}
	snippet, err := resolveSnippetFromSnapshot(snapshot, matches[absPath], opts)
	if err != nil || len(snippet.Ranges) == 0 {
		return treeDocument{}, false
	}

	content, focusLines := buildInternalSnippetPreviewContent(snippet.Ranges, snippet.Lines)
	return buildTreeFilePreviewDocument(relPath, "", content, pattern, false, focusLines), true
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
func internalPreviewPatternIsEmpty(s executionScope) bool {
	if s.Snippet || executionScopeHasStage(s, scopeStageSnippet) {
		return strings.TrimSpace(s.SnippetPattern) == ""
	}
	if executionScopeHasStage(s, scopeStageContains) {
		return strings.TrimSpace(s.Contains) == ""
	}
	return false
}

// buildInternalContentHintDocument returns the appropriate hint document
// for the current content picker mode. Snippet mode gets the snippet hint
// (which already shipped); --contains gets the new richer hint.
func buildInternalContentHintDocument(s executionScope) treeDocument {
	if s.Snippet || executionScopeHasStage(s, scopeStageSnippet) {
		return buildInternalSnippetHintDocument()
	}
	return buildTreeFilePreviewDocument("", "hint.go", internalContainsPreviewEmptyHint, "", false, nil)
}

func buildInternalDiffPreviewDocument(relPath, absPath string, gitCtx gitContext, s executionScope) (treeDocument, bool) {
	entry := fileEntry{
		AbsPath:          absPath,
		RelPath:          relPath,
		DiffWantStaged:   s.Staged,
		DiffWantUnstaged: s.Unstaged,
	}

	content, _, tracked, err := diffEntryOutput(gitCtx, entry)
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

func buildInternalSnippetPreviewContent(ranges []snippetRange, lines []string) (string, []int) {
	previewLines := make([]string, 0, len(lines))
	focusLines := make([]int, 0, len(lines))
	for idx, r := range ranges {
		if idx > 0 && len(previewLines) > 0 {
			previewLines = append(previewLines, "")
		}
		previewLines = append(previewLines, fmt.Sprintf("[lines %d-%d]", r.Start, r.End))
		for i := r.Start - 1; i < r.End; i++ {
			previewLines = append(previewLines, lines[i])
			focusLines = append(focusLines, len(previewLines))
		}
	}
	return strings.Join(previewLines, "\n"), focusLines
}
