package tree

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

type DocumentMode string

const (
	// DocumentModeTree renders tree entries and an optional summary.
	DocumentModeTree DocumentMode = "tree"
	// DocumentModeFile renders a single file preview payload.
	DocumentModeFile DocumentMode = "file"

	TargetKindDir  = "dir"
	TargetKindFile = "file"

	TargetStateOK             = "ok"
	TargetStateText           = "text"
	TargetStateEmpty          = "empty"
	TargetStateNoTextChildren = "no_text_children"
	TargetStateNonText        = "non_text"
)

// Document is the serialized/rendered tree payload shared by catclip and
// catclip-tree.
type Document struct {
	Mode    DocumentMode
	Root    string
	Target  *DocumentTarget
	Entries []DocumentEntry
	File    *FilePreview
	Summary *DocumentSummary
}

// DocumentTarget describes the focused file or directory for an empty or
// trimmed tree payload.
type DocumentTarget struct {
	Path  string
	Kind  string
	State string
}

// DocumentEntry is a single file row in a tree payload.
type DocumentEntry struct {
	Path             string
	Size             *int64
	GitStatus        string
	ModeTag          string
	TargetRoot       string
	AllowedByInclude bool
	BlockRule        string
	BlockSource      string
}

// FilePreview holds the text payload for file-preview mode.
type FilePreview struct {
	Path          string
	HighlightPath string
	FocusLines    []int
	Content       string
	MatchPattern  string
	Truncated     bool
}

// DocumentSummary contains the aggregate footer metadata for a payload.
type DocumentSummary struct {
	Count     int
	Bytes     int64
	HumanSize string
	Tokens    int64
	FileWord  string
}

// RenderOptions controls which metadata fields are shown by the renderer.
type RenderOptions struct {
	Bare          bool
	ShowSizes     bool
	ShowGitStatus bool
	ShowSummary   bool
	ShowTokens    bool
	MaxLines      int
	PreviewTheme  string
}

// Palette provides the ANSI fragments used by the tree renderer.
type Palette struct {
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

// DefaultRenderOptions returns the standard rich tree rendering defaults.
func DefaultRenderOptions() RenderOptions {
	return RenderOptions{
		ShowSizes:     true,
		ShowGitStatus: true,
		ShowSummary:   true,
		ShowTokens:    true,
		MaxLines:      0,
	}
}

// BuildTarget normalizes and validates a payload target descriptor.
func BuildTarget(path, kind, state string) *DocumentTarget {
	path = normalizeRelPath(path)
	if path == "" {
		return nil
	}

	kind = NormalizeTargetKind(kind)
	state = NormalizeTargetState(state)
	switch kind {
	case TargetKindDir:
		if state == "" {
			state = TargetStateOK
		}
	case TargetKindFile:
		if state == "" {
			state = TargetStateText
		}
	}

	return &DocumentTarget{
		Path:  path,
		Kind:  kind,
		State: state,
	}
}

// NormalizeTargetKind normalizes supported target kinds and rejects others.
func NormalizeTargetKind(kind string) string {
	switch normalizeRelPath(kind) {
	case "all", TargetKindDir:
		return TargetKindDir
	case TargetKindFile:
		return TargetKindFile
	default:
		return ""
	}
}

// NormalizeTargetState normalizes supported target states and rejects others.
func NormalizeTargetState(state string) string {
	switch strings.TrimSpace(state) {
	case TargetStateOK, TargetStateText, TargetStateEmpty, TargetStateNoTextChildren, TargetStateNonText:
		return state
	default:
		return ""
	}
}

// BuildSummary constructs a summary from collected file sizes and optional
// precomputed labels.
func BuildSummary(sizes map[string]int64, humanSize string, tokens int64, fileWord string) *DocumentSummary {
	count := len(sizes)
	if count == 0 && humanSize == "" && tokens == 0 {
		return nil
	}

	var totalBytes int64
	for _, size := range sizes {
		totalBytes += size
	}

	if humanSize == "" {
		humanSize, tokens = FormatSizeAndTokens(totalBytes, count)
	}
	if fileWord == "" {
		fileWord = fileWordForCount(count)
	}

	return &DocumentSummary{
		Count:     count,
		Bytes:     totalBytes,
		HumanSize: humanSize,
		Tokens:    tokens,
		FileWord:  fileWord,
	}
}

// FormatSizeAndTokens converts total payload bytes into the human-readable
// size/token estimate shown in preview summaries. Wrapper-tag overhead is
// intentionally excluded so size math stays stable even if payload framing
// changes later.
func FormatSizeAndTokens(totalBytes int64, fileCount int) (string, int64) {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)

	_ = fileCount
	tokens := totalBytes / 4

	switch {
	case totalBytes < kb:
		return formatFloat(float64(totalBytes), "B"), tokens
	case totalBytes < mb:
		return formatFloat(float64(totalBytes)/kb, "KB"), tokens
	case totalBytes < gb:
		return formatFloat(float64(totalBytes)/mb, "MB"), tokens
	default:
		return formatFloat(float64(totalBytes)/gb, "GB"), tokens
	}
}

// SortedEntries returns a stable path-sorted copy of the provided entries.
func SortedEntries(entries []DocumentEntry) []DocumentEntry {
	if len(entries) == 0 {
		return nil
	}

	out := append([]DocumentEntry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		left := normalizeRelPath(out[i].Path)
		right := normalizeRelPath(out[j].Path)
		if left != right {
			return left < right
		}
		return out[i].ModeTag < out[j].ModeTag
	})
	return out
}

func normalizeRelPath(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "./")
	if value == "." || value == "/" {
		return "."
	}
	return value
}

func fileWordForCount(count int) string {
	if count == 1 {
		return "file"
	}
	return "files"
}

func formatFloat(value float64, unit string) string {
	return fmt.Sprintf("%.2f%s", value, unit)
}
