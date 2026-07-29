package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func setupIgnoredIncludeProject(t *testing.T) string {
	t.Helper()
	project := setupTestProject(t, map[string]string{
		".gitignore":     "docs/\nvendor/\n*.log\n",
		"src/main.ts":    "export const main = true\n",
		"src/debug.log":  "ignored debug output\n",
		"docs/guide.md":  "ignored guide\n",
		"vendor/sdk.txt": "ignored sdk\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})
	return project
}

func TestResolveStartupIncludeWithBinariesOffersIgnoredBinary(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"visible.txt":       "visible\n",
		"ignored/image.png": "\x89PNG\x00binary\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})
	narrowMarker := filepath.Join(project, "narrow-opened")
	t.Setenv("CATCLIP_TEST_NARROW_MARKER", narrowMarker)
	installScriptFzf(t, `#!/bin/sh
prompt=""
preview=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	--preview) preview="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
case "$prompt" in
"include> ")
	printf '%s\n' "$preview" | grep -F -- '--with-binaries' >/dev/null || {
		echo "binary include preview omitted --with-binaries: $preview" >&2
		exit 91
	}
	printf '%s\n' "$input" | awk -F '\t' '$2 == "ignored/image.png"'
	;;
"narrow> ")
	: > "$CATCLIP_TEST_NARROW_MARKER"
	printf '%s\n' "$input" | grep -F 'Keep all current files'
	;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolverForArgs([]string{".", "--include", "image", "--with-binaries"})
	if err != nil {
		t.Fatal(err)
	}
	args, _, usedFzf, err := resolveStartupArgsWithUndo(resolver, []string{".", "--include", "image", "--with-binaries"})
	if err != nil {
		t.Fatalf("resolveStartupArgs: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected ignored binary query to open the include picker")
	}
	if _, err := os.Stat(narrowMarker); err != nil {
		t.Fatalf("expected ignored binary query to open the narrow picker: %v", err)
	}
	want := []string{".", "--include", "ignored/image.png", "--with-binaries"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestResolvedScopeViewMarksOnlyIncludedIgnoredFile(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":                              "internal/ui/bench_modifier_menu_test.go\n",
		"internal/cli/help.go":                    "package cli\n",
		"internal/command/invocation.go":          "package command\n",
		"internal/ui/bench_modifier_menu_test.go": "package ui\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"internal"})

	view, err := resolvedCurrentScopeViewForArgs([]string{
		"internal",
		"--include", "internal/ui/bench_modifier_menu_test.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed := make(map[string]bool, len(view.Entries))
	for _, entry := range view.Entries {
		allowed[entry.RelPath] = entry.AllowedByInclude
	}
	want := map[string]bool{
		"internal/cli/help.go":                    false,
		"internal/command/invocation.go":          false,
		"internal/ui/bench_modifier_menu_test.go": true,
	}
	if !reflect.DeepEqual(allowed, want) {
		t.Fatalf("AllowedByInclude attribution = %#v, want %#v", allowed, want)
	}
}

func TestResolvedScopeViewWildcardMarksOnlyActuallyIgnoredFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":                              "internal/ui/bench_modifier_menu_test.go\ninternal/generated/\n",
		"config/catclip/.hiss":                    "internal/ui/local_notes.txt\n",
		"internal/cli/help.go":                    "package cli\n",
		"internal/command/invocation.go":          "package command\n",
		"internal/generated/client.go":            "package generated\n",
		"internal/ui/bench_modifier_menu_test.go": "package ui\n",
		"internal/ui/local_notes.txt":             "private notes\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"internal"})

	view, err := resolvedCurrentScopeViewForArgs([]string{
		"internal",
		"--include", "*",
	})
	if err != nil {
		t.Fatal(err)
	}
	type attribution struct {
		allowed bool
		source  string
	}
	allowed := make(map[string]attribution, len(view.Entries))
	for _, entry := range view.Entries {
		allowed[entry.RelPath] = attribution{entry.AllowedByInclude, entry.BlockSource}
	}
	want := map[string]attribution{
		"internal/cli/help.go":                    {false, ""},
		"internal/command/invocation.go":          {false, ""},
		"internal/generated/client.go":            {true, ".gitignore"},
		"internal/ui/bench_modifier_menu_test.go": {true, ".gitignore"},
		"internal/ui/local_notes.txt":             {true, ".hiss"},
	}
	if !reflect.DeepEqual(allowed, want) {
		t.Fatalf("wildcard AllowedByInclude attribution = %#v, want %#v", allowed, want)
	}
}

func TestResolveStartupSingletonIncludePickerKeepsConcretePath(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":           "internal/ui/bench_modifier_menu_test.go\n",
		"internal/cli/help.go": "package cli\n",
		"internal/ui/bench_modifier_menu_test.go": "package ui\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"internal"})
	installScriptFzf(t, `#!/bin/sh
prompt=""
nth=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	--nth) nth="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
case "$prompt" in
"include> ")
	[ "$nth" = "2" ] || { echo "include picker must search only the path field, got --nth $nth" >&2; exit 91; }
	printf '%s\n' "$input" | awk -F '\t' '$2 == "internal/ui/bench_modifier_menu_test.go"'
	;;
"narrow> ") printf '%s\n' "$input" | grep -F 'Keep all current files' ;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, _, _, err := resolveStartupArgs(resolver, []string{"internal", "--include", "bench"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal", "--include", "internal/ui/bench_modifier_menu_test.go"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want concrete singleton %v", args, want)
	}
}

