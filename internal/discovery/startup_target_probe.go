package discovery

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ProbeStartupTarget classifies one startup target with a single mixed
// file-and-directory fzf filter pass for ordinary fuzzy queries. It preserves
// the normal resolver's exact, ignored, and slash-qualified paths;
// callers use the outcome to decide whether a startup picker can help.
func (r *Resolver) ProbeStartupTarget(target string) (StartupTargetProbe, error) {
	normalized := normalizeRelPath(target)
	if normalized == "" || normalized == "." || hasGlobChars(normalized) {
		return StartupTargetProbe{Outcome: StartupTargetDirect}, nil
	}

	abs := filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(normalized))
	_, statErr := os.Stat(abs)
	if statErr == nil {
		// Exact on-disk targets are direct user instructions, including paths
		// hidden by an ancestor ignore rule. The normal resolver performs the
		// target-bounded walk; startup probing must not pay for a cwd-wide
		// visibility inventory just to authorize the path.
		return StartupTargetProbe{Outcome: StartupTargetDirect}, nil
	}
	if statErr != nil && !os.IsNotExist(statErr) {
		return StartupTargetProbe{}, statErr
	}

	// Slash-qualified targets have chained-directory semantics. Keep that
	// established resolver path rather than treating the entire string as a
	// root fuzzy query.
	if strings.Contains(normalized, "/") {
		canResolve, err := r.canResolveScopedTargetWithoutPrompt(normalized)
		if err != nil {
			return StartupTargetProbe{}, err
		}
		if canResolve {
			return StartupTargetProbe{Outcome: StartupTargetDirect}, nil
		}
		reachable, err := r.scopedStartupTargetIsReachable(normalized)
		if err != nil {
			return StartupTargetProbe{}, err
		}
		if !reachable {
			return StartupTargetProbe{Outcome: StartupTargetMissing}, nil
		}
		return StartupTargetProbe{Outcome: StartupTargetAmbiguousFuzzy}, nil
	}

	if r.NoIgnore {
		matches, err := r.noIgnoreQueryTargetMatches(normalized)
		if err != nil {
			return StartupTargetProbe{}, err
		}
		switch len(matches) {
		case 0:
			return StartupTargetProbe{Outcome: StartupTargetMissing}, nil
		case 1:
			return StartupTargetProbe{Outcome: StartupTargetUniqueFuzzy, Matches: matches}, nil
		default:
			return StartupTargetProbe{Outcome: StartupTargetAmbiguousFuzzy, Matches: matches}, nil
		}
	}

	if resolvedDir, ok, err := r.resolveVisibleDirByExactBasename(".", normalized); err != nil {
		return StartupTargetProbe{}, err
	} else if ok && resolvedDir != "" {
		conflict, err := r.hasVisibleFileBasenameConflict(".", normalized)
		if err != nil {
			return StartupTargetProbe{}, err
		}
		if !conflict {
			return StartupTargetProbe{Outcome: StartupTargetDirect}, nil
		}
	}

	searchedFiles := false
	if prefersDirectFileLookup(normalized) {
		searchedFiles = true
		discovered, skipped, err := r.resolveVisibleFilesByBasename(".", normalized)
		if err != nil {
			return StartupTargetProbe{}, err
		}
		if len(discovered) > 0 {
			return StartupTargetProbe{Outcome: StartupTargetDirect}, nil
		}
		if len(skipped) > 0 {
			return StartupTargetProbe{Outcome: StartupTargetBlocked}, nil
		}
	}

	matches, err := r.fuzzySearchTargetMatches(".", normalized)
	if err != nil {
		return StartupTargetProbe{}, err
	}
	switch len(matches) {
	case 1:
		// A visible exact basename retains priority over hidden duplicates.
		// Otherwise, before accepting one lower-quality fuzzy result, account
		// for an exact hidden extensionless file or directory. Ambiguous fuzzy
		// sets already open the picker, and zero-match execution reaches the
		// existing ignored-ancestor diagnostic, so neither needs this probe.
		if !searchedFiles && path.Base(matches[0].Path) != normalized &&
			len(r.findIgnoredAncestors(normalized)) > 0 {
			return StartupTargetProbe{Outcome: StartupTargetBlocked}, nil
		}
		return StartupTargetProbe{Outcome: StartupTargetUniqueFuzzy, Matches: matches}, nil
	case 0:
	default:
		return StartupTargetProbe{Outcome: StartupTargetAmbiguousFuzzy, Matches: matches}, nil
	}

	if !searchedFiles {
		discovered, skipped, err := r.resolveVisibleFilesByBasename(".", normalized)
		if err != nil {
			return StartupTargetProbe{}, err
		}
		if len(discovered) > 0 {
			return StartupTargetProbe{Outcome: StartupTargetDirect}, nil
		}
		if len(skipped) > 0 {
			return StartupTargetProbe{Outcome: StartupTargetBlocked}, nil
		}
	}

	return StartupTargetProbe{Outcome: StartupTargetMissing}, nil
}

// scopedStartupTargetIsReachable retains the pre-existing chained-path
// reachability check. Its extra per-kind fuzzy probes apply only to slash
// targets; plain startup targets use ProbeStartupTarget's one mixed pass.
func (r *Resolver) scopedStartupTargetIsReachable(normalized string) (bool, error) {
	discovered, _, err := r.resolveVisibleFilesByBasename(".", normalized)
	if err != nil {
		return false, err
	}
	if len(discovered) > 0 {
		return true, nil
	}
	files, err := r.fuzzySearchFiles(".", normalized)
	if err != nil {
		return false, err
	}
	if len(files) > 0 {
		return true, nil
	}
	dirs, err := r.fuzzySearchDirs(".", normalized)
	if err != nil {
		return false, err
	}
	if len(dirs) > 0 {
		return true, nil
	}
	return false, nil
}
