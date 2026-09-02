package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestBuildStartupSinkPickerContextReusesRetainedSingleScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	args := []string{"src", "--only", "*.go"}
	if _, err := resolvedCurrentScopeViewForArgs([]string{"src"}); err != nil {
		t.Fatalf("retain base scope: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "src", "late.go"), []byte("package late\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reused, err := buildStartupSinkPickerContext(args)
	if err != nil {
		t.Fatalf("build retained sink context: %v", err)
	}
	if got, want := reused.Plan.DistinctRelPaths(), []string{"src/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sink rediscovered files instead of using retained scope: got %v, want %v", got, want)
	}
	if len(reused.Discovery.ScopeStats) != 1 || reused.Discovery.ScopeStats[0].Count != 1 {
		t.Fatalf("retained scope stats = %+v", reused.Discovery.ScopeStats)
	}

	// With no complete retained state, the same builder remains usable by
	// non-interactive callers and takes the canonical discovery fallback.
	scopeViewMemoReset()
	fresh, err := buildStartupSinkPickerContext(args)
	if err != nil {
		t.Fatalf("build canonical sink context: %v", err)
	}
	if got, want := fresh.Plan.DistinctRelPaths(), []string{"src/a.go", "src/late.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical fallback paths = %v, want %v", got, want)
	}
}

func TestBuildStartupSinkPickerContextPreservesOutputShapesAfterRetainedFilters(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/keep.txt":        "header\nKEEP selected\nmiddle\ntail\n",
		"src/skip.txt":        "header\nKEEP SKIP\ntail\n",
		"src/drop.txt":        "header\nKEEP excluded\ntail\n",
		"src/nested/deep.txt": "header\nKEEP too deep\ntail\n",
		"src/other.go":        "package other\n// KEEP wrong extension\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/keep.txt", "header changed\nKEEP selected\nmiddle\ntail\n")
	t.Chdir(project)
	defer scopeViewMemoReset()

	// This is deliberately a mixed filter chain: path include/exclude,
	// metadata ordering, depth, positive content, and negative content all run
	// before the output shape. The retained route must carry the exact same
	// bytes and plan tags as a cold canonical evaluation.
	baseArgs := []string{
		"src",
		"--no-ignore",
		"--only", "*.txt",
		"--exclude", "*drop*",
		"--size",
		"--recent",
		"--depth", "1",
		"--contains", "KEEP",
		"--not-contains", "SKIP",
	}
	retainedPrefixes := [][]string{
		{"src"},
		{"src", "--no-ignore"},
		{"src", "--no-ignore", "--only", "*.txt"},
		{"src", "--no-ignore", "--only", "*.txt", "--exclude", "*drop*"},
		{"src", "--no-ignore", "--only", "*.txt", "--exclude", "*drop*", "--size"},
		{"src", "--no-ignore", "--only", "*.txt", "--exclude", "*drop*", "--size", "--recent"},
		{"src", "--no-ignore", "--only", "*.txt", "--exclude", "*drop*", "--size", "--recent", "--depth", "1"},
		{"src", "--no-ignore", "--only", "*.txt", "--exclude", "*drop*", "--size", "--recent", "--depth", "1", "--contains", "KEEP"},
		baseArgs,
	}
	tests := []struct {
		name   string
		output []string
	}{
		{name: "full"},
		{name: "paths", output: []string{"--paths"}},
		{name: "lines_range", output: []string{"--lines", "2", "3"}},
		{name: "lines_range_raw", output: []string{"--lines", "2", "3", "--raw"}},
		{name: "snippet_block", output: []string{"--snippet", "KEEP"}},
		{name: "snippet_numeric", output: []string{"--snippet", "KEEP", "0"}},
		{name: "changed_diff", output: []string{"--changed-diff"}},
	}

	renderPayload := func(t *testing.T, ctx StartupSinkPickerContext) string {
		t.Helper()
		var payload bytes.Buffer
		if err := output.WriteOutputPlanPayloadWithoutPrefetch(&payload, ctx.Emit, ctx.Plan); err != nil {
			t.Fatalf("render output payload: %v", err)
		}
		return payload.String()
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scopeViewMemoReset()
			var parent resolvedScopeView
			for _, prefix := range retainedPrefixes {
				var err error
				parent, err = resolvedCurrentScopeViewForArgs(prefix)
				if err != nil {
					t.Fatalf("retain filter prefix %v: %v", prefix, err)
				}
			}
			// The lines picker captures metadata before adding its output stage.
			// Exercise that real handoff shape for every mode so metadata overlay
			// cannot hide or overwrite mode-specific fields.
			if _, ok := retainedScopeViewEntriesWithMetadata(parent); !ok {
				t.Fatal("filtered parent did not expose retained metadata inventory")
			}

			finalArgs := append(append([]string(nil), baseArgs...), tc.output...)
			retained, err := buildStartupSinkPickerContext(finalArgs)
			if err != nil {
				t.Fatalf("build retained context: %v", err)
			}
			retainedPayload := renderPayload(t, retained)
			retainedTags := retained.Plan.PreviewModeTags(nil)

			scopeViewMemoReset()
			canonical, err := buildStartupSinkPickerContext(finalArgs)
			if err != nil {
				t.Fatalf("build canonical context: %v", err)
			}
			canonicalPayload := renderPayload(t, canonical)
			canonicalTags := canonical.Plan.PreviewModeTags(nil)

			if retainedPayload != canonicalPayload {
				t.Fatalf("retained output differs from canonical\nretained:\n%s\ncanonical:\n%s", retainedPayload, canonicalPayload)
			}
			if !reflect.DeepEqual(retainedTags, canonicalTags) {
				t.Fatalf("retained plan tags differ from canonical: retained=%v canonical=%v", retainedTags, canonicalTags)
			}
			if got, want := retained.Plan.DistinctRelPaths(), canonical.Plan.DistinctRelPaths(); !reflect.DeepEqual(got, want) {
				t.Fatalf("retained paths differ from canonical: got=%v want=%v", got, want)
			}
		})
	}
}

