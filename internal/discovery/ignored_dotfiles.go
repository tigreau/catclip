package discovery

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/search"
)

// FindIgnoredDotfiles supplies optional recovery paths for the bare-extension
// usage error. Call only after exact target resolution has been rejected by
// that syntax check. This does not classify or read candidate contents, infer
// an ignore source, or change the target's selection semantics.
func FindIgnoredDotfiles(workingDir, name string) []string {
	return findIgnoredDotfiles(workingDir, name, 5*time.Second, search.RunRipgrepFiles, ReadableHissPath)
}

func findIgnoredDotfiles(workingDir, name string, budget time.Duration,
	enumerate func(string, search.RipgrepFileOptions) ([]string, error),
	hissPath func() (string, error),
) []string {
	if !strings.HasPrefix(name, ".") || name == "." || name == ".." || strings.ContainsAny(name, "/\\*?[") {
		return nil
	}
	deadline := time.Now().Add(budget)
	run := func(opts search.RipgrepFileOptions) ([]string, error) {
		opts.Timeout = time.Until(deadline)
		if opts.Timeout <= 0 {
			return nil, context.DeadlineExceeded
		}
		opts.Enumeration = search.MembershipEnumerationContext{Reason: search.MembershipReasonIgnoredAncestor}
		return enumerate(workingDir, opts)
	}
	paths, err := run(search.RipgrepFileOptions{NoIgnore: true, Basenames: []string{name}})
	if err != nil || len(paths) == 0 {
		return nil
	}
	wanted := make(map[string]bool, len(paths))
	for _, p := range paths {
		if path.Base(p) == name {
			wanted[p] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	hiss, err := hissPath()
	if err != nil {
		return nil
	}
	// Do not supply Basenames here: rg's explicit -g inclusions can override
	// ignore rules. One ordinary inventory proves visibility for all candidates.
	visible, err := run(search.RipgrepFileOptions{HissPath: hiss})
	if err != nil || time.Now().After(deadline) {
		return nil
	}
	for _, p := range visible {
		delete(wanted, p)
	}
	var ignored []string
	// RunRipgrepFiles already sorts/deduplicates paths. Retain that order and
	// avoid allocating a second map proportional to the entire visible tree.
	for _, p := range paths {
		if wanted[p] {
			ignored = append(ignored, p)
		}
	}
	return ignored
}
