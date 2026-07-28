package ui

// Tests white-box assertions on UI internals (resolver helpers,
// picker headers, modifier-menu state, etc.). Moved here in commit
// 3B from root main_test.go so the assertions don't force ui exports
// just for test reach-in. The integration tests that drive Main()
// stay at root in main_test.go.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

func TestPickerHeadersUseFourLinesMax60Chars(t *testing.T) {
	headers := map[string]string{
		"safe":         discovery.SafeTargetPickerHeader(),
		"ignored":      discovery.IgnoredTargetPickerHeader(),
		"contains":     discovery.ContentMatchPickerHeader("--contains"),
		"snippet":      discovery.ContentMatchPickerHeader("--snippet"),
		"snippet-mode": snippetBoundaryPickerHeader(),
		"modifier":     startupModifierPickerHeader(),
		"extras":       startupExtrasPickerHeader(),
		"only":         startupFileSetPickerHeader("--only"),
		"exclude":      startupFileSetPickerHeader("--exclude"),
	}
	for name, header := range headers {
		lines := strings.Split(header, "\n")
		if got, want := len(lines), 4; got != want {
			t.Fatalf("expected %s header to use %d lines, got %d: %q", name, want, got, header)
		}
		for _, line := range lines {
			if len(line) > 60 {
				t.Fatalf("expected %s header line to fit 60 chars, got %d: %q", name, len(line), line)
			}
		}
	}
}
func TestPickerHeadersCanShowEscExitAndUndo(t *testing.T) {
	headers := map[string]string{
		"target":         discovery.TargetPickerHeaderWithEscHint("select> ", "undo"),
		"ignored":        discovery.IgnoredTargetPickerHeaderWithEscHint("undo"),
		"contains":       discovery.ContentMatchPickerHeaderWithEscHint("--contains", "undo"),
		"snippet-mode":   snippetBoundaryPickerHeaderWithEscHint("undo"),
		"modifier":       startupModifierPickerHeaderWithEscHint("undo"),
		"extras":         startupExtrasPickerHeaderWithEscHint("undo"),
		"only":           startupFileSetPickerHeaderWithEscHint("--only", "undo"),
		"depth":          depthPickerHeaderWithEscHint("undo"),
		"recent":         recentPickerHeaderWithEscHint("undo"),
		"lines-start":    linesPickerStartHeaderWithEscHint("undo"),
		"lines-end":      linesPickerEndHeaderWithEscHint("undo"),
		"output-sink":    startupSinkPickerHeaderWithEscHint("undo"),
		"output-default": startupSinkPickerHeader(),
	}

	for name, header := range headers {
		if name == "output-default" {
			if !strings.Contains(header, "[Esc] exit") {
				t.Fatalf("expected %s header to keep exit by default, got %q", name, header)
			}
			continue
		}
		if !strings.Contains(header, "[Esc] undo") {
			t.Fatalf("expected %s header to show Esc undo, got %q", name, header)
		}
		if strings.Contains(header, "[Esc] exit") {
			t.Fatalf("expected %s header not to also show Esc exit, got %q", name, header)
		}
	}
}
func TestTargetMatchLabelsShowIgnoredSourceTemporarily(t *testing.T) {
	labels, index := discovery.TargetMatchLabels([]discovery.TargetMatch{
		{Path: "src/components", Kind: "dir", State: treeTargetStateOK},
		{Path: "node_modules", Kind: "dir", State: TreeTargetStateNoTextChildren, Ignored: true, IgnoreSource: ".hiss"},
		{Path: "coverage-final.json", Kind: "file", State: TreeTargetStateText, Ignored: true, IgnoreSource: ".gitignore"},
	})

	want := []string{
		"[dir] src/components\tsrc/components\tdir\tok",
		"[ignored dir .hiss] node_modules\tnode_modules\tdir\tno_text_children",
		"[ignored file .gitignore] coverage-final.json\tcoverage-final.json\tfile\ttext",
	}
	if strings.Join(labels, "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected labels %v, got %v", want, labels)
	}
	if got := index["node_modules"]; got.Path != "node_modules" || !got.Ignored {
		t.Fatalf("expected ignored dir path key to resolve back to the match, got %#v", got)
	}
}
func TestResolveStartupOnlyUsesCheckpointPreviewCommand(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"src/skip.test":  "test\n",
		"shared/util.ts": "console.log('shared')\n",
	})
	_ = parseInProject(t, project, []string{"src", "shared"})
	installScriptFzf(t, `#!/bin/sh
prompt=""
preview=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
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

if [ "$prompt" = "only> " ]; then
	printf '%s\n' "$preview" | grep -F -- '--internal-tree-preview --internal-prediscovered' >/dev/null || {
		echo "preview command did not use prediscovered checkpoint: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- ' src ' >/dev/null && {
		echo "preview command leaked typed target src: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- ' shared ' >/dev/null && {
		echo "preview command leaked typed target shared: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- '--only {+2}' >/dev/null || {
		echo "preview command missing --only stage: $preview" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F "shared/util.ts" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"src", "shared"}, "--only", "only> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\nshared\n--only\nshared/util.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupModifierMenuOnlyUsesCheckpointPreviewCommand(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":  "console.log('src')\n",
		"src/other.ts": "console.log('other')\n",
		"docs/read.md": "# docs\n",
	})
	_ = parseInProject(t, project, []string{"src"})
	installScriptFzf(t, `#!/bin/sh
prompt=""
preview=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
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

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F $'\tonly' | head -n 1
	exit 0
fi

if [ "$prompt" = "only> " ]; then
	printf '%s\n' "$preview" | grep -F -- '--internal-tree-preview --internal-prediscovered' >/dev/null || {
		echo "preview command did not use prediscovered checkpoint: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- ' src ' >/dev/null && {
		echo "preview command leaked typed target src: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- '--only {+2}' >/dev/null || {
		echo "preview command missing --only stage: $preview" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F "src/main.ts" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}
	args, _, err := resolveStartupModifierArgs(resolver, []string{"src"}, []string{"src"}, []string{"src"})
	if err != nil {
		t.Fatalf("resolveStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--only\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupExcludeUsesCheckpointPreviewCommand(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":   "console.log('src')\n",
		"src/skip.test": "test\n",
	})
	_ = parseInProject(t, project, []string{"src"})
	installScriptFzf(t, `#!/bin/sh
prompt=""
preview=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
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

if [ "$prompt" = "exclude> " ]; then
	printf '%s\n' "$preview" | grep -F -- '--internal-tree-preview --internal-prediscovered' >/dev/null || {
		echo "preview command did not use prediscovered checkpoint: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- ' src ' >/dev/null && {
		echo "preview command leaked typed target src: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- '--exclude {+2}' >/dev/null || {
		echo "preview command missing --exclude stage: $preview" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F "skip.test" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"src"}, "--exclude", "exclude> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--exclude\nsrc/skip.test"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestStartupFileSetPreviewCommandKeepsDiffPreviewAfterDiffModeChosen(t *testing.T) {
	command := startupFileSetPreviewCommand([]string{"cmd", "--include", "Formula", "--changed-diff"}, "--only", false)
	if !strings.Contains(command, "--internal-file-preview") {
		t.Fatalf("expected --only after diff mode to keep diff preview, got %q", command)
	}
	if !strings.Contains(command, "--changed-diff") {
		t.Fatalf("expected --only after diff mode to inherit current diff scope, got %q", command)
	}
}
func TestStartupFileSetPreviewCommandUsesOnlyRefinementForGitSelectors(t *testing.T) {
	command := startupFileSetPreviewCommand([]string{"cmd", "--changed"}, "--changed", false)
	if !strings.Contains(command, "--only {+2}") {
		t.Fatalf("expected git file-set preview to refine with --only, got %q", command)
	}
	if strings.Contains(command, "--changed {+2}") {
		t.Fatalf("git file-set preview appended value to --changed instead of --only: %q", command)
	}
}
func TestResolveStartupGitScopeArgsUsesCheckpointPreviewCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('main')\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/main.ts", "console.log('changed')\n")
	writeProjectFile(t, project, "src/new.ts", "console.log('new')\n")
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
preview=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
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

if [ "$prompt" = "changed> " ]; then
	printf '%s\n' "$preview" | grep -F -- '--internal-tree-preview --internal-prediscovered' >/dev/null || {
		echo "preview command did not use prediscovered checkpoint: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- '--only {+2}' >/dev/null || {
		echo "preview command did not lower git picker preview to --only: $preview" >&2
		exit 91
	}
	if printf '%s\n' "$preview" | grep -F -- '--changed {+2}' >/dev/null; then
		echo "preview command appended row selection to --changed: $preview" >&2
		exit 91
	fi
	printf '%s\n' "$input" | grep -F "src/main.ts" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupGitScopeArgs(resolver, []string{"--changed"}, "changed> ", nil, true, false)
	if err != nil {
		t.Fatalf("resolveStartupGitScopeArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--changed\n--only\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestStartupModifierCurrentScopePreviewCommandUsesCheckpointHandoff(t *testing.T) {
	limit := 5
	tmpRoot := t.TempDir()
	// Use a real entry pointing at a real file so WriteCheckpoint's size stat
	// can complete; the modifier-menu preview command needs Entries to write
	// a usable checkpoint, otherwise it returns ("","").
	entryPath := filepath.Join(tmpRoot, "src", "main.ts")
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(entryPath, []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	view := resolvedScopeView{
		Invocation: command.Invocation{WorkingDir: tmpRoot},
		Entries: []discovery.Entry{{
			AbsPath:    entryPath,
			RelPath:    "src/main.ts",
			TargetRoot: "src",
		}},
	}
	state := startupCurrentScopeState{
		Known: true,
		Scopes: []command.ExecutionScope{
			{
				Targets: []string{"src"},
				Stages:  []command.Stage{{Kind: command.StageOnly, Values: []string{"*.ts"}}},
			},
			{
				Targets: []string{"docs"},
				Stages:  []command.Stage{{Kind: command.StageRecent, Limit: &limit}},
			},
		},
	}
	cmd, tmpdir := startupModifierCurrentScopePreviewCommand(state, view)
	if tmpdir != "" {
		defer os.RemoveAll(tmpdir)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	// The preview command MUST contain --internal-prediscovered so the child
	// reads the precomputed checkpoint instead of repeating discovery (the
	// v0.6.0 Windows-latency Finding 1 regression — see
	// docs/versions/v0.6.1/reports/ACTIVE_NOTE_windows_interactive_latency_findings.md).
	if !strings.HasPrefix(cmd, discovery.ShellQuoteArg(self)+" --quiet --internal-tree-preview --internal-prediscovered ") {
		t.Fatalf("expected modifier preview to use --internal-prediscovered checkpoint hand-off, got %q", cmd)
	}
	// The current-scope tail still appears so the child renders the right
	// scope from the checkpoint.
	if !strings.Contains(cmd, " docs --recent 5") {
		t.Fatalf("expected modifier preview to carry the current scope tail (docs --recent 5), got %q", cmd)
	}
	// Earlier scopes must not bleed into the preview (the menu is for the
	// current scope only).
	if strings.Contains(cmd, " src --only '*.ts'") {
		t.Fatalf("expected modifier preview to exclude earlier scopes, got %q", cmd)
	}
	if strings.Contains(cmd, "catclip-tree") || strings.Contains(cmd, "|") {
		t.Fatalf("expected modifier preview command to avoid catclip-tree pipe, got %q", cmd)
	}
	// The checkpoint file must actually exist on disk (callers rely on this
	// to clean up).
	if tmpdir == "" {
		t.Fatalf("expected non-empty tmpdir for cleanup, got %q", tmpdir)
	}
	if _, err := os.Stat(filepath.Join(tmpdir, "scope.json")); err != nil {
		t.Fatalf("expected scope.json checkpoint to exist in tmpdir, stat error: %v", err)
	}
}

func TestStartupModifierCurrentScopePreviewCommandSkipsWhenViewIsEmpty(t *testing.T) {
	// If the resolved view has no entries (either the scope is empty or the
	// view resolution race-stale'd), the modifier-menu preview should
	// return ("", "") instead of writing a checkpoint with no entries.
	state := startupCurrentScopeState{
		Known: true,
		Scopes: []command.ExecutionScope{
			{Targets: []string{"src"}},
		},
	}
	cmd, tmpdir := startupModifierCurrentScopePreviewCommand(state, resolvedScopeView{})
	if cmd != "" || tmpdir != "" {
		t.Fatalf("expected empty preview when view has no entries, got cmd=%q tmpdir=%q", cmd, tmpdir)
	}
}
func TestAllIgnoredTargetsIncludesIgnoredEntries(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts":                  "export const app = true\n",
		"node_modules/react/index.js": "module.exports = {}\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "."})

	resolver := discovery.Resolver{
		Cfg:               invocationConfigFromParsedCommand(cfg),
		AllowFileSymlinks: false,
	}

	targets, err := resolver.AllIgnoredTargets(nil)
	if err != nil {
		t.Fatalf("allIgnoredTargets returned error: %v", err)
	}

	lookup := make(map[string]discovery.TargetMatch, len(targets))
	for _, target := range targets {
		lookup[target.Path] = target
	}

	if _, ok := lookup["src/app.ts"]; ok {
		t.Fatalf("expected safe file to stay out of ignored target index, got %#v", lookup["src/app.ts"])
	}
	if got, ok := lookup["node_modules"]; !ok || !got.Ignored || got.IgnoreSource != ".hiss" || got.Kind != "dir" {
		t.Fatalf("expected node_modules dir to appear as ignored .hiss entry, got %#v (present=%v)", got, ok)
	}
	if got := lookup["node_modules"].State; got != treeTargetStateOK {
		t.Fatalf("expected ignored dir with text descendants to be marked ok, got %q", got)
	}
	if got, ok := lookup["node_modules/react/index.js"]; !ok || !got.Ignored || got.IgnoreSource != ".hiss" || got.Kind != "file" {
		t.Fatalf("expected ignored file inside node_modules to appear in the temporary picker, got %#v (present=%v)", got, ok)
	}
	if got := lookup["node_modules/react/index.js"].State; got != TreeTargetStateText {
		t.Fatalf("expected ignored text file to be marked text, got %q", got)
	}
}
func TestAllIgnoredTargetsTracksNoTextDirectoryState(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":       "blocked-empty/\nblocked-binary/\n",
		"src/app.ts":       "export const app = true\n",
		"blocked-binary/a": "\x00\x01\x02",
	})
	if err := os.MkdirAll(filepath.Join(project, "blocked-empty"), 0o755); err != nil {
		t.Fatalf("MkdirAll blocked-empty: %v", err)
	}
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "."})

	resolver := discovery.Resolver{
		Cfg:               invocationConfigFromParsedCommand(cfg),
		GitCtx:            git.Detect(project),
		AllowFileSymlinks: false,
	}

	targets, err := resolver.AllIgnoredTargets(nil)
	if err != nil {
		t.Fatalf("allIgnoredTargets returned error: %v", err)
	}

	lookup := make(map[string]discovery.TargetMatch, len(targets))
	for _, target := range targets {
		lookup[target.Path] = target
	}

	if got, ok := lookup["blocked-empty"]; ok {
		t.Fatalf("expected truly empty directory to be invisible (rg emits no files for it), got %#v", got)
	}
	if got, ok := lookup["blocked-binary"]; !ok || got.State != TreeTargetStateNoTextChildren {
		t.Fatalf("expected blocked-binary to be marked no_text_children, got %#v (present=%v)", got, ok)
	}
}
func TestAllowedByIncludeDirectoryLabelColorsEntireIncludedSubtree(t *testing.T) {
	entry := discovery.Entry{
		RelPath:          "node_modules/.cache/babel-loader/abc123.json",
		TargetRoot:       "node_modules",
		AllowedByInclude: true,
	}

	for _, relDir := range []string{
		"node_modules",
		"node_modules/.cache",
		"node_modules/.cache/babel-loader",
	} {
		if !allowedByIncludeDirectoryLabel(entry, relDir) {
			t.Fatalf("expected %q to inherit include coloring", relDir)
		}
	}
}
func TestTargetMatchArgsBatchesIgnoredOnlySelection(t *testing.T) {
	got := targetMatchArgs([]discovery.TargetMatch{
		{Path: "node_modules", Ignored: true},
		{Path: "coverage", Ignored: true},
	})
	want := []string{"--include", "node_modules", "coverage"}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("unexpected ignored-only target args: got %q want %q", got, want)
	}
}
func TestStartupResolvedTargetPathsSupportsMultiValueInclude(t *testing.T) {
	got := startupResolvedTargetPaths([]string{"src", "--include", "node_modules", "coverage"})
	want := []string{"src", "node_modules", "coverage"}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("unexpected startup resolved target paths: got %q want %q", got, want)
	}
}
func TestResolveStartupScopeInputsNoArgsOpensCopyAllPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installFakeFzf(t)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, targets, _, usedPicker, err := resolveStartupScopeInputs(resolver, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("resolveStartupScopeInputs returned error: %v", err)
	}
	if !usedPicker {
		t.Fatal("expected bare catclip to use the startup picker")
	}
	if got, want := strings.Join(args, "\n"), "."; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
	if got, want := strings.Join(targets, "\n"), "."; got != want {
		t.Fatalf("expected resolved targets %q, got %q", want, got)
	}
}
func TestStartupCommandCanRunDirectlyForUniqueBasenameFile(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"ui/index.html": "<!doctype html>\n",
		"src/main.ts":   "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"index.html"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if !direct {
		t.Fatal("expected unique basename file target to bypass startup fzf")
	}
}
func TestStartupCommandCanRunDirectlyRejectsExplicitIncludeQueries(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"dummy-react-project/package.json":      "{}\n",
		"dummy-react-project/node_modules/a.js": "console.log('a')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"--include", "node_modules"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if direct {
		t.Fatal("expected explicit --include query to stay on startup resolution path")
	}
}
func TestResolveStartupArgsRejectsInvalidIncludeValue(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveStartupArgs(resolver, []string{".", "--include", "src/../vendor"})
	if err == nil {
		t.Fatal("expected invalid startup include value to fail")
	}
	if !strings.Contains(err.Error(), "--include cannot traverse above the current directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestStartupCommandCanRunDirectlyAllowsExactTargetEqualsIncludeIgnoredDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"ignored/common.ts": "export const ok = true\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"ignored", "--include", "ignored"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if !direct {
		t.Fatal("expected exact ignored target==include command to bypass startup fzf")
	}
}
func TestStartupCommandCanRunDirectlyAllowsDescendantOfIncludedIgnoredDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":                "ignored/\n",
		"ignored/deep/path/main.ts": "export const ok = true\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"ignored/deep/path", "--include", "ignored"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if !direct {
		t.Fatal("expected included ignored directory ancestor to bypass startup fzf for descendant target")
	}
}
func TestStartupCommandCanRunDirectlyAllowsExactTargetEqualsIncludeIgnoredFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"ignored/secret.ts": "console.log('ignored')\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"ignored/secret.ts", "--include", "ignored/secret.ts"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if !direct {
		t.Fatal("expected exact ignored file target==include command to bypass startup fzf")
	}
}
func TestStartupCommandCanRunDirectlyRejectsNonExactOnlyQuery(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
		"src/util.ts": "console.log('util')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{".", "--only", "uti"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if direct {
		t.Fatal("expected non-exact --only query to stay on startup resolution path")
	}
}
func TestStartupCommandCanRunDirectlyRejectsNonExactExcludeQuery(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
		"src/util.ts": "console.log('util')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{".", "--exclude", "mai"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if direct {
		t.Fatal("expected non-exact --exclude query to stay on startup resolution path")
	}
}
func TestResolveStartupArgsSkipsCoveredLaterTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/components/ui/Button.tsx":             "export function Button() {}\n",
		"src/features/profile/components/Card.tsx": "export function Card() {}\n",
		"src/shared/components/Badge.tsx":          "export function Badge() {}\n",
		"src/index.js":                             "console.log('ok')\n",
		"docs/components-guide.md":                 "# guide\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
filter=""
query=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--filter)
			filter="$2"
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
value="$filter"
if [ -z "$value" ]; then
	value="$query"
fi

if [ -z "$value" ]; then
	printf '%s\n' "$input"
	exit 0
fi

printf '%s\n' "$input" | grep -F "$value" || exit 1
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, targets, usedPicker, err := resolveStartupArgs(resolver, []string{"src", "components", "--only", "*.js"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if usedPicker {
		t.Fatal("expected covered later target to avoid opening fzf")
	}
	if got, want := strings.Join(args, "\n"), "src\n--only\n*.js"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
	if got, want := strings.Join(targets, "\n"), "src"; got != want {
		t.Fatalf("expected resolved targets %q, got %q", want, got)
	}
}
func TestResolveStartupScopeInputsEmptyIncludeQueryOpensIgnoredPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"node_modules/pkg/index.js": "export const x = 1\n",
		"src/main.ts":               "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
query=""
prompt=""
header=""
print_query=0
while [ "$#" -gt 0 ]; do
	case "$1" in
		--print-query)
			print_query=1
			shift
			;;
		--header)
			header="$2"
			shift 2
			;;
		--query)
			query="$2"
			shift 2
			;;
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

if [ "$prompt" = "include> " ] && [ -z "$query" ]; then
	printf '%s\n' "$header" | grep -F "Add files and folders ignored by .gitignore or .hiss." >/dev/null || {
		echo "missing include header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Type to search by name." >/dev/null || {
		echo "missing include enter help" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F "[ignored dir .hiss] node_modules" | head -n 1
	exit 0
fi

printf '%s\n' "$input" | head -n 1
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, targets, _, usedPicker, err := resolveStartupScopeInputs(resolver, nil, []string{""}, nil, nil)
	if err != nil {
		t.Fatalf("resolveStartupScopeInputs returned error: %v", err)
	}
	if !usedPicker {
		t.Fatal("expected the synthetic empty include query to use the ignored picker")
	}
	if got, want := strings.Join(args, "\n"), "--include\nnode_modules"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
	if got, want := strings.Join(targets, "\n"), "node_modules"; got != want {
		t.Fatalf("expected resolved targets %q, got %q", want, got)
	}
}
func TestResolveStartupScopeInputsExcludePreviouslySelectedTargets(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
query=""
prompt=""
print_query=0
while [ "$#" -gt 0 ]; do
	case "$1" in
		--print-query)
			print_query=1
			shift
			;;
		--query)
			query="$2"
			shift 2
			;;
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

emit_query() {
	if [ "$print_query" -eq 1 ]; then
		printf '%s\n' "$query"
	fi
}

if [ "$prompt" = "select> " ] && [ "$query" = "sr" ]; then
	emit_query
	printf '%s\n' "$input" | grep -F "[dir] src" | head -n 1
	exit 0
fi

if [ "$prompt" = "select> " ] && [ "$query" = "s" ]; then
	if printf '%s\n' "$input" | grep -E '\tsrc($|/)' >/dev/null; then
		echo "src subtree leaked into second picker" >&2
		exit 91
	fi
	emit_query
	printf '%s\n' "$input" | grep -F "[dir] shared" | head -n 1
	exit 0
fi

emit_query
printf '%s\n' "$input" | head -n 1
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, targets, _, usedPicker, err := resolveStartupScopeInputs(resolver, []string{"sr", "s"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("resolveStartupScopeInputs returned error: %v", err)
	}
	if !usedPicker {
		t.Fatal("expected shorthand startup flow to use the picker")
	}
	if got, want := strings.Join(args, "\n"), "src\nshared"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
	if got, want := strings.Join(targets, "\n"), "src\nshared"; got != want {
		t.Fatalf("expected resolved targets %q, got %q", want, got)
	}
}
func TestResolveStartupScopeInputsBatchesAdjacentIncludeSelections(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"config/catclip/.hiss":      "node_modules/\ncoverage/\n",
		"node_modules/pkg/index.js": "export const x = 1\n",
		"coverage/lcov.info":        "TN:\n",
		"src/main.ts":               "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
query=""
prompt=""
filter=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--filter)
			filter="$2"
			shift 2
			;;
		--query)
			query="$2"
			shift 2
			;;
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

if [ -n "$filter" ]; then
	printf '%s\n' "$input" | grep -i -F "$filter"
	exit 0
fi

if [ "$prompt" = "include> " ]; then
	case "$query" in
		node)
			printf '%s\n' "$input" | grep -F "[ignored dir .hiss] node_modules" | head -n 1
			exit 0
			;;
		cov)
			printf '%s\n' "$input" | grep -F "[ignored dir .hiss] coverage" | head -n 1
			exit 0
			;;
	esac
fi

echo "unexpected prompt/query: $prompt / $query" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, targets, _, usedPicker, err := resolveStartupScopeInputs(resolver, nil, []string{"node", "cov"}, nil, nil)
	if err != nil {
		t.Fatalf("resolveStartupScopeInputs returned error: %v", err)
	}
	if !usedPicker {
		t.Fatal("expected ignored-target resolution to use the picker")
	}
	if got, want := strings.Join(args, "\n"), "--include\nnode_modules\ncoverage"; got != want {
		t.Fatalf("expected batched include args %q, got %q", want, got)
	}
	if got, want := strings.Join(targets, "\n"), "node_modules\ncoverage"; got != want {
		t.Fatalf("expected resolved targets %q, got %q", want, got)
	}
}
func TestResolveStartupArgsIncludePickerHidesAuthorizationOnlyAncestorForExplicitDescendantTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"config/catclip/.hiss":            "vendor/\n",
		"src/vendor/lib/util.ts":          "export const util = true\n",
		"src/vendor/lib/internal/deep.ts": "export const deep = true\n",
		"src/vendor/extras/bonus.ts":      "export const bonus = true\n",
		"src/main.ts":                     "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
query=""
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--query)
			query="$2"
			shift 2
			;;
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

if [ "$prompt" = "include> " ] && [ "$query" = "ext" ]; then
	if printf '%s\n' "$input" | grep -F "src/vendor	" >/dev/null; then
		echo "authorization-only ancestor unexpectedly shown in include picker" >&2
		exit 91
	fi
	printf '%s\n' "$input" | grep -F "src/vendor/extras" | head -n 1
	exit 0
fi

echo "unexpected prompt/query: $prompt / $query" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	// Scope target is src/vendor/lib, include query is "ext" for src/vendor/extras.
	// src/vendor is an authorization-only ancestor and should be hidden.
	// src/vendor/extras is a sibling under the same ignored tree — but with
	// scoping, it's outside scope target src/vendor/lib and should not appear.
	// This means selection is cancelled (no options in scope).
	_, _, _, err = resolveStartupArgs(resolver, []string{"src/vendor/lib", "--include", "ext"})
	if err == nil {
		t.Fatal("expected no include options in scope for src/vendor/lib with query ext")
	}
}
func TestResolveStartupArgsIncludeErrorsWhenNoScopedIgnoredTargets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":             "vendor/\n",
		"cmd/catclip/main.go":    "package main\n",
		"vendor/lodash/index.js": "module.exports = {}\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	// "cmd" has no ignored targets under it, so --include should error.
	_, _, _, err = resolveStartupArgs(resolver, []string{"cmd", "--include", "a"})
	if err == nil {
		t.Fatal("expected error when no ignored targets under scope target")
	}
	var noScoped discovery.ErrNoScopedIgnoredTargets
	if !errors.As(err, &noScoped) {
		t.Fatalf("expected discovery.ErrNoScopedIgnoredTargets, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cmd") {
		t.Fatalf("expected error to mention scope target, got: %v", err)
	}
}
func TestResolveModifierMenuIncludeReusesIgnoredPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"node_modules/pkg/index.js": "export const x = 1\n",
		"src/main.ts":               "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
bindings=""
header=""
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
		--bind)
			bindings="$bindings
$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F -- "--include" | head -n 1
	exit 0
fi

if [ "$prompt" = "include> " ]; then
	printf '%s\n' "$input" | grep -F "[ignored dir .hiss] node_modules" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveBareStartupModifierArgs(resolver)
	if err != nil {
		t.Fatalf("resolveBareStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--include\nnode_modules"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveBareStartupModifierArgsChangedDoesNotOpenSecondPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
bindings=""
header=""
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
		--bind)
			bindings="$bindings
$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F $'\tchanged' | head -n 1
	exit 0
fi

echo "unexpected second picker: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveBareStartupModifierArgs(resolver)
	if err != nil {
		t.Fatalf("resolveBareStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--changed"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveInteractiveStartupArgsFinishEarlyClearsPendingModifiers(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
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

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F $'\tfinish' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, []string{"src", "--", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected fzf to be used")
	}
	if got, want := strings.Join(args, "\n"), "src"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveInteractiveStartupArgsExtrasCanSelectMultipleFlags(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	stateFile := filepath.Join(t.TempDir(), "fzf-count")
	installScriptFzf(t, fmt.Sprintf(`#!/bin/sh
prompt=""
multi=0
bindings=""
no_sort=0
nth=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		--multi)
			multi=1
			shift
			;;
		--bind)
			bindings="$bindings
