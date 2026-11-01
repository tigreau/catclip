package output

import (
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
)

type diffComparison struct {
	path             string
	staged, unstaged bool
}
type diffObservation struct {
	body, kind string
	tracked    bool
	uses       int
}
type preparedDiffCache map[diffComparison]diffObservation

func diffKey(e discovery.Entry) diffComparison {
	if !e.DiffWantStaged && !e.DiffWantUnstaged {
		e.DiffWantStaged, e.DiffWantUnstaged = true, true
	}
	return diffComparison{e.RelPath, e.DiffWantStaged, e.DiffWantUnstaged}
}

func prepareDiffCache(ctx git.Context, entries []discovery.Entry, force ...bool) (preparedDiffCache, error) {
	if !ctx.Enabled {
		return nil, nil
	}
	unique := make(map[string]bool)
	var paths []string
	uses := make(map[diffComparison]int)
	count := 0
	for _, e := range entries {
		if e.Mode != command.EntryModeDiff {
			continue
		}
		count++
		uses[diffKey(e)]++
		p := ctx.ToRepoPath(e.RelPath)
		if !unique[p] {
			unique[p] = true
			paths = append(paths, p)
		}
	}
	if count == 0 || (count < 2 && (len(force) == 0 || !force[0])) {
		return nil, nil
	}
	tracked, err := git.TrackedPaths(ctx, paths)
	if err != nil {
		return nil, err
	}
	cache := make(preparedDiffCache)
	type comparison struct{ staged, unstaged bool }
	groups := make(map[comparison][]string)
	groupOrder := []comparison{}
	seen := make(map[diffComparison]bool)
	for _, e := range entries {
		if e.Mode != command.EntryModeDiff || seen[diffKey(e)] {
			continue
		}
		seen[diffKey(e)] = true
		p := ctx.ToRepoPath(e.RelPath)
		if !tracked[p] {
			cache[diffKey(e)] = diffObservation{uses: uses[diffKey(e)]}
			continue
		}
		c := comparison{e.DiffWantStaged, e.DiffWantUnstaged}
		if _, ok := groups[c]; !ok {
			groupOrder = append(groupOrder, c)
		}
		groups[c] = append(groups[c], p)
	}
	canBatch := false
	for _, c := range groupOrder {
		if len(groups[c]) >= 2 {
			canBatch, err = git.CanBatchPatches(ctx, paths)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	for _, c := range groupOrder {
		var patches map[string]string
		if canBatch {
			patches, err = git.BatchModifiedPatches(ctx, groups[c], c.staged, c.unstaged)
			if err != nil {
				return nil, err
			}
		}
		for _, p := range groups[c] {
			e := discovery.Entry{RelPath: ctx.ToWorkPath(p), DiffWantStaged: c.staged, DiffWantUnstaged: c.unstaged}
			body, ok := patches[p]
			kind := diffOutputType(e)
			if !ok {
				body, kind, err = trackedDiffEntryOutput(ctx, e)
				if err != nil {
					return nil, err
				}
			}
			cache[diffKey(e)] = diffObservation{body: body, kind: kind, tracked: true, uses: uses[diffKey(e)]}
		}
	}
	return cache, nil
}
