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
// regex yet (or whenever the live query is empty). It teaches smart-case
// behavior and useful pattern shapes. The smart-case examples here must
// match isSmartCaseInsensitive's behavior in ripgrep.go: all-lowercase ->
// case-insensitive, any uppercase -> exact case.
const internalContainsPreviewEmptyHint = `Content search — PCRE2 regex

  Smart-case:
    todo          → case-insensitive (matches TODO, Todo, todo)
    TODO          → case-sensitive   (matches only TODO)
    Config        → case-sensitive   (matches only Config)

  Patterns:
    func.*Handle  → function signatures
    import.*from  → ES module imports
    ^package      → lines starting with "package"
    (?i)error     → force case-insensitive

  Focus a file on the left to preview it here.
  Matched lines are highlighted in the preview.`

// internalSnippetPreviewEmptyHint is the snippet-mode equivalent. It replaces
// the v0.5.0 two-line placeholder with smart-case guidance and pattern
// examples specific to snippet extraction (blank-line-separated blocks).
const internalSnippetPreviewEmptyHint = `Snippet search — PCRE2 regex

  Extracts blank-line-separated blocks around matches.
  Returns focused code blocks, not full files.

  Smart-case:
    todo          → case-insensitive (matches TODO, Todo, todo)
    Config        → case-sensitive   (matches only Config)

  Patterns:
    func.*Handle  → function blocks
    type.*struct  → struct definitions
    TODO          → comment blocks with TODOs

  Focus a file on the left to preview matching blocks here.`

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
		return buildInternalSnippetPreviewDocument(relPath, absPath, s.SnippetPattern)
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

func buildInternalSnippetPreviewDocument(relPath, absPath, pattern string) (treeDocument, bool) {
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
	snippet, err := resolveSnippetFromSnapshot(snapshot, matches[absPath])
	if err != nil || len(snippet.Ranges) == 0 {
		return treeDocument{}, false
	}

	content, focusLines := buildInternalSnippetPreviewContent(snippet.Ranges, snippet.Lines)
	return buildTreeFilePreviewDocument(relPath, "", content, "", false, focusLines), true
}

func buildInternalSnippetHintDocument() treeDocument {
	return buildTreeFilePreviewDocument("", "", internalSnippetPreviewEmptyHint, "", false, nil)
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
	return buildTreeFilePreviewDocument("", "", internalContainsPreviewEmptyHint, "", false, nil)
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
