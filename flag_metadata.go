package catclip

import "strings"

func isValueTakingFlag(arg string) bool {
	switch arg {
	case "--include", "--only", "--exclude", "--contains",
		"--internal-tree-target", "--internal-tree-kind", "--internal-tree-state",
		"--internal-file-path":
		return true
	default:
		return false
	}
}

func isModifierBoundaryToken(arg string) bool {
	if arg == "--then" || arg == "--" {
		return true
	}
	if isValueTakingFlag(arg) {
		return true
	}
	switch arg {
	case "--changed", "--staged", "--unstaged", "--untracked", "--diff", "--snippet",
		"-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-t", "--no-tree",
		"--preview", "-h", "--help", "--help-all", "--version", "-V", "--hiss", "--hiss-reset":
		return true
	}
	return strings.HasPrefix(arg, "--")
}
