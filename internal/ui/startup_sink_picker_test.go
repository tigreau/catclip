package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

// The output-text preview shows the byte-exact emit shape with chroma
// syntax highlighting applied to file bodies inside <file>...</file>
// wrappers. The wrappers stay verbatim (so the user sees what will
// actually get pasted); only the body bytes receive ANSI styling.
// Stripping ANSI from the preview must recover the byte-exact emit.
func TestRenderSinkPreviewMatchesEmitShapeWithHighlighting(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n\nfunc main() {}\n",
	})
	plan := testSinkOutputPlan(t, project, "src/main.go")

	var want bytes.Buffer
	if err := output.WriteOutputPlanPayloadWithoutPrefetch(&want, output.EmitConfig{}, plan); err != nil {
		t.Fatalf("output.WriteOutputPlanPayloadWithoutPrefetch returned error: %v", err)
	}
	preview, err := renderSinkOutputTextPreview(plan, output.EmitConfig{}, int64(want.Len()+100))
	if err != nil {
		t.Fatalf("renderSinkOutputTextPreview returned error: %v", err)
	}
	if preview.Truncated {
		t.Fatal("expected preview not to be truncated")
	}
	body := string(preview.Body)
	if !strings.Contains(body, "\x1b[") {
		t.Fatalf("expected preview to include ANSI escapes (chroma highlighting on body); got:\n%s", body)
	}
	if got := stripANSIForTest(body); got != want.String() {
		t.Fatalf("ANSI-stripped preview should equal emit bytes exactly\nwant:\n%s\ngot:\n%s", want.String(), got)
	}
	// The <file> wrappers must stay verbatim — they are part of the emit
	// shape the user is confirming.
	if !strings.Contains(body, `<file path="src/main.go">`) {
		t.Fatalf("expected preview to include verbatim <file path=\"src/main.go\"> wrapper; got:\n%s", body)
	}
	if !strings.Contains(body, "</file>") {
		t.Fatalf("expected preview to include verbatim </file> wrapper; got:\n%s", body)
	}
}

func TestRenderSinkPreviewTruncatesAtLimit(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.go": strings.Repeat("x", 200),
	})
	plan := testSinkOutputPlan(t, project, "src/main.go")

	preview, err := renderSinkOutputTextPreview(plan, output.EmitConfig{}, 32)
	if err != nil {
		t.Fatalf("renderSinkOutputTextPreview returned error: %v", err)
	}
	if !preview.Truncated {
		t.Fatal("expected preview to be truncated")
	}
	if got, want := len(preview.Body), 32; got != want {
		t.Fatalf("expected capped body length %d, got %d", want, got)
	}
	formatted := string(formatSinkPreview(preview))
	if !strings.Contains(formatted, "[preview truncated at 128 KiB]") {
		t.Fatalf("expected truncation footer, got %q", formatted)
	}
}

// stripANSIForTest removes ANSI escape sequences so themed-preview
// content can be compared against plain source text.
func stripANSIForTest(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			end := strings.IndexAny(s[i:], "mK")
			if end < 0 {
				break
			}
			i += end + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestMaybeResolveStartupSinkPickerArgsSelectsStdoutAndCarriesPreparedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
bind=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		--bind)
			bind="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

[ "$prompt" = "output> " ] || {
	echo "unexpected prompt: $prompt" >&2
	exit 91
}
case "$bind" in
	*"ctrl-t:execute-silent"*"refresh-preview"*) ;;
	*)
		echo "missing preview toggle binding: $bind" >&2
		exit 91
		;;
esac

input="$(cat)"
if printf '%s\n' "$input" | grep -F "Clipboard - text only" >/dev/null; then
	echo "small menu should not include text-only clipboard row" >&2
	exit 91
