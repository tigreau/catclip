package search

import "path/filepath"

// BinaryHint uses only existing evidence and the built-in filename dictionary.
// Unknown names stay neutral. It never stats or opens a file, and successful
// content observations take priority over a filename shortcut.
func BinaryHint(absPath, relPath string, size int64, sizeKnown bool) bool {
	if sizeKnown && size == 0 {
		return false
	}
	if absPath != "" {
		invocationClassifications.mu.Lock()
		observed, ok := invocationClassifications.complete[filepath.Clean(absPath)]
		invocationClassifications.mu.Unlock()
		if ok {
			return observed.class == contentBinary
		}
	}
	return classifyPathByName(relPath) == nameClassBinary
}