$2"
			shift 2
			;;
		--no-sort)
			no_sort=1
			shift
			;;
		--nth)
			nth="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	count="$(cat %[1]q 2>/dev/null || printf '0')"
	count=$((count + 1))
	printf '%%s\n' "$count" > %[1]q
	if [ "$count" -eq 1 ]; then
		printf '%%s\n' "$input" | grep -F $'\textras' | head -n 1
		exit 0
	fi
	printf '%%s\n' "$input" | grep -F $'\tfinish' | head -n 1
	exit 0
fi

if [ "$prompt" = "extras> " ]; then
	[ "$multi" -eq 1 ] || {
		echo "expected extras picker to enable multi-select" >&2
		exit 91
	}
	[ "$no_sort" -eq 1 ] || {
		echo "expected extras picker to disable sorting" >&2
		exit 91
	}
	[ "$nth" = "1" ] || {
		echo "expected extras picker to search only the label column, got nth=$nth" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F %[2]q >/dev/null || {
		echo "expected extras picker to bind multi-select toggle-all" >&2
		exit 91
	}
	printf '%%s\n' "$input" | grep -F -- "--no-bundle" >/dev/null && {
		echo "extras picker should not offer --no-bundle; output picker owns it" >&2
		exit 91
	}
	printf '%%s\n' "$input" | grep -F -- "--preview" >/dev/null && {
		echo "extras picker should not offer --preview; output picker owns it" >&2
		exit 91
	}
	printf '%%s\n' "$input" | grep -F $'\traw' | head -n 1
	printf '%%s\n' "$input" | grep -F $'\tquiet' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, stateFile, platform.MultiSelectToggleAllBinding()))

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, []string{"src", "--", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected fzf to be used")
	}
	if got, want := strings.Join(args, "\n"), "src\n--raw\n--quiet"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveBareStartupModifierArgsChangedDiffBuildsCombinedArgs(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
bindings=""
header=""
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
		--bind)
			bindings="$bindings