func TestBuildStartupSinkPickerContextPreservesOutputShapeThroughTrailingFilter(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/keep.txt":  "header\nKEEP selected\ntail\n",
		"src/other.txt": "header\nKEEP other\ntail\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/keep.txt", "header changed\nKEEP selected\ntail\n")
	t.Chdir(project)
	defer scopeViewMemoReset()

	tests := []struct {
		name     string
		parent   []string
		trailing []string
	}{
		{name: "snippet_then_only", parent: []string{"src", "--snippet", "KEEP"}, trailing: []string{"--only", "src/keep.txt"}},
		{name: "snippet_numeric_then_exclude", parent: []string{"src", "--snippet", "KEEP", "0"}, trailing: []string{"--exclude", "other.txt"}},
		{name: "snippet_then_size", parent: []string{"src", "--snippet", "KEEP"}, trailing: []string{"--size"}},
		{name: "snippet_then_recent", parent: []string{"src", "--snippet", "KEEP"}, trailing: []string{"--recent"}},
		{name: "snippet_then_depth", parent: []string{"src", "--snippet", "KEEP"}, trailing: []string{"--depth", "1"}},
		{name: "changed_diff_then_only", parent: []string{"src", "--changed-diff"}, trailing: []string{"--only", "src/keep.txt"}},
		{name: "changed_diff_then_size", parent: []string{"src", "--changed-diff"}, trailing: []string{"--size"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scopeViewMemoReset()
			if _, err := resolvedCurrentScopeViewForArgs(tc.parent); err != nil {
				t.Fatalf("retain output-shaped parent: %v", err)
			}
			finalArgs := append(append([]string(nil), tc.parent...), tc.trailing...)
			retained, err := buildStartupSinkPickerContext(finalArgs)
			if err != nil {
				t.Fatalf("build retained context: %v", err)
			}
			var retainedPayload bytes.Buffer
			if err := output.WriteOutputPlanPayloadWithoutPrefetch(&retainedPayload, retained.Emit, retained.Plan); err != nil {
				t.Fatal(err)
			}

			scopeViewMemoReset()
			canonical, err := buildStartupSinkPickerContext(finalArgs)
			if err != nil {
				t.Fatalf("build canonical context: %v", err)
			}
			var canonicalPayload bytes.Buffer
			if err := output.WriteOutputPlanPayloadWithoutPrefetch(&canonicalPayload, canonical.Emit, canonical.Plan); err != nil {
				t.Fatal(err)
			}

			if retainedPayload.String() != canonicalPayload.String() {
				t.Fatalf("trailing filter lost output data\nretained:\n%s\ncanonical:\n%s", retainedPayload.String(), canonicalPayload.String())
			}
			if got, want := retained.Plan.PreviewModeTags(nil), canonical.Plan.PreviewModeTags(nil); !reflect.DeepEqual(got, want) {
				t.Fatalf("trailing filter changed output tags: got=%v want=%v", got, want)
			}
		})
	}
}

