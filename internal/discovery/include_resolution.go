package discovery

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

const (
	includeRecoveryCandidateLimit = 5
	includeRecoveryTimeout        = 5 * time.Second
)

type includeRecoveryResult struct {
	paths     []string
	truncated int
}

// includePathRelatedToScopeTargets is the lexical fast path for exact targets.
// Fuzzy target queries are handled after resolution by checking the selected
// entries, so this helper never guesses what a non-exact query meant.
func includePathRelatedToScopeTargets(includePath string, scopeTargets []string) bool {
	includePath = normalizeRelPath(includePath)
	if includePath == "" {
		return false
	}
	for _, rawTarget := range scopeTargets {
		if hasGlobChars(rawTarget) {
			continue
		}
		target := normalizeRelPath(rawTarget)
		if target == "" || target == "." {
			return true
		}
		if includePath == target ||
			strings.HasPrefix(includePath, target+"/") ||
			strings.HasPrefix(target, includePath+"/") {
			return true
		}
	}
	return false
}

func (r *Resolver) unusedConcreteIncludes(scopeTargets []string, entries []Entry) []string {
	if r.NoIgnore {
		return nil
	}
	unused := make([]string, 0, len(r.IncludedTargets.paths))
	for _, includePath := range r.IncludedTargets.paths {
		if includePathRelatedToScopeTargets(includePath, scopeTargets) {
			continue
		}
		consumed := false
		for _, entry := range entries {
			rel := normalizeRelPath(entry.RelPath)
			if rel == includePath || strings.HasPrefix(rel, includePath+"/") {
				consumed = true
				break
			}
		}
		if !consumed {
			unused = append(unused, includePath)
		}
	}
	return unused
}

func (r *Resolver) unresolvedIncludeDiagnostics(scopeIndex int, scopeTargets, rawIncludes []string, colors platform.Palette) ([]Diagnostic, error) {
	candidates := r.includeRecoveryCandidates(rawIncludes, scopeTargets)
	diagnostics := make([]Diagnostic, 0, len(r.IncludedTargets.unresolved))
	for _, includePath := range r.IncludedTargets.unresolved {
		diagnostics = append(diagnostics, Diagnostic{
			Message:              includePathNotFoundMessage(includePath, scopeIndex, scopeTargets, candidates[includePath], colors),
			IsError:              true,
			IsScopeUnsatisfiable: true,
			ExplainsEmptyResult:  true,
			ScopeIndex:           scopeIndex,
		})
	}
	return diagnostics, nil
}

func (r *Resolver) unusedIncludeDiagnostics(scopeIndex int, scopeTargets, rawIncludes, unused []string, colors platform.Palette) ([]Diagnostic, error) {
	candidates := r.includeRecoveryCandidates(rawIncludes, scopeTargets)
	diagnostics := make([]Diagnostic, 0, len(unused))
	for _, includePath := range unused {
		diagnostics = append(diagnostics, Diagnostic{
			Message:              includeOutsideTargetsMessage(includePath, scopeIndex, scopeTargets, candidates[includePath], colors),
			IsError:              true,
			IsScopeUnsatisfiable: true,
			ExplainsEmptyResult:  true,
			ScopeIndex:           scopeIndex,
		})
	}
	return diagnostics, nil
}

