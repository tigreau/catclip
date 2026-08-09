package render

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const payloadVersion = 1

// ErrEmptyPayload reports that no tree payload records were present on stdin.
var ErrEmptyPayload = errors.New("empty tree payload")

type payloadRecord struct {
	Type           string `json:"type"`
	Version        int    `json:"version,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Root           string `json:"root,omitempty"`
	Path           string `json:"path,omitempty"`
	Kind           string `json:"kind,omitempty"`
	State          string `json:"state,omitempty"`
	Size           *int64 `json:"size,omitempty"`
	Git            string `json:"git,omitempty"`
	Tag            string `json:"tag,omitempty"`
	TargetRoot     string `json:"target_root,omitempty"`
	IgnoreBypassed bool   `json:"ignore_bypassed,omitempty"`
	BlockSource    string `json:"block_source,omitempty"`
	Content        string `json:"content,omitempty"`
	Highlight      string `json:"highlight,omitempty"`
	Focus          []int  `json:"focus,omitempty"`
	Pattern        string `json:"pattern,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
	Count          *int   `json:"count,omitempty"`
	Bytes          *int64 `json:"bytes,omitempty"`
	HumanSize      string `json:"human_size,omitempty"`
	Tokens         *int64 `json:"tokens,omitempty"`
	FileWord       string `json:"file_word,omitempty"`
}

// EncodePayload writes a tree document as newline-delimited JSON records.
func EncodePayload(w io.Writer, doc Document) error {
	mode := doc.Mode
	if mode == "" {
		mode = DocumentModeTree
	}

	bw := bufio.NewWriter(w)
	if err := writePayloadRecord(bw, payloadRecord{
		Type:    "meta",
		Version: payloadVersion,
		Mode:    string(mode),
		Root:    doc.Root,
	}); err != nil {
		return err
	}
	if doc.Target != nil {
		if err := writePayloadRecord(bw, payloadRecord{
			Type:  "target",
			Path:  doc.Target.Path,
			Kind:  doc.Target.Kind,
			State: doc.Target.State,
		}); err != nil {
			return err
		}
	}

	switch mode {
	case DocumentModeFile:
		if doc.File != nil {
			if err := writePayloadRecord(bw, payloadRecord{
				Type:      "file",
				Path:      doc.File.Path,
				Content:   doc.File.Content,
				Highlight: doc.File.HighlightPath,
				Focus:     doc.File.FocusLines,
				Pattern:   doc.File.MatchPattern,
				Truncated: doc.File.Truncated,
			}); err != nil {
				return err
			}
		}
	default:
		entries := doc.Entries
		if !doc.EntriesSorted {
			entries = SortedEntries(entries)
		}
		for _, entry := range entries {
			record := payloadRecord{
				Type:           "entry",
				Path:           entry.Path,
				Kind:           "file",
				Git:            entry.GitStatus,
				Tag:            entry.ModeTag,
				TargetRoot:     entry.TargetRoot,
				IgnoreBypassed: entry.IgnoreBypassed,
				BlockSource:    entry.BlockSource,
			}
			if entry.Size != nil {
				sizeCopy := *entry.Size
				record.Size = &sizeCopy
			}
			if err := writePayloadRecord(bw, record); err != nil {
				return err
			}
		}
	}

	if doc.Summary != nil {
		count := doc.Summary.Count
		totalBytes := doc.Summary.Bytes
		tokens := doc.Summary.Tokens
		if err := writePayloadRecord(bw, payloadRecord{
			Type:      "summary",
			Count:     &count,
			Bytes:     &totalBytes,
			HumanSize: doc.Summary.HumanSize,
			Tokens:    &tokens,
			FileWord:  doc.Summary.FileWord,
		}); err != nil {
			return err
		}
	}

	return bw.Flush()
}

// DecodePayload reads a newline-delimited tree document from stdin.
func DecodePayload(r io.Reader) (Document, error) {
	doc := Document{Mode: DocumentModeTree}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	recordCount := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		recordCount++

		var record payloadRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return Document{}, fmt.Errorf("invalid tree payload on line %d: %w", lineNo, err)
		}

		switch record.Type {
		case "meta":
			if record.Version != 0 && record.Version != payloadVersion {
				return Document{}, fmt.Errorf("unsupported tree payload version %d", record.Version)
			}
			switch record.Mode {
			case "", string(DocumentModeTree):
				doc.Mode = DocumentModeTree
			case string(DocumentModeFile):
				doc.Mode = DocumentModeFile
			default:
				return Document{}, fmt.Errorf("unsupported tree payload mode %q", record.Mode)
			}
			doc.Root = normalizeRelPath(record.Root)
		case "target":
			target := DocumentTarget{
				Path:  normalizeRelPath(record.Path),
				Kind:  NormalizeTargetKind(record.Kind),
				State: NormalizeTargetState(record.State),
			}
			if target.Path == "" || (target.Path == "." && target.Kind == TargetKindFile) {
				return Document{}, fmt.Errorf("tree payload target on line %d is missing a valid path", lineNo)
			}
			doc.Target = &target
		case "entry":
			kind := record.Kind
			if kind == "" {
				kind = "file"
			}
			if kind != "file" {
				return Document{}, fmt.Errorf("unsupported tree payload entry kind %q on line %d", kind, lineNo)
			}
			relPath := normalizeRelPath(record.Path)
			if relPath == "" || relPath == "." {
				return Document{}, fmt.Errorf("tree payload entry on line %d is missing a file path", lineNo)
			}
			doc.Entries = append(doc.Entries, DocumentEntry{
				Path:           relPath,
				Size:           record.Size,
				GitStatus:      record.Git,
				ModeTag:        record.Tag,
				TargetRoot:     normalizeRelPath(record.TargetRoot),
				IgnoreBypassed: record.IgnoreBypassed,
				BlockSource:    record.BlockSource,
			})
		case "summary":
			summary := DocumentSummary{
				HumanSize: record.HumanSize,
				FileWord:  record.FileWord,
			}
			if record.Count != nil {
				summary.Count = *record.Count
			}
			if record.Bytes != nil {
				summary.Bytes = *record.Bytes
			}
			if record.Tokens != nil {
				summary.Tokens = *record.Tokens
			}
			doc.Summary = &summary
		case "file":
			doc.Mode = DocumentModeFile
			doc.File = &FilePreview{
				Path:          normalizeRelPath(record.Path),
				HighlightPath: record.Highlight,
				FocusLines:    record.Focus,
				Content:       record.Content,
				MatchPattern:  record.Pattern,
				Truncated:     record.Truncated,
			}
		default:
			return Document{}, fmt.Errorf("unknown tree payload record type %q on line %d", record.Type, lineNo)
		}
	}
	if err := scanner.Err(); err != nil {
		return Document{}, err
	}
	if recordCount == 0 {
		return Document{}, ErrEmptyPayload
	}
	doc.Entries = SortedEntries(doc.Entries)
	doc.EntriesSorted = true
	return doc, nil
}

func writePayloadRecord(w io.Writer, record payloadRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}
