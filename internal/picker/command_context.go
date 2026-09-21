package picker

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const commandContextEnv = "CATCLIP_INTERNAL_PICKER_CONTEXT"
const commandContextFlag = "--internal-picker-command"

var commandExecutableMarker = regexp.MustCompile(`__catclip_exe_([A-Za-z0-9_-]+)__ --internal-picker-command`)

type commandContext struct {
	WorkingDir string
	TempDir    string
	TempEnv    map[string]*string
}

// PrepareCommand confines fzf's raw selection filenames to a private working
// directory. Fixed absolute paths never travel through raw placeholders.
// The caller must keep the returned cleanup alive until fzf has exited.
func PrepareCommand(cmd *exec.Cmd) (func(), error) {
	root := cmd.Dir
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(cmd.Path) {
		cmd.Path = filepath.Join(root, cmd.Path)
	}
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(tempRoot, "catclip-fzf-session-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	env = append([]string(nil), env...)
	ctx := commandContext{WorkingDir: root, TempDir: dir, TempEnv: map[string]*string{}}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if value, ok := commandEnvValue(env, key); ok {
			ctx.TempEnv[key] = &value
		} else {
			ctx.TempEnv[key] = nil
		}
		env = withCommandEnv(env, key, ".")
	}
	// A relative shell path was relative to the project, not our temp cwd.
	if shell, ok := commandEnvValue(env, "SHELL"); ok && !filepath.IsAbs(shell) && strings.ContainsAny(shell, `/\`) {
		env = withCommandEnv(env, "SHELL", filepath.Join(root, shell))
	}
	if options, ok := commandEnvValue(env, "FZF_DEFAULT_OPTS_FILE"); ok && options != "" && !filepath.IsAbs(options) {
		env = withCommandEnv(env, "FZF_DEFAULT_OPTS_FILE", filepath.Join(root, options))
	}
	data, err := json.Marshal(ctx)
	if err != nil {
		cleanup()
		return nil, err
	}
	cmd.Env = withCommandEnv(env, commandContextEnv, string(data))
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
		cleanup()
		return nil, err
	}
	cmd.Dir = dir
	// Keep fixed quoting aligned with fzf's automatic SHELL executor even if
	// inherited FZF_DEFAULT_OPTS specifies a different --with-shell command.
	cmd.Args = append(cmd.Args, "--with-shell", "")
	return cleanup, nil
}

func commandEnvValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		name, value, ok := strings.Cut(env[i], "=")
		if ok && (name == key || runtime.GOOS == "windows" && strings.EqualFold(name, key)) {
			return value, true
		}
	}
	return "", false
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

// RestoreCommandContext is called only for explicitly marked internal helpers.
// It restores the project cwd and original temp settings before normal startup.
func RestoreCommandContext(args []string) ([]string, error) {
	if len(args) == 0 || args[0] != commandContextFlag {
		return args, nil
	}
	var ctx commandContext
	if err := json.Unmarshal([]byte(os.Getenv(commandContextEnv)), &ctx); err != nil {
		return nil, fmt.Errorf("invalid picker command context: %w", err)
	}
	if !filepath.IsAbs(ctx.WorkingDir) || !filepath.IsAbs(ctx.TempDir) {
		return nil, fmt.Errorf("picker command context requires absolute directories")
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		value, ok := ctx.TempEnv[key]
		if !ok || value != nil && strings.ContainsRune(*value, 0) {
			return nil, fmt.Errorf("picker command context has invalid %s", key)
		}
	}
	if err := os.Chdir(ctx.WorkingDir); err != nil {
		return nil, err
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		value := ctx.TempEnv[key]
		var err error
		if value == nil {
			err = os.Unsetenv(key)
		} else {
			err = os.Setenv(key, *value)
		}
		if err != nil {
			return nil, err
		}
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

// SelectionFilePath resolves fzf-owned relative filenames after the helper has
// restored its project cwd. It does not change selected project paths.
func SelectionFilePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	var ctx commandContext
	if json.Unmarshal([]byte(os.Getenv(commandContextEnv)), &ctx) == nil && filepath.IsAbs(ctx.TempDir) {
		return filepath.Join(ctx.TempDir, path)
	}
	return path
}
