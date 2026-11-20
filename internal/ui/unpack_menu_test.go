package ui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestStartupUnpackOfferedOnlyBeforeAnyFilter(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"targets only", []string{"src", "main.go"}, true},
		{"after --only", []string{"src", "--only", "*.go"}, false},
		{"after --snippet", []string{"src", "--snippet", "Foo"}, false},
		{"later scope", []string{"src", "--then", "docs"}, false},
	}
	for _, c := range cases {
		if got := startupUnpackOffered(c.args); got != c.want {
			t.Errorf("%s: offered=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestUnpackMenuChoicesPutClipboardOnTopOnScreen(t *testing.T) {
	choices := unpackMenuChoices("3 files from a chat reply")
	var keys []string
	for _, c := range choices {
		keys = append(keys, c.Key)
	}
	// Input order is bottom-to-top, so the last input row is the top row.
	if got := strings.Join(keys, ","); got != "reply-format,file,clipboard" {
		t.Fatalf("keys %s", got)
	}
	if !strings.Contains(choices[2].Description, "3 files from a chat reply") {
		t.Fatalf("clipboard row: %q", choices[2].Description)
	}
	if strings.Contains(unpackMenuChoices("")[2].Description, ":") {
		t.Fatal("no summary, no colon")
	}
}

func stubUnpackMenu(t *testing.T, action func() (UnpackMenuAction, error), path func() (string, error), run func(UnpackMenuAction, string) error) {
	t.Helper()
	oldAction, oldPath, oldHooks := unpackMenuChooseAction, unpackMenuChoosePath, UnpackMenu
	t.Cleanup(func() { unpackMenuChooseAction, unpackMenuChoosePath, UnpackMenu = oldAction, oldPath, oldHooks })
	unpackMenuChooseAction, unpackMenuChoosePath = action, path
	UnpackMenu = UnpackMenuHooks{Run: run}
}

func TestStartupUnpackStepsGoBackToTheMenuAndThenEnd(t *testing.T) {
	actions := []UnpackMenuAction{UnpackActionFile, UnpackActionFile, UnpackActionClipboard}
	menuCalls, pathCalls := 0, 0
	var runs []string
	stubUnpackMenu(t,
		func() (UnpackMenuAction, error) {
			a := actions[menuCalls]
			menuCalls++
			return a, nil
		},
		func() (string, error) {
			pathCalls++
			if pathCalls == 1 {
				return "", discovery.ErrSelectionCancelled // Esc in the path input
			}
			return "reply.txt", nil
		},
		func(a UnpackMenuAction, p string) error {
			runs = append(runs, string(a)+":"+p)
			if len(runs) == 1 {
				return discovery.ErrSelectionCancelled // n at the root question
			}
			return nil
		},
	)
	err := chooseStartupUnpack()
	var done *StartupUnpackDone
	if !errors.As(err, &done) {
		t.Fatalf("expected the session to end, got %v", err)
	}
	if menuCalls != 3 || pathCalls != 2 {
		t.Fatalf("menu shown %d times, path asked %d times", menuCalls, pathCalls)
	}
	if got := strings.Join(runs, " "); got != "file:reply.txt clipboard:" {
		t.Fatalf("runs: %s", got)
	}
	if len(done.Args) != 1 || done.Args[0] != "--unpack" {
		t.Fatalf("resolved args %v", done.Args)
	}
}

func TestStartupUnpackEscInMenuGoesBackToTheFilterMenu(t *testing.T) {
	stubUnpackMenu(t,
		func() (UnpackMenuAction, error) { return "", discovery.ErrSelectionCancelled },
		func() (string, error) { return "", nil },
		func(UnpackMenuAction, string) error { t.Fatal("nothing should run"); return nil },
	)
	if err := chooseStartupUnpack(); !errors.Is(err, discovery.ErrSelectionCancelled) {
		t.Fatalf("got %v", err)
	}
}

func TestStartupUnpackFailuresEndTheSession(t *testing.T) {
	boom := errors.New("boom")
	stubUnpackMenu(t,
		func() (UnpackMenuAction, error) { return UnpackActionReplyFormat, nil },
		func() (string, error) { return "", nil },
		func(UnpackMenuAction, string) error { return boom },
	)
	err := chooseStartupUnpack()
	var done *StartupUnpackDone
	if !errors.Is(err, boom) || errors.As(err, &done) {
		t.Fatalf("got %v", err)
	}
}

func TestUnpackResolvedArgs(t *testing.T) {
	if got := unpackResolvedArgs(UnpackActionFile, "a b.txt"); len(got) != 2 || got[0] != "--unpack" || got[1] != "a b.txt" {
		t.Fatalf("%v", got)
	}
	if got := unpackResolvedArgs(UnpackActionReplyFormat, ""); len(got) != 1 || got[0] != "--unpack-reply-format" {
		t.Fatalf("%v", got)
	}
}

func TestCleanUnpackPath(t *testing.T) {
	if got := CleanUnpackPath(`  "/tmp/My File.txt" `); got != "/tmp/My File.txt" {
		t.Fatalf("quotes: %q", got)
	}
	if got := CleanUnpackPath("reply.txt"); got != "reply.txt" {
		t.Fatalf("relative: %q", got)
	}
	if runtime.GOOS != "windows" {
		if got := CleanUnpackPath(`/tmp/My\ File.txt`); got != "/tmp/My File.txt" {
			t.Fatalf("escapes: %q", got)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if got := CleanUnpackPath("~/reply.txt"); got != filepath.Join(home, "reply.txt") {
			t.Fatalf("home: %q", got)
		}
	}
}
