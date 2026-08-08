package discovery

import "path/filepath"

// ValidateTargetBoundary enforces Catclip's cwd-contained positional-target
// contract without touching the filesystem. Startup preflight and runtime
// discovery share it so both routes reject the same input with the same
// recovery text.
func ValidateTargetBoundary(target string) error {
	if filepath.IsAbs(target) {
		return newUsageError("Error: Absolute paths not allowed: %s\n  Use a relative path from your project root instead.", SingleQuoted(target))
	}
	if ContainsParentTraversal(target) {
		return newUsageError("Error: Cannot traverse above working directory: %s\n  catclip only operates within the current directory tree.\n  Use a relative path from your project root instead.\n  Example: catclip config/", SingleQuoted(target))
	}
	return nil
}
