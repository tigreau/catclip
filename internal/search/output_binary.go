package search

import (
	"fmt"
	"path/filepath"
)

// OutputBinary classifies only a selected output file. Name shortcuts and
// completed invocation observations avoid content reads; unknown names use the
// same decoded-NUL scanner as discovery. Failure is never binary evidence.
func OutputBinary(absPath, relPath string, size int64) (bool, error) {
	if size == 0 {
		return false, nil
	}
	absPath = filepath.Clean(absPath)
	invocationClassifications.mu.Lock()
	observed, ok := invocationClassifications.complete[absPath]
	invocationClassifications.mu.Unlock()
	if ok {
		return observed.class == contentBinary, nil
	}
	switch classifyPathByName(relPath) {
	case nameClassText:
		return false, nil
	case nameClassBinary:
		return true, nil
	}
	result := invocationClassifications.classify(reloadCancelCtx, absPath, make([]byte, textScanBufferSize), classifyRegularFile)
	if err := reloadCancelCtx.Err(); err != nil {
		return false, err
	}
	if result.class == contentUnconfirmed {
		return false, fmt.Errorf("could not classify selected file %q for text output", absPath)
	}
	return result.class == contentBinary, nil
}
