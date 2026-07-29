package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
)

// installScriptFzf installs a shell-script fake fzf at $CATCLIP_FZF so
// picker tests can drive interactive flows deterministically. Dup of
// root main_test.go's installScriptFzf — UI test files use this when
// the test moved into internal/ui with its production code.
func installScriptFzf(t *testing.T, script string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows (no /bin/sh); revisit when Go-binary fake-fzf lands")
	}

	script = strings.Replace(script, "#!/bin/sh", "#!/bin/bash", 1)

	dir := t.TempDir()
	path := filepath.Join(dir, "fake-fzf")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake fzf: %v", err)
	}
	t.Setenv("CATCLIP_FZF", path)
	return path
}

// setupTestProject creates a temp project tree and points HOME +
// XDG_CONFIG_HOME at it so .hiss / .gitignore lookups don't pollute
// the developer's real config. Dup of root main_test.go's helper.
func setupTestProject(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("HOME", root)

	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir failed for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write failed for %s: %v", rel, err)
		}
	}

	return root
}

// parseInProject cd's into project, runs cli.ParseArgs, and returns
// the resulting command.Parsed. Dup of root main_test.go's helper.
func parseInProject(t *testing.T, project string, args []string) command.Parsed {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	cfg, err := cli.ParseArgs(args)
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	return cfg
}

// startupModifierChoiceKeysContain reports whether the given key
// appears among the modifier-menu choices. Test-only helper used by
// depth_picker_test and lines_picker_test inside ui.
func startupModifierChoiceKeysContain(choices []StartupModifierChoice, want string) bool {
	for _, choice := range choices {
		if choice.Key == want {
			return true
		}
	}
	return false
}

// startupModifierChoiceKeys extracts the Key field from each choice.
func startupModifierChoiceKeys(choices []StartupModifierChoice) []string {
	keys := make([]string, 0, len(choices))
	for _, choice := range choices {
		keys = append(keys, choice.Key)
	}
	return keys
}

// initGitRepo + runGit are dups of root main_test.go's helpers, for
// ui-side tests that need a real git repo (startup picker gate tests
// validating reachability when a target has been added vs. not).
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "catclip-tests")
	runGit(t, dir, "config", "user.email", "catclip@example.com")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=catclip-tests",
		"GIT_AUTHOR_EMAIL=catclip@example.com",
		"GIT_COMMITTER_NAME=catclip-tests",
		"GIT_COMMITTER_EMAIL=catclip@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// writeProjectFile is a dup of root main_test.go's helper, for ui-side
// tests that mutate the project tree mid-test.
func writeProjectFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir failed for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed for %s: %v", rel, err)
	}
}

func installFakeFzf(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	state := filepath.Join(dir, "target-plus-state")
	backState := filepath.Join(dir, "ignored-back-state")
	script := `#!/bin/sh
filter=""
query=""
prompt=""
expect=""
print_query=0
state_file="` + state + `"
back_state_file="` + backState + `"
while [ "$#" -gt 0 ]; do
	printf 'ARG:%s\n' "$1" >&2
	case "$1" in
		--filter)
			filter="$2"
			shift 2
			;;
		--expect)
			expect="$2"
			shift 2
			;;
		--print-query)
			print_query=1
			shift
			;;
		--query)
			query="$2"
			shift 2
			;;
		--prompt)
			prompt="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
	done

input="$(cat)"

emit_query() {
	if [ "$print_query" -eq 1 ]; then
		printf '%s\n' "$query"
	fi
}

	case "$prompt" in
	"select> ")
		case "$query" in
			"")
				emit_query
				printf '%s\n' "$input" | grep -F "select all files"
				exit 0
				;;
			common)
				emit_query
				printf '%s\n' "$input" | awk -F '\t' '$2 == "src/common"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "lib/common"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "shared/common"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "src/common.ts"'
				exit 0
				;;
			src/)
				emit_query
				printf '%s\n' "$input" | awk -F '\t' '$2 == "src"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "docs/src"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "tools/src"'
				exit 0
				;;
			src/vs/platform)
				emit_query
				printf '%s\n' "$input" | awk -F '\t' '$2 == "src/vs/platform"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "tools/src/vs/platform"'
				exit 0
				;;
		esac
		;;
	"then> ")
		emit_query
		printf '%s\n' "$input" | head -n 1
		exit 0
		;;
	esac

	case "$prompt" in
	"include> ")
		case "$query" in
			node)
				emit_query
				printf '%s\n' "$input" | awk -F '\t' '$2 == "node_modules"' | head -n 1
				exit 0
				;;
		esac
		;;
	esac

value="$filter"
if [ -z "$value" ]; then
	value="$query"
fi

	case "$value" in
		cmpnts)
			emit_query
			printf '%s\n' "$input" | grep -F "src/components" | head -n 1
			exit 0
			;;
		platform)
			emit_query
			printf '%s\n' "$input" | grep -F "platform" | head -n 1
			exit 0
			;;
		src)
			emit_query
			printf '%s\n' "$input" | grep -F "src" | head -n 1
			exit 0
			;;
		common)
			emit_query
			printf '%s\n' "$input" | grep -F "src/common" | head -n 1
			exit 0
			;;
	auth)
		emit_query
		printf '%s\n' "$input" | grep -F "/auth" | head -n 1
		exit 0
		;;
	btn)
		emit_query
		printf '%s\n' "$input" | grep -F "Button.tsx" | head -n 1
		exit 0
		;;
	btn.tsx)
		emit_query
		printf '%s\n' "$input" | grep -F "Button.tsx" | head -n 1
		exit 0
		;;
	policy)
		emit_query
		printf '%s\n' "$input" | grep -F "policy.ts" | head -n 1
		exit 0
		;;
esac

emit_query
printf '%s\n' "$input"
`
	return installScriptFzf(t, script)
}