func TestResolveStartupQueriedIncludeRunsNarrowConfirm(t *testing.T) {
	setupIgnoredIncludeProject(t)
	installScriptFzf(t, `#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
case "$prompt" in
"include> ") printf '%s\n' "$input" | awk -F '\t' '$2 == "docs"' ;;
"narrow> ") printf '%s\n' "$input" | grep -F 'Keep only ignored files' ;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{".", "--include", "doc"})
	if err != nil {
		t.Fatalf("resolveStartupArgs: %v", err)
	}
	if !usedFzf {
		t.Fatal("queried include should use the include and narrow pickers")
	}
	want := []string{".", "--include", "docs", "--only", "docs/*"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestResolveStartupExactVisibleIncludeSkipsIncludeAndNarrowPickers(t *testing.T) {
	setupIgnoredIncludeProject(t)
	installScriptFzf(t, `#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	*) shift ;;
	esac
done
case "$prompt" in
"filter> ") printf '%s\n' 'paths' ;;
"include> "|"narrow> ") echo "exact include unexpectedly opened $prompt" >&2; exit 91 ;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, []string{"src", "--include", "src", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs: %v", err)
	}
	if !usedFzf {
		t.Fatal("trailing modifier placeholder should open the modifier picker")
	}
	want := []string{"src", "--include", "src", "--paths"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestResolveStartupNarrowEscReturnsToIncludePicker(t *testing.T) {
	project := setupIgnoredIncludeProject(t)
	state := filepath.Join(project, "narrow-state")
	t.Setenv("CATCLIP_TEST_NARROW_STATE", state)
	installScriptFzf(t, `#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
case "$prompt" in
"include> ")
	printf '%s\n' "$input" | awk -F '\t' '$2 == "docs"'
	;;
"narrow> ")
	if [ ! -f "$CATCLIP_TEST_NARROW_STATE" ]; then
		: > "$CATCLIP_TEST_NARROW_STATE"
		exit 130
	fi
	printf '%s\n' "$input" | grep -F 'Keep all current files'
	;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{".", "--include", "doc"})
	if err != nil {
		t.Fatalf("resolveStartupArgs: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected interactive include flow")
	}
	want := []string{".", "--include", "docs"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("narrow picker did not reach the Esc branch: %v", err)
	}
}

func TestResolveStartupNarrowEscReopensOnlyLastQueriedIncludePicker(t *testing.T) {
	project := setupIgnoredIncludeProject(t)
	state := filepath.Join(project, "multi-narrow-state")
	logPath := filepath.Join(project, "include-query-log")
	t.Setenv("CATCLIP_TEST_NARROW_STATE", state)
	t.Setenv("CATCLIP_TEST_INCLUDE_LOG", logPath)
	installScriptFzf(t, `#!/bin/sh
prompt=""
query=""
filter=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	--query) query="$2"; shift 2 ;;
	--filter) filter="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
if [ -n "$filter" ]; then
	printf '%s\n' "$input" | grep -i -F "$filter"
	exit 0
fi
case "$prompt" in
"include> ")
	printf '%s\n' "$query" >> "$CATCLIP_TEST_INCLUDE_LOG"
	case "$query" in
	doc) printf '%s\n' "$input" | awk -F '\t' '$2 == "docs"' ;;
	vend) printf '%s\n' "$input" | awk -F '\t' '$2 == "vendor"' ;;
	*) echo "unexpected include query: $query" >&2; exit 91 ;;
	esac
	;;