// includeRecoveryCandidates performs one lazy, target-bounded rg probe for all
// include values in the scope. It is diagnostics-only: callers never promote a
// candidate into IncludedTargets. Timeout or classification failures simply
// remove the optional suggestions while preserving the primary error.
func (r *Resolver) includeRecoveryCandidates(rawIncludes, scopeTargets []string) map[string]includeRecoveryResult {
	queries := make([]string, 0, len(rawIncludes))
	patterns := make([]string, 0, len(rawIncludes)*2)
	seenQueries := make(map[string]struct{}, len(rawIncludes))
	for _, raw := range rawIncludes {
		query := normalizeRelPath(raw)
		if query == "" || query == "." {
			continue
		}
		if _, duplicate := seenQueries[query]; duplicate {
			continue
		}
		seenQueries[query] = struct{}{}
		queries = append(queries, query)
		base := path.Base(query)
		patterns = append(patterns, base, "**/"+base+"/**")
	}
	if len(queries) == 0 {
		return nil
	}

	narrowed := narrowableScopeTargets(r.Cfg.WorkingDir, scopeTargets)
	if len(narrowed) == 0 && !scopeTargetsContainRoot(scopeTargets) {
		return nil
	}
	paths, err := search.RunRipgrepFiles(r.Cfg.WorkingDir, search.RipgrepFileOptions{
		NoIgnore:  true,
		Basenames: DedupePreserveOrder(patterns),
		Paths:     narrowed,
		Timeout:   includeRecoveryTimeout,
	})
	if err != nil {
		return nil
	}

	resultSets := make(map[string]map[string]struct{}, len(queries))
	for _, query := range queries {
		resultSets[query] = make(map[string]struct{})
	}
	for _, rawPath := range paths {
		rel := normalizeRelPath(rawPath)
		if rel == "" {
			continue
		}
		for _, query := range queries {
			candidate, isDir := classifyAncestorCandidate(rel, path.Base(query))
			if candidate == "" || !candidateMatchesIncludeQuery(candidate, query) {
				continue
			}
			if r.findBlockerForPath(candidate, isDir) == nil {
				continue
			}
			resultSets[query][candidate] = struct{}{}
		}
	}

	results := make(map[string]includeRecoveryResult, len(resultSets))
	for query, set := range resultSets {
		values := make([]string, 0, len(set))
		for candidate := range set {
			values = append(values, candidate)
		}
		sort.Strings(values)
		result := includeRecoveryResult{}
		if len(values) > includeRecoveryCandidateLimit {
			result.truncated = len(values) - includeRecoveryCandidateLimit
			values = values[:includeRecoveryCandidateLimit]
		}
		result.paths = values
		results[query] = result
	}
	return results
}

func candidateMatchesIncludeQuery(candidate, query string) bool {
	if candidate == query {
		return true
	}
	return strings.HasSuffix(candidate, "/"+query)
}

func scopeTargetsContainRoot(scopeTargets []string) bool {
	for _, target := range scopeTargets {
		target = normalizeRelPath(target)
		if target == "" || target == "." {
			return true
		}
	}
	return false
}

func includePathNotFoundMessage(includePath string, scopeIndex int, scopeTargets []string, candidates includeRecoveryResult, colors platform.Palette) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s%sError:%s%s --include path %s was not found from the current directory (scope %d).%s\n",
		colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(includePath), scopeIndex+1, colors.Reset)
	writeIncludeRecovery(&b, scopeTargets, candidates, colors)
	return b.String()
}

func includeOutsideTargetsMessage(includePath string, scopeIndex int, scopeTargets []string, candidates includeRecoveryResult, colors platform.Palette) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s%sError:%s%s --include path %s is outside the selected target%s (scope %d), so it cannot affect this command.%s\n",
		colors.Bold, colors.Err, colors.Reset, colors.Err, SingleQuoted(includePath), pluralScopeTarget(scopeTargets), scopeIndex+1, colors.Reset)
	writeIncludeRecovery(&b, scopeTargets, candidates, colors)
	return b.String()
}

func writeIncludeRecovery(b *strings.Builder, scopeTargets []string, candidates includeRecoveryResult, colors platform.Palette) {
	if len(candidates.paths) == 0 {
		fmt.Fprintf(b, "\n  %sSpecific --include paths are written from the current directory.%s\n", colors.Dim, colors.Reset)
		return
	}
	label := "Possible ignored paths"
	if len(candidates.paths) == 1 && candidates.truncated == 0 {
		label = "Possible ignored path"
	}
	fmt.Fprintf(b, "\n  %s%s below %s:%s\n", colors.Dim, label, formattedScopeTargets(scopeTargets), colors.Reset)
	for _, candidate := range candidates.paths {
		fmt.Fprintf(b, "    %s\n", candidate)
	}
	if candidates.truncated > 0 {
		fmt.Fprintf(b, "    ... and %d more\n", candidates.truncated)
	}
	fmt.Fprintf(b, "\n  %sUse the complete path:%s\n    %scatclip %s --include %s%s\n",
		colors.Dim, colors.Reset, colors.OK, formattedCommandTargets(scopeTargets), SingleQuoted(candidates.paths[0]), colors.Reset)
}

func formattedScopeTargets(scopeTargets []string) string {
	if len(scopeTargets) == 1 {
		return SingleQuoted(normalizeRelPath(scopeTargets[0]))
	}
	return "the selected targets"
}

func formattedCommandTargets(scopeTargets []string) string {
	parts := make([]string, 0, len(scopeTargets))
	for _, target := range scopeTargets {
		target = normalizeRelPath(target)
		if target == "" {
			target = "."
		}
		parts = append(parts, SingleQuoted(target))
	}
	return strings.Join(parts, " ")
}

func pluralScopeTarget(scopeTargets []string) string {
	if len(scopeTargets) == 1 {
		return ""
	}
	return "s"
}
