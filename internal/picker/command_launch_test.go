package picker

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareCommandPreservesCwdAndEnvironment(t *testing.T) {
	project := t.TempDir()
	cmd := exec.Command("relative-fzf", "--preview", "echo test")
	cmd.Dir = project
	cmd.Env = []string{"TMP=", "TEMP=relative user temp", "SHELL=relative/shell", "FZF_DEFAULT_OPTS_FILE=config/fzf opts"}
	wantArgs := append([]string(nil), cmd.Args...)
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
	if runtime.GOOS == "windows" && !containsArgPair(cmd.Args, "--with-shell", "") {
		t.Fatal("automatic fzf executor was not pinned")
	}
	if runtime.GOOS != "windows" && !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("overrode inherited Unix options: %q", cmd.Args)
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

func TestPrepareCommandRegistersExecutablesWithoutRewritingCommands(t *testing.T) {
	project := t.TempDir()
	executable := filepath.Join(project, "literal %name% $name {q}", "catclip.exe")
	marker := cmdCommandExecutable(executable)
	key := strings.Trim(commandExecutableReference.FindString(marker), "%")
	cmd := exec.Command("fzf", "--query", marker, "--header", marker, "--preview", marker+" --quiet", "--bind", "enter:execute("+marker+")+execute("+marker+")")
	wantArgs := append([]string(nil), cmd.Args...)
	cmd.Dir = project
	cmd.Env = []string{key + "=stale executable"}
	if err := PrepareCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cmd.Args[:len(wantArgs)], wantArgs) {
		t.Fatal("rewrote command or data text")
	}
	wantEnv := []string{key + "=" + executable}
	if !reflect.DeepEqual(cmd.Env, wantEnv) {
		t.Fatalf("literal executable environment = %q, want %q", cmd.Env, wantEnv)
	}
	if strings.Count(cmd.Args[8], marker) != 2 {
		t.Fatalf("binding launches changed: %s", cmd.Args[8])
	}
}

func TestPrepareCommandPreservesMarkerLikeLiteralPaths(t *testing.T) {
	for _, path := range []string{
		"/project/__catclip_exe_YQ__ --internal-picker-command/scope.json",
		"/project/__catclip_exe_A__ --internal-picker-command/scope.json",
		"/project/%CATCLIP_INTERNAL_EXE_" + strings.Repeat("0", 64) + "%/scope.json",
	} {
		preview := cmdCommandExecutable("/tools/catclip") + " --internal-prediscovered " + CommandArg(path)
		cmd := exec.Command("fzf", "--preview", preview, "--bind", "enter:execute("+preview+")")
		want := append([]string(nil), cmd.Args...)
		if err := PrepareCommand(cmd); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cmd.Args[:len(want)], want) {
			t.Fatalf("literal path rewritten: %q", cmd.Args)
		}
	}
}

func TestPrepareCommandSupportsDistinctExecutableAliases(t *testing.T) {
	first := cmdCommandExecutable("/tools/first")
	second := cmdCommandExecutable("/tools/second")
	cmd := exec.Command("fzf", "--bind", "enter:execute("+first+")+execute("+second+")")
	cmd.Env = []string{}
	if err := PrepareCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if len(cmd.Env) != 2 || first == second {
		t.Fatalf("executable aliases collided: %q", cmd.Env)
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