func TestBuildStartupSinkPickerContextReusesEveryThenScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go":   "package a\n",
		"docs/a.md":  "# A\n",
		"docs/b.txt": "B\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	firstArgs := []string{"src"}
	secondScopeArgs := []string{"src", "--then", "docs"}
	finalArgs := []string{"src", "--then", "docs", "--only", "*.md"}
	if _, err := resolvedCurrentScopeViewForArgs(firstArgs); err != nil {
		t.Fatalf("retain first scope: %v", err)
	}
	if _, err := resolvedCurrentScopeViewForArgs(secondScopeArgs); err != nil {
		t.Fatalf("retain unfiltered second scope: %v", err)
	}

	ctx, err := buildStartupSinkPickerContext(finalArgs)
	if err != nil {
		t.Fatalf("build retained multi-scope sink context: %v", err)
	}
	if got, want := ctx.Plan.DistinctRelPaths(), []string{"docs/a.md", "src/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-scope retained plan = %v, want %v", got, want)
	}
	if got := len(ctx.Discovery.Invocation.Scopes); got != 2 {
		t.Fatalf("retained discovery has %d scopes, want 2", got)
	}
	if got := len(ctx.Discovery.ScopeStats); got != 2 {
		t.Fatalf("retained discovery has %d scope stats, want 2", got)
	}
	if !reflect.DeepEqual(ctx.Discovery.Invocation.Scopes[0].Scope.Targets, []string{"src"}) ||
		!reflect.DeepEqual(ctx.Discovery.Invocation.Scopes[1].Scope.Targets, []string{"docs"}) {
		t.Fatalf("retained scope order changed: %+v", ctx.Discovery.Invocation.Scopes)
	}
}

func TestBuildStartupSinkPickerContextPreservesOverlappingScopeOutputShapes(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/shared.txt": "first\nALPHA middle\nBETA last\n",
		"src/beta.txt":   "first\nBETA only\nlast\n",
	})
	t.Chdir(project)
	defer scopeViewMemoReset()

	tests := []struct {
		name     string
		prefixes [][]string
		final    []string
		wantLen  int
	}{
		{
			name: "distinct line ranges coexist",
			prefixes: [][]string{
				{"src/shared.txt", "--lines", "1", "1"},
				{"src/shared.txt", "--lines", "1", "1", "--then", "src/shared.txt"},
			},
			final:   []string{"src/shared.txt", "--lines", "1", "1", "--then", "src/shared.txt", "--lines", "3", "3"},
			wantLen: 2,
		},
		{
			name: "path and snippet sections both survive",
			prefixes: [][]string{
				{"src/shared.txt", "--paths"},
				{"src/shared.txt", "--paths", "--then", "src/shared.txt"},
			},
			final:   []string{"src/shared.txt", "--paths", "--then", "src/shared.txt", "--snippet", "ALPHA", "0"},
			wantLen: 2,
		},
		{
			name: "overlapping snippet scopes retain unique matches",
			prefixes: [][]string{
				{"src/shared.txt", "--snippet", "ALPHA", "0"},
				{"src/shared.txt", "--snippet", "ALPHA", "0", "--then", "src"},
			},
			final:   []string{"src/shared.txt", "--snippet", "ALPHA", "0", "--then", "src", "--snippet", "BETA", "0"},
			wantLen: 2,
		},
	}

	renderPayload := func(t *testing.T, ctx StartupSinkPickerContext) string {
		t.Helper()
		var payload bytes.Buffer
		if err := output.WriteOutputPlanPayloadWithoutPrefetch(&payload, ctx.Emit, ctx.Plan); err != nil {
			t.Fatal(err)
		}
		return payload.String()
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scopeViewMemoReset()
			for _, prefix := range tc.prefixes {
				if _, err := resolvedCurrentScopeViewForArgs(prefix); err != nil {
					t.Fatalf("retain prefix %v: %v", prefix, err)
				}
			}
			if _, err := resolvedCurrentScopeViewForArgs(tc.final); err != nil {
				t.Fatalf("retain final scope: %v", err)
			}
			retained, err := buildStartupSinkPickerContext(tc.final)
			if err != nil {
				t.Fatalf("retained context: %v", err)
			}
			retainedPayload := renderPayload(t, retained)

			scopeViewMemoReset()
			canonical, err := buildStartupSinkPickerContext(tc.final)
			if err != nil {
				t.Fatalf("canonical context: %v", err)
			}
			canonicalPayload := renderPayload(t, canonical)

			if retainedPayload != canonicalPayload {
				t.Fatalf("overlapping retained scopes changed payload\nretained:\n%s\ncanonical:\n%s", retainedPayload, canonicalPayload)
			}
			if retained.Plan.Len() != tc.wantLen || canonical.Plan.Len() != tc.wantLen {
				t.Fatalf("plan lengths: retained=%d canonical=%d want=%d", retained.Plan.Len(), canonical.Plan.Len(), tc.wantLen)
			}
			if got, want := retained.Plan.PreviewModeTags(nil), canonical.Plan.PreviewModeTags(nil); !reflect.DeepEqual(got, want) {
				t.Fatalf("overlapping scope mode tags differ: got=%v want=%v", got, want)
			}
		})
	}
}

