package picker

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPrepareCommandPreservesCwdAndEnvironment(t *testing.T) {
	project := t.TempDir()
	cmd := exec.Command("relative-fzf", "--preview", "echo test")
	cmd.Dir = project
	cmd.Env = []string{"TMP=", "TEMP=relative user temp", "SHELL=relative/shell", "FZF_DEFAULT_OPTS_FILE=config/fzf opts"}
	wantEnv := append([]string(nil), cmd.Env...)
	wantCwd, _ := os.Getwd()
	wantParentEnv := os.Environ()
	if err := PrepareCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != project || cmd.Path != "relative-fzf" || !reflect.DeepEqual(cmd.Env, wantEnv) {
		t.Fatalf("changed launch context: cwd=%q path=%q env=%q", cmd.Dir, cmd.Path, cmd.Env)
	}
	if cwd, _ := os.Getwd(); cwd != wantCwd || !reflect.DeepEqual(os.Environ(), wantParentEnv) {
		t.Fatal("changed parent context")
	}
	if !containsArgPair(cmd.Args, "--with-shell", "") {
		t.Fatal("automatic fzf executor was not pinned")
	}
}

func TestNormalizeCommandArgsRequiresExplicitMarker(t *testing.T) {
	t.Setenv("FZF_QUERY", "replacement")
	args := []string{"--print", QuerySourceMarker, "--contains", "literal"}
	got, err := NormalizeCommandArgs(args)
	if err != nil || !reflect.DeepEqual(got, args) {
		t.Fatalf("public command was changed: %q %v", got, err)
	}
}

func TestPrepareCommandRewritesOnlyExecutablePositions(t *testing.T) {
	project := t.TempDir()
	executable := filepath.Join(project, "literal %name% $name {q}", "catclip.exe")
	marker := "__catclip_exe_" + base64.RawURLEncoding.EncodeToString([]byte(executable)) + "__ " + commandContextFlag
	cmd := exec.Command("fzf", "--query", marker, "--header", marker, "--preview", marker+" --quiet", "--bind", "enter:execute("+marker+")+execute("+marker+")")
	cmd.Dir = project
	cmd.Env = []string{"CATCLIP_INTERNAL_EXEC_6=stale executable"}
	if err := PrepareCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.Args[2] != marker || cmd.Args[4] != marker {
		t.Fatal("rewrote query/header data as a command")
	}
	if want := `"%CATCLIP_INTERNAL_EXEC_6%" ` + commandContextFlag + " --quiet"; cmd.Args[6] != want {
		t.Fatalf("preview = %q, want %q", cmd.Args[6], want)
	}
	wantEnv := []string{"CATCLIP_INTERNAL_EXEC_6=" + executable, "CATCLIP_INTERNAL_EXEC_8=" + executable}
	if !reflect.DeepEqual(cmd.Env, wantEnv) {
		t.Fatalf("literal executable environment = %q, want %q", cmd.Env, wantEnv)
	}
	if strings.Count(cmd.Args[8], `"%CATCLIP_INTERNAL_EXEC_8%"`) != 2 {
		t.Fatalf("binding launches were not both rewritten: %s", cmd.Args[8])
	}
}

func TestNormalizeCommandArgsRecoversExactQuery(t *testing.T) {
	for _, query := range []string{"", `--changed | "double" 'single' $dollar %percent% ` + "`tick`", QuerySourceMarker} {
		t.Run(query, func(t *testing.T) {
			t.Setenv("FZF_QUERY", query)
			for _, flag := range []string{"--contains", "--not-contains", "--snippet"} {
				for _, tail := range [][]string{{}, {"shell-altered query"}} {
					args := append([]string{commandContextFlag, "--quiet", QuerySourceMarker, flag}, tail...)
					got, err := NormalizeCommandArgs(args)
					want := []string{"--quiet", flag, query}
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("query recovery: got %q, %v; want %q", got, err, want)
					}
				}
			}
		})
	}
}
