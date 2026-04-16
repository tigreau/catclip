package catclip

import (
	"bytes"
	"os"
	"time"
)

type textSnapshot struct {
	AbsPath  string
	RelPath  string
	Size     int64
	ModTime  time.Time
	IsText   bool
	RawBytes []byte
}

func loadTextSnapshot(absPath, relPath string) (textSnapshot, error) {
	return loadSnapshot(absPath, relPath, true)
}

func loadBodySnapshot(absPath, relPath string) (textSnapshot, error) {
	return loadSnapshot(absPath, relPath, false)
}

func loadSnapshot(absPath, relPath string, requireText bool) (textSnapshot, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		return textSnapshot{}, err
	}

	isText, err := isLikelyTextFile(relPath, absPath)
	if err != nil {
		return textSnapshot{}, err
	}

	snapshot := textSnapshot{
		AbsPath: absPath,
		RelPath: relPath,
		Size:    info.Size(),
		ModTime: info.ModTime(),
		IsText:  isText,
	}
	if requireText && !isText {
		return snapshot, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return textSnapshot{}, err
	}
	snapshot.RawBytes = data
	if !isText {
		return snapshot, nil
	}
	return snapshot, nil
}

func (s textSnapshot) PreviewText() string {
	if !s.IsText {
		return ""
	}
	return string(bytes.ToValidUTF8(s.RawBytes, []byte{}))
}

func (s textSnapshot) SnippetLines() []string {
	if !s.IsText {
		return nil
	}
	// Preserve the existing snippet coordinate system by splitting the exact
	// file bytes, not the sanitized preview text.
	return splitLogicalLines(s.RawBytes)
}
