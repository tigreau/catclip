package git

import (
	"context"
	"os/exec"
	"strings"
)

// StatusMapForPathspecs runs `git status --porcelain` and returns
// path -> status (M/S/SM/?) for any tracked or untracked change. When the
// pathspec batch would be too large for git's argv limit, falls back to a
// full-repo scan.
func StatusMapForPathspecs(ctx Context, pathspecs []string) (map[string]string, error) {
	return StatusMapForPathspecsContext(context.Background(), ctx, pathspecs)
}

// StatusMapForPathspecsContext is StatusMapForPathspecs with cancellation for
// short-lived preview helpers that fzf can supersede while git is running.
func StatusMapForPathspecsContext(cancelCtx context.Context, ctx Context, pathspecs []string) (map[string]string, error) {
	out, err := statusOutput(cancelCtx, ctx, pathspecs)
	if err != nil {
		if cancelErr := cancelCtx.Err(); cancelErr != nil {
			return nil, cancelErr
		}
		if len(pathspecs) > 0 {
			out, err = statusOutput(cancelCtx, ctx, nil)
		}
		if err != nil {
			return nil, err
		}
	}
	return parseStatusMap(ctx, string(out)), nil
}

func statusOutput(cancelCtx context.Context, ctx Context, pathspecs []string) ([]byte, error) {
	args := []string{"status", "--porcelain"}
	if len(pathspecs) > 0 && canScopeStatusPathspecs(pathspecs) {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}
	cmd := exec.CommandContext(cancelCtx, "git", args...)
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
	// Do NOT TrimSpace the whole output before splitting — porcelain
	// lines for unstaged-modified entries begin with a literal space
	// (xy[0]=' ', xy[1]='M'), and TrimSpace would silently strip the
	// first such line's leading space, shifting line[3:] by one byte
	// and producing a truncated path with the wrong staged/unstaged
	// classification. Trim trailing newline only.
	output = strings.TrimRight(output, "\n")
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if len(line) < 4 {
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