$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F -- "--changed-diff" | head -n 1
	exit 0
fi

echo "unexpected second picker: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveBareStartupModifierArgs(resolver)
	if err != nil {
		t.Fatalf("resolveBareStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--changed-diff"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveBareStartupModifierArgsChangedInGitRepoOpensFilePicker(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('main')\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/main.ts", "console.log('changed')\n")
	writeProjectFile(t, project, "src/new.ts", "console.log('new')\n")
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
header=""
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
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F $'\tchanged' | head -n 1
	exit 0
fi

if [ "$prompt" = "changed> " ]; then
	printf '%s\n' "$header" | grep -F "Pick git-changed files." >/dev/null || {
		echo "missing changed header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Type a path to narrow the list." >/dev/null || {
		echo "missing changed enter help" >&2
		exit 91
	}
	if ! printf '%s\n' "$input" | grep -F "[all changed files]" >/dev/null; then
		echo "expected changed picker to include all-files row" >&2
		exit 91
	fi
	if ! printf '%s\n' "$input" | grep -F "src/main.ts" >/dev/null; then
		echo "expected changed picker to include src/main.ts" >&2
		exit 91
	fi
	if ! printf '%s\n' "$input" | grep -F "src/new.ts" >/dev/null; then
		echo "expected changed picker to include src/new.ts" >&2
		exit 91
	fi
	printf '%s\n' "$input" | grep -F "src/main.ts" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveBareStartupModifierArgs(resolver)
	if err != nil {
		t.Fatalf("resolveBareStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--changed\n--only\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupGitScopeArgsAllRowKeepsPlainChanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('main')\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/main.ts", "console.log('changed')\n")
	writeProjectFile(t, project, "src/new.ts", "console.log('new')\n")
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

if [ "$prompt" = "changed> " ]; then
	printf '%s\n' "$input" | grep -F "[all changed files]" | head -n 1
	printf '%s\n' "$input" | grep -F "src/main.ts" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupGitScopeArgs(resolver, []string{"--changed"}, "changed> ", nil, true, false)
	if err != nil {
		t.Fatalf("resolveStartupGitScopeArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--changed"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveBareStartupModifierArgsChangedDiffInGitRepoKeepsDiffAfterPicker(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('main')\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/main.ts", "console.log('changed')\n")
	writeProjectFile(t, project, "src/new.ts", "console.log('new')\n")
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
header=""
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
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F $'\tchanged-diff' | head -n 1
	exit 0
fi

if [ "$prompt" = "changed> " ]; then
	printf '%s\n' "$header" | grep -F "Pick diffs for git-changed files." >/dev/null || {
		echo "missing changed header" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F "src/main.ts" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveBareStartupModifierArgs(resolver)
	if err != nil {
		t.Fatalf("resolveBareStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--changed-diff\n--only\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestMaybeResolveStartupPickerArgsTrailingModifierMenuAfterResolvedTargets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/main.ts", "console.log('changed')\n")
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
header=""
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
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F $'\tchanged' | head -n 1
	exit 0
fi

if [ "$prompt" = "changed> " ]; then
	printf '%s\n' "$input" | grep -F "[all changed files]" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupModifierArgs(resolver, []string{"src", "shared"}, []string{"src", "shared"}, []string{"src", "shared"})
	if err != nil {
		t.Fatalf("resolveStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\nshared\n--changed"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupModifierArgsReturnsThenModifier(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
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

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F $'\tthen' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupModifierArgs(resolver, []string{"src"}, []string{"src"}, []string{"src"})
	if err != nil {
		t.Fatalf("resolveStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--then"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestStartupAvailableModifierChoicesHideContainsAndGitRowsWhenScopeHasDiff(t *testing.T) {
	choices := startupAvailableModifierChoicesWithState(
		[]string{"src", "--changed-diff"},
		startupCurrentScopeState{},
	)

	for _, key := range []string{"contains", "snippet", "paths", "changed", "staged", "unstaged", "untracked", "changed-diff", "staged-diff", "unstaged-diff"} {
		if startupModifierChoiceKeysContain(choices, key) {
			t.Fatalf("%s should be hidden when current scope already has --diff: %#v", key, startupModifierChoiceKeys(choices))
		}
	}
	for _, key := range []string{"only", "exclude", "recent", "depth", "then"} {
		if !startupModifierChoiceKeysContain(choices, key) {
			t.Fatalf("%s should remain available when current scope already has --diff: %#v", key, startupModifierChoiceKeys(choices))
		}
	}
}
func TestStartupAvailableModifierChoicesEmptyKnownScopeShowsNoChoices(t *testing.T) {
	choices := startupAvailableModifierChoicesWithState(
		[]string{"src", "--changed-diff"},
		startupCurrentScopeState{Known: true, Empty: true},
	)
	if len(choices) != 0 {
		t.Fatalf("expected no choices for known empty scope, got %#v", startupModifierChoiceKeys(choices))
	}
}
func TestStartupAvailableModifierChoicesHideDiffModesWhenScopeHasSnippet(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: fix\n",
		"src/util.ts": "TODO: keep\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/main.ts", "TODO: changed\n")
	_ = parseInProject(t, project, []string{"."})

	choices := startupAvailableModifierChoices([]string{"src", "--snippet", "TODO"})

	for _, key := range []string{"contains", "snippet", "paths", "changed-diff", "staged-diff", "unstaged-diff"} {
		if startupModifierChoiceKeysContain(choices, key) {
			t.Fatalf("%s should be hidden when current scope already has --snippet: %#v", key, startupModifierChoiceKeys(choices))
		}
	}
	if !startupModifierChoiceKeysContain(choices, "changed") {
		t.Fatalf("plain changed should remain available when current scope has --snippet: %#v", startupModifierChoiceKeys(choices))
	}
}
func TestStartupAvailableModifierChoicesHideGitRowsWhenScopeIsNotGitBacked(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	choices := startupAvailableModifierChoices([]string{"."})

	for _, key := range []string{"changed", "staged", "unstaged", "untracked", "changed-diff", "staged-diff", "unstaged-diff"} {
		if startupModifierChoiceKeysContain(choices, key) {
			t.Fatalf("%s should be hidden when current scope is not git-backed: %#v", key, startupModifierChoiceKeys(choices))
		}
	}
	// --include is hidden because this project has no ignored targets at all.
	if startupModifierChoiceKeysContain(choices, "include") {
		t.Fatalf("include should be hidden when there are no ignored targets: %#v", startupModifierChoiceKeys(choices))
	}
	for _, key := range []string{"contains", "snippet", "only", "exclude", "recent", "depth", "paths", "then"} {
		if !startupModifierChoiceKeysContain(choices, key) {
			t.Fatalf("%s should remain available when current scope is not git-backed: %#v", key, startupModifierChoiceKeys(choices))
		}
	}
}
func TestStartupAvailableModifierChoicesUseCurrentScopeGitState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"staged.txt":   "one\n",
		"unstaged.txt": "one\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "staged.txt", "two\n")
	runGit(t, project, "add", "staged.txt")
	writeProjectFile(t, project, "unstaged.txt", "two\n")
	writeProjectFile(t, project, "new.txt", "brand new\n")
	_ = parseInProject(t, project, []string{"."})

	choices := startupAvailableModifierChoices([]string{".", "--unstaged"})

	for _, key := range []string{"changed", "staged", "unstaged", "untracked", "changed-diff", "staged-diff"} {
		if startupModifierChoiceKeysContain(choices, key) {
			t.Fatalf("%s should be hidden for current unstaged-only scope: %#v", key, startupModifierChoiceKeys(choices))
		}
	}
	for _, key := range []string{"unstaged-diff", "contains", "snippet", "only", "exclude", "recent", "paths", "then", "depth"} {
		if !startupModifierChoiceKeysContain(choices, key) {
			t.Fatalf("%s should remain available for current unstaged-only scope: %#v", key, startupModifierChoiceKeys(choices))
		}
	}
}
func TestStartupAvailableModifierChoicesHideSameScopeModifiersAfterPaths(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	choices := startupAvailableModifierChoices([]string{"src", "--paths"})
	if !startupModifierChoiceKeysContain(choices, "then") {
		t.Fatalf("expected --then to remain available after --paths: %#v", startupModifierChoiceKeys(choices))
	}
	for _, key := range []string{"only", "exclude", "recent", "depth", "paths", "contains", "snippet", "include"} {
		if startupModifierChoiceKeysContain(choices, key) {
			t.Fatalf("%s should be hidden after terminal --paths: %#v", key, startupModifierChoiceKeys(choices))
		}
	}
}
func TestResolveStartupArgsRejectsSnippetAfterDiffInSameScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveStartupArgs(resolver, []string{"src", "--changed-diff", "--snippet", "TODO"})
	if err == nil {
		t.Fatal("expected same-scope --diff --snippet conflict error")
	}
	if !strings.Contains(err.Error(), "--snippet and --diff cannot be combined") {
		t.Fatalf("expected diff/snippet conflict error, got %v", err)
	}
}
func TestResolveStartupArgsRejectsContainsAfterDiffInSameScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveStartupArgs(resolver, []string{"src", "--changed-diff", "--contains", "TODO"})
	if err == nil {
		t.Fatal("expected same-scope --contains after --diff error")
	}
	if !strings.Contains(err.Error(), "--contains must come before --changed-diff in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestResolveStartupArgsRejectsGitFilterAfterDiffInSameScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveStartupArgs(resolver, []string{"src", "--changed-diff", "--staged"})
	if err == nil {
		t.Fatal("expected same-scope git filter after --diff error")
	}
	if !strings.Contains(err.Error(), "--staged must come before --changed-diff in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestResolveInteractiveStartupArgsEmptyCurrentScopeStopsWithNoFilesMessage(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not run for empty current scope" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveInteractiveStartupArgs(resolver, []string{".", "--exclude", "*", "--"})
	if err == nil {
		t.Fatal("expected empty current scope to stop with no-files-found error")
	}
	if !strings.Contains(err.Error(), "No text files found matching your criteria.") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestResolveInteractiveStartupArgsIgnoredExplicitTargetAllowsIncludeModifier(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"config/catclip/.hiss":                        "docs/\n",
		"docs/versions/v0.4.0/ACTIVE_NOTE_version.md": "version\n",
		"docs/policy/ACTIVE_NOTE_report_format.md":    "policy\n",
		"src/main.ts": "console.log('src')\n",
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

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F -- "--include" | head -n 1
	exit 0
fi

if [ "$prompt" = "include> " ]; then
	if printf '%s\n' "$input" | grep -F "[ignored dir .hiss] docs	docs	dir" >/dev/null; then
		echo "ancestor docs unexpectedly shown in include picker" >&2
		exit 91
	fi
	if printf '%s\n' "$input" | grep -F "[ignored dir .hiss] docs/versions	docs/versions	dir" >/dev/null; then
		echo "ancestor docs/versions unexpectedly shown in include picker" >&2
		exit 91
	fi
	printf '%s\n' "$input" | grep -F "[ignored dir .hiss] docs/versions/v0.4.0" | head -n 1
	exit 0
fi

echo "unexpected prompt/query: $prompt / $query" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, []string{"docs/versions/v0.4.0", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected ignored explicit target modifier flow to use fzf")
	}
	if got, want := strings.Join(args, "\n"), "docs/versions/v0.4.0\n--include\ndocs/versions/v0.4.0"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveInteractiveStartupArgsEscFromStageReopensModifierMenu(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
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

case "$prompt" in
	"filter> ")
		count=0
		if [ -f %[1]q ]; then
			count="$(cat %[1]q)"
		fi
		count=$((count + 1))
		printf '%%s' "$count" > %[1]q
		case "$count" in
			1)
				printf '%%s\n' 'only'
				;;
			2)
				printf '%%s\n' 'paths'
				;;
			*)
				echo "unexpected filter count: $count" >&2
				exit 91
				;;
		esac
		;;
	"only> ")
		exit 130
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

	args, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, []string{"src", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected undo flow to use fzf")
	}
	if got, want := strings.Join(args, "\n"), "src\n--paths"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveInteractiveStartupArgsEscFromThenTargetUndoesThenChoice(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"shared/util.ts": "console.log('shared')\n",
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

case "$prompt" in
	"filter> ")
		count=0
		if [ -f %[1]q ]; then
			count="$(cat %[1]q)"
		fi
		count=$((count + 1))
		printf '%%s' "$count" > %[1]q
		case "$count" in
			1)
				printf '%%s\n' 'then'
				;;
			2)
				printf '%%s\n' 'paths'
				;;
			*)
				echo "unexpected filter count: $count" >&2
				exit 91
				;;
		esac
		;;
	"then> ")
		exit 130
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

	args, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, []string{"src", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected undo flow to use fzf")
	}
	if got, want := strings.Join(args, "\n"), "src\n--paths"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveInteractiveStartupArgsEscOnFirstWindowExits(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
exit 130
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveInteractiveStartupArgs(resolver, nil)
	if !errors.Is(err, discovery.ErrSelectionCancelled) {
		t.Fatalf("expected first-window Esc to cancel invocation, got %v", err)
	}
}
func TestStartupCommandCanRunDirectlyRejectsTrailingModifierMenu(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"--"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if direct {
		t.Fatal("expected trailing modifier menu sentinel not to be treated as direct")
	}
}
func TestStartupCommandCanRunDirectlyRejectsLeadingChangedWithoutExplicitTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"--changed"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if direct {
		t.Fatal("expected leading --changed without explicit target not to be treated as direct")
	}
}
func TestStartupCommandCanRunDirectlyRejectsLeadingRecentLimitWithoutExplicitTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"--recent", "5"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if direct {
		t.Fatal("expected leading --recent 5 without explicit target not to be treated as direct")
	}
}
func TestStartupCommandCanRunDirectlyRejectsBarePreviewWithoutExplicitTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"--preview"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if direct {
		t.Fatal("expected bare --preview without explicit target not to be treated as direct")
	}
}
func TestStartupCommandCanRunDirectlyRejectsBareThenWithoutExplicitTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	direct, err := startupCommandCanRunDirectly(resolver, []string{"--then"})
	if err != nil {
		t.Fatalf("startupCommandCanRunDirectly returned error: %v", err)
	}
	if direct {
		t.Fatal("expected bare --then without explicit targets not to be treated as direct")
	}
}
func TestMaybeResolveStartupPickerArgsBareModifierMenuPicksTargetsFirst(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"shared/util.ts": "console.log('shared')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	stateFile := filepath.Join(t.TempDir(), "picker-order")
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

if [ "$prompt" = "select> " ]; then
	printf 'pick\n' > %q
	printf '%%s\n' "$input" | grep -F "[dir] src" | head -n 1
	exit 0
fi

if [ "$prompt" = "filter> " ]; then
	[ -f %q ] || {
		echo "modifier picker opened before target picker" >&2
		exit 91
	}
	printf '%%s\n' "$input" | grep -F $'\tchanged' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, stateFile, stateFile))

	result, handled, err := maybeResolveStartupPickerArgs([]string{"--"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected bare modifier menu to be handled by startup picker")
	}
	if !result.UsedFzf {
		t.Fatal("expected bare modifier menu flow to use fzf")
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--changed"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestMaybeResolveStartupPickerArgsHeadlessRecentSkipsStartupPicker(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not be called in headless startup" >&2
exit 91
`)

	result, handled, err := maybeResolveStartupPickerArgs([]string{"-q", "-p", "--recent"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if handled {
		t.Fatalf("expected headless recent command to bypass startup picker, got %#v", result)
	}
}
func TestMaybeResolveStartupPickerArgsHeadlessExactIncludeSkipsStartupPicker(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"ignored/common.ts": "export const ok = true\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not be called in headless include startup" >&2
exit 91
`)

	result, handled, err := maybeResolveStartupPickerArgs([]string{"-q", "-p", "ignored", "--include", "ignored"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if handled {
		t.Fatalf("expected exact headless include command to bypass startup picker, got %#v", result)
	}
}
func TestMaybeResolveStartupPickerArgsStdinModifierSkipsStartupPicker(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not be called for stdin modifier values" >&2
exit 91
`)

	result, handled, err := maybeResolveStartupPickerArgs([]string{"src", "--exclude", "-"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if handled {
		t.Fatalf("expected stdin modifier command to bypass startup picker, got %#v", result)
	}
}
func TestResolveStartupArgsKeepsExactIncludeWhenLaterOnlyNeedsResolution(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"ignored/common.ts": "export const ok = true\n",
		"ignored/other.ts":  "export const other = true\n",
		"src/main.ts":       "console.log('ok')\n",
	})
	initGitRepo(t, project)
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
	"include> ")
		echo "exact include unexpectedly opened include picker" >&2
		exit 91
		;;
	"only> ")
		printf '%s\n' "$input" | grep -F "ignored/common.ts" | head -n 1
		exit 0
		;;
esac

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{"ignored", "--include", "ignored", "--only", "common"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected only-stage resolution to use fzf")
	}
	if got, want := strings.Join(args, "\n"), "ignored\n--include\nignored\n--only\nignored/common.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestMaybeResolveStartupPickerArgsLeadingOnlyRequiresPattern(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	_, handled, err := maybeResolveStartupPickerArgs([]string{"--only"})
	if err == nil {
		t.Fatal("expected leading --only to require a pattern")
	}
	if !handled {
		t.Fatal("expected leading --only error to be handled by startup picker")
	}
	if !strings.Contains(err.Error(), "--only requires a pattern") {
		t.Fatalf("expected --only requires a pattern error, got %v", err)
	}
}
func TestMaybeResolveStartupPickerArgsLeadingRecentLimitPicksTargetsFirst(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"shared/util.ts": "console.log('shared')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	stateFile := filepath.Join(t.TempDir(), "picker-order-recent")
	installScriptFzf(t, fmt.Sprintf(`#!/bin/sh
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

if [ "$prompt" = "select> " ]; then
	printf 'pick\n' > %q
	printf '%%s\n' "$input" | grep -F "[dir] src" | head -n 1
	exit 0
fi

if [ "$prompt" = "recent> " ]; then
	[ -f %q ] || {
		echo "recent picker opened before target picker" >&2
		exit 91
	}
	[ "$query" = "5" ] || {
		echo "expected recent picker query 5, got $query" >&2
		exit 91
	}
	printf '%%s\n' "$input" | grep -F $'5\t5\tup to ' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, stateFile, stateFile))

	result, handled, err := maybeResolveStartupPickerArgs([]string{"--recent", "5"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected leading --recent 5 to be handled by startup picker")
	}
	if !result.UsedFzf {
		t.Fatal("expected leading --recent 5 flow to use fzf")
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--recent\n5"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestMaybeResolveStartupPickerArgsBarePreviewPicksTargetsFirst(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"shared/util.ts": "console.log('shared')\n",
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

if [ "$prompt" = "select> " ]; then
	printf '%s\n' "$input" | grep -F "[dir] src" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	result, handled, err := maybeResolveStartupPickerArgs([]string{"--preview"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected bare --preview to be handled by startup picker")
	}
	if !result.UsedFzf {
		t.Fatal("expected bare --preview flow to use fzf")
	}
	if got, want := strings.Join(result.Args, "\n"), "--preview\nsrc"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestPromptYesNoErrorsWhenHeadlessPromptGuardActive(t *testing.T) {
	restore := PushHeadlessPromptGuard(true)
	defer restore()

	answer, err := PromptYesNo("Are you sure? [y/N]", false, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected headless prompt guard to fail loudly")
	}
	if answer {
		t.Fatalf("expected false answer on error, got %v", answer)
	}
	if !strings.Contains(err.Error(), "BUG: reached interactive prompt in headless mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestMaybeResolveStartupPickerArgsBareGlobalRunFlagsPickTargetsFirst(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "quiet", arg: "-q", want: "src\n-q"},
		{name: "print", arg: "-p", want: "src\n-p"},
		{name: "no-tree", arg: "-t", want: "src\n-t"},
		{name: "yes", arg: "-y", want: "src\n-y"},
		{name: "verbose", arg: "-v", want: "src\n-v"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := setupTestProject(t, map[string]string{
				"src/main.ts":    "console.log('src')\n",
				"shared/util.ts": "console.log('shared')\n",
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

if [ "$prompt" = "select> " ]; then
	printf '%s\n' "$input" | grep -F "[dir] src" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

			result, handled, err := maybeResolveStartupPickerArgs([]string{tt.arg})
			if err != nil {
				t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
			}
			if !handled {
				t.Fatal("expected bare global flag to be handled by startup picker")
			}
			if !result.UsedFzf {
				t.Fatal("expected bare global flag flow to use fzf")
			}
			if got := strings.Join(result.Args, "\n"); got != tt.want {
				t.Fatalf("expected resolved args %q, got %q", tt.want, got)
			}
		})
	}
}
func TestMaybeResolveStartupPickerArgsBareThenPicksBothScopes(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"shared/util.ts": "console.log('shared')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	stateFile := filepath.Join(t.TempDir(), "picker-order-then")
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
count=0
if [ -f %q ]; then
	count="$(cat %q)"
fi

if [ "$prompt" = "select> " ]; then
	case "$count" in
		0)
			printf '1' > %q
			printf '%%s\n' "$input" | grep -F "[dir] src" | head -n 1
			exit 0
			;;
		1)
			printf '2' > %q
			printf '%%s\n' "$input" | grep -F "[dir] shared" | head -n 1
			exit 0
			;;
		*)
			echo "unexpected pick prompt count: $count" >&2
			exit 91
			;;
	esac
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, stateFile, stateFile, stateFile, stateFile))

	result, handled, err := maybeResolveStartupPickerArgs([]string{"--then"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected bare --then to be handled by startup picker")
	}
	if !result.UsedFzf {
		t.Fatal("expected bare --then flow to use fzf")
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--then\nshared"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestMaybeResolveStartupPickerArgsBareThenPreviewPicksBothScopes(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"shared/util.ts": "console.log('shared')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	stateFile := filepath.Join(t.TempDir(), "picker-order-then-preview")
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
count=0
if [ -f %q ]; then
	count="$(cat %q)"
fi

if [ "$prompt" = "select> " ]; then
	case "$count" in
		0)
			printf '1' > %q
			printf '%%s\n' "$input" | grep -F "[dir] src" | head -n 1
			exit 0
			;;
		1)
			printf '2' > %q
			printf '%%s\n' "$input" | grep -F "[dir] shared" | head -n 1
			exit 0
			;;
		*)
			echo "unexpected pick prompt count: $count" >&2
			exit 91
			;;
	esac
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, stateFile, stateFile, stateFile, stateFile))

	result, handled, err := maybeResolveStartupPickerArgs([]string{"--then", "--preview"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected bare --then --preview to be handled by startup picker")
	}
	if !result.UsedFzf {
		t.Fatal("expected bare --then --preview flow to use fzf")
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--then\nshared\n--preview"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestMaybeResolveStartupPickerArgsResolvedThenPreviewPicksSecondScopeFirst(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"shared/util.ts": "console.log('shared')\n",
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

if [ "$prompt" = "select> " ]; then
	printf '%s\n' "$input" | grep -F "[dir] shared" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	result, handled, err := maybeResolveStartupPickerArgs([]string{"src", "--then", "--preview"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected src --then --preview to be handled by startup picker")
	}
	if !result.UsedFzf {
		t.Fatal("expected src --then --preview flow to use fzf")
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--then\nshared\n--preview"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestMaybeResolveStartupPickerArgsDoubleModifierMenuPicksTargetsFirst(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"shared/util.ts": "console.log('shared')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	stateFile := filepath.Join(t.TempDir(), "picker-order-double")
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
count=0
if [ -f %q ]; then
	count="$(cat %q)"
fi

if [ "$prompt" = "select> " ]; then
	printf '1' > %q
	printf '%%s\n' "$input" | grep -F "[dir] src" | head -n 1
	exit 0
fi

if [ "$prompt" = "filter> " ]; then
	case "$count" in
		1)
			printf '2' > %q
			printf '%%s\n' "$input" | grep -F $'\tchanged' | head -n 1
			exit 0
			;;
		2)
			printf '3' > %q
			printf '%%s\n' "$input" | grep -F $'\tchanged-diff' | head -n 1
			exit 0
			;;
		*)
			echo "unexpected modifier prompt count: $count" >&2
			exit 91
			;;
	esac
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, stateFile, stateFile, stateFile, stateFile, stateFile))

	result, handled, err := maybeResolveStartupPickerArgs([]string{"--", "--"})
	if err != nil {
		t.Fatalf("maybeResolveStartupPickerArgs returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected leading modifier menus to be handled by startup picker")
	}
	if !result.UsedFzf {
		t.Fatal("expected leading modifier menus to use fzf")
	}
	if got, want := strings.Join(result.Args, "\n"), "src\n--changed\n--changed-diff"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsRejectsUntrackedDiffInGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	initGitRepo(t, project)
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveStartupArgs(resolver, []string{"--untracked", "--changed-diff"})
	if err == nil {
		t.Fatal("expected startup resolution error for --untracked --diff")
	}
	if !strings.Contains(err.Error(), "--untracked-diff doesn't make sense") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestResolveStartupModifierArgsChangedInGitRepoAfterResolvedTargetsOpensFilePicker(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"src/util.ts":      "console.log('util')\n",
		"shared/clean.ts":  "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/util.ts", "console.log('changed util')\n")
	writeProjectFile(t, project, "src/new.ts", "console.log('new')\n")
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
header=""
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
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' "$input" | grep -F $'\tchanged' | head -n 1
	exit 0
