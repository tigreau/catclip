package command

// stageFlags is the single home for StageKind → canonical CLI flag
// spelling (modifier_wiring_consolidation_v2 phase 3). CanonicalScopeArgs
// renders from here and cli's ScopeModifierFlagSpecs builder joins on it
// (panicking at init if a kind is missing), so a flag can no longer be
// spelled one way in the parser and another way in the "Resolved
// command:" header.
var stageFlags = map[StageKind]string{
	StageInclude:      "--include",
	StageOnly:         "--only",
	StageExclude:      "--exclude",
	StageRecent:       "--recent",
	StageSize:         "--size",
	StageDepth:        "--depth",
	StageContains:     "--contains",
	StageNotContains:  "--not-contains",
	StageChanged:      "--changed",
	StageStaged:       "--staged",
	StageUnstaged:     "--unstaged",
	StageUntracked:    "--untracked",
	StagePaths:        "--paths",
	StageDiff:         "--diff",
	StageSnippet:      "--snippet",
	StageChangedDiff:  "--changed-diff",
	StageStagedDiff:   "--staged-diff",
	StageUnstagedDiff: "--unstaged-diff",
	StageLines:        "--lines",
}

// StageFlag returns the canonical CLI spelling for a stage kind.
func StageFlag(kind StageKind) (string, bool) {
	flag, ok := stageFlags[kind]
	return flag, ok
}

// StageFlags returns a copy of the kind→flag table. Totality tests
// iterate it; production code should resolve one kind via StageFlag.
func StageFlags() map[StageKind]string {
	out := make(map[StageKind]string, len(stageFlags))
	for kind, flag := range stageFlags {
		out[kind] = flag
	}
	return out
}