"narrow> ")
	if [ ! -f "$CATCLIP_TEST_NARROW_STATE" ]; then
		: > "$CATCLIP_TEST_NARROW_STATE"
		exit 130
	fi
	printf '%s\n' "$input" | grep -F 'Keep all current files'
	;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, _, _, err := resolveStartupArgs(resolver, []string{".", "--include", "doc", "doc", "vend"})
	if err != nil {
		t.Fatalf("resolveStartupArgs: %v", err)
	}
	want := []string{".", "--include", "docs", "vendor"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(logData)), "doc\nvend\nvend"; got != want {
		t.Fatalf("include picker sequence = %q, want %q", got, want)
	}
}

func TestResolveStartupQueriedIncludeEscReturnsToPreviousQuery(t *testing.T) {
	project := setupIgnoredIncludeProject(t)
	state := filepath.Join(project, "query-back-state")
	logPath := filepath.Join(project, "query-back-log")
	t.Setenv("CATCLIP_TEST_QUERY_BACK_STATE", state)
	t.Setenv("CATCLIP_TEST_INCLUDE_LOG", logPath)
	installScriptFzf(t, `#!/bin/sh
prompt=""
query=""
filter=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	--query) query="$2"; shift 2 ;;
	--filter) filter="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
if [ -n "$filter" ]; then
	printf '%s\n' "$input" | grep -i -F "$filter"
	exit 0
fi
case "$prompt" in
"include> ")
	printf '%s\n' "$query" >> "$CATCLIP_TEST_INCLUDE_LOG"
	if [ "$query" = "vend" ] && [ ! -f "$CATCLIP_TEST_QUERY_BACK_STATE" ]; then
		: > "$CATCLIP_TEST_QUERY_BACK_STATE"
		exit 130
	fi
	case "$query" in
	doc) printf '%s\n' "$input" | awk -F '\t' '$2 == "docs"' ;;
	vend) printf '%s\n' "$input" | awk -F '\t' '$2 == "vendor"' ;;
	*) echo "unexpected include query: $query" >&2; exit 91 ;;
	esac
	;;
"narrow> ") printf '%s\n' "$input" | grep -F 'Keep all current files' ;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, _, _, err := resolveStartupArgs(resolver, []string{".", "--include", "doc", "doc", "vend"})
	if err != nil {
		t.Fatalf("resolveStartupArgs: %v", err)
	}
	want := []string{".", "--include", "docs", "vendor"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(logData)), "doc\nvend\ndoc\nvend"; got != want {
		t.Fatalf("include picker sequence = %q, want %q", got, want)
	}
}

func TestResolveStartupRepeatedQueriedIncludeSkipsCoveredPicker(t *testing.T) {
	project := setupIgnoredIncludeProject(t)
	logPath := filepath.Join(project, "repeated-query-log")
	t.Setenv("CATCLIP_TEST_INCLUDE_LOG", logPath)
	installScriptFzf(t, `#!/bin/sh
prompt=""
query=""
filter=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	--query) query="$2"; shift 2 ;;
	--filter) filter="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
if [ -n "$filter" ]; then
	printf '%s\n' "$input" | grep -i -F "$filter"
	exit 0
fi
case "$prompt" in
"include> ")
	printf '%s\n' "$query" >> "$CATCLIP_TEST_INCLUDE_LOG"
	printf '%s\n' "$input" | awk -F '\t' '$2 == "docs"'
	;;
"narrow> ") printf '%s\n' "$input" | grep -F 'Keep all current files' ;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, _, _, err := resolveStartupArgs(resolver, []string{".", "--include", "doc", "doc"})
	if err != nil {
		t.Fatalf("resolveStartupArgs: %v", err)
	}
	want := []string{".", "--include", "docs"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(logData)), "doc"; got != want {
		t.Fatalf("include picker sequence = %q, want %q", got, want)
	}
}

