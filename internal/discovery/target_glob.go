package discovery

import (
	"path"
	"path/filepath"

	"github.com/tigreau/catclip/internal/search"
)

// discoverGlobCandidateFiles applies the target's existing basename-or-path
// matcher to the inventory before classifying content. A target defines the
// scope; this does not move any of the user's ordered filter stages.
func (r *Resolver) discoverGlobCandidateFiles(pattern string) ([]Entry, error) {
	// Validate even when the inventory is empty. No filesystem work is needed
	// to diagnose malformed target syntax.
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, newUsageError("Error: Invalid glob pattern %s: %v", SingleQuoted(pattern), err)
	}
	reason := search.MembershipReasonCanonicalFallback
	if r.NoIgnore {
		reason = search.MembershipReasonNoIgnoreExpansion
	}
	rels, err := ripgrepListUnder(r.Cfg.WorkingDir, ".", r.NoIgnore, r.membershipEnumeration(reason))
	if err != nil {
		return nil, err
	}
	candidates := make([]string, 0)
	for _, rel := range rels {
		matched, _ := path.Match(pattern, path.Base(rel))
		if !matched {
			matched, _ = path.Match(pattern, rel)
		}
		if matched {
			candidates = append(candidates, rel)
		}
	}
	if !r.NoIgnore {
		entries, err := r.textEntriesFromRipgrepPaths(candidates)
		return markEntriesGitVisible(entries), err
	}

	// Keep classification local to this target. Publishing this subset as the
	// resolver's complete text set would exclude later literal or fuzzy targets.
	var textSet map[string]struct{}
	if !r.WithBinaries {
		var binarySet map[string]struct{}
		textSet, binarySet, err = search.ClassifyTextPathsWithBinaryEvidence(r.Cfg.WorkingDir, candidates)
		r.lastClassificationExcludedBinary = len(binarySet) > 0
		if err != nil {
			return nil, err
		}
	}
	entries := make([]Entry, 0, len(candidates))
	for _, rel := range candidates {
		if !r.WithBinaries {
			if _, ok := textSet[rel]; !ok {
				continue
			}
		}
		entry := Entry{RelPath: rel, AbsPath: filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(rel))}
		block, err := r.fileBlockedBy(rel)
		if err != nil {
			return nil, err
		}
		if block != nil {
			entry = withIgnoreBypassed(entry, *block)
		} else {
			entry.GitVisible = true
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
