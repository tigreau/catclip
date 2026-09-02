package ui

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
)

func TestFormatInteractiveFilterProgress(t *testing.T) {
	limit := 5
	scopes := []command.ExecutionScope{
		{
			Targets: []string{"src/components"},
			Stages: []command.Stage{
				{Kind: command.StageOnly, Values: []string{"*.tsx"}},
				{Kind: command.StageExclude, Values: []string{"*.test.tsx"}},
			},
		},
		{
			Targets: []string{"docs"},
			Stages: []command.Stage{
				{Kind: command.StageRecent, Limit: &limit},
				{Kind: command.StageKind("future-stage")},
			},
		},
	}

	extras := interactiveProgressExtrasFromParsed(command.Parsed{Quiet: true, Raw: true})
	if got, want := formatInteractiveFilterProgress(extras, scopes, 2), "catclip --quiet --raw --only --exclude --then --recent ▶ -- --"; got != want {
		t.Fatalf("progress = %q, want %q", got, want)
	}
	if got, want := formatInteractiveFilterProgress(extras, scopes, 0), "catclip --quiet --raw --only --exclude --then --recent ▶ output"; got != want {
		t.Fatalf("output progress = %q, want %q", got, want)
	}
}

func TestInteractiveFilterProgressShowsNeverEmitPolicy(t *testing.T) {
	extras := interactiveProgressExtrasFromParsed(command.Parsed{EmissionPolicy: command.EmissionNever})
	if got, want := formatInteractiveFilterProgress(extras, nil, 0), "catclip --no ▶ report"; got != want {
		t.Fatalf("progress = %q, want %q", got, want)
	}
}

func TestInteractiveFilterProgressShowsMetadataSelectedFromExtras(t *testing.T) {
	extras := interactiveProgressExtrasFromParsed(command.Parsed{
		PayloadKind:    command.PayloadMetadata,
		EmissionPolicy: command.EmissionAlways,
	})
	if got, want := formatInteractiveFilterProgress(extras, nil, 0), "catclip --yes --metadata ▶ output"; got != want {
		t.Fatalf("progress = %q, want %q", got, want)
	}
}

func TestPendingFilterSlotCount(t *testing.T) {
	args := []string{"--", "--recent", "5", "--", "src", "--"}
	if got, want := pendingFilterSlotCount(args), 4; got != want {
		t.Fatalf("pendingFilterSlotCount() = %d, want %d", got, want)
	}
}

func TestInteractiveFilterProgressHoistsExtrasAcrossThenLikeResolver(t *testing.T) {
	args := []string{
		"src", "--only", "*.tsx",
		"--then", "docs", "--raw", "--quiet", "--recent", "5",
	}
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		t.Fatalf("ParseArgsAllowImplicitDot returned error: %v", err)
	}

	resolved := cli.FormatResolvedStartupCommand(args)
	if !strings.HasPrefix(resolved, "catclip --quiet --raw ") {
		t.Fatalf("resolved command did not hoist extras canonically: %q", resolved)
	}
	got := formatInteractiveFilterProgress(
		interactiveProgressExtrasFromParsed(cfg),
		command.ExecutionScopesFromSpec(cfg.Command),
		0,
	)
	if want := "catclip --quiet --raw --only --then --recent ▶ output"; got != want {
		t.Fatalf("progress = %q, want %q", got, want)
	}
}

func TestModifierPickerReceivesFilterProgressFooter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"src"})
	installScriptFzf(t, `#!/bin/sh
footer=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--footer)
		footer="$2"
		shift 2
		;;
	*)
		shift
		;;
	esac
done
[ "$footer" = "catclip --only ▶ -- --" ] || {
	echo "unexpected footer: $footer" >&2
	exit 91
}
grep -F $'\tfinish\t' | head -n 1
`)

	choice, err := chooseStartupModifierChoiceWithProgress(
		[]string{"src", "--only", "*.go"},
		"",
		2,
	)
	if err != nil {
		t.Fatalf("chooseStartupModifierChoiceWithProgress returned error: %v", err)
	}
	if choice.Key != "finish" {
		t.Fatalf("choice key = %q, want finish", choice.Key)
	}
}

