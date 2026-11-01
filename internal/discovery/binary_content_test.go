package discovery

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

func TestBinaryInclusiveContentStagesKeepOnlyText(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		"text.txt": "needle\n", "unknown.ccmixed": "needle\n",
		"archive.jar": "needle\nPK\x00binary", "empty.jar": "",
		// rg can stop on the first match before encountering this NUL.
		"late.ccmixed": "needle\n" + strings.Repeat("padding\n", 30000) + "\x00",
		"other.txt":    "other\n",
	})
	for _, kind := range []command.StageKind{command.StageContains, command.StageSnippet, command.StageNotContains} {
		t.Run(string(kind), func(t *testing.T) {
			scope := command.ExecutionScope{Targets: []string{"."}, WithBinaries: true,
				Stages: []command.Stage{{Kind: command.StageWithBinaries}, {Kind: kind, Values: []string{"needle"}}}}
			got, err := EvaluateScope(command.Invocation{WorkingDir: project, Headless: true}, git.Context{}, 0, scope, io.Discard, platform.Palette{})
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, entry := range got.Entries {
				paths = append(paths, entry.RelPath)
			}
			want := []string{"text.txt", "unknown.ccmixed"}
			if kind == command.StageNotContains {
				want = []string{"config/catclip/.hiss", "empty.jar", "other.txt"}
			}
			if !reflect.DeepEqual(paths, want) {
				t.Fatalf("matched %v", paths)
			}
		})
	}
}

func TestBinaryContentCheckpointClassifiesOnceBeforeReload(t *testing.T) {
	project := writeNoIgnoreTargetFixture(t, map[string]string{
		"text.ccmixed": "needle\n", "binary.ccmixed": "needle\n\x00",
	})
	entries := []Entry{{RelPath: "text.ccmixed"}, {RelPath: "binary.ccmixed"}}
	previous := scopeViewResolverFn
	defer func() { scopeViewResolverFn = previous }()
	scopeViewResolverFn = func([]string) (ScopeView, bool) {
		return ScopeView{WorkingDir: project, Targets: []string{"."}, WithBinaries: true, Entries: entries}, true
	}
	var walks int
	defer search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { walks++ })()
	for pass, flag := range []string{"--contains", "--snippet", "--not-contains"} {
		_, path, cleanup, err := fzfCheckpointContentMatchListCommand([]string{".", "-b"}, flag)
		if err != nil {
			t.Fatal(err)
		}
		checkpoint, err := ReadCheckpoint(path)
		cleanup()
		if err != nil {
			t.Fatal(err)
		}
		if !checkpoint.Scope.WithBinaries || len(checkpoint.Entries) != 1 || checkpoint.Entries[0].RelPath != "text.ccmixed" {
			t.Fatalf("checkpoint %+v", checkpoint)
		}
		if pass == 0 {
			// Completed observations, not new opens, must supply the second
			// filter under the stable-filesystem invocation contract.
			for _, entry := range entries {
				if err := os.Remove(filepath.Join(project, entry.RelPath)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if walks != 0 {
		t.Fatalf("classified by enumerating %d times", walks)
	}
}