fi

if [ "$prompt" = "changed> " ]; then
	printf '%s\n' "$header" | grep -F "Pick git-changed files." >/dev/null || {
		echo "missing changed header" >&2
		exit 91
	}
	if ! printf '%s\n' "$input" | grep -F "src/util.ts" >/dev/null; then
		echo "expected changed picker to include src/util.ts" >&2
		exit 91
	fi
	if ! printf '%s\n' "$input" | grep -F "src/new.ts" >/dev/null; then
		echo "expected changed picker to include src/new.ts" >&2
		exit 91
	fi
	if printf '%s\n' "$input" | grep -F "shared/clean.ts" >/dev/null; then
		echo "did not expect changed picker to include clean files" >&2
		exit 91
	fi
	printf '%s\n' "$input" | grep -F "src/util.ts" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupModifierArgs(resolver, []string{"src"}, []string{"src"}, []string{"src"})
	if err != nil {
		t.Fatalf("resolveStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--changed\n--only\nsrc/util.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestMaybeResolveStartupPickerArgsTrailingOnlyAfterResolvedTargets(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	expectedBinding := platform.MultiSelectToggleAllBinding()
	installScriptFzf(t, fmt.Sprintf(`#!/bin/sh
prompt=""
bindings=""
header=""
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
		--bind)
			bindings="$bindings
$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "only> " ]; then
	printf '%%s\n' "$bindings" | grep -F -- %q >/dev/null || {
		echo "missing toggle-all binding" >&2
		exit 91
	}
	printf '%%s\n' "$header" | grep -F "Keep only files whose paths match." >/dev/null || {
		echo "missing only header" >&2
		exit 91
	}
	printf '%%s\n' "$header" | grep -F "Type a path pattern." >/dev/null || {
		echo "missing only enter help" >&2
		exit 91
	}
	if ! printf '%%s\n' "$input" | grep -F "src/main.ts" >/dev/null; then
		echo "expected src/main.ts in only picker" >&2
		exit 91
	fi
	if ! printf '%%s\n' "$input" | grep -F "shared/util.ts" >/dev/null; then
		echo "expected shared/util.ts in only picker" >&2
		exit 91
	fi
	printf '%%s\n' "$input" | grep -F "shared/util.ts" | head -n 1
	exit 0
fi

	echo "unexpected prompt: $prompt" >&2
exit 91
`, expectedBinding))

	args, _, err := resolveStartupScopeFileSetArgs([]string{"src", "shared"}, "--only", "only> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\nshared\n--only\nshared/util.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupScopeFileSetArgsOnlyOffersExtensionPatternRows(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('ts')\n",
		"src/button.tsx":   "console.log('tsx')\n",
		"src/reset.css":    "body {}\n",
		"src/readme.md":    "# readme\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
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
	if ! printf '%s\n' "$input" | grep -F $'\t*.css\t' >/dev/null; then
		echo "expected *.css synthetic row in only picker" >&2
		exit 91
	fi
	if ! printf '%s\n' "$input" | grep -F $'\t*.ts\t' >/dev/null; then
		echo "expected *.ts synthetic row in only picker" >&2
		exit 91
	fi
	if ! printf '%s\n' "$input" | grep -F $'\t*.tsx\t' >/dev/null; then
		echo "expected *.tsx synthetic row in only picker" >&2
		exit 91
	fi
	printf '%s\n' "$input" | grep -F $'\t*.ts\t' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"src"}, "--only", "only> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--only\n*.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupScopeFileSetArgsOnlyAllowsSelectingMultipleExtensionRows(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('ts')\n",
		"src/button.tsx": "console.log('tsx')\n",
		"src/reset.css":  "body {}\n",
		"src/readme.md":  "# readme\n",
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
	printf '%s\n' "$input" | grep -F $'\t*.ts\t' | head -n 1
	printf '%s\n' "$input" | grep -F $'\t*.tsx\t' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"src"}, "--only", "only> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--only\n*.ts\n*.tsx"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestMaybeResolveStartupPickerArgsBareExcludeUsesCurrentScopeFileSet(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
header=""
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
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "exclude> " ]; then
	printf '%s\n' "$header" | grep -F "Remove files whose paths match." >/dev/null || {
		echo "missing exclude header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Type a path pattern." >/dev/null || {
		echo "missing exclude enter help" >&2
		exit 91
	}
	if ! printf '%s\n' "$input" | grep -F "src/main.ts" >/dev/null; then
		echo "expected src/main.ts in exclude picker" >&2
		exit 91
	fi
	if ! printf '%s\n' "$input" | grep -F "shared/util.ts" >/dev/null; then
		echo "expected shared/util.ts in exclude picker" >&2
		exit 91
	fi
	printf '%s\n' "$input" | grep -F "src/main.ts" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupTrailingActionArgs(resolver, nil, StartupTrailingActionExclude)
	if err != nil {
		t.Fatalf("resolveStartupTrailingActionArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--exclude\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupScopeFileSetArgsExcludeOffersExtensionPatternRows(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('ts')\n",
		"src/button.tsx":   "console.log('tsx')\n",
		"src/reset.css":    "body {}\n",
		"src/readme.md":    "# readme\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
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

if [ "$prompt" = "exclude> " ]; then
	if ! printf '%s\n' "$input" | grep -F $'\t*.css\t' >/dev/null; then
		echo "expected *.css synthetic row in exclude picker" >&2
		exit 91
	fi
	if ! printf '%s\n' "$input" | grep -F $'\t*.ts\t' >/dev/null; then
		echo "expected *.ts synthetic row in exclude picker" >&2
		exit 91
	fi
	if ! printf '%s\n' "$input" | grep -F $'\t*.tsx\t' >/dev/null; then
		echo "expected *.tsx synthetic row in exclude picker" >&2
		exit 91
	fi
	printf '%s\n' "$input" | grep -F $'\t*.ts\t' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"src"}, "--exclude", "exclude> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--exclude\n*.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupScopeFileSetArgsExcludeAllowsSelectingMultipleExtensionRows(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('ts')\n",
		"src/button.tsx": "console.log('tsx')\n",
		"src/reset.css":  "body {}\n",
		"src/readme.md":  "# readme\n",
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

if [ "$prompt" = "exclude> " ]; then
	printf '%s\n' "$input" | grep -F $'\t*.ts\t' | head -n 1
	printf '%s\n' "$input" | grep -F $'\t*.tsx\t' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"src"}, "--exclude", "exclude> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--exclude\n*.ts\n*.tsx"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsResolvesTargetsBeforeFlags(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
query=""
prompt=""
print_query=0
while [ "$#" -gt 0 ]; do
	case "$1" in
		--print-query)
			print_query=1
			shift
			;;
		--query)
			query="$2"
			shift 2
			;;
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

emit_query() {
	if [ "$print_query" -eq 1 ]; then
		printf '%s\n' "$query"
	fi
}

if [ "$prompt" = "select> " ] && [ "$query" = "sr" ]; then
	emit_query
	printf '%s\n' "$input" | grep -F "[dir] src" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, _, err := resolveStartupArgs(resolver, []string{"sr", "--changed"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--changed"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsOpensOnlyPickerForNonExactValue(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"src/util.ts":      "console.log('util')\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
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

if [ "$prompt" = "only> " ]; then
	[ "$query" = "uti" ] || { echo "unexpected query: $query" >&2; exit 91; }
	printf '%s\n' 'util.ts	src/util.ts	src/util.ts	file	text	file'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{"src", "--only", "uti"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected non-exact --only value to use fzf")
	}
	if got, want := strings.Join(args, "\n"), "src\n--only\nsrc/util.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsOpensExcludePickerForNonExactValue(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"src/util.ts":      "console.log('util')\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
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

if [ "$prompt" = "exclude> " ]; then
	[ "$query" = "mai" ] || { echo "unexpected query: $query" >&2; exit 91; }
	printf '%s\n' 'main.ts	src/main.ts	src/main.ts	file	text	file'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{"--exclude", "mai"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected non-exact --exclude value to use fzf")
	}
	if got, want := strings.Join(args, "\n"), "--exclude\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsKeepsExactOnlyAndExcludePathsLiteral(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"discovery.go":      "package catclip\n",
		"cmd/main.go":       "package main\n",
		"content.go":        "package catclip\n",
		"contains_list.go":  "package catclip\n",
		"internal/render/a": "tree\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not run for exact --only/--exclude paths" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{
		"--only", "discovery.go",
		"--only", "cmd/",
		"--exclude", "internal/render",
		"--only", "content.go",
		"--exclude", "content.go",
		"--only", "contains_list.go",
	})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if usedFzf {
		t.Fatal("expected exact file paths to stay literal without fzf")
	}
	if got, want := strings.Join(args, "\n"), "--only\ndiscovery.go\n--only\ncmd/\n--exclude\ninternal/render\n--only\ncontent.go\n--exclude\ncontent.go\n--only\ncontains_list.go"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestResolveStartupArgsKeepsTargetRelativeSubtreeSelectorLiteral(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"internal/handler/a.go":   "package handler\n",
		"internal/search/find.go": "package search\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not run for a trailing-slash subtree selector" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	for _, flag := range []string{"--only", "--exclude"} {
		t.Run(flag, func(t *testing.T) {
			args, _, usedFzf, err := resolveStartupArgs(resolver, []string{"internal", flag, "handler/"})
			if err != nil {
				t.Fatalf("resolveStartupArgs returned error: %v", err)
			}
			if usedFzf {
				t.Fatal("expected target-relative subtree selector to stay literal without fzf")
			}
			if got, want := strings.Join(args, "\n"), "internal\n"+flag+"\nhandler/"; got != want {
				t.Fatalf("expected resolved args %q, got %q", want, got)
			}
		})
	}
}

func TestResolveStartupArgsThenStartsFreshScopeForTargetResolution(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts":                  "export const a = true\n",
		"src/components/Button.tsx": "export const Button = true\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
query=""
prompt=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--query)
			query="$2"
			shift 2
			;;
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

if [ "$prompt" = "then> " ] && [ "$query" = "Button.tsx" ]; then
	if ! printf '%s\n' "$input" | grep -F "src/components/Button.tsx" >/dev/null; then
		echo "expected Button.tsx to be selectable after --then" >&2
		exit 91
	fi
	printf '%s\n' "$query"
	printf '%s\n' "$input" | grep -F "src/components/Button.tsx" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt / query: $query" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, _, err := resolveStartupArgs(resolver, []string{
		"src",
		"--only", "src/a.ts",
		"--then", "Button.tsx",
	})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--only\nsrc/a.ts\n--then\nsrc/components/Button.tsx"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsAllowsGlobalFlagsAfterExactTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not run for exact target plus global flag" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{".", "-y"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if usedFzf {
		t.Fatal("expected exact target plus -y to bypass fzf")
	}
	if got, want := strings.Join(args, "\n"), ".\n-y"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsAllowsBareGlobalFlagsBeforePickerFlow(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
print_query=0
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		--print-query)
			print_query=1
			shift
			;;
		*)
			shift
			;;
	esac
