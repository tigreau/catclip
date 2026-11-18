package discovery

import (
	"os"
	"path"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

// These positions refer to the scope's diagnostic slice before completion.
// Only failed targets allocate requests; successful resolution stays unchanged.
type pendingIgnoredTargetDiagnostic struct {
	target     string
	start, end int
	explained  bool
}

func (r *Resolver) completeIgnoredDiagnostics(diagnostics []Diagnostic, pending []pendingIgnoredTargetDiagnostic, scopeIndex int, colors platform.Palette) ([]Diagnostic, int) {
	if len(pending) == 0 {
		return diagnostics, 0
	}
	targets := make([]string, 0, len(pending))
	for _, request := range pending {
		targets = append(targets, request.target)
	}
	found := r.findIgnoredAncestorsBatch(targets)
	additionallyExplained := 0
	var omit map[int]bool
	for _, request := range pending {
		index := request.end - 1
		for i := request.start; i < request.end; i++ {
			if diagnostics[i].IsTargetNotFound {
				index = i
				break
			}
		}
		matches := found[normalizeRelPath(request.target)]
		if len(matches) > 0 {
			diagnostics[index] = Diagnostic{Message: ignoredAncestorMessage(request.target, scopeIndex, matches, colors), GroupedMessage: ignoredAncestorMessage(request.target, -1, matches, colors), IsTargetNotFound: true}
			if !request.explained {
				additionallyExplained++
			}
		} else if diagnostics[index].Message == "" {
			// The caller-level fallback previously emitted nothing without evidence.
			if omit == nil {
				omit = make(map[int]bool)
			}
			omit[index] = true
		}
	}
	if len(omit) > 0 {
		out := diagnostics[:0]
		for i, d := range diagnostics {
			if !omit[i] {
				out = append(out, d)
			}
		}
		diagnostics = out
	}
	return diagnostics, additionallyExplained
}

type ignoredDiagnosticQuery struct{ target, parent string }

// One metadata-only query completes all failed targets in this scope. It never
// feeds the classification/selection pipeline or expands a successful target.
func (r *Resolver) findIgnoredAncestorsBatch(targets []string) map[string][]ignoredAncestorCandidate {
	if r.NoIgnore || len(targets) == 0 {
		return nil
	}
	byBase := make(map[string][]ignoredDiagnosticQuery)
	seenTargets := make(map[string]bool)
	parentSet := make(map[string]bool)
	var parents, globs []string
	for _, target := range targets {
		target = normalizeRelPath(target)
		if target == "" || target == "." || strings.ContainsAny(target, "*?[") || seenTargets[target] {
			continue
		}
		seenTargets[target] = true
		base, parent := path.Base(target), path.Dir(target)
		if parent == "" {
			parent = "."
		}
		if _, exists := byBase[base]; !exists {
			literal := base
			if strings.ContainsAny(literal, "{}!") {
				literal = strings.NewReplacer("{", "\\{", "}", "\\}", "!", "\\!").Replace(literal)
			}
			globs = append(globs, literal, "**/"+literal+"/**")
		}
		byBase[base] = append(byBase[base], ignoredDiagnosticQuery{target: target, parent: parent})
		if !parentSet[parent] {
			parentSet[parent] = true
			parents = append(parents, parent)
		}
	}
	if len(byBase) == 0 {
		return nil
	}
	if parentSet["."] {
		parents = nil
	}
	var paths []string
	cached := false
	// Only raw, complete inventories qualify. A text-only target inventory or
	// filtered basename query cannot prove that an ignored directory is absent.
	if os.Getenv("RIPGREP_CONFIG_PATH") == "" {
		paths, cached = r.directoryInventories[directoryInventoryKey{root: ".", noIgnore: true}]
		if !cached && len(parents) == 1 {
			paths, cached = r.directoryInventories[directoryInventoryKey{root: parents[0], noIgnore: true}]
		}
	}
	if !cached {
		var err error
		enumeration := r.membershipEnumeration(search.MembershipReasonIgnoredAncestor)
		enumeration.Authority = search.MembershipAuthorityDiagnostic
		paths, err = search.RunRipgrepFiles(r.Cfg.WorkingDir, search.RipgrepFileOptions{
			NoIgnore: true, Paths: parents, Basenames: globs, Timeout: 5 * time.Second,
			Enumeration: enumeration,
		})
		if err != nil {
			return nil
		} // Preserve the original fallback; never retry walks.
	}
	return r.ignoredCandidatesFromPaths(paths, byBase)
}

func (r *Resolver) ignoredCandidatesFromPaths(paths []string, queries map[string][]ignoredDiagnosticQuery) map[string][]ignoredAncestorCandidate {
	out := make(map[string][]ignoredAncestorCandidate)
	seen := make(map[string]map[string]bool)
	type candidateKey struct {
		path string
		dir  bool
	}
	blocks := make(map[candidateKey]*ignoredAncestorCandidate)
	firstDirectory := make(map[string]int, len(queries))
	add := func(candidate string, isDir bool, requests []ignoredDiagnosticQuery, generation int) {
		for _, q := range requests {
			if q.parent != "." && !strings.HasPrefix(candidate, q.parent+"/") {
				continue
			}
			if isDir {
				if firstDirectory[q.target] == generation {
					continue
				}
				firstDirectory[q.target] = generation
			}
			if seen[q.target] == nil {
				seen[q.target] = make(map[string]bool)
			}
			if seen[q.target][candidate] {
				continue
			}
			seen[q.target][candidate] = true
			key := candidateKey{candidate, isDir}
			block, known := blocks[key]
			if !known {
				block = r.findBlockerForPath(candidate, isDir)
				blocks[key] = block
			}
			if block != nil {
				out[q.target] = append(out[q.target], *block)
			}
		}
	}
	// rg's shared wrapper already returns sorted paths. Retain that order rather
	// than adding sorting or filesystem checks just for diagnostic presentation.
	for index, rel := range paths {
		rel = normalizeRelPath(rel)
		if rel == "" {
			continue
		}
		start := 0
		leaf := path.Base(rel)
		for i := 0; i <= len(rel); i++ {
			if i < len(rel) && rel[i] != '/' {
				continue
			}
			name := rel[start:i]
			if requests := queries[name]; len(requests) > 0 {
				isDir := i < len(rel)
				// Preserve the old per-query candidate semantics: an exact file
				// basename wins within this path; otherwise use its outermost
				// matching directory, not every nested node_modules occurrence.
				if !isDir || name != leaf {
					add(rel[:i], isDir, requests, index+1)
				}
			}
			start = i + 1
		}
	}
	return out
}
