package ui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/search"
)

func TestInteractiveTokenLimitPresets(t *testing.T) {
	for _, current := range []int64{0, 12345, 4000, 8000, 16000, 32000, 64000, 100000, 200000, 500000} {
		req := tokenLimitPickerRequest("/path with space/catclip", current)
		if req.Multi || len(req.Lines) != 8 || !strings.Contains(req.Header, "[Esc] back") {
			t.Fatalf("invalid request: %+v", req)
		}
		for _, row := range req.Lines {
			label, value, ok := strings.Cut(row, "\t")
			n, err := strconv.ParseInt(value, 10, 64)
			if !ok || err != nil || n <= 0 || !isTokenLimitPreset(n) {
				t.Fatalf("invalid preset %q", row)
			}
			if !strings.Contains(label, value) {
				t.Fatalf("typing the full CLI amount cannot match %q", label)
			}
		}
		position, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(req.Bindings[0], "load:pos("), ")"))
		if err != nil || position < 1 || position > len(req.Lines) {
			t.Fatal(req.Bindings)
		}
		_, value, _ := strings.Cut(req.Lines[position-1], "\t")
		want := current
		if !isTokenLimitPreset(want) {
			want = command.DefaultTokenLimit
		}
		if value != strconv.FormatInt(want, 10) {
			t.Fatalf("focused %s, want %d", value, want)
		}
	}
}

func TestInteractiveTokenLimitExtrasCancelAndRestore(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CATCLIP_TEST_EXTRA_STEP", filepath.Join(t.TempDir(), "step"))
	installScriptFzf(t, `#!/bin/sh
prompt=""
bindings=""
while [ "$#" -gt 0 ]; do
 case "$1" in
 --prompt) prompt="$2"; shift 2 ;;
 --bind) bindings="$bindings $2"; shift 2 ;;
 *) shift ;;
 esac
done
case "$prompt" in
"extras> ")
 if [ -f "$CATCLIP_TEST_EXTRA_STEP" ]; then
  case "$bindings" in *"load:pos("*"+select+pos("*"+select"*) ;; *) exit 90 ;; esac
 fi
 grep -E $'\t(token-limit|verbose)\t'
 ;;
"token-limit> ")
 if [ ! -f "$CATCLIP_TEST_EXTRA_STEP" ]; then
  cat >/dev/null
  touch "$CATCLIP_TEST_EXTRA_STEP"
  exit 130
 fi
 grep -F $'\t64000'
 ;;
*) exit 91 ;;
esac
`)
	current := []string{"src", "--token-limit", "16000", "--then", "tests"}
	before := append([]string(nil), current...)
	var scans int
	restore := search.SetMembershipEnumerationObserver(func(search.MembershipEnumerationEvent) { scans++ })
	defer restore()
	extra, err := chooseStartupExtrasWithEscHint("", current)
	if err != nil {
		t.Fatal(err)
	}
	got := appendStartupGlobalOptions(current, extra)
	cfg, err := cli.ParseArgs(got)
	if err != nil || cfg.TokenLimit != 64000 || !cfg.Verbose || scans != 0 || !reflect.DeepEqual(current, before) {
		t.Fatalf("args=%v cfg=%+v err=%v scans=%d", got, cfg, err, scans)
	}
	if strings.Count(strings.Join(got, " "), "--token-limit") != 1 {
		t.Fatal(got)
	}
	if progress := formatInteractiveFilterProgress(interactiveProgressExtrasFromParsed(cfg), nil, 1); progress != "catclip --verbose --token-limit ▶ --" {
		t.Fatal(progress)
	}
}

func TestInteractiveTokenLimitEscapeCommitsNothing(t *testing.T) {
	installScriptFzf(t, "#!/bin/sh\ncat >/dev/null\nexit 130\n")
	current := []string{"src", "-tl", "12345"}
	before := append([]string(nil), current...)
	got, err := chooseStartupExtrasWithEscHint("", current)
	if !errors.Is(err, discovery.ErrSelectionCancelled) || got != nil || !reflect.DeepEqual(current, before) {
		t.Fatalf("got=%v err=%v current=%v", got, err, current)
	}
}