func TestRetainedSinkDiscoveryPreservesDerivedDiagnostics(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
	})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()

	baseArgs := []string{"src"}
	args := []string{"src", "--only", "*/"}
	if _, err := resolvedCurrentScopeViewForArgs(baseArgs); err != nil {
		t.Fatalf("retain base scope: %v", err)
	}
	if _, err := resolvedCurrentScopeViewForArgs(args); err != nil {
		t.Fatalf("derive empty scope: %v", err)
	}

	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		t.Fatal(err)
	}
	resolved := command.ResolvedFromParsed(cfg)
	retained, _, ok := scopeViewMemoDiscoveryResult(resolved)
	if !ok {
		t.Fatal("expected complete retained discovery")
	}
	canonical, err := discovery.DiscoverInvocation(resolved, git.Detect(project), io.Discard, platform.ActivePalette())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retained.Diagnostics, canonical.Diagnostics) {
		t.Fatalf("retained diagnostics differ from canonical:\nretained=%+v\ncanonical=%+v", retained.Diagnostics, canonical.Diagnostics)
	}
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

func TestMaybeResolveStartupSinkPickerContainsDestinationsOnly(t *testing.T) {
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
printf '%s\n' "$input" | grep -F "metadata only" >/dev/null && {
	echo "metadata must be a payload selected in extras, not an output destination" >&2
	exit 91
}
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

	for _, rawArg := range []string{"-p", "--print", "--headless", "--no-bundle", "--no", "-q", "--quiet"} {
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
			if result.PreparedOutput == nil {
				t.Fatal("expected retained prepared output when only the sink picker is skipped")
			}
			if got, want := result.PreparedOutput.Plan.DistinctRelPaths(), []string{"src/main.go"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("prepared paths = %v, want %v", got, want)
			}
		})
	}
}

func TestSinkPickerGateUsesParsedMeaningOfFlagLikeValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "no emission", args: []string{"src", "--no"}, want: true},
		{name: "no used as contains value", args: []string{"src", "--contains", "--no"}, want: false},
		{name: "yes used as contains value", args: []string{"src", "--contains", "--yes"}, want: false},
		{name: "stdout", args: []string{"src", "--print"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rawArgsSkipOutputSinkPicker(tc.args)
			if got != tc.want {
				t.Fatalf("rawArgsSkipOutputSinkPicker(%q) = %v, want %v", tc.args, got, tc.want)
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

func TestTargetPickerCommitPreventsOutputPickerRediscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.go": "package b\n",
	})
	_ = parseInProject(t, project, []string{"."})
	laterPath := filepath.Join(project, "src", "later.go")
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
	printf 'package later\n' > %[1]q
	printf '%%s\n' "$input" | awk -F '\t' '$2 == "."' | head -n 1
	;;
"output> ")
	printf '%%s\n' "$input" | awk -F '\t' '$2 == "stdout"' | head -n 1
	;;
