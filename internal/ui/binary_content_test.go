package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
)

func TestBinaryContentMenuAndRetainedSelection(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"text.txt": "needle\n", "unknown.ccmixed": "needle\n",
		"archive.jar": "needle\n\x00", "empty.jar": "",
		"late.ccmixed": "needle\n" + strings.Repeat("padding\n", 30000) + "\x00",
	})
	t.Chdir(project)
	for _, tc := range []struct {
		target string
		hide   bool
	}{
		{".", false}, {"archive.jar", true}, {"unknown.ccmixed", false}, {"empty.jar", false},
	} {
		scopeViewMemoReset()
		args := []string{tc.target, "-b"}
		if err := scopeViewMemoSealEvaluatedTarget(args); err != nil {
			t.Fatal(err)
		}
		state, _, err := startupCurrentScopeStateForArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		choices := startupAvailableModifierChoicesWithState(args, state)
		for _, key := range []string{"contains", "snippet", "not-contains"} {
			found := false
			for _, choice := range choices {
				if choice.Key == key {
					found = true
				}
			}
			if found == tc.hide {
				t.Fatalf("%s: %s available=%v, want %v", tc.target, key, found, !tc.hide)
			}
		}
	}
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	if err := scopeViewMemoSealEvaluatedTarget([]string{".", "-b"}); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--contains", "--snippet", "--not-contains"} {
		want := []string{"text.txt", "unknown.ccmixed"}
		if flag == "--not-contains" {
			want = []string{"config/catclip/.hiss", "empty.jar"}
		}
		args := []string{".", "-b", flag, "needle"}
		ctx, err := buildStartupSinkPickerContext(args)
		if err != nil {
			t.Fatal(err)
		}
		if got := ctx.Plan.DistinctRelPaths(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s retained %v", flag, got)
		}
		cfg, err := cli.ParseArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := contentMatchRowsForScope(ContentMatchListConfigFromParsedCommand(cfg))
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, row := range rows {
			paths = append(paths, row.RelPath)
		}
		if !reflect.DeepEqual(paths, want) {
			t.Fatalf("%s fallback %v", flag, paths)
		}
	}
}

func TestDirectBinaryRetainedContentDiagnostics(t *testing.T) {
	project := setupTestProject(t, map[string]string{"tool": "needle\x00", "text.txt": "needle\n"})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	if err := scopeViewMemoSealEvaluatedTarget([]string{"tool"}); err != nil {
		t.Fatal(err)
	}
	state, _, err := startupCurrentScopeStateForArgs([]string{"tool"})
	if err != nil || !state.AllBinary {
		t.Fatalf("state %+v err %v", state, err)
	}
	for _, flag := range []string{"--contains", "--snippet", "--not-contains"} {
		view, err := resolvedCurrentScopeViewForArgs([]string{"tool", flag, "needle"})
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Entries) != 0 || len(view.Discovered.Diagnostics) != 1 || !view.Discovered.Diagnostics[0].ExplainsEmptyResult || !strings.Contains(view.Discovered.Diagnostics[0].Message, "'tool' is binary") {
			t.Fatalf("%s: %+v", flag, view.Discovered)
		}
		err = startupNoFilesMatchedError(view.Scopes, view.Discovered.Diagnostics...)
		if !strings.Contains(err.Error(), "'tool' is binary") || strings.Contains(err.Error(), "Possible causes") {
			t.Fatal(err)
		}
	}
}
