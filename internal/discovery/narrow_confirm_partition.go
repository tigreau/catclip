package discovery

import "strings"

// PartitionIgnoredByIncludes splits discovered entries into two views used
// by the v0.6.x narrow-confirm screen:
//
//   - allEntries: the unchanged entry list, identical to the "Keep all
//     current files" tree.
//   - ignoredEntries: the subset whose paths descend from (or equal) one of
//     the supplied include paths AND that came in via `--include`
//     authorization rather than the base visible-file walk. This is the
//     "Keep only ignored files" tree.
//
// Pure function. Stable order (inherits the input order). Lets the narrow-
// confirm screen render both previews from a single EvaluateScope result
// — see ACTIVE_PLAN_include_narrow_confirm.md "Avoiding re-discovery."
func PartitionIgnoredByIncludes(entries []Entry, includePaths []string, noIgnore bool) (allEntries, ignoredEntries []Entry) {
	allEntries = entries
	if len(entries) == 0 || (len(includePaths) == 0 && !noIgnore) {
		return entries, nil
	}
	normalizedIncludes := make([]string, 0, len(includePaths))
	for _, p := range includePaths {
		n := normalizeRelPath(p)
		if n == "" || n == "." {
			continue
		}
		normalizedIncludes = append(normalizedIncludes, n)
	}
	if len(normalizedIncludes) == 0 && !noIgnore {
		return entries, nil
	}
	for _, e := range entries {
		if !e.AllowedByInclude {
			continue
		}
		if noIgnore {
			ignoredEntries = append(ignoredEntries, e)
			continue
		}
		rel := normalizeRelPath(e.RelPath)
		for _, inc := range normalizedIncludes {
			if rel == inc || strings.HasPrefix(rel, inc+"/") {
				ignoredEntries = append(ignoredEntries, e)
				break
			}
		}
	}
	return entries, ignoredEntries
}
