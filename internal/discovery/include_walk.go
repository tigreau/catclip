package discovery

import (
	"path/filepath"
)

func (r *Resolver) includeAwareTargetWalked(target string) bool {
	if r.includeAwareTargetWalks == nil {
		return false
	}
	_, ok := r.includeAwareTargetWalks[normalizeRelPath(target)]
	return ok
}

func (r *Resolver) markIncludeAwareTargetWalk(target string) {
	if r.includeAwareTargetWalks == nil {
		r.includeAwareTargetWalks = make(map[string]struct{})
	}
	r.includeAwareTargetWalks[normalizeRelPath(target)] = struct{}{}
}

// discoverFilesUnderWithIncludes performs one no-ignore walk below a target
// whose traversal is authorized by at least one concrete include. Visible
// files remain part of the ordinary target result; ignored files are admitted
// only when the concrete include set covers their path.
func (r *Resolver) discoverFilesUnderWithIncludes(rootRel string) ([]Entry, error) {
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
			if !r.targetIncluded(rel) {
				continue
			}
			entry = withAllowedByInclude(entry, *block)
		} else {
			entry.GitVisible = true
		}
		files = append(files, entry)
	}
	return files, nil
}
