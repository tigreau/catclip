package picker

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

const commandContextFlag = "--internal-picker-command"

var commandExecutableMarker = regexp.MustCompile(`__catclip_exe_([A-Za-z0-9_-]+)__ --internal-picker-command`)

// PrepareCommand supplies literal cmd executable paths and pins fzf's automatic
// shell selection. It does not change cwd, temp settings, or create any files.
func PrepareCommand(cmd *exec.Cmd) error {
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append([]string(nil), env...)
	var err error
	// Only process fzf command-bearing options, never query/row/header data.
	for i := 1; i < len(cmd.Args); i++ {
		if cmd.Args[i-1] != "--preview" && cmd.Args[i-1] != "--bind" {
			continue
		}
		var actionExecutable string
		cmd.Args[i] = commandExecutableMarker.ReplaceAllStringFunc(cmd.Args[i], func(marker string) string {
			encoded := commandExecutableMarker.FindStringSubmatch(marker)[1]
			path, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
			if decodeErr != nil {
				err = decodeErr
				return marker
			}
			key := fmt.Sprintf("CATCLIP_INTERNAL_EXEC_%d", i)
			// A binding can contain multiple launches of the same executable.
			if actionExecutable != "" && actionExecutable != string(path) {
				err = fmt.Errorf("mixed executables in one picker action")
				return marker
			}
			actionExecutable = string(path)
			cmd.Env = withCommandEnv(cmd.Env, key, string(path))
			return `"%` + key + `%" ` + commandContextFlag
		})
	}
	if err != nil {
		return err
	}
	// Keep fixed quoting aligned with fzf's automatic SHELL executor even if
	// inherited FZF_DEFAULT_OPTS specifies a different --with-shell command.
	cmd.Args = append(cmd.Args, "--with-shell", "")
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
