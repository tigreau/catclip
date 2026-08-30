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

// ApplyGitStatusStageIDs applies one Git selection stage to stable retained
// entry IDs using an already-collected porcelain status map. Membership
// semantics stay discovery-owned and identical for normal and *-diff stages;
// diff affects only the later output projection.
func ApplyGitStatusStageIDs(gitCtx git.Context, stage command.Stage, entries []Entry, ids []uint32, statuses map[string]string) ([]uint32, bool) {
	wantsStatus := func(status string) bool {
		switch stage.Kind {
		case command.StageChanged, command.StageChangedDiff:
			return status == "S" || status == "M" || status == "SM" || status == "?"
		case command.StageStaged, command.StageStagedDiff:
			return status == "S" || status == "SM"
		case command.StageUnstaged, command.StageUnstagedDiff:
			return status == "M" || status == "SM"
		case command.StageUntracked:
			return status == "?"
		default:
			return false
		}
	}
	switch stage.Kind {
	case command.StageChanged, command.StageChangedDiff, command.StageStaged,
		command.StageStagedDiff, command.StageUnstaged, command.StageUnstagedDiff,
		command.StageUntracked:
	default:
		return nil, false
	}
	for _, id := range ids {
		if uint64(id) >= uint64(len(entries)) {
			return nil, false
		}
	}
	if !gitCtx.Enabled {
		// Membership cannot be evaluated without a Git context. Canonical and
		// retained scope resolvers attach the user-facing unavailable diagnostic;
		// never make this low-level projector silently preserve every file.
		return nil, true
	}

	out := make([]uint32, 0, len(ids))
	for _, id := range ids {
		if wantsStatus(statuses[normalizeRelPath(entries[id].RelPath)]) {
			out = append(out, id)
		}
	}
	return out, true
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
