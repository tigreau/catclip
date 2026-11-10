package theme

import (
	"embed"
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// Keep only the supported upstream palettes, byte-for-byte from the pinned
// Chroma release. Importing chroma/styles eagerly parses its entire catalog at
// process startup, even for headless output. These bytes stay untouched until
// Lookup needs one palette; its immutable Theme is then cached for the process.
// Sources and license are recorded in SOURCES.md.
//
//go:embed palettes/*.xml
var paletteFiles embed.FS

func loadStyle(id string) *chroma.Style {
	if style := customStyle(id); style != nil {
		return style
	}
	data, err := paletteFiles.ReadFile("palettes/" + id + ".xml")
	if err != nil {
		return nil
	}
	return chroma.MustNewXMLStyle(strings.NewReader(string(data)))
}
