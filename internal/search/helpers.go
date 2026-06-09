package search

import (
	"path"
	"strings"
)

// hasGlobChars / normalizeRelPath / dedupeSortedStrings are private copies of
// root-package helpers used by the rg wrapper. They are stdlib-only string
// utilities; duplicating them privately keeps internal/search a leaf (no
// catclip-domain imports) at the cost of ~30 lines of trivial code.

func hasGlobChars(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

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

func dedupeSortedStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