func TestResolveStartupNarrowEscSkipsCoveredQuerySlot(t *testing.T) {
	project := setupIgnoredIncludeProject(t)
	state := filepath.Join(project, "covered-narrow-state")
	logPath := filepath.Join(project, "covered-narrow-log")
	t.Setenv("CATCLIP_TEST_NARROW_STATE", state)
	t.Setenv("CATCLIP_TEST_INCLUDE_LOG", logPath)
	installScriptFzf(t, `#!/bin/sh
prompt=""
query=""
filter=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	--query) query="$2"; shift 2 ;;
	--filter) filter="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
if [ -n "$filter" ]; then
	printf '%s\n' "$input" | grep -i -F "$filter"
	exit 0
fi
case "$prompt" in
"include> ")
	printf '%s\n' "$query" >> "$CATCLIP_TEST_INCLUDE_LOG"
	printf '%s\n' "$input" | awk -F '\t' '$2 == "docs"'
	;;
"narrow> ")
	if [ ! -f "$CATCLIP_TEST_NARROW_STATE" ]; then
		: > "$CATCLIP_TEST_NARROW_STATE"
		exit 130
	fi
	printf '%s\n' "$input" | grep -F 'Keep all current files'
	;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, _, _, err := resolveStartupArgs(resolver, []string{".", "--include", "doc", "doc"})
	if err != nil {
		t.Fatalf("resolveStartupArgs: %v", err)
	}
	want := []string{".", "--include", "docs"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(logData)), "doc\ndoc"; got != want {
		t.Fatalf("include picker sequence = %q, want %q", got, want)
	}
}

func TestResolveStartupScopeIncludeEscSkipsCoveredQuerySlot(t *testing.T) {
	project := setupIgnoredIncludeProject(t)
	state := filepath.Join(project, "scope-query-back-state")
	logPath := filepath.Join(project, "scope-query-back-log")
	t.Setenv("CATCLIP_TEST_QUERY_BACK_STATE", state)
	t.Setenv("CATCLIP_TEST_INCLUDE_LOG", logPath)
	installScriptFzf(t, `#!/bin/sh
prompt=""
query=""
filter=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	--query) query="$2"; shift 2 ;;
	--filter) filter="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
if [ -n "$filter" ]; then
	printf '%s\n' "$input" | grep -i -F "$filter"
	exit 0
fi
[ "$prompt" = "include> " ] || { echo "unexpected prompt: $prompt" >&2; exit 91; }
printf '%s\n' "$query" >> "$CATCLIP_TEST_INCLUDE_LOG"
if [ "$query" = "vend" ] && [ ! -f "$CATCLIP_TEST_QUERY_BACK_STATE" ]; then
	: > "$CATCLIP_TEST_QUERY_BACK_STATE"
	exit 130
fi
case "$query" in
doc) printf '%s\n' "$input" | awk -F '\t' '$2 == "docs"' ;;
vend) printf '%s\n' "$input" | awk -F '\t' '$2 == "vendor"' ;;
*) echo "unexpected include query: $query" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, targets, _, usedPicker, err := resolveStartupScopeInputs(
		resolver,
		[]string{"."},
		[]string{"doc", "doc", "vend"},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("resolveStartupScopeInputs: %v", err)
	}
	if !usedPicker {
		t.Fatal("expected include picker")
	}
	if got, want := args, []string{".", "--include", "docs", "vendor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	if got, want := targets, []string{".", "docs", "vendor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(logData)), "doc\nvend\ndoc\nvend"; got != want {
		t.Fatalf("include picker sequence = %q, want %q", got, want)
	}
}

func TestSelectionPathsExcludingExplicitTargetsPreservesEqualInclude(t *testing.T) {
	got := selectionPathsExcludingExplicitTargets(
		[]string{"docs", "docs", "vendor"},
		[]string{"docs"},
	)
	want := []string{"docs", "vendor"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolveStartupWildcardNarrowExcludesVisibleSibling(t *testing.T) {
	setupIgnoredIncludeProject(t)
	installScriptFzf(t, `#!/bin/sh
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--prompt) prompt="$2"; shift 2 ;;
	*) shift ;;
	esac
done
input="$(cat)"
case "$prompt" in
"include> ") printf '%s\n' "$input" ;;
"narrow> ") printf '%s\n' "$input" | grep -F 'Keep only ignored files' ;;
*) echo "unexpected prompt: $prompt" >&2; exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	args, _, _, _, err := resolveStartupModifierStage(
		resolver,
		[]string{"."},
		[]string{"."},
		[]string{"."},
		[]string{"--include"},
		nil,
		true,
	)
	if err != nil {
		t.Fatalf("resolveStartupModifierStage: %v", err)
	}
	joined := strings.Join(args, "\n")
	for _, want := range []string{"--include\n*", "--only", "docs/*", "vendor/*", "src/debug.log"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %v do not contain %q", args, want)
		}
	}
	if strings.Contains(joined, "src/*") {
		t.Fatalf("broad src/* would re-include visible src/main.ts: %v", args)
	}

	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		t.Fatalf("resolve replay: %v", err)
	}
	got := make([]string, 0, len(view.Entries))
	for _, entry := range view.Entries {
		got = append(got, entry.RelPath)
	}
	if strings.Contains(strings.Join(got, "\n"), "src/main.ts") {
		t.Fatalf("narrow replay retained visible sibling: %v", got)
	}
}