fi
printf '%s\n' "$input" | grep -F $'\tstdout\t' | head -n 1
`)

	result, err := maybeResolveStartupSinkPickerArgs(nil, StartupPickerResult{
		Args:    []string{"src"},
		UsedFzf: true,
	})
	if err != nil {
		t.Fatalf("maybeResolveStartupSinkPickerArgs returned error: %v", err)
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n-p"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
	if result.PreparedOutput == nil {
		t.Fatal("expected prepared output state to be carried into final run")
	}
	if result.PreparedOutput.Plan.Len() != 1 {
		t.Fatalf("expected prepared plan with 1 item, got %d", result.PreparedOutput.Plan.Len())
	}
	assertPreparedOutputReplays(t, result)
}

func TestMaybeResolveStartupSinkPickerArgsSelectsHeadlessAndForcesResolvedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			[ "$2" = "output> " ] || {
				echo "unexpected prompt: $2" >&2
				exit 91
			}
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"
printf '%s\n' "$input" | grep -F $'\theadless\t' | head -n 1
`)

	result, err := maybeResolveStartupSinkPickerArgs(nil, StartupPickerResult{
		Args:    []string{"src"},
		UsedFzf: true,
	})
	if err != nil {
		t.Fatalf("maybeResolveStartupSinkPickerArgs returned error: %v", err)
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--headless"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
	if !result.ForceResolvedCommand {
		t.Fatal("expected picker-selected --headless to force the resolved command header")
	}
	if result.PreparedOutput == nil {
		t.Fatal("expected prepared output state to be carried into final run")
	}
}

func TestMaybeResolveStartupSinkPickerArgsSelectsPreview(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			[ "$2" = "output> " ] || {
				echo "unexpected prompt: $2" >&2
				exit 91
			}
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"
printf '%s\n' "$input" | grep -F "Stdout - metadata only" >/dev/null || {
	echo "missing metadata-only stdout label" >&2
	exit 91
}
printf '%s\n' "$input" | grep -F "Print paths, sizes, tokens, git, dates." >/dev/null || {
	echo "missing metadata-only stdout description" >&2
	exit 91
}
printf '%s\n' "$input" | grep -F $'\tpreview\t' | head -n 1
`)

	result, err := maybeResolveStartupSinkPickerArgs(nil, StartupPickerResult{
		Args:    []string{"src"},
		UsedFzf: true,
	})
	if err != nil {
		t.Fatalf("maybeResolveStartupSinkPickerArgs returned error: %v", err)
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--preview"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
	if result.PreparedOutput == nil {
		t.Fatal("expected prepared output state to be carried into final run")
	}
}

func TestMaybeResolveStartupSinkPickerArgsSkipsWhenRawArgsAlreadyChooseSink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not be called when raw args already choose a sink" >&2
exit 91
`)

	for _, rawArg := range []string{"-p", "--print", "--headless", "--no-bundle", "--preview", "-q", "--quiet"} {
		t.Run(rawArg, func(t *testing.T) {
			result, err := maybeResolveStartupSinkPickerArgs([]string{rawArg}, StartupPickerResult{
				Args:    []string{"src", rawArg},
				UsedFzf: true,
			})
			if err != nil {
				t.Fatalf("maybeResolveStartupSinkPickerArgs returned error: %v", err)
			}
			if got, want := strings.Join(result.Args, "\n"), "src\n"+rawArg; got != want {
				t.Fatalf("expected args %q, got %q", want, got)
			}
			if result.PreparedOutput != nil {
				t.Fatal("expected no prepared output when sink picker is skipped")
			}
		})
	}
}

