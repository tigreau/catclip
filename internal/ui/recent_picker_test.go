package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestResolveStartupTrailingActionArgsRecentUsesPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/newest.ts": "newest\n",
		"src/older.ts":  "older\n",
		"src/oldest.ts": "oldest\n",
	})
	now := time.Now()
	for rel, modTime := range map[string]time.Time{
		"src/newest.ts": now.Add(-1 * time.Hour),
		"src/older.ts":  now.Add(-2 * time.Hour),
		"src/oldest.ts": now.Add(-3 * time.Hour),
	} {
		setProjectModTime(t, project, rel, modTime)
	}
	_ = parseInProject(t, project, []string{"."})

	installScriptFzf(t, `#!/bin/sh
prompt=""
header=""
preview=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		--header)
			header="$2"
			shift 2
			;;
		--preview)
			preview="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

	if [ "$prompt" = "recent> " ]; then
	printf '%s\n' "$header" | grep -F "Pick recent files." >/dev/null || {
		echo "missing recent header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Type a number to choose how many to keep." >/dev/null || {
		echo "missing Enter header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "[Up/Down] move  [Enter] confirm  [Esc] exit" >/dev/null || {
		echo "missing preview header" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "--internal-recent-preview" >/dev/null || {
		echo "missing internal recent preview command" >&2
		exit 91
	}
	first="$(printf '%s\n' "$input" | sed -n '1p')"
	printf '%s\n' "$first" | grep -F "[sort all by recent]" >/dev/null || {
		echo "missing sort all recent row" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F '1	1	up to ' >/dev/null || {
		echo "missing numeric recent row 1" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F '2	2	up to ' >/dev/null || {
		echo "missing numeric recent row 2" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F '3	3	up to ' >/dev/null || {
		echo "missing numeric recent row 3" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F 'src/newest.ts' >/dev/null && {
		echo "recent numeric picker should not show file paths on the left" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F '2	2	up to ' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupTrailingActionArgs(resolver, []string{"src"}, startupTrailingActionRecent)
	if err != nil {
		t.Fatalf("resolveStartupTrailingActionArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--recent\n2"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func setProjectModTime(t *testing.T, project, rel string, modTime time.Time) {
	t.Helper()
	absPath := filepath.Join(project, rel)
	if err := os.Chtimes(absPath, modTime, modTime); err != nil {
		t.Fatalf("os.Chtimes(%s) returned error: %v", rel, err)
	}
}

func TestResolveStartupArgsExplicitRecentLimitAfterAmbiguousTargetStaysDeterministic(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "src\n",
		"scripts/build.ts": "scripts\n",
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

if [ "$prompt" = "select> " ] && [ "$query" = "sr" ]; then
	printf '%s\n' "$query"
	printf '%s\n' "$input" | grep -F "[dir] src" | head -n 1
	exit 0
fi

if [ "$prompt" = "recent> " ]; then
	echo "recent picker should not open when --recent already has a limit" >&2
	exit 91
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, _, err := resolveStartupArgs(resolver, []string{"sr", "--recent", "2"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--recent\n2"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestRenderRecentPreviewUsesSelectedLimit(t *testing.T) {
	now := time.Date(2026, time.April, 5, 12, 0, 0, 0, time.UTC)
	entries := []recentPreviewEntry{
		{RelPath: "src/a.ts", ModTime: now.Add(-1 * time.Minute)},
		{RelPath: "src/b.ts", ModTime: now.Add(-2 * time.Minute)},
		{RelPath: "src/c.ts", ModTime: now.Add(-3 * time.Minute)},
		{RelPath: "src/d.ts", ModTime: now.Add(-4 * time.Minute)},
	}

	got := renderRecentPreview(entries, "3", now)
	for _, want := range []string{
		"Top 3 recent files",
		"Enter applies --recent 3.",
		"src/a.ts",
		"src/b.ts",
		"src/c.ts",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected preview to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "src/d.ts") {
		t.Fatalf("expected preview to stop at the first 3 files, got %q", got)
	}
}

func TestRenderRecentPreviewSortAllShowsFullScope(t *testing.T) {
	now := time.Date(2026, time.April, 5, 12, 0, 0, 0, time.UTC)
	entries := []recentPreviewEntry{
		{RelPath: "src/a.ts", ModTime: now.Add(-1 * time.Minute)},
		{RelPath: "src/b.ts", ModTime: now.Add(-2 * time.Minute)},
	}

	got := renderRecentPreview(entries, recentPickerSortAllToken, now)
	for _, want := range []string{
		"Sort all files by recent",
		"2 files in the current scope. Enter keeps them all newest-first.",
		"src/a.ts",
		"src/b.ts",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected preview to contain %q, got %q", want, got)
		}
	}
}

func TestStartupRecentPickerLinesUseFinderStyleModifiedCutoffs(t *testing.T) {
	now := time.Date(2026, time.April, 5, 12, 0, 0, 0, time.UTC)
	entries := []discovery.Entry{
		{RelPath: "src/newest.ts", ModTime: time.Date(2026, time.April, 5, 9, 30, 0, 0, time.UTC)},
		{RelPath: "src/older.ts", ModTime: time.Date(2026, time.April, 4, 16, 45, 0, 0, time.UTC)},
		{RelPath: "src/oldest.ts", ModTime: time.Date(2026, time.March, 28, 8, 15, 0, 0, time.UTC)},
	}

	got := startupRecentPickerLinesAt(entries, now)
	want := []string{
		"[sort all by recent]\tall\t",
		"1\t1\tup to Today at 9:30 AM",
		"2\t2\tup to Yesterday at 4:45 PM",
		"3\t3\tup to Mar 28 at 8:15 AM",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected startup recent picker lines %q, got %q", want, got)
	}
}

func TestFormatFinderModifiedLabelUsesWeekdayAndYearWhenNeeded(t *testing.T) {
	now := time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC)

	got := formatFinderModifiedLabel(now, time.Date(2026, time.April, 7, 10, 30, 0, 0, time.UTC))
	want := "Tuesday at 10:30 AM"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}

	got = formatFinderModifiedLabel(now, time.Date(2025, time.December, 18, 18, 5, 0, 0, time.UTC))
	want = "Dec 18, 2025 at 6:05 PM"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