*)
	echo "unexpected prompt: $prompt" >&2
	exit 91
	;;
esac
`, laterPath))

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolveStartupPickerResultWithUndo(resolver, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.PreparedOutput == nil {
		t.Fatal("expected output picker to retain prepared output")
	}
	got := result.PreparedOutput.Plan.DistinctRelPaths()
	if slices.Contains(got, "src/later.go") || !slices.Contains(got, "src/a.go") || !slices.Contains(got, "src/b.go") {
		t.Fatalf("output picker membership = %v, want original rows and no later-created path", got)
	}
}

func TestBareMetadataRunsTargetAndDestinationPickers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{"src/main.go": "package main\n"})
	_ = parseInProject(t, project, []string{"."})
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
"select> ") printf '%s\n' "$input" | awk -F '\t' '$2 == "src"' | head -n 1 ;;
"output> ")
	printf '%s\n' "$input" | grep -F "metadata only" >/dev/null && exit 91
	printf '%s\n' "$input" | awk -F '\t' '$2 == "stdout"' | head -n 1
	;;
*) exit 91 ;;
esac
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolveStartupPickerResultWithUndo(resolver, []string{"--metadata"})
	if err != nil {
		t.Fatalf("bare metadata interactive flow: %v", err)
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--metadata\n-p"; got != want {
		t.Fatalf("resolved args = %q, want %q", got, want)
	}
	if result.PreparedOutput == nil || result.PreparedOutput.Metadata == nil {
		t.Fatal("metadata report was not retained through the output picker")
	}
	var payload bytes.Buffer
	if err := WriteMetadataReport(&payload, result.PreparedOutput.Metadata); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.String(), "src/main.go") {
		t.Fatalf("metadata payload lost selected file:\n%s", payload.String())
	}
}

func TestTargetPickerCommitWithExplicitSinkCarriesPreparedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	project := setupTestProject(t, map[string]string{
		"src/a.go": "package a\n",
		"src/b.go": "package b\n",
	})
	_ = parseInProject(t, project, []string{"."})
	laterPath := filepath.Join(project, "src", "later.go")
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
	printf 'package later\n' > %[1]q
	printf '%%s\n' "$input" | awk -F '\t' '$2 == "."' | head -n 1
	;;
*)
	echo "unexpected prompt: $prompt" >&2
	exit 91
	;;
esac
`, laterPath))

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolveStartupPickerResultWithUndo(resolver, []string{"--print"})
	if err != nil {
		t.Fatal(err)
	}
	if result.PreparedOutput == nil {
		t.Fatal("explicit sink lost the retained prepared output")
	}
	got := result.PreparedOutput.Plan.DistinctRelPaths()
	if slices.Contains(got, "src/later.go") || !slices.Contains(got, "src/a.go") || !slices.Contains(got, "src/b.go") {
		t.Fatalf("explicit-sink membership = %v, want original rows and no later-created path", got)
	}
}

