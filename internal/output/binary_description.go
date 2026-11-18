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
	return binaryFacts(relPath, size) + "Binary contents omitted.\n"
}

// AttachedBinaryDescription is the bundle entry for a binary that the same
// clipboard copy also offers as its original file. It names the attachment,
// which receiving apps usually show by file name only, so readers can match
// it to this project path.
func AttachedBinaryDescription(relPath string, size int64) string {
	return binaryFacts(relPath, size) + fmt.Sprintf("Attached separately as the original file %q.\n", filepath.Base(relPath))
}

func binaryFacts(relPath string, size int64) string {
	kind := "Unknown"
	if ext := filepath.Ext(relPath); ext != "" {
		kind = fmt.Sprintf("%q extension (format not verified)", ext)
	}
	sizeText, _ := formatSizeAndTokens(size, 1)
	return fmt.Sprintf("Format: %s\nSize: %s (%d bytes)\n", kind, sizeText, size)
}

// withAttachedBinaryDescriptions rewrites the bundle entries of binaries that
// are attached as original files. It copies the item slices: retained plans
// shared with previews and other sinks stay unchanged.
func (p Plan) withAttachedBinaryDescriptions(refs []BinaryReference) Plan {
	if len(refs) == 0 {
		return p
	}
	attached := make(map[string]bool, len(refs))
	for _, ref := range refs {
		attached[ref.RelPath] = true
	}
	// Copy-on-write: a slice is copied only if it holds an attached binary,
	// and each new entry is built once even when sections share it.
	built := make(map[string]PreparedFileUnit, len(refs))
	rewrite := func(items []PlanItem) []PlanItem {
		var out []PlanItem
		for i := range items {
			item := items[i]
			if item.kind != SectionKindFiles || item.unit.sourceType != "binary" || !attached[item.relPath] {
				continue
			}
			if out == nil {
				out = append([]PlanItem(nil), items...)
			}
			unit, ok := built[item.relPath]
			if !ok {
				unit = item.unit
				body := AttachedBinaryDescription(item.relPath, item.entry.SizeBytes)
				unit.Payload, unit.BodyBytes = BuildWrappedPayload(item.relPath, "binary", []byte(body))
				built[item.relPath] = unit
			}
			out[i].unit, out[i].bodyBytes = unit, unit.BodyBytes
		}
		if out == nil {
			return items
		}
		return out
	}
	p.items = rewrite(p.items)
	p.sections = append([]PlanSection(nil), p.sections...)
	for i := range p.sections {
		p.sections[i].items = rewrite(p.sections[i].items)
	}
	return p
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
