package git

import (
	"os/exec"
	"strings"
)

// StatusMapForPathspecs runs `git status --porcelain` and returns
// path -> status (M/S/SM/?) for any tracked or untracked change. When the
// pathspec batch would be too large for git's argv limit, falls back to a
// full-repo scan.
func StatusMapForPathspecs(ctx Context, pathspecs []string) (map[string]string, error) {
	out, err := statusOutput(ctx, pathspecs)
	if err != nil {
		if len(pathspecs) > 0 {
			out, err = statusOutput(ctx, nil)
		}
		if err != nil {
			return nil, err
		}
	}
	return parseStatusMap(ctx, string(out)), nil
}

func statusOutput(ctx Context, pathspecs []string) ([]byte, error) {
	args := []string{"status", "--porcelain"}
	if len(pathspecs) > 0 && canScopeStatusPathspecs(pathspecs) {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = ctx.Root
	return cmd.Output()
}

func canScopeStatusPathspecs(pathspecs []string) bool {
	if len(pathspecs) == 0 {
		return false
	}
	if len(pathspecs) > 256 {
		return false
	}
	total := 0
	for _, pathspec := range pathspecs {
		total += len(pathspec) + 1
	}
	return total <= 32768
}

func parseStatusMap(ctx Context, output string) map[string]string {
	statuses := make(map[string]string)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || len(line) < 4 {
			continue
		}
		xy := line[:2]
		pathPart := line[3:]
		if strings.Contains(pathPart, " -> ") {
			parts := strings.Split(pathPart, " -> ")
			pathPart = parts[len(parts)-1]
		}
		repoPath := normalizeRelPath(pathPart)
		workPath := ctx.ToWorkPath(repoPath)
		if workPath == "" {
			continue
		}

		if xy == "??" {
			statuses[workPath] = "?"
			continue
		}

		staged := len(xy) >= 1 && xy[0] != ' ' && xy[0] != '?'
		unstaged := len(xy) >= 2 && xy[1] != ' ' && xy[1] != '?'
		switch {
		case staged && unstaged:
			statuses[workPath] = "SM"
		case staged:
			statuses[workPath] = "S"
		case unstaged:
			statuses[workPath] = "M"
		}
	}
	return statuses
}