done

if [ "$prompt" = "select> " ]; then
	first_line="$(head -n 1)"
	if [ "$print_query" -eq 1 ]; then
		printf '\n%s\n' "$first_line"
	else
		printf '%s\n' "$first_line"
	fi
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{"-y"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected bare -y to still go through safe-target picker flow")
	}
	if got, want := strings.Join(args, "\n"), "-y\n."; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}

	args, _, usedFzf, err = resolveStartupArgs(resolver, []string{"--no-bundle"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error for bare --no-bundle: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected bare --no-bundle to go through safe-target picker flow")
	}
	if got, want := strings.Join(args, "\n"), "--no-bundle\n."; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveBareStartupModifierArgsContainsOpensLiveRegexPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/todo.ts": "TODO: wire this up\n",
	})
	_ = parseInProject(t, project, []string{"."})
	expectedBinding := platform.MultiSelectToggleAllBinding()
	expectedKey := platform.MultiSelectToggleAllKey()
	installScriptFzf(t, fmt.Sprintf(`#!/bin/sh
prompt=""
header=""
expect=""
disabled=0
bindings=""
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
		--expect)
			expect="$2"
			shift 2
			;;
		--bind)
			bindings="$bindings
$2"
			shift 2
			;;
		--disabled)
			disabled=1
			shift
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	printf '%%s\n' "$input" | grep -F -- "--contains" | head -n 1
	exit 0
fi

if [ "$prompt" = "match> " ]; then
	[ "$disabled" -eq 1 ] || { echo "contains picker must use --disabled" >&2; exit 91; }
	[ -z "$expect" ] || { echo "unexpected --expect: $expect" >&2; exit 91; }
	printf '%%s\n' "$header" | grep -F "Keep files whose contents match a regex." >/dev/null || {
		echo "missing match header" >&2
		exit 91
	}
	printf '%%s\n' "$header" | grep -F "Type a regex." >/dev/null || {
		echo "missing enter header" >&2
		exit 91
	}
	printf '%%s\n' "$header" | grep -F %q >/dev/null || {
		echo "missing toggle-all header" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- "start:preview<" >/dev/null || {
		echo "missing start searching preview binding" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- "change:preview<" >/dev/null || {
		echo "missing change searching preview binding" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- "+reload<" >/dev/null || {
		echo "missing chained reload binding" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- "--internal-searching-preview" >/dev/null || {
		echo "missing internal searching preview command" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- %q >/dev/null || {
		echo "missing toggle-all binding" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- "--internal-content-match-list" >/dev/null || {
		echo "missing internal content match list command" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- "--internal-prediscovered" >/dev/null || {
		echo "missing prediscovered content match list checkpoint command" >&2
		exit 91
	}
	if printf '%%s\n' "$bindings" | grep -F -- " src " >/dev/null; then
		echo "content match list command leaked typed target src: $bindings" >&2
		exit 91
	fi
	printf 'TODO\n'
	printf 'todo.ts\tsrc/todo.ts\tfile\ttext\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, "["+expectedKey+"] toggle", expectedBinding))

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveBareStartupModifierArgs(resolver)
	if err != nil {
		t.Fatalf("resolveBareStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--contains\nTODO"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveBareStartupModifierArgsSnippetAppendsSnippetPattern(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/todo.ts": "TODO: wire this up\n",
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

	if [ "$prompt" = "filter> " ]; then
		printf '%s\n' 'selected	snippet'
	exit 0
fi

if [ "$prompt" = "match> " ]; then
	printf 'TODO\n'
	printf 'todo.ts\tsrc/todo.ts\tfile\ttext\n'
	exit 0
fi

if [ "$prompt" = "snippet mode> " ]; then
	printf 'block\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveBareStartupModifierArgs(resolver)
	if err != nil {
		t.Fatalf("resolveBareStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--snippet\nTODO"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupModifierArgsSnippetConsumesTrailingBarePlaceholder(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/todo.ts":    "TODO: wire this up\n",
		"uninstall.sh":   "#!/bin/sh\n",
		"scripts/run.sh": "echo hi\n",
	})
	_ = parseInProject(t, project, []string{"."})
	expectedBinding := platform.MultiSelectToggleAllBinding()
	expectedKey := platform.MultiSelectToggleAllKey()
	installScriptFzf(t, fmt.Sprintf(`#!/bin/sh
prompt=""
header=""
bindings=""
disabled=0
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
		--bind)
			bindings="$bindings
$2"
			shift 2
			;;
		--disabled)
			disabled=1
			shift
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "filter> " ]; then
	printf '%%s\n' "$input" | grep -F -- "snippet" | head -n 1
	exit 0