func TestStartupSinkPickerSkipsEmptyPreparedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf must not run for an empty prepared output" >&2
exit 91
`)

	args := []string{"src", "--only", "*.ts"}
	result, err := maybeResolveStartupSinkPickerArgs(nil, StartupPickerResult{
		Args:    args,
		UsedFzf: true,
	})
	if err != nil {
		t.Fatalf("legacy sink gate returned error: %v", err)
	}
	if got, want := strings.Join(result.Args, "\n"), strings.Join(args, "\n"); got != want {
		t.Fatalf("empty sink gate changed args\nwant: %q\ngot:  %q", want, got)
	}
	if result.PreparedOutput == nil || !result.PreparedOutput.Plan.IsEmpty() {
		t.Fatalf("expected carried empty prepared output, got %#v", result.PreparedOutput)
	}

	frameResult, err := runStartupSinkFrame(startupInteractiveFrame{
		Kind:      startupInteractiveFrameSink,
		StartArgs: args,
	}, nil)
	if err != nil {
		t.Fatalf("undo-aware sink gate returned error: %v", err)
	}
	if !frameResult.SinkResolved {
		t.Fatal("empty sink frame must resolve without opening another picker")
	}
	if frameResult.UsedFzf {
		t.Fatal("empty sink frame must not invoke fzf")
	}
	if frameResult.PreparedOutput == nil || !frameResult.PreparedOutput.Plan.IsEmpty() {
		t.Fatalf("expected carried empty frame output, got %#v", frameResult.PreparedOutput)
	}
}

func TestStartupUndoEscFromSinkReturnsToPreviousPicker(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go":     "package src\n",
		"shared/util.go":  "package shared\n",
		"shared/extra.go": "package shared\n",
	})
	_ = parseInProject(t, project, []string{"."})
	stateFile := filepath.Join(t.TempDir(), "fzf-state")
	installScriptFzf(t, fmt.Sprintf(`#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

case "$prompt" in
	"select> ")
		select_count=0
		if [ -f %[1]q.select ]; then
			select_count="$(cat %[1]q.select)"
		fi
		select_count=$((select_count + 1))
		printf '%%s' "$select_count" > %[1]q.select
		case "$select_count" in
			1)
					printf '%%s\n' "$input" | awk -F '\t' '$2 == "src"' | head -n 1
				;;
			2)
					printf '%%s\n' "$input" | awk -F '\t' '$2 == "shared"' | head -n 1
				;;
			*)
				echo "unexpected select count: $select_count" >&2
				exit 91
				;;
		esac
		;;
	"output> ")
		output_count=0
		if [ -f %[1]q.output ]; then
			output_count="$(cat %[1]q.output)"
		fi
		output_count=$((output_count + 1))
		printf '%%s' "$output_count" > %[1]q.output
		case "$output_count" in
			1)
				exit 130
				;;
			2)
				printf '%%s\n' 'stdout'
				;;
			*)
				echo "unexpected output count: $output_count" >&2
				exit 91
				;;
		esac
		;;
	*)
		echo "unexpected prompt: $prompt" >&2
		exit 91
		;;
