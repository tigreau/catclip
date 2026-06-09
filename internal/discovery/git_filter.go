package discovery

import (
	"sort"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
)

// filterChangedEntries returns the subset of entries whose repo paths match
// git's view of changed files for the requested scope (staged/unstaged/
// untracked). Stays at root because command.ExecutionScope and Entry are domain
// types — the leaf git package can't take them.
func FilterChangedEntries(gitCtx git.Context, s command.ExecutionScope, entries []Entry) ([]Entry, error) {
	changedRepoPaths, err := CollectChangedRepoPaths(gitCtx, s)
	if err != nil {
		return nil, err
	}

	changed := make(map[string]struct{}, len(changedRepoPaths))
	for _, repoPath := range changedRepoPaths {
		changed[normalizeRelPath(repoPath)] = struct{}{}
	}

	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := changed[normalizeRelPath(gitCtx.ToRepoPath(entry.RelPath))]; ok {
			out = append(out, entry)
		}
	}
	return out, nil
}

func CollectChangedRepoPaths(gitCtx git.Context, s command.ExecutionScope) ([]string, error) {
	wantStaged, wantUnstaged, wantUntracked := changeSelection(s)
	set := make(map[string]struct{})

	if wantStaged {
		lines, err := git.Lines(gitCtx.Root, nil, "diff", "--name-only", "--cached")
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			set[normalizeRelPath(line)] = struct{}{}
		}
	}
	if wantUnstaged {
		lines, err := git.Lines(gitCtx.Root, nil, "diff", "--name-only")
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			set[normalizeRelPath(line)] = struct{}{}
		}
	}
	if wantUntracked {
		lines, err := git.Lines(gitCtx.Root, nil, "ls-files", "--others", "--exclude-standard")
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			set[normalizeRelPath(line)] = struct{}{}
		}
	}
	if wantStaged && wantUnstaged && wantUntracked && gitCtx.HasHead {
		lines, err := git.Lines(gitCtx.Root, nil, "diff", "--name-only", "HEAD")
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			set[normalizeRelPath(line)] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for line := range set {
		if workPath := gitCtx.ToWorkPath(line); workPath != "" {
			out = append(out, line)
		}
	}
	sort.Strings(out)
	return out, nil
}

func changeSelection(s command.ExecutionScope) (wantStaged, wantUnstaged, wantUntracked bool) {
	if s.Staged || s.Unstaged || s.Untracked {
		return s.Staged, s.Unstaged, s.Untracked
	}
	return true, true, true
}
