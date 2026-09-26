package ui

import (
	"strings"
	"testing"
)

func TestFileSetPreviewDoesNotTreatContentOperandAsDiffFlag(t *testing.T) {
	project := setupTestProject(t, map[string]string{"src/a.go": "// --changed-diff --staged-diff --unstaged-diff\n"})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	for _, pattern := range []string{"--changed-diff", "--staged-diff", "--unstaged-diff"} {
		args := []string{"src", "--contains", pattern}
		for _, flag := range []string{"--only", "--exclude"} {
			cmd, cleanup := startupCheckpointFileSetPreviewCommand(args, flag, false)
			if cmd == "" || !strings.Contains(cmd, "--internal-file-set-selection") || strings.Contains(cmd, "--internal-diff-preview-state") {
				t.Errorf("%s operand %q misrouted preview: %s", flag, pattern, cmd)
			}
			cleanup()
		}
	}
}

func TestCurrentScopeDiffPreviewFlagUsesParsedStages(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"src", "--contains", "--changed-diff"}, ""},
		// Content stages after a diff are invalid; do not infer a preview from
		// an option-looking token in an invalid command either.
		{[]string{"src", "--changed-diff", "--contains", "--then"}, ""},
		{[]string{"src", "--contains", "--then", "--changed-diff"}, "--changed-diff"},
		{[]string{"src", "--staged-diff", "--then", "src"}, ""},
		{[]string{"src", "--paths", "--then", "src", "--unstaged-diff"}, "--unstaged-diff"},
	} {
		if got := currentScopeDiffPreviewFlag(tc.args); got != tc.want {
			t.Errorf("%v: got %q want %q", tc.args, got, tc.want)
		}
	}
}