fi

if [ "$prompt" = "match> " ]; then
	[ "$disabled" -eq 1 ] || { echo "snippet picker must use --disabled" >&2; exit 91; }
	printf '%%s\n' "$header" | grep -F "Extract snippets whose contents match a regex." >/dev/null || {
		echo "missing match header" >&2
		exit 91
	}
	printf '%%s\n' "$header" | grep -F "Type a regex." >/dev/null || {
		echo "missing snippet enter header" >&2
		exit 91
	}
	printf '%%s\n' "$header" | grep -F %q >/dev/null || {
		echo "missing toggle-all header" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- %q >/dev/null || {
		echo "missing toggle-all binding" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- "--internal-content-match-list" >/dev/null || {
		echo "missing internal content match list command" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- "--internal-prediscovered" >/dev/null || {
		echo "missing prediscovered snippet match list checkpoint command" >&2
		exit 91
	}
	printf '%%s\n' "$bindings" | grep -F -- '--snippet {q}' >/dev/null || {
		echo "missing trimmed snippet contains-list command" >&2
		exit 91
	}
	if printf '%%s\n' "$bindings" | grep -F -- " uninstall.sh " >/dev/null; then
		echo "content match list command leaked current args: $bindings" >&2
		exit 91
	fi
	printf 'TODO\n'
	printf 'todo.ts\tsrc/todo.ts\tfile\ttext\n'
	exit 0
fi

if [ "$prompt" = "snippet mode> " ]; then
	printf 'block\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, "["+expectedKey+"] toggle", expectedBinding))

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupModifierArgs(resolver, []string{".", "--exclude", "uninstall.sh", "--"}, []string{"."}, []string{"."})
	if err != nil {
		t.Fatalf("resolveStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), ".\n--exclude\nuninstall.sh\n--snippet\nTODO"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupSnippetArgsUsesSnippetPreviewCommand(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/todo.ts": "TODO: wire this up\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
preview=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
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

if [ "$prompt" = "match> " ]; then
	printf '%s\n' "$preview" | grep -F -- '--internal-file-preview --internal-searching-preview --internal-file-path {3} --internal-tree-target {1}' >/dev/null || {
		echo "missing file preview command: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- '--snippet {q}' >/dev/null || {
		echo "missing snippet flag/query: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- 'catclip-tree' >/dev/null && {
		echo "preview command must not invoke catclip-tree: $preview" >&2
		exit 91
	}
	case "$preview" in
		*'|'*)
			echo "preview command must not contain a shell pipe: $preview" >&2
			exit 91
			;;
	esac
	printf 'TODO\n'
	printf 'todo.ts\tsrc/todo.ts\tfile\ttext\n'
	exit 0
fi

if [ "$prompt" = "snippet mode> " ]; then
	printf 'block\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	if _, _, err := resolveStartupContentArgs([]string{"."}, "--snippet"); err != nil {
		t.Fatalf("resolveStartupContentArgs returned error: %v", err)
	}
}
func TestChooseSnippetBoundaryWithFzfReturnsNumericContext(t *testing.T) {
	installScriptFzf(t, `#!/bin/sh
prompt=""
header=""
with_nth=""
nth=""
no_sort=0
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
		--with-nth)
			with_nth="$2"
			shift 2
			;;
		--nth)
			nth="$2"
			shift 2
			;;
		--no-sort)
			no_sort=1
			shift
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "snippet mode> " ]; then
	[ "$with_nth" = "1,3" ] || { echo "expected --with-nth 1,3, got $with_nth" >&2; exit 91; }
	[ "$nth" = "1" ] || { echo "expected --nth 1, got $nth" >&2; exit 91; }
	[ "$no_sort" -eq 1 ] || { echo "expected snippet mode picker to disable sorting" >&2; exit 91; }
	printf '%s\n' "$header" | grep -F "Choose snippet boundaries." >/dev/null || {
		echo "missing snippet boundary header" >&2
		exit 91
	}
	first="$(printf '%s\n' "$input" | head -n 1 | cut -f2)"
	[ "$first" = "block" ] || { echo "expected block default row first, got $first" >&2; exit 91; }
	printf '%s\n' "$input" | grep -F $'\t3\tmatch +/- 3 lines' >/dev/null || {
		echo "missing context 3 row" >&2
		exit 91
	}
	printf '3\t3\tmatch +/- 3 lines\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	choice, err := chooseSnippetBoundaryWithFzfAndEscHint("", "")
	if err != nil {
		t.Fatalf("chooseSnippetBoundaryWithFzfAndEscHint returned error: %v", err)
	}
	if !choice.SnippetContextSet || choice.SnippetContextLines != 3 {
		t.Fatalf("choice context = set:%v lines:%d, want set:true lines:3", choice.SnippetContextSet, choice.SnippetContextLines)
	}
}
func TestResolveStartupSnippetArgsNumericBoundaryAppendsContext(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: one\n",
		"src/util.ts": "TODO: two\n",
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

if [ "$prompt" = "match> " ]; then
	printf 'TODO\n'
	printf '[all current matches]\t\t\t\t\n'
	printf 'main.ts\tsrc/main.ts\tfile\ttext\n'
	exit 0
fi

if [ "$prompt" = "snippet mode> " ]; then
	printf '3\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupContentArgs([]string{"src"}, "--snippet")
	if err != nil {
		t.Fatalf("resolveStartupContentArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--snippet\nTODO\n3"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupContentArgsUsesSingleVisibleDisplayColumn(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cli.go": "TODO: root file\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
with_nth=""
nth=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		--with-nth)
			with_nth="$2"
			shift 2
			;;
		--nth)
			nth="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

if [ "$prompt" = "match> " ]; then
	[ "$with_nth" = "1" ] || { echo "expected --with-nth 1, got $with_nth" >&2; exit 91; }
	[ "$nth" = "1" ] || { echo "expected --nth 1, got $nth" >&2; exit 91; }
	printf 'TODO\n'
	printf 'cli.go\tcli.go\tfile\ttext\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	if _, _, err := resolveStartupContentArgs([]string{"."}, "--contains"); err != nil {
		t.Fatalf("resolveStartupContentArgs returned error: %v", err)
	}
}
func TestResolveStartupScopeFileSetArgsUsesSingleVisibleDisplayColumn(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cli.go": "package main\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
with_nth=""
nth=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		--with-nth)
			with_nth="$2"
			shift 2
			;;
		--nth)
			nth="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done

if [ "$prompt" = "only> " ]; then
	[ "$with_nth" = "1" ] || { echo "expected --with-nth 1, got $with_nth" >&2; exit 91; }
	[ "$nth" = "1" ] || { echo "expected --nth 1, got $nth" >&2; exit 91; }
	printf 'cli.go\tcli.go\tcli.go\tfile\ttext\tfile\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	if _, _, err := resolveStartupScopeFileSetArgs([]string{"."}, "--only", "only> "); err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
}
func TestResolveBareStartupModifierArgsUsesOrderedModifierMenu(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/todo.ts": "TODO: wire this up\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
header=""
bindings=""
no_sort=0
nth=""
print_query=0
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
		--bind)
			bindings="$bindings
$2"
			shift 2
			;;
		--no-sort)
			no_sort=1
			shift
			;;
		--nth)
			nth="$2"
			shift 2
			;;
		--print-query)
			print_query=1
			shift
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

emit_query() {
	if [ "$print_query" -eq 1 ]; then
		printf '%s\n' ""
	fi
}

if [ "$prompt" = "filter> " ]; then
	[ "$(printf '%s\n' "$header" | wc -l | tr -d ' ')" = "4" ] || {
		echo "expected 4-line modifier header" >&2
		exit 91
	}
	[ "$no_sort" -eq 1 ] || {
		echo "expected modifier picker to disable sorting" >&2
		exit 91
	}
	[ "$nth" = "1" ] || {
		echo "expected modifier picker to search only the label column, got nth=$nth" >&2
		exit 91
	}
	[ -z "$bindings" ] || {
		echo "unexpected modifier bindings: $bindings" >&2
		exit 91
	}
	first="$(printf '%s\n' "$input" | head -n 1)"
	last="$(printf '%s\n' "$input" | tail -n 1)"
	second_last="$(printf '%s\n' "$input" | tail -n 2 | head -n 1)"
	third_last="$(printf '%s\n' "$input" | tail -n 3 | head -n 1)"
	first_label="$(printf '%s\n' "$first" | cut -f1)"
	first_key="$(printf '%s\n' "$first" | cut -f2)"
	last_key="$(printf '%s\n' "$last" | cut -f2)"
	last_label="$(printf '%s\n' "$last" | cut -f1)"
	last_desc="$(printf '%s\n' "$last" | cut -f3)"
	second_last_key="$(printf '%s\n' "$second_last" | cut -f2)"
	second_last_label="$(printf '%s\n' "$second_last" | cut -f1)"
	third_last_key="$(printf '%s\n' "$third_last" | cut -f2)"
	third_last_label="$(printf '%s\n' "$third_last" | cut -f1)"
	[ "$first_key" = "only" ] || {
		echo "unexpected first modifier row: $first" >&2
		exit 91
	}
	[ "$last_key" = "finish" ] || {
		echo "unexpected last modifier row: $last" >&2
		exit 91
	}
	printf '%s\n' "$last_label" | grep -F -- "[finish early]" >/dev/null || {
		echo "missing finish label in last row: $last_label" >&2
		exit 91
	}
	[ "$second_last_key" = "extras" ] || {
		echo "unexpected second-last modifier row: $second_last" >&2
		exit 91
	}
	printf '%s\n' "$second_last_label" | grep -F -- "[extras]" >/dev/null || {
		echo "missing extras label in second-last row: $second_last_label" >&2
		exit 91
	}
	[ "$third_last_key" = "then" ] || {
		echo "unexpected third-last modifier row: $third_last" >&2
		exit 91
	}
	printf '%s\n' "$third_last_label" | grep -F -- "--then" >/dev/null || {
		echo "missing --then label in third-last row: $third_last_label" >&2
		exit 91
	}
	printf '%s\n' "$third_last_label" | grep -F -- "Chain a new scope with its own targets and filters" >/dev/null && {
		echo "label column should not contain description text: $last_label" >&2
		exit 91
	}
	printf '%s\n' "$third_last" | cut -f3 | grep -F -- "Chain a new scope with its own targets and filters" >/dev/null || {
		echo "missing --then description in description column: $third_last" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F $'\tsnippet' >/dev/null || {
		echo "missing snippet modifier row" >&2
		exit 91
	}
	printf '%s\n' 'only'
	exit 0
fi

if [ "$prompt" = "select> " ]; then
	emit_query
	printf '%s\n' 'src'
	exit 0
fi

if [ "$prompt" = "only> " ]; then
	printf '%s\n' 'src/todo.ts'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveBareStartupModifierArgs(resolver)
	if err != nil {
		t.Fatalf("resolveBareStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--only\nsrc/todo.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupContainsArgsEnterUsesSelectedPaths(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: one\n",
		"src/util.ts": "TODO: two\n",
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

if [ "$prompt" = "match> " ]; then
	{
		printf 'TODO\n'
		printf 'main.ts\tsrc/main.ts\tfile\ttext\n'
		printf 'util.ts\tsrc/util.ts\tfile\ttext\n'
	}
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupContentArgs([]string{"src"}, "--contains")
	if err != nil {
		t.Fatalf("resolveStartupContentArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--contains\nTODO"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupContainsArgsAllRowKeepsPlainContains(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: one\n",
		"src/util.ts": "TODO: two\n",
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

if [ "$prompt" = "match> " ]; then
	{
		printf 'TODO\n'
		printf '[all current matches]\t\t\t\t\n'
		printf 'main.ts\tsrc/main.ts\tfile\ttext\n'
	}
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupContentArgs([]string{"src"}, "--contains")
	if err != nil {
		t.Fatalf("resolveStartupContentArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--contains\nTODO"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupContainsArgsSubsetStillUsesOnly(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: one\n",
		"src/util.ts": "TODO: two\n",
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

if [ "$prompt" = "match> " ]; then
	{
		printf 'TODO\n'
		printf 'main.ts\tsrc/main.ts\tfile\ttext\n'
	}
	exit 0
fi

if [ "$prompt" = "snippet mode> " ]; then
	printf 'block\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupContentArgs([]string{"src"}, "--contains")
	if err != nil {
		t.Fatalf("resolveStartupContentArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--contains\nTODO\n--only\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupSnippetArgsSubsetStillUsesOnly(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: one\n",
		"src/util.ts": "TODO: two\n",
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

if [ "$prompt" = "match> " ]; then
	{
		printf 'TODO\n'
		printf 'main.ts\tsrc/main.ts\tfile\ttext\n'
	}
	exit 0
fi

if [ "$prompt" = "snippet mode> " ]; then
	printf 'block\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupContentArgs([]string{"src"}, "--snippet")
	if err != nil {
		t.Fatalf("resolveStartupContentArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--snippet\nTODO\n--only\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsBareSnippetErrors(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: one\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveInteractiveStartupArgs(resolver, []string{"src", "--snippet"})
	if err == nil {
		t.Fatal("expected bare --snippet to fail")
	}
	if !strings.Contains(err.Error(), "--snippet requires a regex pattern") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestResolveStartupArgsPlaceholderOnlyConsumesMultipleValues(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('main')\n",
		"src/util.ts": "console.log('util')\n",
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

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' 'selected	only'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, _, err := resolveStartupArgs(resolver, []string{"--", "src/main.ts", "src/util.ts"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--only\n*.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsModifierMenuThenOnlyStartsNewScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":   "console.log('main')\n",
		"src/util.ts":   "console.log('util')\n",
		"shared/app.ts": "console.log('shared')\n",
		"shared/lib.ts": "console.log('lib')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	selectStateFile := filepath.Join(t.TempDir(), "modifier-then-select-count")
	modifierStateFile := filepath.Join(t.TempDir(), "modifier-then-modifier-count")
	installScriptFzf(t, fmt.Sprintf(`#!/bin/sh
prompt=""
query=""
print_query=0
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
		--print-query)
			print_query=1
			shift
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

emit_query() {
	if [ "$print_query" -eq 1 ]; then
		printf '%%s\n' "$query"
	fi
}

if [ "$prompt" = "select> " ]; then
	count=0
	if [ -f %q ]; then
		count="$(cat %q)"
	fi
	count=$((count + 1))
	printf '%%s' "$count" > %q
	case "$count" in
		1)
			emit_query
			printf '%%s\n' 'src'
			;;
		2)
			emit_query
			printf '%%s\n' 'shared'
			;;
		*)
			echo "unexpected select count: $count" >&2
			exit 91
			;;
	esac
	exit 0
