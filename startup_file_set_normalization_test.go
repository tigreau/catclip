package catclip

import (
	"strings"
	"testing"
)

func TestResolveStartupScopeFileSetArgsOnlyDropsExactFilesCoveredBySelectedPattern(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go":             "package main\n",
		"cmd/catclip/main.go": "package main\n",
		"README.md":           "# readme\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
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

if [ "$prompt" = "only> " ]; then
	printf '%s\n' "$input" | grep -F $'\t*.go\t' | head -n 1
	printf '%s\n' "$input" | grep -F $'main.go\tmain.go\tmain.go\tfile\ttext\tfile' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"."}, "--only", "only> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), ".\n--only\n*.go"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestResolveStartupScopeFileSetArgsOnlyKeepsPatternRowsAtStableBottomOrder(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go":   "package main\n",
		"README.md": "# readme\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
nosort=0
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		--no-sort)
			nosort=1
			shift
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "only> " ]; then
	[ "$nosort" -eq 1 ] || { echo "expected --no-sort for only> picker" >&2; exit 91; }
	printf '%s\n' "$input" | grep -F $'main.go\tmain.go\tmain.go\tfile\ttext\tfile' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"."}, "--only", "only> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), ".\n--only\nmain.go"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestStartupFileSetRowsOnlyPlacesPatternRowsBeforeFilesForBottomPromptLayout(t *testing.T) {
	rows := startupFileSetRows("--only", []string{"README.md", "main.go"})
	if len(rows) < 3 {
		t.Fatalf("expected pattern and file rows, got %#v", rows)
	}
	if rows[0].Kind != startupFileSetRowExtensionPattern {
		t.Fatalf("expected first row to be an extension pattern, got %#v", rows[0])
	}
	if rows[len(rows)-1].Kind != startupFileSetRowFile {
		t.Fatalf("expected last row to be a file row, got %#v", rows[len(rows)-1])
	}
}

func TestResolveStartupArgsExcludeDropsExactFilesCoveredBySelectedPattern(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go":     "package main\n",
		"pkg/util.go": "package pkg\n",
		"README.md":   "# readme\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
query=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		--query)
			query="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "exclude> " ]; then
	[ "$query" = "go" ] || { echo "unexpected query: $query" >&2; exit 91; }
	printf '%s\n' "$input" | grep -F $'\t*.go\t' | head -n 1
	printf '%s\n' "$input" | grep -F $'main.go\tmain.go\tmain.go\tfile\ttext\tfile' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{".", "--exclude", "go"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected non-exact --exclude value to use fzf")
	}
	if got, want := strings.Join(args, "\n"), ".\n--exclude\n*.go"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestNormalizeInteractiveFileSetStageValuesDropsExactFilesCoveredBySelectedSubtree(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('main')\n",
		"src/util.ts":    "console.log('util')\n",
		"docs/readme.md": "# readme\n",
	})
	_ = parseInProject(t, project, []string{"."})

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{"src/", "src/main.ts"})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"src/"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected normalized values %q, got %q", want, got)
	}
}
