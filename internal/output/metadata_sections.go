package output

import (
	"fmt"
	"io"

	"github.com/tigreau/catclip/internal/platform"
)

// MetadataPayload keeps report presentation in UI without making output import
// UI or render. Prepared reports are reused by measurement, preview and emission.
type MetadataPayload interface {
	EncodedSize() (int64, error)
	WriteMetadata(io.Writer, platform.Palette) (int64, error)
}

func (p Plan) HasMetadata() bool {
	for _, section := range p.sections {
		if section.kind == SectionKindMetadata {
			return true
		}
	}
	return false
}

func (p Plan) MetadataPrepared() bool {
	for _, section := range p.sections {
		if section.kind == SectionKindMetadata && section.metadata == nil {
			return false
		}
	}
	return true
}

// PrepareMetadata returns a new plan sharing immutable items. The callback runs
// once per metadata section, never during output or a repeated sink preview.
func (p Plan) PrepareMetadata(build func([]int, Plan) (MetadataPayload, error)) (Plan, error) {
	if !p.HasMetadata() {
		return p, nil
	}
	for _, section := range p.sections {
		if section.kind == SectionKindMetadata && section.metadata == nil {
			p.sections = append([]PlanSection(nil), p.sections...)
			for i := range p.sections {
				s := &p.sections[i]
				if s.kind != SectionKindMetadata || s.metadata != nil {
					continue
				}
				// Borrow the immutable rows. Metadata needs entry facts, not
				// another inventory or a temporary array of prepared file bodies.
				payload, err := build(s.scopeIndices, Plan{items: s.items})
				if err != nil {
					return Plan{}, err
				}
				s.metadataBytes, err = payload.EncodedSize()
				if err != nil {
					return Plan{}, err
				}
				s.metadata = payload
			}
			break
		}
	}
	return p, nil
}

func (s PlanSection) writeMetadata(w io.Writer, colors platform.Palette) (int64, error) {
	if s.metadata == nil {
		return 0, fmt.Errorf("internal error: metadata section has not been prepared")
	}
	return s.metadata.WriteMetadata(w, colors)
}
