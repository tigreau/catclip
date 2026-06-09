package git

import (
	"path"
	"strings"
)

// normalizeRelPath is a private copy of the root-package helper. Duplicating
// 11 lines keeps internal/git a leaf (no catclip-domain imports). Mirrors
// internal/search/helpers.go's pattern.
func normalizeRelPath(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "./")
	if value == "." || value == "/" {
		return "."
	}
	return value
}
