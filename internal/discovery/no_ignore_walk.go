package discovery

import (
	"path/filepath"
)

func (r *Resolver) noIgnoreTargetWalked(target string) bool {
	if r.noIgnoreTargetWalks == nil {
		return false
	}
	_, ok := r.noIgnoreTargetWalks[normalizeRelPath(target)]
	return ok
}

func (r *Resolver) markNoIgnoreTargetWalk(target string) {
	if r.noIgnoreTargetWalks == nil {
		r.noIgnoreTargetWalks = make(map[string]struct{})
	}
	r.noIgnoreTargetWalks[normalizeRelPath(target)] = struct{}{}
}

// discoverFilesUnderNoIgnore performs one no-ignore walk below a target while
// retaining ignore attribution for rendering and picker previews.
func (r *Resolver) discoverFilesUnderNoIgnore(rootRel string) ([]Entry, error) {
	rels, err := ripgrepListUnder(r.Cfg.WorkingDir, rootRel, true)
	if err != nil {
		return nil, err
	}
	files := make([]Entry, 0, len(rels))
	for _, rel := range rels {
		rel = normalizeRelPath(rel)
		if rel == "" {
			continue
		}
		absPath := filepath.Join(r.Cfg.WorkingDir, filepath.FromSlash(rel))
		text, err := r.classifyTextFile(rel, absPath)
		if err != nil {
			return nil, err
		}
		if !text {
			continue
		}
		entry := Entry{AbsPath: absPath, RelPath: rel}
		block, err := r.fileBlockedBy(rel)
		if err != nil {
			return nil, err
		}
		if block != nil {
			entry = withIgnoreBypassed(entry, *block)
		} else {
			entry.GitVisible = true
		}
		files = append(files, entry)
	}
	return files, nil
}