fi

if [ "$prompt" = "filter> " ]; then
	count=0
	if [ -f %q ]; then
		count="$(cat %q)"
	fi
	count=$((count + 1))
	printf '%%s' "$count" > %q
	case "$count" in
		1)
			printf '%%s\n' 'only'
			;;
		2)
			printf '%%s\n' 'then'
			;;
		3)
			printf '%%s\n' 'only'
			;;
		*)
			echo "unexpected modifier count: $count" >&2
			exit 91
			;;
	esac
	exit 0
fi

if [ "$prompt" = "only> " ]; then
	if printf '%%s\n' "$input" | grep -F "shared/" >/dev/null; then
		printf '%%s\n' "$input" | grep -F "shared/app.ts" | head -n 1
		exit 0
	fi
	if printf '%%s\n' "$input" | grep -F "src/" >/dev/null; then
		printf '%%s\n' "$input" | grep -F "src/main.ts" | head -n 1
		exit 0
	fi
	echo "unexpected only scope" >&2
	exit 91
fi

if [ "$prompt" = "then> " ]; then
	emit_query
	printf '%%s\n' 'shared'
	exit 0
fi

echo "unexpected prompt: $prompt query=$query" >&2
exit 91
`, selectStateFile, selectStateFile, selectStateFile, modifierStateFile, modifierStateFile, modifierStateFile))

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, _, err := resolveInteractiveStartupArgs(resolver, []string{"--", "--", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--only\nsrc/main.ts\n--then\nshared\n--only\nshared/app.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveInteractiveStartupArgsBarePlaceholderChainKeepsNextPlaceholderAfterContains(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "hello world\n",
	})
	_ = parseInProject(t, project, []string{"."})
	modifierStateFile := filepath.Join(t.TempDir(), "modifier-count")
	regexStateFile := filepath.Join(t.TempDir(), "regex-count")
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

if [ "$prompt" = "filter> " ]; then
	count=0
	if [ -f %q ]; then
		count="$(cat %q)"
	fi
	count=$((count + 1))
	printf '%%s' "$count" > %q
	case "$count" in
		1)
			printf '%%s\n' 'contains'
			;;
		2)
			printf '%%s\n' 'snippet'
			;;
		*)
			echo "unexpected modifier count: $count" >&2
			exit 91
			;;
	esac
	exit 0
fi

if [ "$prompt" = "match> " ]; then
	count=0
	if [ -f %q ]; then
		count="$(cat %q)"
	fi
	count=$((count + 1))
	printf '%%s' "$count" > %q
	case "$count" in
		1)
			printf 'hello\n'
			;;
		2)
			printf 'world\n'
			;;
		*)
			echo "unexpected regex count: $count" >&2
			exit 91
			;;
	esac
	printf '[all current matches]\t\t\t\t\n'
	printf 'main.ts\tsrc/main.ts\tfile\ttext\n'
	exit 0
fi

if [ "$prompt" = "snippet mode> " ]; then
	printf 'block\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, modifierStateFile, modifierStateFile, modifierStateFile, regexStateFile, regexStateFile, regexStateFile))

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, _, err := resolveInteractiveStartupArgs(resolver, []string{".", "--", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), ".\n--contains\nhello\n--snippet\nworld"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestResolveInteractiveStartupArgsIncludeWildcardContinuesToModifierMenu(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('main')\n",
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

case "$prompt" in
"filter> ")
	printf '%s\n' 'paths'
	;;
"include> ")
	echo "resolved --include '*' unexpectedly opened the include picker" >&2
	exit 91
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
	args, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, []string{".", "--include", "*", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected trailing placeholder to open the modifier menu")
	}
	if got, want := strings.Join(args, "\n"), ".\n--include\n*\n--paths"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestResolveStartupArgsPlaceholderIncludeOnlyOnlyKeepsDotScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":               "console.log('main')\n",
		"src/util.ts":               "console.log('util')\n",
		"node_modules/pkg/index.js": "export const pkg = 1\n",
	})
	_ = parseInProject(t, project, []string{"."})
	stateFile := filepath.Join(t.TempDir(), "modifier-count")
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

if [ "$prompt" = "filter> " ]; then
	count=0
	if [ -f %q ]; then
		count="$(cat %q)"
	fi
	count=$((count + 1))
	printf '%%s' "$count" > %q
	case "$count" in
		1)
			printf '%%s\n' 'selected	include'
			;;
		2)
			printf '%%s\n' 'selected	only'
			;;
		3)
			printf '%%s\n' 'selected	only'
			;;
		*)
			echo "unexpected modifier count: $count" >&2
			exit 91
			;;
	esac
	exit 0
fi

if [ "$prompt" = "only> " ]; then
	if printf '%%s\n' "$input" | grep -F "node_modules/pkg/index.js" >/dev/null; then
		if ! printf '%%s\n' "$input" | grep -F "src/main.ts" >/dev/null; then
			echo "src/main.ts missing from second only picker" >&2
			exit 91
		fi
		printf '%%s\n' "$input" | grep -F "src/main.ts" | head -n 1
		exit 0
	fi
	printf '%%s\n' "$input" | grep -F "src/main.ts" | head -n 1
	exit 0
fi

if [ "$prompt" = "include> " ]; then
	printf '%%s\n' "$input" | grep -F "[ignored dir .hiss] node_modules" | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`, stateFile, stateFile, stateFile))

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, _, err := resolveStartupArgs(resolver, []string{"--", "--", "--"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "--include\nnode_modules\n--only\nsrc/main.ts\n--only\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupArgsPlaceholderContainsRejectsExtraValue(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: src\n",
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

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' '--contains	contains'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveStartupArgs(resolver, []string{"--", "TODO", "extra"})
	if err == nil {
		t.Fatal("expected extra plain token after placeholder contains stage to fail")
	}
	if !strings.Contains(err.Error(), "positional targets must come before modifiers") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestResolveStartupArgsPlaceholderChangedRejectsPlainValue(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('src')\n",
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

if [ "$prompt" = "filter> " ]; then
	printf '%s\n' 'selected	changed'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveStartupArgs(resolver, []string{"--", "extra"})
	if err == nil {
		t.Fatal("expected plain token after placeholder changed stage to fail")
	}
	if !strings.Contains(err.Error(), "positional targets must come before modifiers") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestResolveStartupArgsExplicitChangedRejectsPlainValue(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"README.md": "hello\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "README.md", "changed\n")
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, _, err = resolveStartupArgs(resolver, []string{"--changed", "README.md"})
	if err == nil {
		t.Fatal("expected explicit changed shorthand to fail")
	}
	if !strings.Contains(err.Error(), "positional targets must come before modifiers") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestMaybeResolveStartupPickerArgsTrailingContainsAfterResolvedTargets(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: src\n",
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

if [ "$prompt" = "match> " ]; then
		printf 'TODO\n'
		printf 'main.ts\tsrc/main.ts\tfile\ttext\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupTrailingActionArgs(resolver, []string{"src"}, StartupTrailingActionContains)
	if err != nil {
		t.Fatalf("resolveStartupTrailingActionArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--contains\nTODO"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestResolveStartupTrailingActionArgsRejectsContainsAfterDiff(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: src\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, err = resolveStartupTrailingActionArgs(resolver, []string{"src", "--changed-diff"}, StartupTrailingActionContains)
	if err == nil {
		t.Fatal("expected trailing contains action after --diff to fail")
	}
	if !strings.Contains(err.Error(), "--contains must come before --changed-diff in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestResolveStartupTrailingActionArgsRejectsContainsAfterSnippet(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: src\n",
	})
	_ = parseInProject(t, project, []string{"."})

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	_, _, err = resolveStartupTrailingActionArgs(resolver, []string{"src", "--snippet", "TODO"}, StartupTrailingActionContains)
	if err == nil {
		t.Fatal("expected trailing contains action after --snippet to fail")
	}
	if !strings.Contains(err.Error(), "--contains must come before --snippet in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestResolveStartupTrailingActionArgsSnippetStillUsesPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: src\n",
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

if [ "$prompt" = "match> " ]; then
	printf 'TODO\n'
	printf 'main.ts\tsrc/main.ts\tfile\ttext\n'
	exit 0
fi

if [ "$prompt" = "snippet mode> " ]; then
	printf 'block\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupTrailingActionArgs(resolver, []string{"src"}, StartupTrailingActionSnippet)
	if err != nil {
		t.Fatalf("resolveStartupTrailingActionArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--snippet\nTODO"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
func TestRunInternalContentMatchListUsesSingleLabelForRootFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cli.go": "TODO: root\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-content-match-list", ".", "--contains", "TODO"})

	var stdout bytes.Buffer
	if err := RunInternalContentMatchList(ContentMatchListConfigFromParsedCommand(cfg), &stdout); err != nil {
		t.Fatalf("RunInternalContentMatchList returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "cli.go\tcli.go\tcli.go\tfile\ttext") {
		t.Fatalf("expected root file row in internal contains list output, got %q", out)
	}
	if strings.Contains(out, "cli.go  cli.go\tcli.go\tcli.go\tfile\ttext") {
		t.Fatalf("root file label duplicated basename and relpath: %q", out)
	}
}
func TestStartupFilePathRowsUseSingleLabelForRootFiles(t *testing.T) {
	rows := startupFilePathRows([]string{"cli.go"})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %#v", rows)
	}
	if got, want := rows[0].Display, "cli.go"; got != want {
		t.Fatalf("expected display %q, got %q", want, got)
	}
}

// writeContentMatchRows emits 6 TSV columns. Column 6 is the first-match
// line number used by fzf's --preview-window +{6}-/2 offset. The
// [all current matches] row uses "1" as a well-formed placeholder so the
// fzf flag parse never sees an empty {6}. File rows without a known
// line (FirstMatchLine == 0) get downgraded to "1" so the substitution
// always lands at the top of the preview pane instead of going negative.
func TestWriteContentMatchRowsIncludesFirstMatchLine(t *testing.T) {
	var buf bytes.Buffer
	if err := writeContentMatchRows(&buf, []contentMatchRow{
		{RelPath: "src/a.go", FirstMatchLine: 42},
		{RelPath: "src/b.go", FirstMatchLine: 0},
	}); err != nil {
		t.Fatalf("writeContentMatchRows returned error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (all-matches + 2 files), got %d: %q", len(lines), buf.String())
	}

	headerCols := strings.Split(lines[0], "\t")
	if len(headerCols) != 6 {
		t.Fatalf("all-matches row should have 6 columns, got %d: %q", len(headerCols), lines[0])
	}
	if headerCols[0] != contentMatchAllMatchesLabel {
		t.Fatalf("all-matches label = %q, want %q", headerCols[0], contentMatchAllMatchesLabel)
	}
	if headerCols[5] != contentMatchAllMatchesPreviewLine {
		t.Fatalf("all-matches column 6 = %q, want %q", headerCols[5], contentMatchAllMatchesPreviewLine)
	}

	aCols := strings.Split(lines[1], "\t")
	if len(aCols) != 6 {
		t.Fatalf("a.go row should have 6 columns, got %d: %q", len(aCols), lines[1])
	}
	if aCols[5] != "42" {
		t.Fatalf("a.go first-match line column = %q, want 42", aCols[5])
	}

	bCols := strings.Split(lines[2], "\t")
	if bCols[5] != "1" {
		t.Fatalf("b.go (FirstMatchLine=0) should downgrade to 1, got %q", bCols[5])
	}
}

// internalPreviewPatternIsEmpty fires only in content-picker context
// (snippet stage OR contains stage). A scope with no content stage
// should not be treated as "empty pattern" — that would short-circuit
// non-content pickers' preview into the hint.
func TestInternalPreviewPatternIsEmptyOnlyContentMode(t *testing.T) {
	cases := []struct {
		name string
		s    command.ExecutionScope
		want bool
	}{
		{
			name: "contains-empty",
			s: command.ExecutionScope{
				Contains: "",
				Stages:   []command.Stage{{Kind: command.StageContains}},
			},
			want: true,
		},
		{
			name: "contains-non-empty",
			s: command.ExecutionScope{
				Contains: "TODO",
				Stages:   []command.Stage{{Kind: command.StageContains}},
			},
			want: false,
		},
		{
			name: "snippet-empty",
			s: command.ExecutionScope{
				Snippet:        true,
				SnippetPattern: "  ",
				Stages:         []command.Stage{{Kind: command.StageSnippet}},
			},
			want: true,
		},
		{
			name: "no-content-stage",
			s: command.ExecutionScope{
				Targets: []string{"src"},
			},
			want: false,
		},
	}
	for _, c := range cases {
		got := internalPreviewPatternIsEmpty(c.s)
		if got != c.want {
			t.Errorf("%s: internalPreviewPatternIsEmpty = %v, want %v", c.name, got, c.want)
		}
	}
}

// buildInternalContentHintDocument returns different hint text based on
// the scope's content mode. Snippet scope -> snippet hint; contains
// scope (or neither, falling through) -> contains hint.
func TestBuildInternalContentHintDocumentRoutesByMode(t *testing.T) {
	snippetDoc := buildInternalContentHintDocument(command.ExecutionScope{
		Snippet: true,
		Stages:  []command.Stage{{Kind: command.StageSnippet}},
	})
	if snippetDoc.File == nil || snippetDoc.File.Content != internalSnippetPreviewEmptyHint {
		t.Fatalf("snippet hint mismatch: %#v", snippetDoc.File)
	}

	containsDoc := buildInternalContentHintDocument(command.ExecutionScope{
		Stages: []command.Stage{{Kind: command.StageContains}},
	})
	if containsDoc.File == nil || containsDoc.File.Content != internalContainsPreviewEmptyHint {
		t.Fatalf("contains hint mismatch: %#v", containsDoc.File)
	}
}

// AllIgnoredTargets narrows its enumeration universe to literal scope
// targets: entries outside the targets (and off their ancestor chains)
// are never walked or classified. Pinned 2026-07-07 after the Desktop
// repro (cwd=parent dir, target=repo → the wide walk content-scanned
// every sibling project just to discard it). Ancestor dirs of a deep
// target must survive narrowing via path-prefix derivation, and
// "."/fuzzy targets must fall back to the wide universe.
func TestAllIgnoredTargetsNarrowsToScopeTargets(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"repo/src/app.ts":              "export const app = true\n",
		"repo/node_modules/r/index.js": "module.exports = {}\n",
		"sibling/node_modules/x.js":    "module.exports = {}\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "repo"})
	resolver := discovery.Resolver{
		Cfg:               invocationConfigFromParsedCommand(cfg),
		AllowFileSymlinks: false,
	}

	targets, err := resolver.AllIgnoredTargets([]string{"repo"})
	if err != nil {
		t.Fatalf("AllIgnoredTargets returned error: %v", err)
	}
	lookup := make(map[string]discovery.TargetMatch, len(targets))
	for _, target := range targets {
		lookup[target.Path] = target
	}
	if _, ok := lookup["repo/node_modules"]; !ok {
		t.Fatalf("expected in-scope ignored dir repo/node_modules, got %v", pathsOfTargetMatches(targets))
	}
	if _, ok := lookup["sibling/node_modules"]; ok {
		t.Fatalf("narrowed universe must not include the sibling project's ignored dir, got %v", pathsOfTargetMatches(targets))
	}
	// Ignored ANCESTOR of a deep target survives narrowing via
	// path-prefix derivation: a walk rooted at blocked/sub still
	// contributes "blocked" to the dir set, and attribution marks it
	// gitignore-blocked.
	ancestorProject := setupTestProject(t, map[string]string{
		".gitignore":         "blocked/\n",
		"blocked/sub/kit.md": "kit\n",
		"visible.go":         "package x\n",
	})
	ancestorCfg := parseInProject(t, ancestorProject, []string{"--quiet", "--print", "."})
	ancestorResolver := discovery.Resolver{
		Cfg:               invocationConfigFromParsedCommand(ancestorCfg),
		AllowFileSymlinks: false,
	}
	ancestors, err := ancestorResolver.AllIgnoredTargets([]string{"blocked/sub"})
	if err != nil {
		t.Fatalf("AllIgnoredTargets ancestor case returned error: %v", err)
	}
	ancestorLookup := make(map[string]discovery.TargetMatch, len(ancestors))
	for _, target := range ancestors {
		ancestorLookup[target.Path] = target
	}
	if got, ok := ancestorLookup["blocked"]; !ok || !got.Ignored || got.Kind != "dir" {
		t.Fatalf("expected ignored ancestor dir 'blocked' to survive the narrowed walk, got %#v (present=%v, all=%v)", got, ok, pathsOfTargetMatches(ancestors))
	}

	// "." target falls back to the wide universe: the sibling appears.
	wide, err := resolver.AllIgnoredTargets([]string{"."})
	if err != nil {
		t.Fatalf("AllIgnoredTargets wide returned error: %v", err)
	}
	wideLookup := make(map[string]struct{}, len(wide))
	for _, target := range wide {
		wideLookup[target.Path] = struct{}{}
	}
	if _, ok := wideLookup["sibling/node_modules"]; !ok {
		t.Fatalf("expected wide fallback for '.' target to include sibling, got %v", pathsOfTargetMatches(wide))
	}
}

func pathsOfTargetMatches(targets []discovery.TargetMatch) []string {
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		out = append(out, target.Path)
	}
	return out
}
