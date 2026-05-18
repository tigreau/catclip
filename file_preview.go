package catclip

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const internalDiffHighlightPath = "diff"
const internalSnippetPreviewEmptyHint = "Type a regex to preview snippet blocks.\nSnippet mode extracts blank-line-separated blocks around matches."

type filePreviewConfig struct {
	WorkingDir string
	FilePath   string
	Scopes     []executionScope
}

func filePreviewConfigFromParsedCommand(cfg parsedCommand) filePreviewConfig {
	return filePreviewConfig{
		WorkingDir: cfg.WorkingDir,
		FilePath:   cfg.FilePath,
		Scopes:     executionScopesFromCommandSpec(cfg.Command),
	}
}

func runInternalFilePreview(cfg filePreviewConfig, stdout io.Writer) error {
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
