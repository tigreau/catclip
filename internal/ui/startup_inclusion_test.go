package ui

import (
	"github.com/tigreau/catclip/internal/search"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFileInclusionRequiresTargetPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"visible.txt": "text", "visible.jar": "PK\x00data",
		".gitignore": "hidden/\n", "hidden/file.txt": "hidden",
	})
	t.Chdir(project)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	missingPicker := filepath.Join(t.TempDir(), "inclusion-picker-must-run.exe")
	t.Setenv("CATCLIP_FZF", missingPicker)
	for _, flags := range [][]string{{"--no-ignore"}, {"--with-binaries"}, {"--no-ignore", "--with-binaries"}} {
		t.Run(strings.Join(flags, "+"), func(t *testing.T) {
			resolver, err := newStartupPickerResolver()
			if err != nil {
				t.Fatal(err)
			}
			defer resolver.ReleaseRetainedTargetPreviewInventory()
			frame := startupInclusionTargetFrame(startupInteractiveFrame{StartArgs: []string{"."}}, flags)
			// Unlike the membership test, leave targets empty. The reopened
			// picker must be attempted, rather than silently retaining '.'.
			_, err = runStartupTargetFrame(resolver, frame)
			if err == nil || !strings.Contains(err.Error(), "missing bundled fzf") {
				t.Fatalf("expected reopened picker failure, got %v", err)
			}
		})
	}
}

func TestFileInclusionMenuBeforeFilters(t *testing.T) {
	choices := startupAvailableModifierChoicesWithState([]string{"."}, startupCurrentScopeState{})
	keys := startupModifierChoiceKeys(choices)
	// fzf's default layout displays the input rows bottom-to-top.
	if len(keys) < 3 || !reflect.DeepEqual(keys[len(keys)-3:], []string{"file-inclusion", "extras", "finish"}) {
		t.Fatalf("unexpected top-of-menu order (reversed): %v", keys)
	}
	for _, tc := range []struct {
		args  []string
		count int
	}{
		{[]string{"."}, 2}, {[]string{".", "-b"}, 1},
		{[]string{".", "--no-ignore", "-b"}, 0},
		{[]string{".", "--only", "*.go"}, 0},
		{[]string{".", "--paths"}, 0},
		{[]string{"src", "--paths", "--then", "assets"}, 2},
	} {
		if got := len(startupAvailableInclusionChoices(tc.args)); got != tc.count {
			t.Fatalf("%v: got %d want %d", tc.args, got, tc.count)
		}
	}
	for _, choice := range startupExtrasChoices {
		if choice.Key == "with-binaries" {
			t.Fatal("binary remains in extras")
		}
	}
}

func TestFileInclusionReopensAndSealsSelection(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"visible.txt": "text", "visible.jar": "PK\x00data",
		"ignored/hidden.txt": "text", "ignored/hidden.jar": "PK\x00data",
		".gitignore": "ignored/\n",
	})
	t.Chdir(project)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, tc := range []struct {
		name        string
		flags, want []string
	}{
		{"ignored", []string{"--no-ignore"}, []string{".gitignore", "ignored/hidden.txt", "visible.txt"}},
		{"binary", []string{"-b"}, []string{".gitignore", "visible.jar", "visible.txt"}},
		{"both", []string{"--no-ignore", "--with-binaries"}, []string{".gitignore", "ignored/hidden.jar", "ignored/hidden.txt", "visible.jar", "visible.txt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scopeViewMemoReset()
			defer scopeViewMemoReset()
			resolver, err := newStartupPickerResolver()
			if err != nil {
				t.Fatal(err)
			}
			defer resolver.ReleaseRetainedTargetPreviewInventory()
			if err := scopeViewMemoSealEvaluatedTarget([]string{"."}); err != nil {
				t.Fatal(err)
			}
			frame := startupInclusionTargetFrame(startupInteractiveFrame{StartArgs: []string{"."}}, tc.flags)
			if len(frame.TargetTokens) != 0 || len(frame.StartArgs) != 0 {
				t.Fatalf("did not reopen targets: %+v", frame)
			}
			// Stand in for choosing all files in the reopened native picker.
			frame.TargetTokens = []string{"."}
			result, err := runStartupTargetFrame(resolver, frame)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Pending, []string{"--"}) {
				t.Fatalf("next menu: %v", result.Pending)
			}
			if err := scopeViewMemoSealEvaluatedTarget(result.Args); err != nil {
				t.Fatal(err)
			}
			var scans int
			restore := search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { scans++ })
			ctx, err := buildStartupSinkPickerContext(result.Args)
			restore()
			if err != nil {
				t.Fatal(err)
			}
			if got := ctx.Plan.DistinctRelPaths(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if scans != 0 {
				t.Fatalf("sink rescanned %d times", scans)
			}
			// Earlier sealed generation remains available for Escape/undo.
			old, err := buildStartupSinkPickerContext([]string{"."})
			if err != nil {
				t.Fatal(err)
			}
			if got := old.Plan.DistinctRelPaths(); !reflect.DeepEqual(got, []string{".gitignore", "visible.txt"}) {
				t.Fatalf("undo membership %v", got)
			}
		})
	}
}
