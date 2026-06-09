package output

import (
	"bytes"
	"os"
	"time"
)

type TextSnapshot struct {
	AbsPath  string
	RelPath  string
	Size     int64
	ModTime  time.Time
	IsText   bool
	RawBytes []byte
}

func LoadTextSnapshot(absPath, relPath string) (TextSnapshot, error) {
	return loadSnapshot(absPath, relPath, true)
}

func loadBodySnapshot(absPath, relPath string) (TextSnapshot, error) {
	return loadSnapshot(absPath, relPath, false)
}

func loadSnapshot(absPath, relPath string, requireText bool) (TextSnapshot, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		return TextSnapshot{}, err
	}

	snapshot := TextSnapshot{
		AbsPath: absPath,
		RelPath: relPath,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return TextSnapshot{}, err
	}
	// Emission-time NUL-byte check on bytes we'd read anyway. This is
	// single-file byte inspection of a file we're processing, distinct
	// from discovery-time content classification (which flows through
	// rg's text-file set).
	snapshot.IsText = bytes.IndexByte(data, 0) < 0
	if requireText && !snapshot.IsText {
		snapshot.RawBytes = nil
		return snapshot, nil
	}
	snapshot.RawBytes = data
	return snapshot, nil
}

func (s TextSnapshot) PreviewText() string {
	if !s.IsText {
		return ""
	}
	return string(bytes.ToValidUTF8(s.RawBytes, []byte{}))
}

func (s TextSnapshot) SnippetLines() []string {
	if !s.IsText {
		return nil
	}
	// Preserve the existing snippet coordinate system by splitting the exact
	// file bytes, not the sanitized preview text.
	return SplitLogicalLines(s.RawBytes)
}

// SplitLogicalLines is the snippet/prepared-output line splitter. Kept
// at root because text_snapshot.go and prepared output use the exact
// raw-byte split coordinate system; render/ has its own private copy
// for preview-text splitting and the two shouldn't share.
func SplitLogicalLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	lines := make([]string, 0, bytes.Count(data, []byte{'\n'})+1)
	start := 0
	for start < len(data) {
		offset := bytes.IndexByte(data[start:], '\n')
		if offset < 0 {
			lines = append(lines, string(data[start:]))
			break
		}
		end := start + offset
		lines = append(lines, string(data[start:end]))
		start = end + 1
	}
	return lines
}
