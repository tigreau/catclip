package ui

import (
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/search"
)

// maxLinesForSizedEntries is the root-side bridge: it maps a domain
// []discovery.Entry into the leaf []search.SizedFile that the rg wrapper accepts,
// then delegates to search.MaxLinesForSizedFiles. Domain coupling stops here
// so the search package can stay leaf-utility.
func maxLinesForSizedEntries(entries []discovery.Entry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	files := make([]search.SizedFile, len(entries))
	for i, entry := range entries {
		files[i] = search.SizedFile{
			AbsPath:   entry.AbsPath,
			SizeBytes: entry.SizeBytes,
			SizeKnown: entry.SizeKnown,
		}
	}
	return search.MaxLinesForSizedFiles(files)
}
