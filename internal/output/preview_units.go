package output

import (
	"errors"
	"io"
	"io/fs"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

// WriteOutputPlanPreview preserves the prepared plan's order and boundaries,
// with an optional per-unit display replacement. Replacements never affect
// real emission or byte accounting. Like other previews it does not prefetch.
func WriteOutputPlanPreview(w io.Writer, cfg EmitConfig, plan Plan, present func(io.Writer, PreparedFileUnit) (bool, error), palettes ...platform.Palette) ([]search.AccessFailure, error) {
	observed := &payloadWriter{w: w}
	w = observed
	var skipped []search.AccessFailure
	for i, section := range plan.sections {
		if i > 0 && !cfg.Raw {
			if _, err := io.WriteString(w, outputSectionSeparator(plan.sections[i-1].kind, section.kind)); err != nil {
				return skipped, err
			}
		}
		if section.kind == SectionKindMetadata {
			colors := platform.Palette{}
			if len(palettes) > 0 {
				colors = palettes[0]
			}
			if _, err := section.writeMetadata(w, colors); err != nil {
				return skipped, err
			}
			continue
		}
		for _, item := range section.items {
			if item.kind == SectionKindPaths {
				if _, err := io.WriteString(w, item.relPath+"\n"); err != nil {
					return skipped, err
				}
				continue
			}
			before := observed.n
			if err := writePreviewFileUnit(w, cfg, item, present); err != nil {
				// Use the final emitter's actual-write boundary. Only source
				// permission/vanished errors before this unit writes can skip.
				if !observed.skip(err, item.unit.Entry.AbsPath, item.relPath, before) {
					return skipped, err
				}
				skipped = append(skipped, search.AccessFailure{Path: item.relPath, Vanished: errors.Is(err, fs.ErrNotExist)})
			}
		}
	}
	return skipped, nil
}

func writePreviewFileUnit(w io.Writer, cfg EmitConfig, item PlanItem, present func(io.Writer, PreparedFileUnit) (bool, error)) error {
	if present != nil {
		handled, err := present(w, item.unit)
		if err != nil || handled {
			return err
		}
	}
	if !cfg.Raw {
		return emitEntry(w, item.unit, 0, nil)
	}
	if item.mode != command.EntryModeFull && item.mode != command.EntryModeLines {
		return newUsageError("Error: --raw requires full-file or line-slice items.")
	}
	entry := item.unit.Entry
	if entry.Lines {
		return emitLinesSliceRaw(w, entry.AbsPath, entry.LinesStart, entry.LinesEnd)
	}
	return emitFileBodyFromDisk(w, entry.AbsPath)
}

// HasBinaryHint reads only evidence already present in the plan/dictionary.
func (p Plan) HasBinaryHint() bool {
	for _, item := range p.items {
		if item.kind == SectionKindFiles && item.unit.Entry.HasBinaryHint() {
			return true
		}
	}
	return false
}
