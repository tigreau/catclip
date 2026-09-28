package render

import "io"

// PreparedTree owns immutable, sorted rows and directory disambiguation facts.
// It holds neither colored/rendered bytes nor file bodies, and can render a
// capped picker preview and an uncapped final tree with different palettes.
type PreparedTree struct {
	entries   []DocumentEntry
	landmarks map[string]bool
}

func PrepareTree(entries []DocumentEntry) *PreparedTree {
	rows := SortedEntries(entries)
	for i := range rows {
		if rows[i].Size != nil {
			size := *rows[i].Size
			rows[i].Size = &size
		}
	}
	return &PreparedTree{entries: rows, landmarks: detectLandmarks(rows)}
}

// Render writes tree rows only; the caller supplies headers and summary.
func (p *PreparedTree) Render(w io.Writer, opts RenderOptions, colors Palette) error {
	return renderEntriesWithLandmarks(w, p.entries, opts, colors, p.landmarks)
}