esac
`, stateFile))

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	result, err := resolveStartupPickerResultWithUndo(resolver, nil)
	if err != nil {
		t.Fatalf("resolveStartupPickerResultWithUndo returned error: %v", err)
	}
	if !result.UsedFzf {
		t.Fatal("expected sink undo flow to use fzf")
	}
	if result.PreparedOutput == nil {
		t.Fatal("expected prepared output from selected sink")
	}
	if got, want := strings.Join(result.Args, "\n"), "shared\n-p"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
	assertPreparedOutputReplays(t, result)
}

func assertPreparedOutputReplays(t *testing.T, result StartupPickerResult) {
	t.Helper()
	if result.PreparedOutput == nil {
		t.Fatal("expected prepared output")
	}
	if _, err := cli.ParseArgs(result.Args); err != nil {
		t.Fatalf("resolved command must parse without interactive state: %v", err)
	}
	replayed, err := buildStartupSinkPickerContext(result.Args)
	if err != nil {
		t.Fatalf("rebuilding output from resolved command: %v", err)
	}
	if got, want := replayed.Plan.DistinctRelPaths(), result.PreparedOutput.Plan.DistinctRelPaths(); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved command changed prepared file set: got %v, want %v", got, want)
	}
}

// TestSinkPreviewTogglePersistsAcrossPreviewReruns moved to root
// (startup_sink_picker_root_test.go) because it re-execs the test
// binary with CATCLIP_TEST_RUN_MAIN=1 — only root TestMain handles
// that branch. PrepareStartupSinkPreviewFiles + StartupSinkPickerContext
// stay exported (instead of unexported during the 3A cleanup) so the
// relocated test can construct the preview-files context.

func TestSinkTreeReportPreviewRendersAnsiColors(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	plan := testSinkOutputPlan(t, project, "src/main.go")
	report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{IncludeTreeMetadata: true})
	if err != nil {
		t.Fatalf("output.BuildReportForPlan returned error: %v", err)
	}
	preview, err := renderSinkTreeReportPreview(StartupSinkPickerContext{
		Render: RenderConfig{Preview: true},
		Plan:   plan,
		Report: report,
	}, output.PreviewByteLimit)
	if err != nil {
		t.Fatalf("renderSinkTreeReportPreview returned error: %v", err)
	}
	if !bytes.Contains(preview.Body, []byte("\x1b[")) {
		t.Fatalf("expected ANSI color sequences in tree/report preview, got %q", string(preview.Body))
	}
}

func testSinkOutputPlan(t *testing.T, project, relPath string) output.Plan {
	t.Helper()
	absPath := filepath.Join(project, filepath.FromSlash(relPath))
	info, err := os.Stat(absPath)
	if err != nil {
		t.Fatalf("stat %s: %v", relPath, err)
	}
	plan, err := output.PreparePlan(git.Context{}, []discovery.Entry{{
		AbsPath:    absPath,
		RelPath:    relPath,
		Mode:       command.EntryModeFull,
		SizeBytes:  info.Size(),
		SizeKnown:  true,
		GitVisible: true,
	}})
	if err != nil {
		t.Fatalf("output.PreparePlan returned error: %v", err)
	}
	if plan.IsEmpty() {
		t.Fatal("expected non-empty output plan")
	}
	return plan
}

func TestMeasureOutputForSinkMenuUsesLargeMenuAtBundleThreshold(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/large.txt": strings.Repeat("x", output.BundleThreshold),
	})
	plan := testSinkOutputPlan(t, project, "src/large.txt")

	measurement := measureOutputForSinkMenu(plan, output.EmitConfig{})
	if measurement.Err != nil {
		t.Fatalf("measureOutputForSinkMenu returned error: %v", measurement.Err)
	}
	if !measurement.WouldBundle {
		t.Fatalf("expected large menu for payload over threshold, got %#v", measurement)
	}
	if measurement.Bytes < output.BundleThreshold {
		t.Fatalf("expected measured bytes >= %d, got %d", output.BundleThreshold, measurement.Bytes)
	}
}

func TestStartupSinkChoiceLinesIndexesByStableKey(t *testing.T) {
	lines, index := startupSinkChoiceLines(startupSinkChoicesLarge)
	if len(lines) != len(startupSinkChoicesLarge) {
		t.Fatalf("expected %d lines, got %d", len(startupSinkChoicesLarge), len(lines))
	}
	choice, ok := index["text"]
	if !ok {
		t.Fatalf("expected text row in index: %#v", index)
	}
	if got, want := fmt.Sprint(choice.Args), "[--no-bundle]"; got != want {
		t.Fatalf("expected text row args %s, got %s", want, got)
	}
}

func TestStartupSinkPreviewToggleBindingUsesCtrlT(t *testing.T) {
	binding := startupSinkPreviewToggleBinding("/tmp/catclip-toggle")
	if !strings.Contains(binding, "ctrl-t:execute-silent(/tmp/catclip-toggle)+refresh-preview") {
		t.Fatalf("expected ctrl-t binding, got %q", binding)
	}
	// Earlier candidates that have to stay rejected:
	//   `?`     — required shift+/, undiscoverable.
	//   `ctrl-/`— failed macOS dogfood.
	//   `ctrl-p`/`ctrl-n` — fzf's input-history navigation.
	if strings.Contains(binding, "ctrl-_") || strings.Contains(binding, "ctrl-/") ||
		strings.HasPrefix(binding, "?:") ||
		strings.Contains(binding, "ctrl-p:") || strings.Contains(binding, "ctrl-n:") {
		t.Fatalf("expected ctrl-t only, got %q", binding)
	}
}
