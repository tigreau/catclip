package ui

import (
	"bytes"
	"context"
	"io"
	"os"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/output"
)

func bytesContainBinaryPreviewData(body []byte) bool {
	return bytes.IndexByte(body, 0) >= 0
}

// Inspect only bytes needed by the bounded preview, never classify the whole
// selection. Text continues through the same reader without a second open.
func presentSinkFileUnit(ctx context.Context, w io.Writer, unit output.PreparedFileUnit, raw bool, limit int64, replaced *bool) (bool, error) {
	entry := unit.Entry
	if (entry.Mode != "" && entry.Mode != command.EntryModeFull) || entry.Lines || len(unit.Payload) > 0 || len(unit.SnippetRanges) > 0 {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	writeBinary := func() (bool, error) {
		*replaced = true
		text := binaryPreviewText(entry.RelPath, unit.BodyBytes)
		if raw {
			_, err := io.WriteString(w, entry.RelPath+"\n"+text+"\n")
			return true, err
		}
		return true, output.EmitWrappedReader(w, entry.RelPath, "binary-preview", bytes.NewBufferString(text))
	}
	if entry.HasBinaryHint() {
		return writeBinary()
	}
	f, err := os.Open(entry.AbsPath)
	if err != nil {
		return true, err
	}
	defer f.Close()
	// NUL inspection here is a display safeguard, not a classification cache
	// result. It neither claims text eligibility nor adds content reads.
	prefixLimit := limit
	if unit.BodyBytes > 0 && unit.BodyBytes < prefixLimit {
		prefixLimit = unit.BodyBytes
	}
	prefix := make([]byte, prefixLimit)
	n, err := io.ReadFull(f, prefix)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return true, err
	}
	prefix = prefix[:n]
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if bytesContainBinaryPreviewData(prefix) {
		// UTF-16 text also contains byte NULs. Avoid labelling that encoding
		// binary just because this byte-oriented display cannot show it.
		if utf16TextPreviewPrefix(prefix) {
			*replaced = true
			text := "UTF-16 text\nContents are not displayed in this preview.\n"
			if raw {
				_, err := io.WriteString(w, entry.RelPath+"\n"+text+"\n")
				return true, err
			}
			return true, output.EmitWrappedReader(w, entry.RelPath, "text-preview", bytes.NewBufferString(text))
		}
		return writeBinary()
	}
	var reader io.Reader = bytes.NewReader(prefix)
	if int64(len(prefix)) == limit {
		reader = io.MultiReader(reader, f)
	}
	if raw {
		_, err := io.Copy(w, reader)
		return true, err
	}
	return true, output.EmitWrappedReader(w, entry.RelPath, "", reader)
}

func utf16TextPreviewPrefix(body []byte) bool {
	if len(body) < 2 || !((body[0] == 0xff && body[1] == 0xfe) || (body[0] == 0xfe && body[1] == 0xff)) {
		return false
	}
	for i := 2; i+1 < len(body); i += 2 {
		if body[i] == 0 && body[i+1] == 0 {
			return false
		}
	}
	return true
}
