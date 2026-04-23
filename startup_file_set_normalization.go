package catclip

type interactiveFileSetSelectedValue struct {
	raw         string
	normalized  string
	matcher     stageValueMatcher
	isExactFile bool
}

// normalizeInteractiveFileSetStageValues removes redundant exact file values
// from an interactive file-set stage when another selected value already
// covers that same file under the current scope's path-pattern semantics.
//
// This intentionally stays stage-local. It does not rewrite across repeated
// stages or across --then.
func normalizeInteractiveFileSetStageValues(currentArgs []string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	relPaths, err := startupScopeFileSetPaths(currentArgs)
	if err != nil {
		return nil, err
	}
	if len(relPaths) == 0 {
		return dedupeInteractiveFileSetValues(values), nil
	}

	exactFiles := make(map[string]struct{}, len(relPaths))
	for _, relPath := range relPaths {
		normalized := normalizeRelPath(relPath)
		if normalized == "" {
			continue
		}
		exactFiles[normalized] = struct{}{}
	}

	selected := make([]interactiveFileSetSelectedValue, 0, len(values))
	seenRaw := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seenRaw[value]; ok {
			continue
		}
		seenRaw[value] = struct{}{}

		matcher, err := classifyStageValue(value)
		if err != nil {
			return nil, err
		}
		normalized := normalizeRelPath(value)
		_, isExactFile := exactFiles[normalized]
		selected = append(selected, interactiveFileSetSelectedValue{
			raw:         value,
			normalized:  normalized,
			matcher:     matcher,
			isExactFile: isExactFile,
		})
	}

	out := make([]string, 0, len(selected))
	for i, value := range selected {
		if value.isExactFile && interactiveExactFileCoveredByOtherSelection(selected, i) {
			continue
		}
		out = append(out, value.raw)
	}
	return out, nil
}

func interactiveExactFileCoveredByOtherSelection(values []interactiveFileSetSelectedValue, current int) bool {
	target := values[current]
	if !target.isExactFile || target.normalized == "" {
		return false
	}
	for i, other := range values {
		if i == current {
			continue
		}
		if matchesStageValue(target.normalized, other.matcher) {
			return true
		}
	}
	return false
}

func dedupeInteractiveFileSetValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
