package picker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

const commandContextFlag = "--internal-picker-command"

// Register executable aliases when constructing commands, not by interpreting
// arbitrary text in a finished command. Production uses the current executable;
// tests can register a relocated helper without changing the process environment.
var commandExecutables sync.Map // environment key -> literal executable path

var commandExecutableReference = regexp.MustCompile(`%CATCLIP_INTERNAL_EXE_[0-9A-F]{64}%`)

func cmdCommandExecutable(path string) string {
	sum := sha256.Sum256([]byte(path))
	key := "CATCLIP_INTERNAL_EXE_" + strings.ToUpper(hex.EncodeToString(sum[:]))
	commandExecutables.Store(key, path)
	return `"%` + key + `%" ` + commandContextFlag
}

// PrepareCommand supplies registered cmd executable paths. It does not change
// cwd, temp settings, or create any files.
func PrepareCommand(cmd *exec.Cmd) error {
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append([]string(nil), env...)
	// Only process fzf command-bearing options, never query/row/header data.
	seen := make(map[string]bool)
	for i := 1; i < len(cmd.Args); i++ {
		if cmd.Args[i-1] != "--preview" && cmd.Args[i-1] != "--bind" {
			continue
		}
		for _, ref := range commandExecutableReference.FindAllString(cmd.Args[i], -1) {
			key := strings.Trim(ref, "%")
			path, registered := commandExecutables.Load(key)
			if !registered || seen[key] {
				continue
			}
			seen[key] = true
			cmd.Env = withCommandEnv(cmd.Env, key, path.(string))
		}
	}
	// Windows still needs a separate compatibility repair: pinned fzf does not
	// select shell-specific placeholder quoting with a custom --with-shell.
	// Do not apply this workaround on Unix, where it breaks inherited commands
	// and shell flags without fixing any quoting problem.
	if runtime.GOOS == "windows" {
		cmd.Args = append(cmd.Args, "--with-shell", "")
	}
	return nil
}

func withCommandEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, pair := range env {
		name, _, _ := strings.Cut(pair, "=")
		if name != key && !(runtime.GOOS == "windows" && strings.EqualFold(name, key)) {
			out = append(out, pair)
		}
	}
	return append(out, key+"="+value)
}

// NormalizeCommandArgs recovers exact live queries for marked internal helpers.
// It never changes process cwd or environment.
func NormalizeCommandArgs(args []string) ([]string, error) {
	if len(args) == 0 || args[0] != commandContextFlag {
		return args, nil
	}
	args = args[1:]
	for _, i := range []int{len(args) - 3, len(args) - 2} {
		if i < 0 || args[i] != QuerySourceMarker {
			continue
		}
		flag := args[i+1]
		if flag != "--contains" && flag != "--not-contains" && flag != "--snippet" {
			continue
		}
		query, ok := os.LookupEnv("FZF_QUERY")
		if !ok {
			return nil, fmt.Errorf("picker query environment is missing")
		}
		return append(append([]string(nil), args[:i]...), flag, query), nil
	}
	return args, nil
}
