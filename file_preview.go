package catclip

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const internalFilePreviewByteLimit = 128 * 1024
const internalDiffHighlightPath = "diff"
const internalSnippetPreviewEmptyHint = "Type a regex to preview snippet blocks.\nSnippet mode extracts blank-line-separated blocks around matches."

func runInternalFilePreview(cfg runConfig, stdout io.Writer) error {
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

func internalPreviewRelPath(cfg runConfig) string {
	relPath := normalizeRelPath(cfg.FilePath)
	if relPath == "" {
		if len(cfg.Scopes) == 1 && len(cfg.Scopes[0].Targets) == 1 {
			relPath = normalizeRelPath(cfg.Scopes[0].Targets[0])
		}
	}
	return relPath
}

func buildInternalPreviewDocument(cfg runConfig, gitCtx gitContext, relPath string) (treeDocument, bool) {
	absPath := filepath.Join(cfg.WorkingDir, filepath.FromSlash(relPath))
	s := internalPreviewScope(cfg)
	switch {
	case s.Snippet:
		return buildInternalSnippetPreviewDocument(relPath, absPath, s.Contains)
	case s.Diff:
		return buildInternalDiffPreviewDocument(relPath, absPath, gitCtx, s)
	default:
		return buildInternalFullFilePreviewDocument(relPath, absPath, s.Contains)
	}
}

func internalPreviewScope(cfg runConfig) scope {
	if len(cfg.Scopes) == 0 {
		return scope{}
	}
	return cfg.Scopes[len(cfg.Scopes)-1]
}

func buildInternalFullFilePreviewDocument(relPath, absPath, matchPattern string) (treeDocument, bool) {
	content, truncated, ok := readInternalPreviewContent(relPath, absPath)
	if !ok {
		return treeDocument{}, false
	}
	return buildTreeFilePreviewDocument(relPath, "", content, matchPattern, truncated, nil), true
}

func buildInternalSnippetPreviewDocument(relPath, absPath, pattern string) (treeDocument, bool) {
	if strings.TrimSpace(pattern) == "" {
		return buildInternalSnippetHintDocument(), true
	}

	content, truncated, ok := readInternalPreviewContent(relPath, absPath)
	if !ok {
		return treeDocument{}, false
	}

	re, err := compileContainsPattern(pattern)
	if err != nil {
		return treeDocument{}, false
	}

	ranges, lines, err := extractSnippetRangesFromContent([]byte(content), re)
	if err != nil || len(ranges) == 0 {
		return treeDocument{}, false
	}

	content, focusLines := buildInternalSnippetPreviewContent(ranges, lines)
	return buildTreeFilePreviewDocument(relPath, "", content, "", truncated, focusLines), true
}

func buildInternalSnippetHintDocument() treeDocument {
	return buildTreeFilePreviewDocument("", "", internalSnippetPreviewEmptyHint, "", false, nil)
}

func buildInternalDiffPreviewDocument(relPath, absPath string, gitCtx gitContext, s scope) (treeDocument, bool) {
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

	content, truncated := truncateInternalPreviewContent(content, internalFilePreviewByteLimit)
	return buildTreeFilePreviewDocument(relPath, internalDiffHighlightPath, content, "", truncated, nil), true
}

func readInternalPreviewContent(relPath, absPath string) (string, bool, bool) {
	text, err := isLikelyTextFile(relPath, absPath)
	if err != nil || !text {
		return "", false, false
	}

	content, truncated, err := readInternalFilePreview(absPath, internalFilePreviewByteLimit)
	if err != nil {
		return "", false, false
	}
	return content, truncated, true
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

func truncateInternalPreviewContent(content string, maxBytes int64) (string, bool) {
	data := []byte(content)
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	data = bytes.ToValidUTF8(data, []byte{})
	return string(data), truncated
}

func readInternalFilePreview(absPath string, maxBytes int64) (string, bool, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return "", false, err
	}

	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	data = bytes.ToValidUTF8(data, []byte{})
	return string(data), truncated, nil
}
