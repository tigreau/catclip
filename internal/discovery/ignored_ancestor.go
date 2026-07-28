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

// --- Ignored-ancestor probe: when a typed *target* doesn't resolve, ask whether
// the basename exists *anywhere* in the ignored set under (the parent of) the
// typed path, and if so, attribute the highest-precedence ancestor (.gitignore
// > .hiss). Triggers only on target-resolution miss — never for filters or for
// `--include '*'` runs. See
// docs/versions/v0.5.7/reports/ACTIVE_PLAN_surface_ignored_ancestor.md.

// ignoredAncestorCandidate is one file the probe found under the ignored set,
// with the path on disk and the blocking (file or ancestor) attribution.
type ignoredAncestorCandidate struct {
	Path    string // relative to workingDir, slash-form
	Blocker string // == Path when the file itself matches a rule; else a parent
	Source  string // ".hiss" / ".gitignore" / ".git/info/exclude" / etc.
}

// targetNotFoundOrIgnoredAncestorMessage is the call-site shim: try the
// ancestor probe first; if it surfaces ≥1 candidate, render the tailored
// "hidden by ignored ancestor" error. Otherwise fall back to today's
// targetNotFoundWarning unchanged.
func targetNotFoundOrIgnoredAncestorMessage(r *Resolver, target string, scopeIndex int, colors platform.Palette) string {
	if cands := r.findIgnoredAncestors(target); len(cands) > 0 {
		return ignoredAncestorMessage(target, scopeIndex, cands, colors)
	}
	return targetNotFoundWarning(target, scopeIndex, colors)
}

// findIgnoredAncestors runs the lookup-miss probe. Returns nil when:
//   - --include '*' is active (ignore is disabled, nothing to surface);
//   - the target itself looks glob-shaped (defensive — globs shouldn't reach
//     this code, but if they did the probe would be meaningless);
//   - rg unavailable, errored, or the 5s hung-process guard tripped;
//   - no on-disk candidates with that basename exist anywhere under the typed
//     parent dir;
//   - candidates exist but none are actually blocked (shouldn't happen — they
//     came from --no-ignore, so they're either visible or blocked — but we
//     guard rather than assert).
func (r *Resolver) findIgnoredAncestors(target string) []ignoredAncestorCandidate {
	if r.IncludedTargets.wildcard {
		return nil
	}
	target = normalizeRelPath(target)
	if target == "" || target == "." {
		return nil
	}
	if strings.ContainsAny(target, "*?[") {
		return nil
	}
	// If the user already named this target via --include, they know it's
	// blocked — the ancestor "hidden by ignore" framing is just noise. Skip.
	if r.targetIncluded(target) {
		return nil
	}

	base := path.Base(target)
	parent := path.Dir(target)
	if parent == "" || parent == "." {
		parent = "."
	}

	// Two globs so we catch both file-targets (basename match) and dir-targets
	// (rg --files lists files only, so a dir-target like `docker` is found via
	// files inside a `docker/` segment anywhere). gitignore-style globs without
	// a leading `**/` are anchored, so the inside-dir form needs explicit
	// `**/<name>/**` to match at any depth.
	opts := search.RipgrepFileOptions{
		NoIgnore:  true,
		Basenames: []string{base, "**/" + base + "/**"},
		Timeout:   5 * time.Second,
	}
	if parent != "." {
		opts.Paths = []string{parent}
	}
	paths, err := search.RunRipgrepFiles(r.Cfg.WorkingDir, opts)
	if err != nil || len(paths) == 0 {
		return nil
	}

	seen := map[string]bool{}
	out := make([]ignoredAncestorCandidate, 0, len(paths))
	for _, p := range paths {
		rel := normalizeRelPath(p)
		if rel == "" {
			continue
		}
		// Classify: file-candidate (basename matches) vs dir-candidate
		// (`target` appears as a non-leaf path segment, so the candidate is
		// the prefix up to and including that segment).
		candidatePath, isDir := classifyAncestorCandidate(rel, base)
		if candidatePath == "" || seen[candidatePath] {
			continue
		}
		if r.targetIncluded(candidatePath) {
			continue
		}
		seen[candidatePath] = true
		block := r.findBlockerForPath(candidatePath, isDir)
		if block == nil {
			continue
		}
		out = append(out, *block)
	}
	return out
}

