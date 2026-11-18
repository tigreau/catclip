package ui

import (
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/search"
	"os"
	"path/filepath"
	"time"
)

func TestInteractiveThenRetainsAcceptedStages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake fzf")
	}
	var controlEnumerations int
	for _, stages := range [][]string{
		nil,
		{"--snippet", "help"},
		{"--snippet", "help", "--size"},
		{"--lines", "1", "100"},
		{"--contains", "help"},
		{"--only", "*.txt", "--contains", "help", "--size"},
	} {
		t.Run(strings.Join(stages, " "), func(t *testing.T) {
			project := setupTestProject(t, map[string]string{
				"src/keep.txt": "help first\n\nlast\n",
				"src/drop.txt": "unrelated\n",
				"cmd/main.go":  "package main\n",
			})
			_ = parseInProject(t, project, []string{"."})
			installScriptFzf(t, `#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
 case "$1" in
 --prompt) prompt="$2"; shift 2 ;;
 *) shift ;;
 esac
done
case "$prompt" in
"then> ")
 # A file arriving after scope one was accepted must not enter it.
 printf 'help late\n' > src/late.txt
 awk -F '\t' '$2 == "cmd"' | head -n 1
 ;;
*) cat >/dev/null; echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)
			resolver, err := newStartupPickerResolver()
			if err != nil {
				t.Fatal(err)
			}
			var scans []search.MembershipEnumerationEvent
			restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) { scans = append(scans, e) })
			defer restore()
			args := append([]string{"src"}, stages...)
			args = append(args, "--only-tree", "--then")
			result, err := resolveStartupPickerResultWithUndo(resolver, args)
			if err != nil {
				t.Fatal(err)
			}
			if result.PreparedOutput == nil {
				t.Fatal("missing prepared output")
			}
			scopes := result.PreparedOutput.Discovery.Invocation.Scopes
			if len(scopes) != 2 {
				t.Fatalf("scope count=%d", len(scopes))
			}
			for _, entry := range scopes[0].Entries {
				if entry.RelPath == "src/late.txt" {
					t.Fatal("first scope was rediscovered")
				}
				if len(stages) > 0 && stages[0] != "--lines" && entry.RelPath == "src/drop.txt" {
					t.Fatal("first scope lost its filter")
				}
			}
			if len(scopes[0].Entries) == 0 || len(scopes[1].Entries) != 1 || scopes[1].Entries[0].RelPath != "cmd/main.go" {
				t.Fatalf("wrong scope membership: %+v", scopes)
			}

			if len(stages) == 0 {
				controlEnumerations = len(scans)
			} else if len(scans) != controlEnumerations {
				t.Fatalf("filtered flow used %d enumerations; unfiltered control used %d", len(scans), controlEnumerations)
			}
			t.Logf("stages=%v err=%v enumerations=%d", stages, err, len(scans))
		})
	}
}

// Compare potential handoff strategies using the existing APIs only. This does
// not install either strategy in the application.
func TestDiagnoseThenRetentionStrategies(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/keep.txt": "help first\n\nlast\n", "src/drop.txt": "unrelated\n", "cmd/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"."})
	for _, early := range []bool{false, true} {
		t.Run(map[bool]string{false: "at_handoff", true: "at_acceptance"}[early], func(t *testing.T) {
			scopeViewMemoReset()
			defer scopeViewMemoReset()
			base := []string{"src"}
			if err := scopeViewMemoSealEvaluatedTarget(base); err != nil {
				t.Fatal(err)
			}
			var enumerations int
			restore := search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { enumerations++ })
			defer restore()
			first := append(append([]string(nil), base...), "--only", "*.txt")
			finalFirst := append(append([]string(nil), first...), "--contains", "help")
			if early {
				for _, args := range [][]string{first, finalFirst} {
					if _, err := resolvedCurrentScopeViewForArgs(args); err != nil {
						t.Fatal(err)
					}
				}
			}
			all := append(append([]string(nil), finalFirst...), "--then", "cmd", "--only-tree")
			info, err := os.Stat(filepath.Join(project, "cmd/main.go"))
			if err != nil {
				t.Fatal(err)
			}
			entries := []discovery.Entry{{RelPath: "cmd/main.go", AbsPath: filepath.Join(project, "cmd/main.go"), SizeBytes: info.Size(), SizeKnown: true}}
			if !scopeViewMemoAdoptTargetSelection(all, git.Detect(project), entries, metadataForMemoEntries(t, entries)) {
				t.Fatal("adopt second target")
			}
			cfg, err := cli.ParseArgsAllowImplicitDot(all)
			if err != nil {
				t.Fatal(err)
			}
			resolved := command.ResolvedFromParsed(cfg)
			_, _, before := scopeViewMemoDiscoveryResult(resolved)
			if before != early {
				t.Fatalf("retained before handoff=%v, early=%v", before, early)
			}
			if !early {
				if _, err := resolvedCurrentScopeViewForArgs(finalFirst); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, ok := scopeViewMemoDiscoveryResult(resolved); !ok {
				t.Fatal("handoff still missing scope")
			}
			if early && enumerations != 0 {
				t.Fatalf("stage derivation rediscovered: %d", enumerations)
			}
			t.Logf("extra membership enumerations after sealing base=%d", enumerations)
		})
	}
}

func TestDiagnoseThenRetentionCorpusTiming(t *testing.T) {
	corpus := os.Getenv("CATCLIP_THEN_DIAG_CORPUS")
	if corpus == "" {
		t.Skip("opt-in large corpus diagnosis")
	}
	t.Chdir(corpus)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, early := range []bool{false, true} {
		for round := 0; round < 3; round++ {
			scopeViewMemoReset()
			base := []string{"drivers"}
			if err := scopeViewMemoSealEvaluatedTarget(base); err != nil {
				t.Fatal(err)
			}
			var enumerations int
			restore := search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { enumerations++ })
			first := append(append([]string(nil), base...), "--only", "*.c")
			final := append(append([]string(nil), first...), "--contains", "help")
			start := time.Now()
			if early {
				if _, err := resolvedCurrentScopeViewForArgs(first); err != nil {
					t.Fatal(err)
				}
			}
			view, err := resolvedCurrentScopeViewForArgs(final)
			elapsed := time.Since(start)
			restore()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("early=%v round=%d selected=%d ms=%.3f extra_enumerations=%d", early, round, len(view.Entries), float64(elapsed)/float64(time.Millisecond), enumerations)
		}
	}
	scopeViewMemoReset()
}
