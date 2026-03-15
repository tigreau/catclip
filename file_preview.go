package catclip

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const internalFilePreviewByteLimit = 128 * 1024

func runInternalFilePreview(cfg runConfig, stdout io.Writer) error {
	relPath := normalizeRelPath(cfg.FilePath)
	if relPath == "" {
		if len(cfg.Scopes) == 1 && len(cfg.Scopes[0].Targets) == 1 {
			relPath = normalizeRelPath(cfg.Scopes[0].Targets[0])
		}
	}
	if relPath == "" || relPath == "." {
		return nil
	}

	absPath := filepath.Join(cfg.WorkingDir, filepath.FromSlash(relPath))
	text, err := isLikelyTextFile(relPath, absPath)
	if err != nil || !text {
		return nil
	}

	content, truncated, err := readInternalFilePreview(absPath, internalFilePreviewByteLimit)
	if err != nil {
		return nil
	}

	matchPattern := ""
	if len(cfg.Scopes) > 0 {
		matchPattern = strings.TrimSpace(cfg.Scopes[len(cfg.Scopes)-1].Contains)
	}

	doc := buildTreeFilePreviewDocument(relPath, content, matchPattern, truncated)
	return encodeTreePayload(stdout, doc)
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
