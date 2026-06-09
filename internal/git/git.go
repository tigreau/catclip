package git

import (
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// Context holds the catclip view of a git repo for the working directory.
// A zero-value Context (Enabled=false) represents "not in a git repo / git
// not available", which all consumers must treat as a no-op.
type Context struct {
	Enabled    bool
	Root       string
	WorkPrefix string
	HasHead    bool
}

// Detect resolves a Context for workingDir by asking git for its top-level
// and prefix. Returns a zero-value Context when workingDir is not inside a
// git repo or git is not on PATH.
func Detect(workingDir string) Context {
	root, err := Capture(workingDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Context{}
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return Context{}
	}

	prefix, err := Capture(workingDir, "rev-parse", "--show-prefix")
	if err != nil {
		prefix = ""
	}
	hasHead := NoOutput(root, "rev-parse", "--verify", "HEAD") == nil

	return Context{
		Enabled:    true,
		Root:       filepath.Clean(root),
		WorkPrefix: normalizeGitPrefix(prefix),
		HasHead:    hasHead,
	}
}

func normalizeGitPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.TrimSuffix(prefix, "/")
	prefix = strings.TrimPrefix(prefix, "./")
	return strings.ReplaceAll(prefix, "\\", "/")
}

// Capture runs git in dir and returns stdout as a string.
func Capture(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// NoOutput runs git in dir, ignoring stdout. Returns the exec error, if any.
func NoOutput(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

// Lines runs git in dir with the given args and optional stdin, splitting
// stdout into normalized relative-path lines. Exit-code 1 is treated as an
// empty result (matches the git --name-only "no diff" exit convention).
func Lines(dir string, stdin []string, args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if len(stdin) > 0 {
		cmd.Stdin = strings.NewReader(strings.Join(stdin, "\n") + "\n")
	}

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = normalizeRelPath(line)
	}
	return lines, nil
}

// DiffAgainstHeadOrIndex returns the unified diff for a path. When HEAD
// exists, diff against HEAD. Otherwise concatenate staged + unstaged diffs
// so a fresh repo (initial commit not made) still shows the change.
func DiffAgainstHeadOrIndex(ctx Context, repoPath string) (string, error) {
	if ctx.HasHead {
		return Capture(ctx.Root, "diff", "HEAD", "--", repoPath)
	}

	stagedDiff, stagedErr := Capture(ctx.Root, "diff", "--cached", "--", repoPath)
	if stagedErr != nil {
		return "", stagedErr
	}
	unstagedDiff, unstagedErr := Capture(ctx.Root, "diff", "--", repoPath)
	if unstagedErr != nil {
		return "", unstagedErr
	}

	switch {
	case stagedDiff == "":
		return unstagedDiff, nil
	case unstagedDiff == "":
		return stagedDiff, nil
	default:
		return stagedDiff + "\n" + unstagedDiff, nil
	}
}

// ToRepoPath converts a working-directory-relative path to a repo-root-relative
// path by prepending the work prefix (the cwd's offset under the repo root).
func (c Context) ToRepoPath(workRel string) string {
	workRel = normalizeRelPath(workRel)
	if c.WorkPrefix == "" {
		return workRel
	}
	return normalizeRelPath(path.Join(c.WorkPrefix, workRel))
}

// ToWorkPath is the inverse of ToRepoPath: it strips the work prefix from a
// repo-root-relative path. Returns "" if repoRel is outside the work prefix.
func (c Context) ToWorkPath(repoRel string) string {
	repoRel = normalizeRelPath(repoRel)
	if c.WorkPrefix == "" {
		return repoRel
	}
	prefix := normalizeRelPath(c.WorkPrefix)
	if repoRel == prefix {
		return "."
	}
	if strings.HasPrefix(repoRel, prefix+"/") {
		return normalizeRelPath(strings.TrimPrefix(repoRel, prefix+"/"))
	}
	return ""
}