// ignoredExactFileCandidates turns the skipped side of the exact-basename
// index into the same concrete candidates used by the ignored-ancestor
// diagnostic. This avoids a second rg walk for file-shaped targets: the
// no-ignore basename query already proved which exact files exist.
func (r *Resolver) ignoredExactFileCandidates(matches []SkippedMatch) []ignoredAncestorCandidate {
	if r.IncludedTargets.wildcard || len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	out := make([]ignoredAncestorCandidate, 0, len(matches))
	for _, match := range matches {
		rel := normalizeRelPath(match.RelPath)
		if rel == "" || r.targetIncluded(rel) {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}

		block := r.findBlockerForPath(rel, false)
		if block == nil {
			continue
		}
		out = append(out, *block)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// classifyAncestorCandidate inspects a rg result path. If its basename equals
// the target, it's a file-candidate. Otherwise the target appeared somewhere
// as a directory segment — the candidate is the prefix up to and including
// that segment.
func classifyAncestorCandidate(rel, target string) (string, bool) {
	if path.Base(rel) == target {
		return rel, false
	}
	segs := strings.Split(rel, "/")
	for i, seg := range segs {
		if seg == target && i < len(segs)-1 {
			return strings.Join(segs[:i+1], "/"), true
		}
	}
	return "", false
}

// findBlockerForPath returns the *topmost* blocked ancestor of the candidate
// (= the rule's actual hit point). Starts at the candidate, confirms it's
// blocked, then walks parents as long as each parent is still blocked. Stops
// at the first visible parent; the previous step is the rule hit. Returns
// nil for a fully-visible path.
//
// Why topmost, not the candidate itself: for `lodash.js` inside
// `dummy-react-project/node_modules/...`, fileBlockedBy on the file says
// ".gitignore" — but only because the file lives under a gitignored dir.
// The *rule* matches `dummy-react-project/`, not the file. Surfacing the
// topmost blocked dir is what makes the --include suggestion useful.
func (r *Resolver) findBlockerForPath(rel string, isDir bool) *ignoredAncestorCandidate {
	var initial *BlockInfo
	var err error
	if isDir {
		initial, err = r.dirBlockedBy(rel)
	} else {
		initial, err = r.fileBlockedBy(rel)
	}
	if err != nil || initial == nil || initial.Source == "" {
		return nil
	}

	topBlocker := rel
	topSource := initial.Source
	current := rel
	for {
		parent := path.Dir(current)
		if parent == "" || parent == "." || parent == "/" {
			break
		}
		block, parentErr := r.dirBlockedBy(parent)
		if parentErr != nil || block == nil || block.Source == "" {
			break
		}
		topBlocker = parent
		topSource = block.Source
		current = parent
	}
	return &ignoredAncestorCandidate{Path: rel, Blocker: topBlocker, Source: topSource}
}

// ignoredAncestorMessage renders the candidates list as a tailored error,
// pointing at --include of the blocker (the ancestor or the file itself) and
// at --all-ignore-rules for the full picture. Empty candidates → empty string;
// caller falls back to the existing not-found warning.
func ignoredAncestorMessage(target string, scopeIndex int, candidates []ignoredAncestorCandidate, colors platform.Palette) string {
	if len(candidates) == 0 {
		return ""
	}
	const maxShown = 5
	shown := candidates
	truncated := 0
	if len(shown) > maxShown {
		shown = candidates[:maxShown]
		truncated = len(candidates) - maxShown
	}

	var b strings.Builder
	if len(candidates) == 1 {
		c := candidates[0]
		if c.Blocker == c.Path {
			fmt.Fprintf(&b, "\n%s%sError:%s%s %s is ignored by %s (scope %d).%s\n",
				colors.Bold, colors.Err, colors.Reset, colors.Err,
				SingleQuoted(target), c.Source, scopeIndex+1, colors.Reset)
		} else {
			fmt.Fprintf(&b, "\n%s%sError:%s%s %s is hidden by an ignored ancestor (scope %d).%s\n\n  %s./%s%s — parent %s./%s%s ignored by %s\n",
				colors.Bold, colors.Err, colors.Reset, colors.Err,
				SingleQuoted(target), scopeIndex+1, colors.Reset,
				colors.Dim, c.Path, colors.Reset,
				colors.Dim, c.Blocker, colors.Reset, c.Source)
		}
		fmt.Fprintf(&b, "\n  %sUse --include to access for this run:%s\n    %scatclip %s --include %s%s\n",
			colors.Dim, colors.Reset,
			colors.OK, SingleQuoted(target), SingleQuoted(c.Blocker), colors.Reset)
	} else {
		fmt.Fprintf(&b, "\n%s%sError:%s%s %s is hidden by ignore rules. Found in (scope %d):%s\n\n",
			colors.Bold, colors.Err, colors.Reset, colors.Err,
			SingleQuoted(target), scopeIndex+1, colors.Reset)
		for _, c := range shown {
			if c.Blocker == c.Path {
				fmt.Fprintf(&b, "  %s./%s%s — ignored by %s\n",
					colors.Dim, c.Path, colors.Reset, c.Source)
			} else {
				fmt.Fprintf(&b, "  %s./%s%s (parent %s./%s%s ignored by %s)\n",
					colors.Dim, c.Path, colors.Reset,
					colors.Dim, c.Blocker, colors.Reset, c.Source)
			}
		}
		if truncated > 0 {
			fmt.Fprintf(&b, "  %s…and %d more%s\n", colors.Dim, truncated, colors.Reset)
		}
		firstBlocker := candidates[0].Blocker
		fmt.Fprintf(&b, "\n  %sUse --include to access for this run:%s\n    %scatclip %s --include %s%s\n",
			colors.Dim, colors.Reset,
			colors.OK, SingleQuoted(target), SingleQuoted(firstBlocker), colors.Reset)
	}
	fmt.Fprintf(&b, "\n  %sSee every active rule:%s   %scatclip --all-ignore-rules%s",
		colors.Dim, colors.Reset, colors.OK, colors.Reset)
	return b.String()
}
