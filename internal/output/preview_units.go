package output

import (
	"io"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
)

// WriteOutputPlanPreview preserves the prepared plan's order and boundaries,
// with an optional per-unit display replacement. Replacements never affect
// real emission or byte accounting. Like other previews it does not prefetch.
func WriteOutputPlanPreview(w io.Writer, cfg EmitConfig, plan Plan, present func(io.Writer, PreparedFileUnit) (bool, error), palettes ...platform.Palette) error {
	for i, section := range plan.sections {
		if i > 0 && !cfg.Raw {
			if _, err := io.WriteString(w, outputSectionSeparator(plan.sections[i-1].kind, section.kind)); err != nil {
				return err
			}
		}
		if section.kind == SectionKindMetadata {
			colors := platform.Palette{}
			if len(palettes) > 0 {
				colors = palettes[0]
			}
			if _, err := section.writeMetadata(w, colors); err != nil {
				return err
			}
			continue
		}
		for _, item := range section.items {
			if item.kind == SectionKindPaths {
				if _, err := io.WriteString(w, item.relPath+"\n"); err != nil {
					return err
				}
				continue
			}
			if present != nil {
				handled, err := present(w, item.unit)
				if err != nil {
					return err
				}
				if handled {
					continue
				}
			}
			if cfg.Raw {
				if item.mode != command.EntryModeFull && item.mode != command.EntryModeLines {
					return newUsageError("Error: --raw requires full-file or line-slice items.")
				}
				if item.unit.Entry.Lines {
					entry := item.unit.Entry
					if err := emitLinesSliceRaw(w, entry.AbsPath, entry.LinesStart, entry.LinesEnd); err != nil {
						return err
					}
				} else if err := emitFileBodyFromDisk(w, item.unit.Entry.AbsPath); err != nil {
					return err
				}
			} else if err := emitEntry(w, item.unit, 0, nil); err != nil {
				return err
			}
		}
	}
	return nil
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