func TestOutputPickerReceivesFilterProgressFooter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"src"})
	installScriptFzf(t, `#!/bin/sh
footer=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--footer)
		footer="$2"
		shift 2
		;;
	*)
		shift
		;;
	esac
done
[ "$footer" = "catclip --only ▶ output" ] || {
	echo "unexpected footer: $footer" >&2
	exit 91
}
grep -F $'\tstdout\t' | head -n 1
`)

	ctx, err := buildStartupSinkPickerContext([]string{"src", "--only", "*.go"})
	if err != nil {
		t.Fatalf("buildStartupSinkPickerContext returned error: %v", err)
	}
	args, usedFzf, err := pickOutputSinkWithEscHint(ctx, measureOutputForSinkMenu(ctx.Plan, ctx.Emit), "")
	if err != nil {
		t.Fatalf("pickOutputSinkWithEscHint returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected output picker to report fzf use")
	}
	if len(args) != 1 || args[0] != "-p" {
		t.Fatalf("sink args = %#v, want []string{\"-p\"}", args)
	}
}

func TestInteractiveFilterProgressJourney(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go":      "package main\n",
		"src/skip_test.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"src"})
	installScriptFzf(t, `#!/bin/sh
prompt=""
footer=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt)
		prompt="$2"
		shift 2
		;;
	--footer)
		footer="$2"
		shift 2
		;;
	*)
		shift
		;;
	esac
done
input="$(cat)"

case "$prompt" in
"filter> ")
	case "$footer" in
	"catclip ▶ -- -- --") key="only" ;;
	"catclip --only ▶ -- --") key="exclude" ;;
	"catclip --only --exclude ▶ --") key="recent" ;;
	*)
		echo "unexpected filter footer: $footer" >&2
		exit 91
		;;
	esac
	printf '%s\n' "$input" | awk -F '\t' -v key="$key" '$2 == key' | head -n 1
	;;
"only> ")
	printf '%s\n' "$input" | grep -F $'\t*.go\t' | head -n 1
	;;
"exclude> ")
	printf '%s\n' "$input" | grep -F "src/skip_test.go" | head -n 1
	;;
"recent> ")
	printf '%s\n' "$input" | grep -F $'1\t1\tup to ' | head -n 1
	;;
"output> ")
	[ "$footer" = "catclip --only --exclude --recent ▶ output" ] || {
		echo "unexpected output footer: $footer" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F $'\tstdout\t' | head -n 1
	;;
*)
	echo "unexpected prompt: $prompt" >&2
	exit 91
	;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}
	result, err := resolveStartupPickerResultWithUndo(resolver, []string{"src", "--", "--", "--"})
	if err != nil {
		t.Fatalf("resolveStartupPickerResultWithUndo returned error: %v", err)
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--only\n*.go\n--exclude\nsrc/skip_test.go\n--recent\n1\n-p"; got != want {
		t.Fatalf("resolved args = %q, want %q", got, want)
	}
}

func TestInteractiveFilterProgressFollowsUndoFrames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"src"})
	stateBase := filepath.Join(t.TempDir(), "progress-undo")
	t.Setenv("CATCLIP_PROGRESS_TEST_STATE", stateBase)
	installScriptFzf(t, `#!/bin/sh
prompt=""
footer=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt)
		prompt="$2"
		shift 2
		;;
	--footer)
		footer="$2"
		shift 2
		;;
	*)
		shift
		;;
	esac
done
input="$(cat)"

case "$prompt" in
"filter> ")
	case "$footer" in
	"catclip ▶ -- --") key="only" ;;
	"catclip --only ▶ --")
		count=0
		[ -f "$CATCLIP_PROGRESS_TEST_STATE.filter" ] && count="$(cat "$CATCLIP_PROGRESS_TEST_STATE.filter")"
		count=$((count + 1))
		printf '%s' "$count" > "$CATCLIP_PROGRESS_TEST_STATE.filter"
		if [ "$count" -eq 1 ]; then key="recent"; else key="finish"; fi
		;;
	*)
		echo "unexpected filter footer: $footer" >&2
		exit 91
		;;
	esac
	printf '%s\n' "$input" | awk -F '\t' -v key="$key" '$2 == key' | head -n 1
	;;
"only> ")
	printf '%s\n' "$input" | grep -F $'\t*.go\t' | head -n 1
	;;
