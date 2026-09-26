package search

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
)

const tinyResidueMaxFiles = 2
const tinyResidueMaxBytes = 1 << 20

// scanTinyResidue is an exact byte-NUL fast path, not a prefix classifier.
// UTF-16 BOMs require rg's decoder, and special/large paths keep rg's handling.
// Configured rg behavior is also left entirely to rg.
func scanTinyResidue(ctx context.Context, workingDir string, paths []string, observations ...map[string]os.FileInfo) (map[string]struct{}, []string, error) {
	if len(paths) > tinyResidueMaxFiles || os.Getenv("RIPGREP_CONFIG_PATH") != "" {
		return nil, paths, nil
	}
	text := make(map[string]struct{}, len(paths))
	var fallback []string
	buf := make([]byte, 32*1024)
	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		abs := filepath.Join(workingDir, filepath.FromSlash(rel))
		info, _ := os.Lstat(abs)
		if len(observations) > 0 && observations[0] != nil {
			observations[0][normalizeRelPath(rel)] = info
		}
		isText, handled, err := scanTinyResidueFile(ctx, abs, info, buf)
		if err != nil {
			return nil, nil, err
		}
		if !handled {
			fallback = append(fallback, rel)
			continue
		}
		if normalized := normalizeRelPath(rel); isText && normalized != "" && normalized != "." {
			text[normalized] = struct{}{}
		}
	}
	return text, fallback, ctx.Err()
}

func scanTinyResidueFile(ctx context.Context, abs string, info os.FileInfo, buf []byte) (isText, handled bool, err error) {
	if info == nil {
		return false, true, nil
	}
	if !info.Mode().IsRegular() || info.Size() > tinyResidueMaxBytes {
		return false, false, nil
	}
	f, openErr := os.Open(abs)
	if openErr != nil {
		return false, true, nil
	}
	defer f.Close()
	// Read the complete BOM prefix even if a regular-file read is short.
	n, firstErr := io.ReadFull(f, buf[:2])
	if n == 2 && (bytes.Equal(buf[:2], []byte{0xff, 0xfe}) || bytes.Equal(buf[:2], []byte{0xfe, 0xff})) {
		return false, false, nil
	}
	if bytes.IndexByte(buf[:n], 0) >= 0 {
		return false, true, nil
	}
	if firstErr == io.EOF || firstErr == io.ErrUnexpectedEOF {
		return true, true, nil
	}
	if firstErr != nil {
		return false, true, nil
	}
	total := n
	for {
		if err := ctx.Err(); err != nil {
			return false, false, err
		}
		n, readErr := f.Read(buf)
		total += n
		if total > tinyResidueMaxBytes {
			return false, false, nil
		}
		if bytes.IndexByte(buf[:n], 0) >= 0 {
			return false, true, nil
		}
		if readErr == io.EOF {
			return true, true, nil
		}
		if readErr != nil {
			return false, true, nil
		}
	}
}
