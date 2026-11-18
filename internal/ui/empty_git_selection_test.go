package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
)

func TestEmptyGitSelectionPreservesStageAndActualCancellation(t *testing.T) {
	for _, flag := range []string{"--changed", "--staged", "--unstaged", "--untracked", "--changed-diff", "--staged-diff", "--unstaged-diff"} {
		t.Run(flag, func(t *testing.T) {
			project := setupTestProject(t, map[string]string{
				"clean/main.go": "package clean\n",
				"dirty/main.go": "package dirty\n",
			})
			initGitRepo(t, project)
			writeProjectFile(t, project, "dirty/main.go", "package staged\n")
			runGit(t, project, "add", "dirty/main.go")
			writeProjectFile(t, project, "dirty/main.go", "package unstaged\n")
			writeProjectFile(t, project, "dirty/new.go", "package untracked\n")
			_ = parseInProject(t, project, []string{"."})
			scopeViewMemoReset()
			defer scopeViewMemoReset()
			resolver, err := newStartupPickerResolver()
			if err != nil {
				t.Fatal(err)
			}
			installScriptFzf(t, "#!/bin/sh\necho 'empty Git picker must not open' >&2\nexit 91\n")
			args := []string{"clean", flag}
			got, used, err := resolveStartupGitScopeArgs(resolver, args, startupGitStagePrompt(flag), nil, true, strings.HasSuffix(flag, "-diff"))
			if err != nil || used || !reflect.DeepEqual(got, args) {
				t.Fatalf("empty result: args=%q used=%v err=%v; want %q without a picker", got, used, err, args)
			}

			// A nonempty selection still opens the picker and preserves all rows.
			installScriptFzf(t, "#!/bin/sh\ncat\n")
			args = []string{"dirty", flag}
			got, used, err = resolveStartupGitScopeArgs(resolver, args, startupGitStagePrompt(flag), nil, true, false)
			if err != nil || !used || !reflect.DeepEqual(got, args) {
				t.Fatalf("nonempty result: args=%q used=%v err=%v; want %q with picker", got, used, err, args)
			}
			installScriptFzf(t, "#!/bin/sh\ncat >/dev/null\nexit 130\n")
			if _, _, err := resolveStartupGitScopeArgs(resolver, args, startupGitStagePrompt(flag), nil, true, false); err != discovery.ErrSelectionCancelled {
				t.Fatalf("actual Escape must remain cancellation, got %v", err)
			}
		})
	}
}

func TestEmptyChangedTargetDoesNotReopenPickerAndKeepsSiblingScopes(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []string
		want []string
	}{
		{name: "entirely empty", raw: []string{"--changed"}, want: []string{"clean", "--changed"}},
		{name: "empty first", raw: []string{"--changed", "--then", "keep", "--only-tree"}, want: []string{"clean", "--changed", "--then", "keep", "--only-tree"}},
		{name: "empty last", raw: []string{"keep", "--then", "--changed", "--only-tree"}, want: []string{"keep", "--then", "clean", "--changed", "--only-tree"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := setupTestProject(t, map[string]string{"clean/main.go": "package clean\n", "keep/main.go": "package keep\n"})
			initGitRepo(t, project)
			_ = parseInProject(t, project, []string{"."})
			logPath := filepath.Join(t.TempDir(), "picker-log")
			installScriptFzf(t, fmt.Sprintf(`#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
  case "$1" in --prompt) prompt="$2"; shift 2 ;; *) shift ;; esac
done
input="$(cat)"
if [ -e %[1]q ]; then echo "unexpected extra picker: $prompt" >&2; exit 91; fi
printf '%%s\n' "$prompt" > %[1]q
case "$prompt" in
  "select> "|"then> ") printf '%%s\n' "$input" | awk -F '\t' '$2 == "clean"' | head -n 1 ;;
  *) echo "unexpected picker: $prompt" >&2; exit 91 ;;
esac
`, logPath))
			resolver, err := newStartupPickerResolver()
			if err != nil {
				t.Fatal(err)
			}
			result, err := resolveStartupPickerResultWithUndo(resolver, tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if !result.UsedFzf || !reflect.DeepEqual(result.Args, tc.want) || result.PreparedOutput == nil {
				t.Fatalf("result = %+v; want %q with retained output", result, tc.want)
			}
			log, err := os.ReadFile(logPath)
			if err != nil || strings.Count(string(log), "\n") != 1 {
				t.Fatalf("expected exactly one target picker; log=%q err=%v", log, err)
			}
			if tc.name == "entirely empty" {
				if !result.PreparedOutput.Plan.IsEmpty() {
					t.Fatal("empty Git filter must not restore unfiltered files")
				}
				cfg, err := cli.ParseArgs(result.Args)
				if err != nil {
					t.Fatal(err)
				}
				var diagnostics strings.Builder
				if err := discovery.WriteNoFilesMatchedMessage(command.ResolvedFromParsed(cfg).Scopes, &diagnostics, platform.Palette{}, result.PreparedOutput.Discovery.HadSelectionCancel); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(diagnostics.String(), "Git selection requested: --changed.") {
					t.Fatalf("empty selection must retain its explanation, got %q", diagnostics.String())
				}
			} else {
				if got := result.PreparedOutput.Plan.DistinctRelPaths(); !reflect.DeepEqual(got, []string{"keep/main.go"}) {
					t.Fatalf("sibling scope lost or empty target restored: %q", got)
				}
				assertPreparedOutputReplays(t, result)
			}
		})
	}
}