"recent> ")
	count=0
	[ -f "$CATCLIP_PROGRESS_TEST_STATE.recent" ] && count="$(cat "$CATCLIP_PROGRESS_TEST_STATE.recent")"
	count=$((count + 1))
	printf '%s' "$count" > "$CATCLIP_PROGRESS_TEST_STATE.recent"
	if [ "$count" -eq 1 ]; then
		printf '%s\n' "$input" | grep -F $'1\t1\tup to ' | head -n 1
	else
		exit 130
	fi
	;;
"output> ")
	case "$footer" in
	"catclip --only --recent ▶ output") exit 130 ;;
	"catclip --only ▶ output")
		printf '%s\n' "$input" | grep -F $'\tstdout\t' | head -n 1
		;;
	*)
		echo "unexpected output footer: $footer" >&2
		exit 91
		;;
	esac
	;;
*)
	echo "unexpected prompt: $prompt" >&2
	exit 91
	;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}
	result, err := resolveStartupPickerResultWithUndo(resolver, []string{"src", "--", "--"})
	if err != nil {
		t.Fatalf("resolveStartupPickerResultWithUndo returned error: %v", err)
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--only\n*.go\n-p"; got != want {
		t.Fatalf("resolved args after undo = %q, want %q", got, want)
	}
}

func TestInteractiveFilterProgressShowsCommittedExtras(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"src"})
	installScriptFzf(t, `#!/bin/sh
prompt=""
footer=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt)
		prompt="$2"
		shift 2
		;;
	--footer)
		footer="$2"
		shift 2
		;;
	*)
		shift
		;;
	esac
done
input="$(cat)"

case "$prompt" in
"filter> ")
	case "$footer" in
	"catclip ▶ -- --") key="extras" ;;
	"catclip --quiet --raw ▶ --") key="finish" ;;
	*)
		echo "unexpected filter footer: $footer" >&2
		exit 91
		;;
	esac
	printf '%s\n' "$input" | awk -F '\t' -v key="$key" '$2 == key' | head -n 1
	;;
"extras> ")
	printf '%s\n' "$input" | awk -F '\t' '$2 == "quiet"'
	printf '%s\n' "$input" | awk -F '\t' '$2 == "raw"'
	;;
"output> ")
	[ "$footer" = "catclip --quiet --raw ▶ output" ] || {
		echo "unexpected output footer: $footer" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F $'\tstdout\t' | head -n 1
	;;
*)
	echo "unexpected prompt: $prompt" >&2
	exit 91
	;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}
	result, err := resolveStartupPickerResultWithUndo(resolver, []string{"src", "--", "--"})
	if err != nil {
		t.Fatalf("resolveStartupPickerResultWithUndo returned error: %v", err)
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--quiet\n--raw\n-p"; got != want {
		t.Fatalf("resolved args with extras = %q, want %q", got, want)
	}
}

func TestFormatInteractiveFilterProgressAllocatesOnlyResult(t *testing.T) {
	extras := interactiveProgressExtrasFromParsed(command.Parsed{Quiet: true, Raw: true})
	scopes := []command.ExecutionScope{{
		Stages: []command.Stage{
			{Kind: command.StageOnly},
			{Kind: command.StageExclude},
			{Kind: command.StageRecent},
		},
	}}

	if got := testing.AllocsPerRun(1000, func() {
		_ = formatInteractiveFilterProgress(extras, scopes, 2)
	}); got > 1 {
		t.Fatalf("formatting allocations = %.0f, want at most the returned string", got)
	}
}

func BenchmarkFormatInteractiveFilterProgress(b *testing.B) {
	limit := 5
	extras := interactiveProgressExtrasFromParsed(command.Parsed{Quiet: true, Raw: true})
	scopes := []command.ExecutionScope{
		{Stages: []command.Stage{
			{Kind: command.StageNoIgnore},
			{Kind: command.StageOnly, Values: []string{"*.tsx"}},
			{Kind: command.StageExclude, Values: []string{"*.test.tsx"}},
			{Kind: command.StageRecent, Limit: &limit},
			{Kind: command.StageContains, Values: []string{"Button"}},
		}},
		{Stages: []command.Stage{
			{Kind: command.StageChanged},
			{Kind: command.StageSnippet, Values: []string{"HandleClick"}},
			{Kind: command.StagePaths},
		}},
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = formatInteractiveFilterProgress(extras, scopes, 2)
	}
}
