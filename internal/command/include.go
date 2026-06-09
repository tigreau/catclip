package command

import (
	"sort"
	"strings"
)

// RewriteDeepIncludeScope detects the "deep include" form — every
// --include value names a path inside a scope target — and rewrites it
// to the equivalent "--include <ancestor> --only <deep paths>" form.
//
// Motivating bug (ACTIVE_BUG_include_ancestor_target_not_authorized):
// `catclip docs --include docs/versions/X.json` was rejected with
// "your --include does not cover this target", because targetIncluded
// only recognized the include set as covering a target when an include
// value EQUALED the target or was its ancestor — never when an include
// value was a descendant. The user's intent ("authorize docs, give me
// exactly X.json") matches the manual form
// `--include docs --only docs/versions/X.json`, which works. This
// rewrite produces that form automatically, so the deep-include shape
// (whether typed or produced by the interactive include picker) just
// works.
//
// Pure path-string transform — no filesystem access. No-op (returns s
// unchanged) unless EVERY --include value is either equal to a scope
// target or a strict descendant of one, and at least one is a strict
// descendant. Any include value outside every target, any glob in an
// include value, a wildcard include, or a "." target leaves the scope
// untouched so existing behavior / errors are preserved. Idempotent:
// once rewritten, every include value equals a target, so a second
// pass is a no-op.
func RewriteDeepIncludeScope(s ExecutionScope) ExecutionScope {
	if len(s.IncludedTargets) == 0 {
		return s
	}
	if includeTargetsContainWildcard(s.IncludedTargets) {
		return s
	}
	targetSet := make(map[string]struct{}, len(s.Targets))
	normTargets := make([]string, 0, len(s.Targets))
	for _, t := range s.Targets {
		t = normalizeRelPath(t)
		if t == "" || t == "." {
			// A root target authorizes everything already; the
			// deep-include rejection cannot arise. Bail.
			return s
		}
		if _, dup := targetSet[t]; dup {
			continue
		}
		targetSet[t] = struct{}{}
		normTargets = append(normTargets, t)
	}
	if len(normTargets) == 0 {
		return s
	}

	keptOrder := make([]string, 0, len(s.IncludedTargets))
	keptSet := make(map[string]struct{}, len(s.IncludedTargets))
	deepOrder := make([]string, 0, len(s.IncludedTargets))
	ancestorSet := make(map[string]struct{}, len(normTargets))

	for _, inc := range s.IncludedTargets {
		n := normalizeRelPath(inc)
		if n == "" || n == "." {
			return s
		}
		if hasGlobChars(n) {
			// Globbed include — out of scope for this rewrite; leave
			// the glob to its existing handling.
			return s
		}
		if _, isTarget := targetSet[n]; isTarget {
			if _, dup := keptSet[n]; !dup {
				keptSet[n] = struct{}{}
				keptOrder = append(keptOrder, n)
			}
			continue
		}
		anc := longestAncestorTarget(n, normTargets)
		if anc == "" {
			// inc is not under any target — a genuine "include does
			// not cover this target" case for that path. Don't
			// rewrite; let the existing resolution / error stand.
			return s
		}
		deepOrder = append(deepOrder, n)
		ancestorSet[anc] = struct{}{}
	}

	if len(deepOrder) == 0 {
		// Every include value equals a target already — this is the
		// plain, already-correct --include form. Nothing to rewrite.
		return s
	}

	ancestors := make([]string, 0, len(ancestorSet))
	for a := range ancestorSet {
		ancestors = append(ancestors, a)
	}
	sort.Strings(ancestors)
	newIncludes := make([]string, 0, len(keptOrder)+len(ancestors))
	newIncludes = append(newIncludes, keptOrder...)
	for _, a := range ancestors {
		if _, dup := keptSet[a]; dup {
			continue
		}
		newIncludes = append(newIncludes, a)
	}

	out := s
	out.IncludedTargets = newIncludes
	out.Stages = rewriteStagesForDeepInclude(s.Stages, newIncludes, deepOrder)
	return out
}

// longestAncestorTarget returns the longest entry in targets that is a
// strict ancestor of path (path lives under target+"/"), or "" if none.
// Longest wins so the most specific authorizing target is chosen when
// the scope has nested targets.
func longestAncestorTarget(path string, targets []string) string {
	best := ""
	for _, t := range targets {
		if strings.HasPrefix(path, t+"/") && len(t) > len(best) {
			best = t
		}
	}
	return best
}

// rewriteStagesForDeepInclude collapses every StageInclude into a single
// include stage carrying newIncludes, and inserts a StageOnly stage
// (carrying the deep paths) immediately after it. Non-include stages
// keep their relative order; the synthesized --only runs right after
// include so the deep paths narrow the authorized scope before any
// downstream filter.
func rewriteStagesForDeepInclude(stages []Stage, newIncludes, deep []string) []Stage {
	out := make([]Stage, 0, len(stages)+1)
	insertedInclude := false
	for _, st := range stages {
		if st.Kind == StageInclude {
			if insertedInclude {
				// Additional include stages — their values are already
				// folded into newIncludes; drop the duplicates.
				continue
			}
			insertedInclude = true
			out = append(out,
				Stage{Kind: StageInclude, Values: append([]string(nil), newIncludes...)},
				Stage{Kind: StageOnly, Values: append([]string(nil), deep...)},
			)
			continue
		}
		out = append(out, st)
	}
	if !insertedInclude {
		// Defensive: IncludedTargets was non-empty but no include stage
		// existed. Prepend the synthesized pair.
		out = append([]Stage{
			{Kind: StageInclude, Values: append([]string(nil), newIncludes...)},
			{Kind: StageOnly, Values: append([]string(nil), deep...)},
		}, out...)
	}
	return out
}
