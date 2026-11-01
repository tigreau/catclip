package output

import (
	"fmt"
	"path/filepath"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/search"
)

func rawPreparation(raw []bool) bool { return len(raw) > 0 && raw[0] }

// BinaryDescription uses filename information only; it does not claim to have
// decoded a format. The exact byte size remains available even after rounding.
func BinaryDescription(relPath string, size int64) string {
	kind := "Unknown"
	if ext := filepath.Ext(relPath); ext != "" {
		kind = fmt.Sprintf("%q extension (format not verified)", ext)
	}
	sizeText, _ := formatSizeAndTokens(size, 1)
	return fmt.Sprintf("Format: %s\nSize: %s (%d bytes)\nBinary contents omitted.\n", kind, sizeText, size)
}

func prepareBinaryDescription(entry discovery.Entry) (PreparedFileUnit, bool, error) {
	size, err := FileBodySize(entry)
	if err != nil {
		return PreparedFileUnit{}, false, err
	}
	entry.SizeBytes, entry.SizeKnown = size, true
	binary := entry.Binary && size > 0
	if !binary {
		binary, err = search.OutputBinary(entry.AbsPath, entry.RelPath, size)
		if err != nil {
			return PreparedFileUnit{}, false, err
		}
	}
	if !binary {
		return PreparedFileUnit{Entry: entry}, false, nil
	}
	entry.Binary = true
	// A description is not a source-line excerpt. Keep its tree annotations
	// consistent with the payload rather than claiming the requested lines.
	entry.Mode = command.EntryModeFull
	entry.Lines = false
	entry.LinesStart, entry.LinesEnd = 0, 0
	body := BinaryDescription(entry.RelPath, size)
	payload, n := BuildWrappedPayload(entry.RelPath, "binary", []byte(body))
	return PreparedFileUnit{Entry: entry, Payload: payload, BodyBytes: n, sourceType: "binary"}, true, nil
}
