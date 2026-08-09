package discovery

// noIgnoreQueryTargetMatches resolves a shorthand query against the complete
// --no-ignore universe. Exact basenames retain their normal priority; fuzzy
// matching runs only when no exact basename exists.
func (r *Resolver) noIgnoreQueryTargetMatches(query string) ([]TargetMatch, error) {
	targets, err := r.AllNoIgnoreTargets(nil)
	if err != nil {
		return nil, err
	}
	targets = eligibleTargetMatches(targets)
	if exact := exactBasenameTargetMatches(targets, query); len(exact) > 0 {
		return exact, nil
	}
	return fuzzyFilterTargetMatches(query, targets)
}