func TestInteractiveThenExactTargetSealsEveryScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows")
	}

	tests := []struct {
		name      string
		files     map[string]string
		second    string
		wantPaths []string
	}{
		{
			name: "visible exact target projects retained inventory",
			files: map[string]string{
				"src/a.go":  "package a\n",
				"docs/b.md": "# B\n",
			},
			second:    "docs",
			wantPaths: []string{"docs/b.md", "src/a.go"},
		},
		{
			name: "exact ignored target seals authorized generation",
			files: map[string]string{
				".gitignore":         "ignored/\n",
				"src/a.go":           "package a\n",
				"ignored/private.go": "package private\n",
			},
			second:    "ignored",
			wantPaths: []string{"ignored/private.go", "src/a.go"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project := setupTestProject(t, tc.files)
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
case "$prompt" in
"select> ")
	printf '%s\n' "$input" | awk -F '\t' '$2 == "src"' | head -n 1
	;;
*)
	echo "unexpected prompt: $prompt" >&2
	exit 91
	;;
esac
`)

			resolver, err := newStartupPickerResolver()
			if err != nil {
				t.Fatal(err)
			}
			result, err := resolveStartupPickerResultWithUndo(resolver, []string{"source-query", "--then", tc.second, "--print"})
			if err != nil {
				t.Fatal(err)
			}
			if result.PreparedOutput == nil {
				t.Fatal("multi-scope explicit sink lost prepared output")
			}
			if got := result.PreparedOutput.Plan.DistinctRelPaths(); !reflect.DeepEqual(got, tc.wantPaths) {
				t.Fatalf("multi-scope membership = %v, want %v", got, tc.wantPaths)
			}
			if got := len(result.PreparedOutput.Discovery.Invocation.Scopes); got != 2 {
				t.Fatalf("prepared discovery has %d scopes, want 2", got)
			}
		})
	}
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
		Render: RenderConfig{ForceTreeMetadata: true},
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

func TestMeasureStartupSinkPayloadUsesMetadataBytes(t *testing.T) {
	report := &MetadataReport{
		Root:   "project",
		Scopes: []MetadataScope{{Summary: "src"}},
		Rows:   []MetadataRow{{Path: "src/main.go", Size: "13.00B", Tokens: "~3", Git: "-", Modified: "Today"}},
	}
	measurement := measureStartupSinkPayload(StartupSinkPickerContext{
		Config:   command.Parsed{PayloadKind: command.PayloadMetadata},
		Metadata: report,
	})
	if measurement.Err != nil {
		t.Fatalf("measureStartupSinkPayload: %v", measurement.Err)
	}
	wantBytes, err := report.EncodedSize()
	if err != nil {
		t.Fatal(err)
	}
	if measurement.Bytes != wantBytes {
		t.Fatalf("measurement = %#v, want bytes %d", measurement, wantBytes)
	}
	if measurement.PreviewReady || len(measurement.OutputPreview.Body) != 0 {
		t.Fatalf("metadata measurement eagerly rendered the picker preview: %#v", measurement)
	}
}

func TestRunInternalSinkPreviewWaitsForAsyncArtifact(t *testing.T) {
	dir := t.TempDir()
	modePath := filepath.Join(dir, "mode")
	outputPath := filepath.Join(dir, "output.txt")
	treePath := filepath.Join(dir, "tree.txt")
	if err := os.WriteFile(modePath, []byte("output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath+".pending", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(25 * time.Millisecond)
		_ = os.WriteFile(outputPath, []byte("ready\n"), 0o600)
		_ = os.Remove(outputPath + ".pending")
	}()
	var stdout bytes.Buffer
	if err := RunInternalSinkPreview(modePath, outputPath, treePath, &stdout); err != nil {
		t.Fatalf("RunInternalSinkPreview: %v", err)
	}
	<-done
	if got, want := stdout.String(), "ready\n"; got != want {
		t.Fatalf("preview = %q, want %q", got, want)
	}
}

func TestPrepareStartupSinkPreviewFilesReusesMeasuredOutput(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n\nfunc main() {}\n",
	})
	plan := testSinkOutputPlan(t, project, "src/main.go")
	report, err := output.BuildReportForPlan(git.Context{}, plan, output.ReportOptions{IncludeTreeMetadata: true})
	if err != nil {
		t.Fatal(err)
	}
	measurement := measureOutputForSinkMenu(plan, output.EmitConfig{})
	if measurement.Err != nil || len(measurement.OutputPreview.Body) == 0 {
		t.Fatalf("measurement did not retain its output preview: %#v", measurement)
	}
	if err := os.Remove(filepath.Join(project, "src", "main.go")); err != nil {
		t.Fatal(err)
	}

	files, err := prepareStartupSinkPreviewFiles(StartupSinkPickerContext{
		Emit:   output.EmitConfig{},
		Render: RenderConfig{ForceTreeMetadata: true},
		Plan:   plan,
		Report: report,
	}, &measurement.OutputPreview)
	if err != nil {
		t.Fatalf("preview setup re-read output after measurement: %v", err)
	}
	files.Cleanup()
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
