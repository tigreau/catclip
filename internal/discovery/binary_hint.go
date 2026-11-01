package discovery

import "github.com/tigreau/catclip/internal/search"

// HasBinaryHint consults retained evidence and filename rules without I/O.
// Empty files remain text even if an earlier size-less filename hint existed.
func (e Entry) HasBinaryHint() bool {
	return !(e.SizeKnown && e.SizeBytes == 0) &&
		(e.Binary || search.BinaryHint(e.AbsPath, e.RelPath, e.SizeBytes, e.SizeKnown))
}
