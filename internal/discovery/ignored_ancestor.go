package discovery

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/platform"
)

// --- Ignored-ancestor probe: when a typed *target* doesn't resolve, ask whether
// the basename exists *anywhere* in the ignored set under (the parent of) the
// typed path, and if so, attribute the highest-precedence ancestor (.gitignore
// > .hiss). Triggers only on target-resolution miss — never for filters or for
// `--no-ignore` runs. See
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
	return targetNotFoundDiagnostic(r, target, scopeIndex, colors).Message
}

func targetNotFoundDiagnostic(r *Resolver, target string, scopeIndex int, colors platform.Palette) Diagnostic {
	var candidates []ignoredAncestorCandidate
	if r.deferIgnoredDiagnostics && !r.NoIgnore {
		r.ignoredDiagnosticPending = true
	} else {
		candidates = r.findIgnoredAncestors(target)
	}
	diag := scopedDiagnostic(scopeIndex, func(si int) string {
		if len(candidates) > 0 {
			return ignoredAncestorMessage(target, si, candidates, colors)
		}
		message := targetNotFoundWarning(target, si, colors)
		if !r.WithBinaries {
			message += fmt.Sprintf("\n  %sFor binary files, name the complete relative path or add -wb (--with-binaries).%s", colors.Dim, colors.Reset)
		}
		return message
	})
	diag.IsTargetNotFound = true
	return diag
}

// findIgnoredAncestors runs the lookup-miss probe. Returns nil when:
//   - --no-ignore is active (ignore is disabled, nothing to surface);
//   - the target itself looks glob-shaped (defensive — globs shouldn't reach
//     this code, but if they did the probe would be meaningless);
//   - rg unavailable, errored, or the 5s hung-process guard tripped;
//   - no on-disk candidates with that basename exist anywhere under the typed
//     parent dir;
//   - candidates exist but none are actually blocked (shouldn't happen — they
//     came from --no-ignore, so they're either visible or blocked — but we
//     guard rather than assert).
func (r *Resolver) findIgnoredAncestors(target string) []ignoredAncestorCandidate {
	return r.findIgnoredAncestorsBatch([]string{target})[normalizeRelPath(target)]
}

// ignoredExactFileCandidates turns the skipped side of the exact-basename
// index into the same concrete candidates used by the ignored-ancestor
// diagnostic. This avoids a second rg walk for file-shaped targets: the
// no-ignore basename query already proved which exact files exist.
func (r *Resolver) ignoredExactFileCandidates(matches []SkippedMatch) []ignoredAncestorCandidate {
	if r.NoIgnore || len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	out := make([]ignoredAncestorCandidate, 0, len(matches))
	for _, match := range matches {
		rel := normalizeRelPath(match.RelPath)
		if rel == "" {
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
// topmost blocked dir explains why the exact target is absent from normal
// discovery.
func (r *Resolver) findBlockerForPath(rel string, isDir bool) *ignoredAncestorCandidate {
	var initial *BlockInfo
	var err error
	if isDir {
		// Directory candidates were derived from a file below them in rg's
		// inventory; file candidates likewise prove all their parents nonempty.
		initial, err = r.dirBlockedByWithDescendant(rel, true)
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
		block, parentErr := r.dirBlockedByWithDescendant(parent, true)
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
// pointing at the exact ignored target and --no-ignore alternatives, plus
// --all-ignore-rules for the full picture. Empty candidates → empty string;
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
	// The failed query is already known. Keep recovery scoped to that query
	// instead of suggesting an export of the entire project; no new probe.
	queryArg := ShellQuoteArg(target)
	if len(candidates) == 1 {
		c := candidates[0]
		if c.Blocker == c.Path {
			fmt.Fprintf(&b, "\n%s%sError:%s%s %s is ignored by %s%s.%s\n",
				colors.Bold, colors.Err, colors.Reset, colors.Err,
				SingleQuoted(target), c.Source, diagnosticScopeSuffix(scopeIndex), colors.Reset)
			fmt.Fprintf(&b, "\n  %s./%s%s\n", colors.Dim, c.Path, colors.Reset)
		} else {
			fmt.Fprintf(&b, "\n%s%sError:%s%s %s is hidden by an ignored ancestor%s.%s\n\n  %s./%s%s — parent %s./%s%s ignored by %s\n",
				colors.Bold, colors.Err, colors.Reset, colors.Err,
				SingleQuoted(target), diagnosticScopeSuffix(scopeIndex), colors.Reset,
				colors.Dim, c.Path, colors.Reset,
				colors.Dim, c.Blocker, colors.Reset, c.Source)
		}
		fmt.Fprintf(&b, "\n  %sName the complete path above:%s\n    %scatclip %s%s\n\n  %sOr look up the same target with ignore rules disabled:%s\n    %scatclip %s --no-ignore%s\n",
			colors.Dim, colors.Reset,
			colors.OK, SingleQuoted(c.Path), colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, queryArg, colors.Reset)
	} else {
		fmt.Fprintf(&b, "\n%s%sError:%s%s %s is hidden by ignore rules. Found in%s:%s\n\n",
			colors.Bold, colors.Err, colors.Reset, colors.Err,
			SingleQuoted(target), diagnosticScopeSuffix(scopeIndex), colors.Reset)
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
		fmt.Fprintf(&b, "\n  %sName one of the complete paths above:%s\n    %scatclip %s%s\n\n  %sOr look up the same target with ignore rules disabled:%s\n    %scatclip %s --no-ignore%s\n",
			colors.Dim, colors.Reset,
			colors.OK, SingleQuoted(candidates[0].Path), colors.Reset,
			colors.Dim, colors.Reset,
			colors.OK, queryArg, colors.Reset)
	}
	fmt.Fprintf(&b, "\n  %sSee every active rule:%s   %scatclip --all-ignore-rules%s",
		colors.Dim, colors.Reset, colors.OK, colors.Reset)
	return b.String()
}