func TestInteractiveTokenLimitReplacementKeepsOperands(t *testing.T) {
	current := []string{"src", "--contains", "--token-limit", "-tl", "12345", "--then", "docs", "--token-limit", "16000"}
	got := appendStartupGlobalOptions(current, []string{"--token-limit", "32000", "--verbose"})
	want := []string{"src", "--contains", "--token-limit", "--then", "docs", "--token-limit", "32000", "--verbose"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestOnlyTreeOutputChoice(t *testing.T) {
	for _, bundle := range []bool{false, true} {
		choices := startupAvailableSinkChoices(command.Parsed{}, bundle)
		if choices[len(choices)-1].Key != "only-tree" || choices[len(choices)-2].Key != "headless" {
			t.Fatal(choices)
		}
		wantDefault := "clipboard"
		if bundle {
			wantDefault = "bundle"
		}
		if choices[0].Key != wantDefault {
			t.Fatal(choices)
		}
		cfg, err := cli.ParseArgs(append([]string{"src"}, choices[len(choices)-1].Args...))
		if err != nil || cfg.EmissionPolicy != command.EmissionNever {
			t.Fatalf("%+v %v", cfg, err)
		}
		for _, cfg := range []command.Parsed{{NoTree: true}, {Quiet: true}, {Headless: true}, {OutputMode: command.OutputModeStdout}, {EmissionPolicy: command.EmissionAlways}} {
			for _, choice := range startupAvailableSinkChoices(cfg, bundle) {
				if choice.Key == "only-tree" {
					t.Fatalf("irrelevant for %+v", cfg)
				}
			}
		}
	}
	if _, err := cli.ParseArgs([]string{"src", "--only-tree", "--no-tree"}); err == nil {
		t.Fatal(err)
	}
}

func TestOnlyTreePickerKeepsInitialHint(t *testing.T) {
	project := setupTestProject(t, map[string]string{"a.go": "package a\n"})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	installScriptFzf(t, `#!/bin/sh
while [ "$#" -gt 0 ]; do
 case "$1" in
 --header) [ "$2" = "$CATCLIP_TEST_OUTPUT_HINT" ] || exit 90; shift 2 ;;
 --preview) case "$2" in *internal-sink-choice*) exit 91 ;; esac; shift 2 ;;
 *) shift ;;
 esac
done
grep -F $'\tonly-tree\t'
`)
	t.Setenv("CATCLIP_TEST_OUTPUT_HINT", startupSinkPickerHeader())
	ctx, err := buildStartupSinkPickerContext([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range []bool{false, true} {
		args, used, err := pickOutputSink(ctx, sinkPayloadMeasurement{WouldBundle: bundle})
		if err != nil || !used || !reflect.DeepEqual(args, []string{"--only-tree"}) {
			t.Fatalf("args=%v used=%v err=%v", args, used, err)
		}
	}
}

func TestOnlyTreeFocusPreviewAndControls(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	dir := t.TempDir()
	mode, source, tree := filepath.Join(dir, "mode"), filepath.Join(dir, "source"), filepath.Join(dir, "tree")
	for path, body := range map[string]string{mode: "output\n", source: "source code\n", tree: sinkTreePreviewHeading + "tree and summary\n"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, choice := range []string{"only-tree", "stdout", "only-tree", "clipboard", "headless"} {
		var actions, preview bytes.Buffer
		if err := RunInternalSinkFocus(mode, "", choice, &actions); err != nil {
			t.Fatal(err)
		}
		if err := RunInternalSinkPreview(mode, source, tree, &preview); err != nil {
			t.Fatal(err)
		}
		if choice == "only-tree" {
			if preview.String() != "tree and summary\n" || !strings.Contains(actions.String(), "Only tree has no source code.") || !strings.Contains(actions.String(), "[Ctrl-T] disabled") || !strings.Contains(actions.String(), "unbind(ctrl-t)") {
				t.Fatalf("%q %q", preview.String(), actions.String())
			}
		} else {
			if preview.String() != "source code\n" || !strings.Contains(actions.String(), startupSinkPickerHeader()) || !strings.Contains(actions.String(), "rebind(ctrl-t)") {
				t.Fatalf("%q %q", preview.String(), actions.String())
			}
			// Ctrl-T still works normally after returning to an output row.
			if err := RunInternalSinkToggle(mode, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			stored, err := os.ReadFile(mode)
			if err != nil || string(stored) != "tree\n" {
				t.Fatalf("%q %v", stored, err)
			}
		}
	}
}

func TestInteractiveTokenLimitPreviewIsStatic(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, n := range tokenLimitPresets() {
		var out bytes.Buffer
		if err := RunInternalTokenLimitPreview(n, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), formatTokenLimitAmount(n)) || !strings.Contains(out.String(), "--then") {
			t.Fatal(out.String())
		}
		if words := map[int64]string{4000: "2,700", 100000: "66,700", 500000: "333,300"}[n]; words != "" && !strings.Contains(out.String(), "Roughly "+words+" average English words.") {
			t.Fatalf("incorrect word comparison for %d: %s", n, out.String())
		}
	}
	var out bytes.Buffer
	if err := RunInternalTokenLimitPreview(0, &out); err == nil || out.Len() != 0 {
		t.Fatal("accepted invalid preset")
	}
}

func BenchmarkInteractiveTokenLimitRequest(b *testing.B) {
	b.Setenv("NO_COLOR", "1")
	b.Setenv("XDG_CONFIG_HOME", b.TempDir())
	_ = tokenLimitPickerRequest("/tmp/catclip", 0)
	b.ReportAllocs()
	for b.Loop() {
		_ = tokenLimitPickerRequest("/tmp/catclip", 0)
	}
}
