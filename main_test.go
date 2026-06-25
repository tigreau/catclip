package catclip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tigreau/catclip/fileclip"
	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
	"github.com/tigreau/catclip/internal/ui"
)

func TestMain(m *testing.M) {
	if os.Getenv("CATCLIP_TEST_RUN_MAIN") == "1" {
		Main()
		os.Exit(0)
	}
	if _, ok := search.RipgrepBinary(); !ok {
		fmt.Fprintln(os.Stderr, "FATAL: rg not found. Run 'make dev' to set up dev tools.")
		os.Exit(1)
	}
	if _, ok := discovery.FzfBinary(); !ok {
		fmt.Fprintln(os.Stderr, "FATAL: fzf not found. Run 'make dev' to set up dev tools.")
		os.Exit(1)
	}
	// Tests exercise the discovery resolver's content-match checkpoint
	// path (TestResolveBareStartupModifierArgsContains*,
	// TestResolveStartupModifierArgsSnippet*); those tests run inside
	// the catclip test binary without going through Main(), so the
	// scope-view callback that Main() normally registers is unwired.
	// Register it here too.
	discovery.SetScopeViewResolver(ui.ScopeViewForDiscoveryArgs)
	os.Exit(m.Run())
}

func skipUnlessLinux(t *testing.T, feature string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("%s is Linux-specific; running on %s", feature, runtime.GOOS)
	}
}

type errAfterReader struct {
	data []byte
	err  error
	read bool
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, r.err
	}
	r.read = true
	n := copy(p, r.data)
	return n, nil
}

func parsedExecutionScopes(t *testing.T, cfg command.Parsed) []command.ExecutionScope {
	t.Helper()
	return command.ExecutionScopesFromSpec(cfg.Command)
}

func parsedExecutionScope(t *testing.T, cfg command.Parsed) command.ExecutionScope {
	t.Helper()
	scopes := parsedExecutionScopes(t, cfg)
	if len(scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(scopes))
	}
	return scopes[0]
}

func setTestStdinFile(t *testing.T, file *os.File) {
	t.Helper()
	old := os.Stdin
	os.Stdin = file
	t.Cleanup(func() {
		os.Stdin = old
		_ = file.Close()
	})
}

func setTestPipeStdin(t *testing.T, input string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	if _, err := io.WriteString(writer, input); err != nil {
		t.Fatalf("writing stdin pipe failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing stdin pipe writer failed: %v", err)
	}
	setTestStdinFile(t, reader)
}

func TestParseArgsRejectsBareInvocationWithoutTargets(t *testing.T) {
	_, err := cli.ParseArgs(nil)
	if err == nil {
		t.Fatal("expected bare parseArgs to error (no implicit '.' target)")
	}
	if !strings.Contains(err.Error(), "no target specified") {
		t.Fatalf("expected no-target error, got: %v", err)
	}
}

func TestParseArgsBuildsMultipleScopes(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--only", "*.ts", "--then", "tests", "--only", "*.test.ts"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scopes := parsedExecutionScopes(t, cfg)
	if got, want := len(scopes), 2; got != want {
		t.Fatalf("expected %d scopes, got %d", want, got)
	}

	if got := scopes[0].Targets[0]; got != "src" {
		t.Fatalf("expected first scope target 'src', got %q", got)
	}
	if got := scopes[0].Only[0]; got != "*.ts" {
		t.Fatalf("expected first scope only '*.ts', got %q", got)
	}
	if got := scopes[1].Targets[0]; got != "tests" {
		t.Fatalf("expected second scope target 'tests', got %q", got)
	}
	if got := scopes[1].Only[0]; got != "*.test.ts" {
		t.Fatalf("expected second scope only '*.test.ts', got %q", got)
	}
}

func TestParseArgsRejectsModifierOnlyWithoutTargets(t *testing.T) {
	_, err := cli.ParseArgs([]string{"--changed-diff"})
	if err == nil {
		t.Fatal("expected modifier-only invocation to error (no implicit '.' target)")
	}
	if !strings.Contains(err.Error(), "no target specified") {
		t.Fatalf("expected no-target error, got: %v", err)
	}
}

func TestParseArgsRejectsRetiredInternalTreePayloadFlag(t *testing.T) {
	_, err := cli.ParseArgs([]string{"--internal-tree-payload"})
	if err == nil {
		t.Fatal("expected retired --internal-tree-payload to fail")
	}
	if !strings.Contains(err.Error(), "Unknown option '--internal-tree-payload'") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsConsumesMultiValueExcludeStage(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--exclude", "*.snap", "build/"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if got, want := len(scope.Exclude), 2; got != want {
		t.Fatalf("expected %d exclude patterns, got %d", want, got)
	}
}

func TestParseArgsAcceptsBareRecentStage(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--recent"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if got, want := len(scope.Stages), 1; got != want {
		t.Fatalf("expected %d stage, got %d", want, got)
	}
	if scope.Stages[0].Kind != command.StageRecent {
		t.Fatalf("expected recent stage, got %q", scope.Stages[0].Kind)
	}
	if scope.Stages[0].Limit != nil {
		t.Fatalf("expected bare --recent to have no limit, got %v", *scope.Stages[0].Limit)
	}
}

func TestParseArgsAcceptsRecentLimitAndKeepsStageBoundaries(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--only", "*.ts", "--recent", "5"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if got, want := len(scope.Only), 1; got != want {
		t.Fatalf("expected %d only pattern, got %d", want, got)
	}
	if got, want := len(scope.Stages), 2; got != want {
		t.Fatalf("expected %d stages, got %d", want, got)
	}
	if scope.Stages[0].Kind != command.StageOnly {
		t.Fatalf("expected first stage to be only, got %q", scope.Stages[0].Kind)
	}
	if scope.Stages[1].Kind != command.StageRecent {
		t.Fatalf("expected second stage to be recent, got %q", scope.Stages[1].Kind)
	}
	if scope.Stages[1].Limit == nil || *scope.Stages[1].Limit != 5 {
		t.Fatalf("expected recent limit 5, got %+v", scope.Stages[1].Limit)
	}
}

func TestParseArgsRejectsInvalidRecentValue(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--recent", "later"})
	if err == nil {
		t.Fatal("expected error for invalid --recent value")
	}
	if !strings.Contains(err.Error(), "--recent takes an optional positive integer") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsRecentEqualsForm(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--recent=5"})
	if err == nil {
		t.Fatal("expected error for --recent=5")
	}
	if !strings.Contains(err.Error(), "--recent requires a space before the value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsAcceptsSizeStageForms(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []int
	}{
		{name: "bare sort", args: []string{"src", "--size"}, want: nil},
		{name: "minimum only", args: []string{"src", "--size", "10"}, want: []int{10}},
		{name: "zero minimum", args: []string{"src", "--size", "0"}, want: []int{0}},
		{name: "range", args: []string{"src", "--size", "0", "100"}, want: []int{0, 100}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := cli.ParseArgs(tc.args)
			if err != nil {
				t.Fatalf("ParseArgs returned error: %v", err)
			}
			scope := parsedExecutionScope(t, cfg)
			if got, want := len(scope.Stages), 1; got != want {
				t.Fatalf("expected %d stage, got %d", want, got)
			}
			stage := scope.Stages[0]
			if stage.Kind != command.StageSize {
				t.Fatalf("expected size stage, got %q", stage.Kind)
			}
			if !reflect.DeepEqual(stage.Nums, tc.want) {
				t.Fatalf("size nums = %v, want %v", stage.Nums, tc.want)
			}
		})
	}
}

func TestParseArgsRejectsInvalidSizeValues(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "non integer", args: []string{"src", "--size", "large"}, wantErr: "--size expects integer KiB values"},
		{name: "negative", args: []string{"src", "--size", "-1"}, wantErr: "--size expects integer KiB values"},
		{name: "zero max", args: []string{"src", "--size", "0", "0"}, wantErr: "--size max must be >= 1 KiB"},
		{name: "max before min", args: []string{"src", "--size", "100", "10"}, wantErr: "--size max (10) must be >= min (100)"},
		{name: "too many", args: []string{"src", "--size", "1", "2", "3"}, wantErr: "--size takes at most two values"},
		{name: "equals form", args: []string{"src", "--size=10"}, wantErr: "--size requires a space before the value"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cli.ParseArgs(tc.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseArgsSizeTreatsUnknownFlagAsBoundary(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--size", "--foo"})
	if err == nil {
		t.Fatal("expected unknown option error")
	}
	if !strings.Contains(err.Error(), "Unknown option '--foo'") {
		t.Fatalf("expected unknown option error, got: %v", err)
	}
}

func TestParseArgsAcceptsDepthStage(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--depth", "2"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if got, want := len(scope.Stages), 1; got != want {
		t.Fatalf("expected %d stage, got %d", want, got)
	}
	if scope.Stages[0].Kind != command.StageDepth {
		t.Fatalf("expected depth stage, got %q", scope.Stages[0].Kind)
	}
	if scope.Stages[0].Limit == nil || *scope.Stages[0].Limit != 2 {
		t.Fatalf("expected depth limit 2, got %+v", scope.Stages[0].Limit)
	}
}

func TestParseArgsRejectsInvalidDepthValue(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--depth", "0"})
	if err == nil {
		t.Fatal("expected error for invalid --depth value")
	}
	if !strings.Contains(err.Error(), "--depth takes a positive integer") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsDepthEqualsForm(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--depth=2"})
	if err == nil {
		t.Fatal("expected error for --depth=2")
	}
	if !strings.Contains(err.Error(), "--depth requires a space before the value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsAcceptsPathsStage(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--paths"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if !scope.Paths {
		t.Fatal("expected scope to record --paths output mode")
	}
	if got, want := len(scope.Stages), 1; got != want {
		t.Fatalf("expected %d stage, got %d", want, got)
	}
	if scope.Stages[0].Kind != command.StagePaths {
		t.Fatalf("expected paths stage, got %q", scope.Stages[0].Kind)
	}
}

func TestParseArgsAcceptsRawFlag(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "-r"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !cfg.Raw {
		t.Fatal("expected raw output mode to be enabled")
	}
}

func TestParseArgsPreviewAndPrintCoexist(t *testing.T) {
	for _, args := range [][]string{
		{"src", "--preview", "--print"},
		{"src", "--print", "--preview"},
	} {
		cfg, err := cli.ParseArgs(args)
		if err != nil {
			t.Fatalf("cli.ParseArgs(%v) returned error: %v", args, err)
		}
		if !cfg.Preview {
			t.Fatalf("cli.ParseArgs(%v): expected Preview=true, got %+v", args, cfg)
		}
		if cfg.OutputMode != command.OutputModeStdout {
			t.Fatalf("cli.ParseArgs(%v): expected OutputMode=stdout, got %q", args, cfg.OutputMode)
		}
	}
}

func TestParseArgsRejectsContainsAfterPaths(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--paths", "--contains", "TODO"})
	if err == nil {
		t.Fatal("expected terminal boundary error after --paths")
	}
	if !strings.Contains(err.Error(), "--paths finalizes the current scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsTreatsIncludeAsAllowedTargetSelection(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{".", "--include", "node_modules", "coverage"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scope := parsedExecutionScope(t, cfg)
	if got := scope.Targets; strings.Join(got, "\n") != "." {
		t.Fatalf("expected include to preserve the explicit base target only, got %#v", got)
	}
	if got := scope.IncludedTargets; strings.Join(got, "\n") != "node_modules\ncoverage" {
		t.Fatalf("expected included target metadata, got %#v", got)
	}
}

func TestParseArgsBareIncludeAugmentsExplicitDotScope(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{".", "--include", "node_modules", "coverage"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scope := parsedExecutionScope(t, cfg)
	if got := scope.Targets; strings.Join(got, "\n") != "." {
		t.Fatalf("expected explicit dot scope, got %#v", got)
	}
	if got := scope.IncludedTargets; strings.Join(got, "\n") != "node_modules\ncoverage" {
		t.Fatalf("expected included target metadata, got %#v", got)
	}
}

func TestParseArgsRejectsBareDashTarget(t *testing.T) {
	_, err := cli.ParseArgs([]string{"-"})
	if err == nil {
		t.Fatal("expected bare '-' target to fail")
	}
	if !strings.Contains(err.Error(), "'-' is not a valid target path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsStdinModifierWithoutPipe(t *testing.T) {
	if !platform.IsTerminalFile(os.Stdin) {
		t.Skip("terminal stdin not available")
	}

	_, err := cli.ParseArgs([]string{"src", "--exclude", "-"})
	if err == nil {
		t.Fatal("expected --exclude - without a pipe to fail")
	}
	if !strings.Contains(err.Error(), "--exclude - reads paths from stdin, but no input is being piped") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsEmptyStdinPathList(t *testing.T) {
	setTestPipeStdin(t, "")

	_, err := cli.ParseArgs([]string{"src", "--only", "-"})
	if err == nil {
		t.Fatal("expected empty stdin path list to fail")
	}
	if !strings.Contains(err.Error(), "--only - received no paths from stdin") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsInvalidTypedIncludeValues(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "absolute", value: "/vendor", wantErr: "--include does not accept absolute paths"},
		{name: "windows absolute", value: `C:\vendor`, wantErr: "--include does not accept absolute paths"},
		{name: "parent traversal", value: "../vendor", wantErr: "--include cannot traverse above the current target scope"},
		{name: "normalizing parent traversal", value: "src/../vendor", wantErr: "--include cannot traverse above the current target scope"},
		{name: "glob", value: "*.js", wantErr: "--include does not accept glob patterns"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cli.ParseArgs([]string{".", "--include", tc.value})
			if err == nil {
				t.Fatalf("expected %q to fail", tc.value)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestParseArgsAllowsIncludeWildcardSpecialCase(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{".", "--include", "*"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scopes := cfg.Command.Scopes()
	if got := scopes[0].IncludedTargets(); !reflect.DeepEqual(got, []string{"*"}) {
		t.Fatalf("IncludedTargets = %#v, want [*]", got)
	}
}

func TestParseArgsRejectsInvalidStdinIncludeValues(t *testing.T) {
	tests := []struct {
		name    string
		stdin   string
		wantErr string
	}{
		{name: "absolute", stdin: "/vendor\n", wantErr: "--include does not accept absolute paths"},
		{name: "absolute normalized to dot", stdin: "/tmp/..\n", wantErr: "--include does not accept absolute paths"},
		{name: "windows absolute", stdin: "C:\\vendor\n", wantErr: "--include does not accept absolute paths"},
		{name: "parent traversal", stdin: "../vendor\n", wantErr: "--include cannot traverse above the current target scope"},
		{name: "normalizing parent traversal", stdin: "src/../vendor\n", wantErr: "--include cannot traverse above the current target scope"},
		{name: "glob", stdin: "*.js\n", wantErr: "--include does not accept glob patterns"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setTestPipeStdin(t, tc.stdin)
			_, err := cli.ParseArgs([]string{".", "--include", "-"})
			if err == nil {
				t.Fatalf("expected stdin %q to fail", tc.stdin)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestParseArgsStagedImpliesChanged(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--staged"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if !scope.Staged || !scope.Changed {
		t.Fatalf("expected staged to imply changed, got %+v", scope)
	}
}

func TestParseArgsSnippetRequiresPattern(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--snippet"})
	if err == nil {
		t.Fatal("expected error for --snippet without a regex pattern")
	}
	if !strings.Contains(err.Error(), "--snippet requires a regex pattern") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsSnippetWithDiff(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--snippet", "TODO", "--changed-diff"})
	if err == nil {
		t.Fatal("expected error for --snippet with --diff")
	}
	if !strings.Contains(err.Error(), "--snippet and --diff cannot be combined") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsContainsAfterSnippet(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--snippet", "TODO", "--contains", "FIXME"})
	if err == nil {
		t.Fatal("expected error for --contains after --snippet")
	}
	if !strings.Contains(err.Error(), "--contains must come before --snippet in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsRepeatedSnippet(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--snippet", "TODO", "--snippet", "FIXME"})
	if err == nil {
		t.Fatal("expected error for repeated --snippet")
	}
	if !strings.Contains(err.Error(), "--snippet cannot be repeated in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsAllowsSnippetAfterEarlierFilters(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{".", "--contains", "keep", "--only", "README.md", "--snippet", "show"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scope := parsedExecutionScope(t, cfg)
	if !scope.Snippet || scope.Contains != "keep" || scope.SnippetPattern != "show" {
		t.Fatalf("unexpected parsed scope: %+v", scope)
	}
}

func TestParseArgsAllowsContainsBeforeChangedDiff(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--contains", "TODO", "--changed-diff"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if !scope.Diff || !scope.Changed || scope.Contains != "TODO" {
		t.Fatalf("unexpected parsed scope: %+v", scope)
	}
}

func TestParseArgsRejectsContainsAfterDiff(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--changed-diff", "--contains", "TODO"})
	if err == nil {
		t.Fatal("expected error for --contains after --diff")
	}
	if !strings.Contains(err.Error(), "--contains must come before --changed-diff in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "content filters cannot come after it") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsAllowsModifierLikeContainsPattern(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--contains", "--snippet"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scope := parsedExecutionScope(t, cfg)
	if got := scope.Contains; got != "--snippet" {
		t.Fatalf("scope.Contains = %q, want --snippet", got)
	}
}

func TestParseArgsAllowsModifierLikeSnippetPattern(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--snippet", "--contains"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scope := parsedExecutionScope(t, cfg)
	if got := scope.SnippetPattern; got != "--contains" {
		t.Fatalf("scope.SnippetPattern = %q, want --contains", got)
	}
}

func TestParseArgsAllowsDoubleDashRegexPatterns(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--contains", "--", "--snippet", "--"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scope := parsedExecutionScope(t, cfg)
	if got := scope.Contains; got != "--" {
		t.Fatalf("scope.Contains = %q, want --", got)
	}
	if got := scope.SnippetPattern; got != "--" {
		t.Fatalf("scope.SnippetPattern = %q, want --", got)
	}
}

// TestParseStartupInputTokens* tests moved to internal/ui/startup_input_parse_test.go
// during the v0.6.0 internal/ui extraction — they assert against the
// private startupInputParse.modifiers field which can't be read from
// root.

func TestParseArgsRejectsGitFilterAfterDiff(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--changed-diff", "--staged"})
	if err == nil {
		t.Fatal("expected error for git filter after --diff")
	}
	if !strings.Contains(err.Error(), "--staged must come before --changed-diff in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "git change filters are not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsAllowsRecentAfterDiff(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--changed-diff", "--recent", "5"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if !scope.Diff || !scope.Changed {
		t.Fatalf("unexpected parsed scope: %+v", scope)
	}
	if len(scope.Stages) == 0 || scope.Stages[len(scope.Stages)-1].Kind != command.StageRecent {
		t.Fatalf("expected trailing recent stage, got %+v", scope.Stages)
	}
}

func TestParseArgsRejectsDiffWithoutChangeSelector(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--diff"})
	if err == nil {
		t.Fatal("expected error for --diff without change selector")
	}
	if !strings.Contains(err.Error(), "--diff is no longer a standalone modifier") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsUntrackedDiffAlone(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--untracked", "--changed-diff"})
	if err == nil {
		t.Fatal("expected error for --untracked --diff")
	}
	if !strings.Contains(err.Error(), "--untracked-diff doesn't make sense") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsContainsEqualsForm(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--contains=TODO"})
	if err == nil {
		t.Fatal("expected error for --contains=TODO")
	}
	if !strings.Contains(err.Error(), "--contains requires a space") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsWarnsAboutGlobLikeContainsPattern(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"src", "--contains", "use*Context"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if len(cfg.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(cfg.Warnings))
	}
}

func TestParseArgsRejectsExtraContainsValueAfterModifierMode(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--contains", "TODO", "extra"})
	if err == nil {
		t.Fatal("expected error for extra plain token after --contains")
	}
	// --contains is a regex, so an extra bare token gets the quote hint (the
	// usual cause is an unquoted regex with spaces split by the shell).
	if !strings.Contains(err.Error(), "quote it if it contains spaces") || !strings.Contains(err.Error(), "--contains 'TODO extra'") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsPlainTokenAfterZeroArgModifier(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--changed", "extra"})
	if err == nil {
		t.Fatal("expected error for plain token after zero-arg modifier")
	}
	if !strings.Contains(err.Error(), "--changed takes no value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsBareDoubleDashAsPositionalDelimiter(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--", "other"})
	if err == nil {
		t.Fatal("expected bare -- delimiter usage to fail")
	}
	if !strings.Contains(err.Error(), "bare -- can only be followed by another bare -- in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsTrailingBareDoubleDashOutsideInteractive(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--"})
	if err == nil {
		t.Fatal("expected trailing bare -- to fail outside startup interactive flow")
	}
	if !strings.Contains(err.Error(), "opens interactive modifier selection") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "headless mode") {
		t.Fatalf("non-headless invocation should not mention headless mode: %v", err)
	}
}

func TestParseArgsRejectsTrailingBareDoubleDashInHeadlessMode(t *testing.T) {
	_, err := cli.ParseArgs([]string{".", "--headless", "--"})
	if err == nil {
		t.Fatal("expected trailing bare -- to fail under --headless")
	}
	if !strings.Contains(err.Error(), "headless mode") {
		t.Fatalf("expected headless-specific error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--headless") {
		t.Fatalf("expected error to mention --headless, got: %v", err)
	}
	if strings.Contains(err.Error(), "(-p -q)") || strings.Contains(err.Error(), "(-q -p)") {
		t.Fatalf("error should not reference -p -q anymore, got: %v", err)
	}
}

func TestParseArgsHeadlessImpliesStdoutAndQuiet(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{".", "--headless"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !cfg.Headless {
		t.Fatal("expected cfg.Headless to be true")
	}
	if cfg.OutputMode != command.OutputModeStdout {
		t.Fatalf("expected OutputMode=stdout, got %q", cfg.OutputMode)
	}
	if !cfg.Quiet {
		t.Fatal("expected cfg.Quiet to be true")
	}
}

func TestParseArgsHeadlessRequiresExplicitTargets(t *testing.T) {
	_, err := cli.ParseArgs([]string{"--headless"})
	if err == nil {
		t.Fatal("expected --headless without targets to fail")
	}
	if !strings.Contains(err.Error(), "--headless requires explicit targets") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := cli.ParseArgs([]string{".", "--headless"}); err != nil {
		t.Fatalf("--headless with explicit target should succeed, got: %v", err)
	}
}

func TestParseArgsHeadlessPreviewRequiresTargets(t *testing.T) {
	_, err := cli.ParseArgs([]string{"--preview", "--headless"})
	if err == nil {
		t.Fatal("expected --preview --headless without targets to fail")
	}
	if !strings.Contains(err.Error(), "--headless requires explicit targets") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := cli.ParseArgs([]string{".", "--preview", "--headless"}); err != nil {
		t.Fatalf("--preview --headless with explicit target should succeed, got: %v", err)
	}
}

func TestRunHeadlessRejectsBareDoubleDash(t *testing.T) {
	_, err := cli.ParseArgs([]string{".", "--headless", "--"})
	if err == nil {
		t.Fatal("expected --headless . -- to fail")
	}
	if !strings.Contains(err.Error(), "headless mode") {
		t.Fatalf("expected headless error, got: %v", err)
	}
}

func TestRunHeadlessRejectsFuzzyAmbiguity(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a/Button.tsx": "export const A = 1\n",
		"src/b/Banner.tsx": "export const B = 2\n",
		"docs/notes.md":    "notes\n",
	})

	cfg := parseInProject(t, project, []string{"bn", "--headless"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected fuzzy ambiguity to error under --headless\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}
	msg := err.Error() + stderr.String()
	if !strings.Contains(msg, "Multiple") {
		t.Fatalf("expected ambiguity error, got: err=%v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(msg, "--headless") {
		t.Fatalf("expected error to mention --headless, got: err=%v stderr=%s", err, stderr.String())
	}
}

func TestRunPrintQuietAllowsBareDoubleDash(t *testing.T) {
	if !platform.CanPromptInteractively() {
		t.Skip("interactive terminal not available; bare -- test requires TTY for picker")
	}
	_, err := cli.ParseArgs([]string{".", "-p", "-q", "--"})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "headless mode") {
		t.Fatalf("-p -q should not produce a headless error, got: %v", err)
	}
}

func TestInternalCommandRetiredTreePayloadMessageNotShadowed(t *testing.T) {
	_, err := cli.ParseArgs([]string{"--internal-tree-payload"})
	if err == nil {
		t.Fatal("expected retired --internal-tree-payload to fail")
	}
	if !strings.Contains(err.Error(), "Unknown option '--internal-tree-payload'") {
		t.Fatalf("expected retired flag error, got: %v", err)
	}
	if strings.Contains(err.Error(), "--headless") {
		t.Fatalf("internal error should not mention --headless, got: %v", err)
	}
}

func TestRunAppliesMultiValueOnlyExcludeStages(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "export const a = 1\n",
		"src/b.ts": "export const b = 2\n",
		"src/c.ts": "export const c = 3\n",
	})
	cfg := parseInProject(t, project, []string{"-p", "src", "--only", "a.ts", "b.ts", "--exclude", "b.ts"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/a.ts">`) {
		t.Fatalf("expected src/a.ts in output, got %q", out)
	}
	if strings.Contains(out, `src/b.ts`) || strings.Contains(out, `src/c.ts`) {
		t.Fatalf("unexpected extra file in output: %q", out)
	}
}

func TestRunTreatsRepeatedOnlyAsSequentialStages(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "export const a = 1\n",
		"src/b.ts": "export const b = 2\n",
	})
	cfg := parseInProject(t, project, []string{"-p", "src", "--only", "a.ts", "b.ts", "--exclude", "b.ts", "--only", "b.ts"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected no-match exit error after sequential stages removed all files")
	}

	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout after sequential stages removed all files, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "No text files found matching your criteria.") {
		t.Fatalf("expected no-match stderr, got %q", stderr.String())
	}
}

func TestRunRecentReordersOutputByNewestFirst(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"alpha.txt": "alpha\n",
		"beta.txt":  "beta\n",
		"gamma.txt": "gamma\n",
	})

	now := time.Now()
	for relPath, modTime := range map[string]time.Time{
		"alpha.txt": now.Add(-3 * time.Hour),
		"beta.txt":  now.Add(-1 * time.Hour),
		"gamma.txt": now.Add(-2 * time.Hour),
	} {
		absPath := filepath.Join(project, relPath)
		if err := os.Chtimes(absPath, modTime, modTime); err != nil {
			t.Fatalf("chtimes %s failed: %v", relPath, err)
		}
	}

	cfg := parseInProject(t, project, []string{"-q", "-p", ".", "--only", "*.txt", "--recent"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	positions := []int{
		strings.Index(out, `<file path="beta.txt">`),
		strings.Index(out, `<file path="gamma.txt">`),
		strings.Index(out, `<file path="alpha.txt">`),
	}
	for i, pos := range positions {
		if pos < 0 {
			t.Fatalf("expected file marker %d in output, got:\n%s", i, out)
		}
	}
	if !(positions[0] < positions[1] && positions[1] < positions[2]) {
		t.Fatalf("expected newest-first output order beta, gamma, alpha; got:\n%s", out)
	}
}

func TestRunSizeFiltersAndOrdersByLargestFirst(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"small.txt": "small\n",
		"one.txt":   strings.Repeat("a", 1024),
		"two.txt":   strings.Repeat("b", 1536),
		"big.txt":   strings.Repeat("c", 4096),
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--only", "*.txt", "--size", "1", "2", "--paths"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if got, want := strings.TrimSpace(stdout.String()), "two.txt\none.txt"; got != want {
		t.Fatalf("size-filtered paths = %q, want %q", got, want)
	}
}

func TestRunPreviewSizePreservesLargestFirstOrder(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"aaa-small.txt":  "small\n",
		"mmm-medium.txt": strings.Repeat("m", 1024),
		"zzz-big.txt":    strings.Repeat("b", 2048),
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--preview", ".", "--only", "*.txt", "--size"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	big := strings.Index(out, "zzz-big.txt")
	medium := strings.Index(out, "mmm-medium.txt")
	small := strings.Index(out, "aaa-small.txt")
	if big < 0 || medium < 0 || small < 0 {
		t.Fatalf("preview missing expected files:\n%s", out)
	}
	if !(big < medium && medium < small) {
		t.Fatalf("--preview --size should preserve largest-first order, got:\n%s", out)
	}
}

func TestRunThenRecentPreservesPerScopeOrder(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.txt": "a\n",
		"src/b.txt": "b\n",
		"src/c.txt": "c\n",
		"src/d.txt": "d\n",
		"lib/w.txt": "w\n",
		"lib/x.txt": "x\n",
		"lib/y.txt": "y\n",
		"lib/z.txt": "z\n",
	})

	now := time.Now()
	for relPath, modTime := range map[string]time.Time{
		"src/b.txt": now.Add(-1 * time.Hour),
		"src/c.txt": now.Add(-2 * time.Hour),
		"src/a.txt": now.Add(-3 * time.Hour),
		"src/d.txt": now.Add(-4 * time.Hour),
		"lib/y.txt": now.Add(-5 * time.Hour),
		"lib/z.txt": now.Add(-6 * time.Hour),
		"lib/x.txt": now.Add(-7 * time.Hour),
		"lib/w.txt": now.Add(-8 * time.Hour),
	} {
		absPath := filepath.Join(project, relPath)
		if err := os.Chtimes(absPath, modTime, modTime); err != nil {
			t.Fatalf("chtimes %s failed: %v", relPath, err)
		}
	}

	cfg := parseInProject(t, project, []string{
		"-q", "-p",
		"src", "--only", "*.txt", "--recent", "3",
		"--then",
		"lib", "--only", "*.txt", "--recent", "3",
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	assertFileMarkerOrder(t, stdout.String(), []string{
		"src/b.txt",
		"src/c.txt",
		"src/a.txt",
		"lib/y.txt",
		"lib/z.txt",
		"lib/x.txt",
	})
}

func TestRunThenRecentOverlapKeepsFirstScopePosition(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"top.txt":        "top\n",
		"second.txt":     "second\n",
		"lib/shared.txt": "shared\n",
		"lib/older.txt":  "older\n",
		"lib/oldest.txt": "oldest\n",
	})

	now := time.Now()
	for relPath, modTime := range map[string]time.Time{
		"top.txt":        now.Add(-1 * time.Hour),
		"lib/shared.txt": now.Add(-2 * time.Hour),
		"second.txt":     now.Add(-3 * time.Hour),
		"lib/older.txt":  now.Add(-4 * time.Hour),
		"lib/oldest.txt": now.Add(-5 * time.Hour),
	} {
		absPath := filepath.Join(project, relPath)
		if err := os.Chtimes(absPath, modTime, modTime); err != nil {
			t.Fatalf("chtimes %s failed: %v", relPath, err)
		}
	}

	cfg := parseInProject(t, project, []string{
		"-q", "-p",
		".", "--only", "*.txt", "--recent", "3",
		"--then",
		"lib", "--only", "*.txt", "--recent", "3",
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if got := strings.Count(out, `<file path="lib/shared.txt">`); got != 1 {
		t.Fatalf("expected lib/shared.txt once after overlap dedupe, got %d copies:\n%s", got, out)
	}
	assertFileMarkerOrder(t, out, []string{
		"top.txt",
		"lib/shared.txt",
		"second.txt",
		"lib/older.txt",
		"lib/oldest.txt",
	})
}

func assertFileMarkerOrder(t *testing.T, out string, paths []string) {
	t.Helper()

	last := -1
	for _, relPath := range paths {
		marker := `<file path="` + relPath + `">`
		pos := strings.Index(out, marker)
		if pos < 0 {
			t.Fatalf("expected %s in output, got:\n%s", relPath, out)
		}
		if pos <= last {
			t.Fatalf("expected %s after previous marker, got:\n%s", relPath, out)
		}
		last = pos
	}
}

func TestRunIncludeSupportsGitignoreOutsideGitRepo(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":   "blocked/\n",
		"blocked/a.ts": "export const blocked = true\n",
		"src/main.ts":  "export const main = true\n",
	})

	cfg := parseInProject(t, project, []string{".", "--quiet", "--print", "--include", "blocked", "--only", "blocked/a.ts"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="blocked/a.ts">`) {
		t.Fatalf("expected output to include gitignored file outside git repo, got:\n%s", out)
	}
}

func TestParseArgsImmediateActionsReturnEarly(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"--version", "src", "--changed"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if cfg.Action != command.ActionVersion {
		t.Fatalf("expected version action, got %q", cfg.Action)
	}
	if got := len(parsedExecutionScopes(t, cfg)); got != 0 {
		t.Fatalf("expected no scopes for immediate action, got %d", got)
	}
}

func TestParseArgsHissResetStillParsesGlobalFlags(t *testing.T) {
	cfg, err := cli.ParseArgs([]string{"--hiss-reset", "--yes", "--quiet"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if cfg.Action != command.ActionResetHiss {
		t.Fatalf("expected hiss-reset action, got %q", cfg.Action)
	}
	if !cfg.Yes {
		t.Fatal("expected --yes to be preserved for hiss-reset")
	}
	if !cfg.Quiet {
		t.Fatal("expected --quiet to be preserved for hiss-reset")
	}
}

func TestRunPrintsSortedTaggedFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"b.txt": "second\n",
		"a.txt": "first\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "<file path=\"a.txt\">\nfirst\n</file>\n\n") {
		t.Fatalf("expected a.txt in output, got:\n%s", out)
	}
	if !strings.Contains(out, "<file path=\"b.txt\">\nsecond\n</file>\n\n") {
		t.Fatalf("expected b.txt in output, got:\n%s", out)
	}
	if strings.Index(out, "a.txt") > strings.Index(out, "b.txt") {
		t.Fatalf("expected sorted output, got:\n%s", out)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "Count:") || !strings.Contains(errOut, "Size:") || !strings.Contains(errOut, "Tokens:") {
		t.Fatalf("expected stderr summary, got:\n%s", errOut)
	}
	if !strings.Contains(errOut, "a.txt") || !strings.Contains(errOut, "b.txt") {
		t.Fatalf("expected stderr tree for --print, got:\n%s", errOut)
	}
}

func TestRunDepthFiltersByWorkingDirectoryPathDepth(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"README.md":                 "readme\n",
		"src/main.ts":               "main\n",
		"src/components/Button.tsx": "button\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--depth", "2"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="README.md">`) {
		t.Fatalf("expected README.md in output, got:\n%s", out)
	}
	if !strings.Contains(out, `<file path="src/main.ts">`) {
		t.Fatalf("expected src/main.ts in output, got:\n%s", out)
	}
	if strings.Contains(out, `src/components/Button.tsx`) {
		t.Fatalf("expected deep file to be filtered out, got:\n%s", out)
	}
}

func TestRunDepthRejectsValuesGreaterThanCurrentScopeMaxDepth(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"README.md":                 "readme\n",
		"src/components/Button.tsx": "button\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--depth", "4"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected run to reject out-of-range depth")
	}
	if !strings.Contains(err.Error(), "current scope max depth 3") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPathsEmitsBareRelativePaths(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts":        "a\n",
		"src/nested/b.ts": "b\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--paths"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := "src/a.ts\nsrc/nested/b.ts\n"
	if got := stdout.String(); got != want {
		t.Fatalf("unexpected paths output\nwant:\n%s\ngot:\n%s", want, got)
	}
	if strings.Contains(stdout.String(), "<file path=") {
		t.Fatalf("did not expect wrapped file output, got:\n%s", stdout.String())
	}
}

func TestRunRawEmitsExactSingleFileBody(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"VERSION": "0.4.1",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "VERSION", "--raw"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if got := stdout.String(); got != "0.4.1" {
		t.Fatalf("unexpected raw output\nwant:\n%s\ngot:\n%s", "0.4.1", got)
	}
	if strings.Contains(stdout.String(), "<file path=") {
		t.Fatalf("did not expect wrapped file output, got:\n%s", stdout.String())
	}
}

func TestRunRawConcatenatesMultipleFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "alpha\n",
		"b.txt": "beta\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "a.txt", "b.txt", "--raw"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := "alpha\nbeta\n"
	if got := stdout.String(); got != want {
		t.Fatalf("unexpected raw concat\nwant:\n%s\ngot:\n%s", want, got)
	}
	if strings.Contains(stdout.String(), "<file path=") {
		t.Fatalf("did not expect wrapped file output, got:\n%s", stdout.String())
	}
}

func TestRunRawWithLinesStripsNumbers(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go": "line1\nline2\nline3\nline4\nline5\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "main.go", "--lines", "2", "4", "--raw"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := "line2\nline3\nline4\n"
	if got := stdout.String(); got != want {
		t.Fatalf("unexpected raw lines output\nwant:\n%s\ngot:\n%s", want, got)
	}
	if strings.Contains(stdout.String(), "<file path=") {
		t.Fatalf("did not expect wrapped file output, got:\n%s", stdout.String())
	}
	for _, prefix := range []string{"2:", "3:", "4:", "  2 ", "  3 ", "  4 "} {
		if strings.Contains(stdout.String(), prefix) {
			t.Fatalf("did not expect line-number prefix %q, got:\n%s", prefix, stdout.String())
		}
	}
}

func TestRunRawMultiFileWithLines(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "a1\na2\na3\na4\n",
		"b.txt": "b1\nb2\nb3\nb4\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "a.txt", "b.txt", "--lines", "2", "3", "--raw"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := "a2\na3\nb2\nb3\n"
	if got := stdout.String(); got != want {
		t.Fatalf("unexpected raw multi-lines output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunRawAllowedWithoutPrint(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"VERSION": "0.4.2\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "VERSION", "--raw"})

	if !cfg.Raw {
		t.Fatal("expected Raw=true")
	}
	if cfg.OutputMode != command.OutputModeClipboard {
		t.Fatalf("expected OutputMode=clipboard, got %q", cfg.OutputMode)
	}
}

func TestRunRawRejectsPathsOutput(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go": "package main\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "main.go", "--paths", "--raw"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected raw mode to reject --paths")
	}
	if !strings.Contains(err.Error(), "--raw cannot be combined with --paths") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRawRejectsSnippetOutput(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go": "TODO\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "main.go", "--snippet", "TODO", "--raw"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected raw mode to reject snippet output")
	}
	if !strings.Contains(err.Error(), "--raw cannot be combined with snippet output") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "multiple regex-derived ranges per file") ||
		!strings.Contains(err.Error(), `<file ... lines="..."> wrappers`) {
		t.Fatalf("expected snippet/raw ambiguity reason, got: %v", err)
	}
}

func TestRunRawRejectsDiffOutput(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go": "before\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "main.go", "after\n")

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "main.go", "--changed-diff", "--raw"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected raw mode to reject diff output")
	}
	if !strings.Contains(err.Error(), "--raw cannot be combined with diff output") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPreviewIgnoresRawOutputFraming(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"VERSION": "0.4.1\n",
	})

	baseCfg := parseInProject(t, project, []string{"--preview", "--quiet", "VERSION"})
	rawCfg := parseInProject(t, project, []string{"--preview", "--quiet", "VERSION", "--raw"})

	var baseStdout, baseStderr bytes.Buffer
	if err := run(baseCfg, &baseStdout, &baseStderr); err != nil {
		t.Fatalf("base preview run returned error: %v", err)
	}

	var rawStdout, rawStderr bytes.Buffer
	if err := run(rawCfg, &rawStdout, &rawStderr); err != nil {
		t.Fatalf("raw preview run returned error: %v", err)
	}

	if rawStdout.String() != baseStdout.String() {
		t.Fatalf("expected raw preview output to match normal preview\nwant:\n%s\ngot:\n%s", baseStdout.String(), rawStdout.String())
	}
	if rawStderr.String() != baseStderr.String() {
		t.Fatalf("expected raw preview stderr to match normal preview\nwant:\n%s\ngot:\n%s", baseStderr.String(), rawStderr.String())
	}
}

func TestRunLinesAddsLineNumbers(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"hello.txt": "alpha\nbeta\ngamma\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "hello.txt", "--lines"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "<file path=\"hello.txt\">") {
		t.Fatalf("expected file open tag without lines attribute, got:\n%s", got)
	}
	if !strings.Contains(got, "     1\talpha\n") {
		t.Fatalf("expected numbered line 1, got:\n%s", got)
	}
	if !strings.Contains(got, "     3\tgamma\n") {
		t.Fatalf("expected numbered line 3, got:\n%s", got)
	}
}

func TestRunLinesRangeSlice(t *testing.T) {
	lines := ""
	for i := 1; i <= 20; i++ {
		lines += fmt.Sprintf("line %d\n", i)
	}
	project := setupTestProject(t, map[string]string{
		"data.txt": lines,
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "data.txt", "--lines", "5", "10"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, `lines="5-10"`) {
		t.Fatalf("expected lines attribute in tag, got:\n%s", got)
	}
	if !strings.Contains(got, "     5\tline 5\n") {
		t.Fatalf("expected numbered line 5, got:\n%s", got)
	}
	if !strings.Contains(got, "    10\tline 10\n") {
		t.Fatalf("expected numbered line 10, got:\n%s", got)
	}
	if strings.Contains(got, "line 4\n") {
		t.Fatalf("should not contain line 4, got:\n%s", got)
	}
	if strings.Contains(got, "line 11\n") {
		t.Fatalf("should not contain line 11, got:\n%s", got)
	}
}

func TestRunLinesOpenEndedRange(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"data.txt": "a\nb\nc\nd\ne\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "data.txt", "--lines", "3"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, `lines="3-"`) {
		t.Fatalf("expected open-ended lines attribute, got:\n%s", got)
	}
	if !strings.Contains(got, "     3\tc\n") {
		t.Fatalf("expected line 3, got:\n%s", got)
	}
	if !strings.Contains(got, "     5\te\n") {
		t.Fatalf("expected line 5, got:\n%s", got)
	}
	if strings.Contains(got, "     1\t") || strings.Contains(got, "     2\t") {
		t.Fatalf("should not contain lines before start, got:\n%s", got)
	}
}

// Files where the requested start line exceeds the file length emit
// zero bytes — no open tag, no close tag. Matches sed/awk/Python slicing
// conventions and keeps multi-file --lines output free of empty wrappers.
// The lines picker preview path inherits this via output.EmitOutputPlan.
func TestRunLinesSliceDropsFilesShorterThanStart(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"short.txt": "a\nb\nc\n",                            // 3 lines
		"long.txt":  "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n", // 12 lines
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "short.txt", "long.txt", "--lines", "5", "10"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	got := stdout.String()
	if strings.Contains(got, `<file path="short.txt"`) {
		t.Fatalf("expected short.txt to be dropped (no lines in range 5-10), got:\n%s", got)
	}
	if !strings.Contains(got, `<file path="long.txt" lines="5-10">`) {
		t.Fatalf("expected long.txt with lines=\"5-10\", got:\n%s", got)
	}
	if !strings.Contains(got, "     5\te\n") || !strings.Contains(got, "    10\tj\n") {
		t.Fatalf("expected long.txt lines 5-10 in body, got:\n%s", got)
	}
}

// Files too short for the requested --lines range must be absent from
// the tree and the file count, not just from the emit body. v0.5.2's
// short-file work suppressed the <file> wrapper at emit time but left
// the unit in the plan, so the preview tree still listed the file as
// "(0B) [lines N-M]" and counted it. See
// docs/versions/v0.5.3/reports/ACTIVE_BUG_lines_filter_leak.md.
func TestRunLinesSliceDropsShortFilesFromTreeAndCount(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"short.txt": "a\nb\nc\n",                            // 3 lines
		"long.txt":  "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n", // 12 lines
	})

	// Non-quiet so the tree + summary render to stderr.
	cfg := parseInProject(t, project, []string{"--print", "short.txt", "long.txt", "--lines", "5", "10"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	tree := stderr.String()
	if strings.Contains(tree, "short.txt") {
		t.Fatalf("short.txt should be absent from the tree (no lines in range 5-10), got:\n%s", tree)
	}
	if !strings.Contains(tree, "long.txt") {
		t.Fatalf("long.txt should be present in the tree, got:\n%s", tree)
	}
	if !strings.Contains(tree, "Count:   1 file") {
		t.Fatalf("expected count of exactly 1 file (long.txt only), got:\n%s", tree)
	}
	if strings.Contains(tree, "(0B)") {
		t.Fatalf("no entry should render as (0B) — short files are dropped, got:\n%s", tree)
	}
}

func TestRunLinesRawMode(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"data.txt": "a\nb\nc\nd\ne\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "--raw", "data.txt", "--lines", "2", "4"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	got := stdout.String()
	want := "b\nc\nd\n"
	if got != want {
		t.Fatalf("unexpected raw lines output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestParseArgsLinesRejectsZeroStart(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--lines", "0"})
	if err == nil {
		t.Fatal("expected error for --lines 0")
	}
	if !strings.Contains(err.Error(), "--lines start must be >= 1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsEndLessThanStart(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--lines", "10", "5"})
	if err == nil {
		t.Fatal("expected error for --lines 10 5")
	}
	if !strings.Contains(err.Error(), "--lines end (5) must be >= start (10)") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsNonIntegerStart(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--lines", "abc"})
	if err == nil {
		t.Fatal("expected error for --lines abc")
	}
	if !strings.Contains(err.Error(), "--lines expects line numbers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsNonIntegerEnd(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--lines", "10", "abc"})
	if err == nil {
		t.Fatal("expected error for --lines 10 abc")
	}
	if !strings.Contains(err.Error(), "--lines expects line numbers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsWithSnippet(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--lines", "--snippet", "a"})
	if err == nil {
		t.Fatal("expected error for --lines --snippet")
	}
	if !strings.Contains(err.Error(), "--lines finalizes the current scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsWithPaths(t *testing.T) {
	_, err := cli.ParseArgs([]string{"src", "--lines", "--paths"})
	if err == nil {
		t.Fatal("expected error for --lines --paths")
	}
	if !strings.Contains(err.Error(), "--lines finalizes the current scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPathsThenFilesSeparatesKindsWithSingleBlankLine(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts":  "a\n",
		"docs/x.md": "doc\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--paths", "--then", "docs"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := "src/a.ts\n\n<file path=\"docs/x.md\">\ndoc\n</file>\n\n"
	if got := stdout.String(); got != want {
		t.Fatalf("unexpected mixed output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunPreviewPathAndFileSummaryUsesCombinedPayloadStats(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go": "package main\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "--quiet", "--headless", "main.go", "--paths", "--then", "main.go"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	// One file appearing as both a path and a full file: combined per-file size,
	// shape "path + file". (The modified date varies, so we don't exact-match the row.)
	recs := parsePreviewRecordsRoot(t, stdout.String())
	m, ok := recs["main.go"]
	if !ok {
		t.Fatalf("missing main.go record in:\n%s", stdout.String())
	}
	if m[0] != "21.00B" || m[1] != "~5" || m[4] != "path + file" {
		t.Fatalf("combined stats wrong: size=%q tokens=%q shape=%q\n%s", m[0], m[1], m[4], stdout.String())
	}
	// Footer uses the shared summary language and the combined totals.
	for _, want := range []string{"Count:   1 item", "Size:    21.00B", "Tokens:  ~5"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("footer missing %q in:\n%s", want, stdout.String())
		}
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected quiet preview to keep stderr empty, got:\n%s", got)
	}
}

func TestRunAdjacentPathScopesCoalesceAndDedupe(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts":        "a\n",
		"src/nested/b.ts": "b\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--paths", "--then", "src/nested", "--paths"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := "src/a.ts\nsrc/nested/b.ts\n"
	if got := stdout.String(); got != want {
		t.Fatalf("unexpected adjacent path output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunPathsDoNotDedupeAgainstLaterFileSections(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "a\n",
		"src/b.ts": "b\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--paths", "--then", "src", "--only", "a.ts"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.HasPrefix(out, "src/a.ts\nsrc/b.ts\n\n") {
		t.Fatalf("expected path section first, got:\n%s", out)
	}
	if !strings.Contains(out, "<file path=\"src/a.ts\">\na\n</file>\n\n") {
		t.Fatalf("expected later file section to keep overlapping file content, got:\n%s", out)
	}
}

func TestRunPathsThenFilesThenPathsSeparatesOnEachKindChange(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts":    "a\n",
		"app/main.ts": "main\n",
		"docs/x.md":   "doc\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--paths", "--then", "app", "--then", "docs", "--paths"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := "src/a.ts\n\n<file path=\"app/main.ts\">\nmain\n</file>\n\ndocs/x.md\n"
	if got := stdout.String(); got != want {
		t.Fatalf("unexpected paths/files/paths output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunFilesThenPathsThenFilesSeparatesOnEachKindChange(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"app/main.ts": "main\n",
		"docs/x.md":   "doc\n",
		"pkg/a.ts":    "test\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "app", "--then", "docs", "--paths", "--then", "pkg"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := "<file path=\"app/main.ts\">\nmain\n</file>\n\ndocs/x.md\n\n<file path=\"pkg/a.ts\">\ntest\n</file>\n\n"
	if got := stdout.String(); got != want {
		t.Fatalf("unexpected files/paths/files output\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestEmitWrappedReaderAddsTrailingNewlineBeforeFooter(t *testing.T) {
	var out bytes.Buffer
	if err := output.EmitWrappedReader(&out, "plain.txt", "", strings.NewReader("hello")); err != nil {
		t.Fatalf("output.EmitWrappedReader returned error: %v", err)
	}

	want := "<file path=\"plain.txt\">\nhello\n</file>\n\n"
	if out.String() != want {
		t.Fatalf("unexpected streamed output:\n%s", out.String())
	}
}

func TestEmitWrappedReaderReturnsPathAwareStreamError(t *testing.T) {
	var out bytes.Buffer
	err := output.EmitWrappedReader(&out, "broken.txt", "", &errAfterReader{
		data: []byte("abc"),
		err:  errors.New("boom"),
	})
	if err == nil {
		t.Fatal("expected streaming error")
	}
	if !strings.Contains(err.Error(), "broken.txt") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected path-aware streaming error, got: %v", err)
	}
	if !strings.Contains(out.String(), "<file path=\"broken.txt\">\nabc") {
		t.Fatalf("expected partial output before failure, got:\n%s", out.String())
	}
}

func TestEmitFullOutputPrefetchMatchesSequential(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "first\n",
		"b.txt": "second\n",
		"c.txt": "third\n",
	})

	entries := []discovery.Entry{
		{RelPath: "a.txt", AbsPath: filepath.Join(project, "a.txt")},
		{RelPath: "b.txt", AbsPath: filepath.Join(project, "b.txt")},
		{RelPath: "c.txt", AbsPath: filepath.Join(project, "c.txt")},
	}
	units, err := output.PrepareFileUnits(git.Context{}, entries)
	if err != nil {
		t.Fatalf("output.PrepareFileUnits returned error: %v", err)
	}

	cfg := output.EmitConfig{OutputMode: command.OutputModeStdout}

	var sequential bytes.Buffer
	if _, err := output.EmitFullOutput(cfg, output.EmitEnvironment{}, units, &sequential, platform.Palette{}); err != nil {
		t.Fatalf("sequential output.EmitFullOutput returned error: %v", err)
	}

	t.Setenv("CATCLIP_READ_WORKERS", "2")
	t.Setenv("CATCLIP_PREFETCH_FILE_KIB", "64")

	var prefetched bytes.Buffer
	if _, err := output.EmitFullOutput(cfg, output.EmitEnvironment{}, units, &prefetched, platform.Palette{}); err != nil {
		t.Fatalf("prefetch output.EmitFullOutput returned error: %v", err)
	}

	if prefetched.String() != sequential.String() {
		t.Fatalf("prefetch output mismatch:\nwant:\n%s\n\ngot:\n%s", sequential.String(), prefetched.String())
	}
}

func TestRunQuietPrintSuppressesDiagnostics(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "first\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), `<file path="a.txt">`) {
		t.Fatalf("expected payload on stdout, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected quiet print to suppress stderr diagnostics, got:\n%s", stderr.String())
	}
}

func TestRunAppliesDefaultIgnoredDirectories(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":   "console.log('ok')\n",
		"tests/main.ts": "console.log('ignored')\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "tests/main.ts") {
		t.Fatalf("expected tests/ to be ignored by default, got:\n%s", out)
	}
	if !strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected src/main.ts in output, got:\n%s", out)
	}
}

func TestRunBlockedDirectoryRequiresInclude(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":   "console.log('ok')\n",
		"tests/main.ts": "console.log('test')\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "tests"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected blocked directory without --include to fail")
	}
	if strings.Contains(stdout.String(), "tests/main.ts") {
		t.Fatalf("expected blocked directory to stay excluded, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Use --include to allow it for this run") {
		t.Fatalf("expected --include guidance, got:\n%s", stderr.String())
	}
}

func TestRunIncludeAllowsBlockedDirectory(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":   "console.log('ok')\n",
		"tests/main.ts": "console.log('test')\n",
	})

	cfg := parseInProject(t, project, []string{".", "--print", "--include", "tests"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "tests/main.ts") {
		t.Fatalf("expected --include to allow tests/, got:\n%s", stdout.String())
	}
	if strings.Contains(stderr.String(), "bypassing ignore rule") {
		t.Fatalf("expected no bypass warning, got:\n%s", stderr.String())
	}
}

func TestRunIncludeWildcardBypassesAllIgnoreRules(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":             "console.log('ok')\n",
		"tests/main.ts":           "console.log('test')\n",
		"node_modules/lib/idx.js": "module.exports = {}\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--include", "*", "--paths"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected src/main.ts in output, got:\n%s", out)
	}
	if !strings.Contains(out, "tests/main.ts") {
		t.Fatalf("expected tests/main.ts (normally ignored by .hiss) in output, got:\n%s", out)
	}
	if !strings.Contains(out, "node_modules/lib/idx.js") {
		t.Fatalf("expected node_modules/lib/idx.js (normally ignored by .hiss) in output, got:\n%s", out)
	}
}

func TestRunIncludeScopedToTargetsSkipsOutOfScopeDir(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":              "console.log('ok')\n",
		"lib/util.ts":              "export const x = 1\n",
		"vendor/lodash/index.js":   "module.exports = {}\n",
		"src/vendor/local/util.js": "module.exports = {}\n",
	})

	// "vendor" with target "src" should resolve to "src/vendor", not root "vendor"
	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--include", "vendor", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "src/vendor/local/util.js") {
		t.Fatalf("expected scoped --include to find src/vendor/, got:\n%s", out)
	}
	if strings.Contains(out, "vendor/lodash") {
		t.Fatalf("expected scoped --include to NOT find root vendor/, got:\n%s", out)
	}
}

func TestRunIncludeRootTargetAllowsAnyInclude(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":            "console.log('ok')\n",
		"vendor/lodash/index.js": "module.exports = {}\n",
	})

	// With target ".", include "vendor" should work at root level
	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--include", "vendor", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "vendor/lodash/index.js") {
		t.Fatalf("expected root-scoped --include to find vendor/, got:\n%s", out)
	}
}

func TestRunIncludeAnchoredPathScopedToTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":              "console.log('ok')\n",
		"src/vendor/local/util.js": "module.exports = {}\n",
		"lib/vendor/other.js":      "module.exports = {}\n",
	})

	// Anchored include "src/vendor" should work when target is "src"
	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--include", "src/vendor", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "src/vendor/local/util.js") {
		t.Fatalf("expected anchored --include within target to work, got:\n%s", out)
	}
	if strings.Contains(out, "lib/vendor") {
		t.Fatalf("expected anchored --include to NOT find lib/vendor/, got:\n%s", out)
	}
}

func TestRunIncludeWildcardScopedToTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":            "console.log('ok')\n",
		"src/tests/test.ts":      "test('ok', () => {})\n",
		"vendor/lodash/index.js": "module.exports = {}\n",
	})

	// --include '*' with target "src" should only bypass ignore rules under src/
	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--include", "*", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected --include '*' to include src files, got:\n%s", out)
	}
	if !strings.Contains(out, "src/tests/test.ts") {
		t.Fatalf("expected --include '*' to bypass ignore rules under src/, got:\n%s", out)
	}
	if strings.Contains(out, "vendor/lodash") {
		t.Fatalf("expected --include '*' scoped to src to NOT include root vendor/, got:\n%s", out)
	}
}

func TestRunIncludeGlobPatternErrors(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})

	wd, _ := os.Getwd()
	_ = os.Chdir(project)
	defer os.Chdir(wd)

	_, err := cli.ParseArgs([]string{"--quiet", "--print", ".", "--include", "*.js"})
	if err == nil {
		t.Fatalf("expected error for --include glob pattern, got nil")
	}
	if !strings.Contains(err.Error(), "--include does not accept glob patterns") {
		t.Fatalf("expected glob pattern error, got: %v", err)
	}
}

func TestRunWithBinariesIncludesBinaryFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":     "console.log('ok')\n",
		"assets/icon.png": "\x89PNG\r\n\x1a\n\x00\x00\x00fake png data",
		"data/dump.bin":   "\x00\x01\x02binary data",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--with-binaries", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "assets/icon.png") {
		t.Fatalf("expected --with-binaries to include icon.png, got:\n%s", out)
	}
	if !strings.Contains(out, "data/dump.bin") {
		t.Fatalf("expected --with-binaries to include dump.bin, got:\n%s", out)
	}
	if !strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected --with-binaries to still include text files, got:\n%s", out)
	}
}

func TestRunWithBinariesNotSetExcludesBinaryFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":     "console.log('ok')\n",
		"assets/icon.png": "\x89PNG\r\n\x1a\n\x00\x00\x00fake png data",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "icon.png") {
		t.Fatalf("expected binary files to be excluded without --with-binaries, got:\n%s", out)
	}
	if !strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected text files to still appear, got:\n%s", out)
	}
}

func TestRunExcludesWindowsSystemFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.go": "package main\n",
		// UTF-16 LE BOM + bytes — Windows desktop.ini files are typically
		// UTF-16 with null bytes that truncate clip.exe mid-stream. The .ini
		// extension would otherwise force text classification.
		"desktop.ini": "\xFF\xFE[\x00.\x00S\x00h\x00e\x00l\x00l\x00]\x00",
		"Thumbs.db":   "\x00\x01\x02 thumbs cache",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "desktop.ini") {
		t.Fatalf("expected desktop.ini to be excluded, got:\n%s", out)
	}
	if strings.Contains(out, "Thumbs.db") {
		t.Fatalf("expected Thumbs.db to be excluded, got:\n%s", out)
	}
	if !strings.Contains(out, "src/main.go") {
		t.Fatalf("expected text files to still appear, got:\n%s", out)
	}
}

func TestRunWithBinariesErrorsWithContains(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})

	wd, _ := os.Getwd()
	_ = os.Chdir(project)
	defer os.Chdir(wd)

	_, err := cli.ParseArgs([]string{"--quiet", "--print", "--with-binaries", ".", "--contains", "pattern"})
	if err == nil {
		t.Fatalf("expected error for --with-binaries + --contains, got nil")
	}
	if !strings.Contains(err.Error(), "--with-binaries cannot be combined with --contains") {
		t.Fatalf("expected --with-binaries + --contains error, got: %v", err)
	}
}

func TestRunWithBinariesErrorsWithSnippet(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})

	wd, _ := os.Getwd()
	_ = os.Chdir(project)
	defer os.Chdir(wd)

	_, err := cli.ParseArgs([]string{"--quiet", "--print", "--with-binaries", ".", "--snippet", "pattern"})
	if err == nil {
		t.Fatalf("expected error for --with-binaries + --snippet, got nil")
	}
	if !strings.Contains(err.Error(), "--with-binaries cannot be combined with --snippet") {
		t.Fatalf("expected --with-binaries + --snippet error, got: %v", err)
	}
}

func TestRunWithBinariesErrorsWithDiff(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})

	wd, _ := os.Getwd()
	_ = os.Chdir(project)
	defer os.Chdir(wd)

	_, err := cli.ParseArgs([]string{"--quiet", "--print", "--with-binaries", ".", "--changed-diff"})
	if err == nil {
		t.Fatalf("expected error for --with-binaries + --changed-diff, got nil")
	}
	if !strings.Contains(err.Error(), "--with-binaries cannot be combined with diff") {
		t.Fatalf("expected --with-binaries + diff error, got: %v", err)
	}
}

func TestRunOnlyFromStdinMatchesExactNormalizedPaths(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/types.go":        "package src\n",
		"src/types.go.bak.ts": "export const backup = true\n",
		"src/space name.ts":   "export const spaced = true\n",
		"src/other.ts":        "export const other = true\n",
	})

	setTestPipeStdin(t, "src\\types.go\r\n\r\nsrc\\space name.ts\r\n")
	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--only", "-"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/types.go">`) || !strings.Contains(out, `<file path="src/space name.ts">`) {
		t.Fatalf("expected exact stdin-selected files in output, got:\n%s", out)
	}
	if strings.Contains(out, "src/types.go.bak") || strings.Contains(out, "src/other.ts") {
		t.Fatalf("expected stdin --only to match exact normalized paths only, got:\n%s", out)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected quiet stdin --only run to keep stderr empty, got:\n%s", got)
	}
}

func TestRunExcludeFromStdinMatchesExactPathsWithoutPrefixMatch(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/types.go":        "package src\n",
		"src/types.go.bak.ts": "export const backup = true\n",
		"src/keep.ts":         "export const keep = true\n",
	})

	setTestPipeStdin(t, "src/types.go\n")
	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--exclude", "-"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, `<file path="src/types.go">`) {
		t.Fatalf("expected exact stdin exclude path to be removed, got:\n%s", out)
	}
	if !strings.Contains(out, `<file path="src/types.go.bak.ts">`) || !strings.Contains(out, `<file path="src/keep.ts">`) {
		t.Fatalf("expected non-exact neighbors to remain after stdin exclude, got:\n%s", out)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected quiet stdin --exclude run to keep stderr empty, got:\n%s", got)
	}
}

func TestRunMatchesMultiSegmentDirectoryRulesWithGitignoreAnchoring(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"config/catclip/.hiss": "foo/bar/\n",
		"foo/bar/hidden.ts":    "hidden\n",
		"foo/baz/keep.ts":      "keep\n",
		"foo/barista/live.ts":  "live\n",
		"qux/foo/bar/deep.ts":  "deep\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "foo/bar/hidden.ts") {
		t.Fatalf("expected root-anchored foo/bar/ to be ignored, got:\n%s", out)
	}
	if !strings.Contains(out, "qux/foo/bar/deep.ts") {
		t.Fatalf("expected nested qux/foo/bar/ to remain visible (gitignore anchors mid-pattern slashes to root), got:\n%s", out)
	}
	if !strings.Contains(out, "foo/baz/keep.ts") || !strings.Contains(out, "foo/barista/live.ts") {
		t.Fatalf("expected unrelated paths to remain visible, got:\n%s", out)
	}
}

func TestRunAppliesOnlyAndExcludeFilters(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts":      "app\n",
		"src/app.test.ts": "test\n",
		"src/app.js":      "js\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "src", "--only", "*.ts", "--exclude", "*.test.ts"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "src/app.ts") {
		t.Fatalf("expected src/app.ts in output, got:\n%s", out)
	}
	if strings.Contains(out, "src/app.test.ts") || strings.Contains(out, "src/app.js") {
		t.Fatalf("expected filters to remove app.test.ts and app.js, got:\n%s", out)
	}
}

func TestRunExcludeBareNameMatchesFilesAndDirectorySegments(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"platform":                "root marker\n",
		"nested/platform/main.ts": "export const ok = true\n",
		"src/platform.ts":         "export const keep = true\n",
		"src/app.ts":              "export const app = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", ".", "--exclude", "platform"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "nested/platform/main.ts") {
		t.Fatalf("expected bare exclude name to remove platform subtree, got:\n%s", out)
	}
	if strings.Contains(out, "platform (") {
		t.Fatalf("expected bare exclude name to remove exact file basename matches, got:\n%s", out)
	}
	if !strings.Contains(out, "src/platform.ts") || !strings.Contains(out, "src/app.ts") {
		t.Fatalf("expected unrelated files to remain visible, got:\n%s", out)
	}
	errOut := stderr.String()
	if strings.Contains(errOut, `--exclude pattern 'platform' looks like a directory name`) {
		t.Fatalf("expected old directory warning to be gone, got:\n%s", errOut)
	}
	if strings.Contains(errOut, `Directory rules require a trailing slash`) {
		t.Fatalf("expected trailing slash warning to be gone, got:\n%s", errOut)
	}
}

func TestRunContainsFiltersContent(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "const ok = true\n",
		"src/b.ts": "TODO: fix this\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "src", "--contains", "TODO"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "src/a.ts") {
		t.Fatalf("expected src/a.ts to be filtered out, got:\n%s", out)
	}
	if !strings.Contains(out, "src/b.ts") {
		t.Fatalf("expected src/b.ts in output, got:\n%s", out)
	}
}

func TestRunBareDirectoryLikeTargetStillGuidesTowardDirectTargeting(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"tests/main.ts": "console.log('ignored')\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "missing-tests"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected no-files-matched error")
	}

	errOut := stderr.String()
	if !strings.Contains(errOut, `Warning: No file or directory 'missing-tests' found`) {
		t.Fatalf("expected missing target warning, got:\n%s", errOut)
	}
	if !strings.Contains(errOut, `use --include to browse blocked targets for this scope`) {
		t.Fatalf("expected --include guidance, got:\n%s", errOut)
	}
}

func TestRunDirectFileTargetSearchesWholeRepo(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts":      "export const web = true\n",
		"packages/app.ts": "export const pkg = true\n",
		"docs/readme.md":  "# docs\n",
		"src/feature.tsx": "export const feature = true\n",
		"assets/logo.svg": "<svg></svg>\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "app.ts"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		`<file path="packages/app.ts">`,
		`<file path="src/app.ts">`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %s, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, `docs/readme.md`) || strings.Contains(out, `src/feature.tsx`) {
		t.Fatalf("expected only basename matches, got:\n%s", out)
	}
}

func TestRunFzfDirectorySearchResolvesNestedDirectory(t *testing.T) {
	installFakeFzf(t)

	project := setupTestProject(t, map[string]string{
		"src/components/App.tsx": "export const App = true\n",
		"src/other/Skip.tsx":     "export const Skip = true\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "cmpnts"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/components/App.tsx">`) {
		t.Fatalf("expected fake fzf directory resolution to land on src/components, got:\n%s", out)
	}
	if strings.Contains(out, `src/other/Skip.tsx`) {
		t.Fatalf("expected only the fake fzf-selected directory, got:\n%s", out)
	}
}

func TestRunFzfFileSearchFallsBackWhenExactBasenameMisses(t *testing.T) {
	installFakeFzf(t)

	project := setupTestProject(t, map[string]string{
		"src/components/Button.tsx": "export const Button = true\n",
		"src/components/Input.tsx":  "export const Input = true\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "btn.tsx"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/components/Button.tsx">`) {
		t.Fatalf("expected fake fzf file resolution to land on Button.tsx, got:\n%s", out)
	}
	if strings.Contains(out, `src/components/Input.tsx`) {
		t.Fatalf("expected only the fake fzf-selected file, got:\n%s", out)
	}
}

func TestRunFzfFileSearchFallsBackForUnscopedShorthand(t *testing.T) {
	installFakeFzf(t)

	project := setupTestProject(t, map[string]string{
		"src/components/Button.tsx": "export const Button = true\n",
		"src/components/Input.tsx":  "export const Input = true\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "btn"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/components/Button.tsx">`) {
		t.Fatalf("expected unscoped shorthand to fuzzy-resolve Button.tsx, got:\n%s", out)
	}
	if strings.Contains(out, `src/components/Input.tsx`) {
		t.Fatalf("expected only the fake fzf-selected file, got:\n%s", out)
	}
}

func TestRunUnscopedFzfCanSelectFileWhenDirectoriesAlsoMatch(t *testing.T) {
	installFakeFzf(t)

	project := setupTestProject(t, map[string]string{
		"src/policy/README.md": "policy docs\n",
		"src/policy.ts":        "export const policy = true\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "policy"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/policy.ts">`) {
		t.Fatalf("expected mixed target selection to allow the file match, got:\n%s", out)
	}
	if strings.Contains(out, `src/policy/README.md`) {
		t.Fatalf("expected fake fzf to select the file instead of the directory, got:\n%s", out)
	}
}

func TestFilterRedundantTargetMatchesRemovesCoveredChildren(t *testing.T) {
	candidates := []discovery.TargetMatch{
		{Path: "src", Kind: "dir"},
		{Path: "src/vs", Kind: "dir"},
		{Path: "src/vs/platform", Kind: "dir"},
		{Path: "src/index.ts", Kind: "file"},
		{Path: "docs/src", Kind: "dir"},
	}

	got := discovery.FilterRedundantTargetMatches(candidates, []string{"src"})
	want := []discovery.TargetMatch{{Path: "docs/src", Kind: "dir"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected filtered matches %v, got %v", want, got)
	}
}

func TestSafeTargetPickerHeaderOmitsCtrlO(t *testing.T) {
	header := discovery.SafeTargetPickerHeader()
	if strings.Contains(header, "[Ctrl-O]") || strings.Contains(header, "ignored ones") {
		t.Fatalf("expected safe picker header to stay visible-target-only, got %q", header)
	}
	if !strings.Contains(header, "Pick files and folders to include.") || !strings.Contains(header, "[Up/Down] move  [Enter] confirm  [Tab] mark  [Esc] exit") {
		t.Fatalf("expected safe picker header to guide first-time fzf users, got %q", header)
	}
}

func TestTargetMatchLabelsMapsPlainCopyAllSelection(t *testing.T) {
	labels, index := discovery.TargetMatchLabels([]discovery.TargetMatch{{Path: ".", Kind: "all"}})
	if len(labels) != 1 {
		t.Fatalf("expected one label, got %d", len(labels))
	}
	if labels[0] != "\x1b[1m[select all files]\x1b[0m\t.\tdir\tok" {
		t.Fatalf("unexpected copy-all label: %q", labels[0])
	}
	match, ok := index["."]
	if !ok {
		t.Fatalf("expected copy-all path key to resolve, index keys: %#v", index)
	}
	if match.Kind != "all" || match.Path != "." {
		t.Fatalf("unexpected match: %#v", match)
	}
}

func TestFzfPreviewCommandUsesInternalTreePreview(t *testing.T) {
	command := discovery.FzfPreviewCommand(false)
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, discovery.ShellQuoteArg(self)+" --quiet --internal-tree-preview --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4} {+2}") {
		t.Fatalf("expected preview command to invoke internal tree preview, got %q", command)
	}
	if strings.Contains(command, "catclip-tree") || strings.Contains(command, "|") {
		t.Fatalf("expected preview command to avoid catclip-tree pipe, got %q", command)
	}
}

func TestFzfPreviewCommandIncludesIgnoredTargetAuthorization(t *testing.T) {
	command := discovery.FzfPreviewCommand(true)
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}
	if !strings.Contains(command, discovery.ShellQuoteArg(self)+" --quiet {+2} --internal-tree-preview --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4} --include {+2}") {
		t.Fatalf("expected ignored target preview to allow the hovered path, got %q", command)
	}
	if strings.Contains(command, "catclip-tree") || strings.Contains(command, "|") {
		t.Fatalf("expected ignored target preview to avoid catclip-tree pipe, got %q", command)
	}
}

func TestFzfContentPreviewCommandUsesFilePreviewRenderer(t *testing.T) {
	command := discovery.FzfContentPreviewCommand("--contains", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, discovery.ShellQuoteArg(self)+` --quiet --internal-file-preview --internal-file-path {3} --contains {q}`) {
		t.Fatalf("expected contains preview to invoke file preview renderer, got %q", command)
	}
	if strings.Contains(command, "catclip-tree") || strings.Contains(command, "|") {
		t.Fatalf("expected contains preview command to avoid catclip-tree pipe, got %q", command)
	}
}

func TestFzfContentSnippetPreviewCommandUsesSnippetFlag(t *testing.T) {
	command := discovery.FzfContentPreviewCommand("--snippet", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, discovery.ShellQuoteArg(self)+` --quiet --internal-file-preview --internal-file-path {3} --snippet {q}`) {
		t.Fatalf("expected snippet contains preview to forward --snippet, got %q", command)
	}
	if strings.Contains(command, "catclip-tree") || strings.Contains(command, "|") {
		t.Fatalf("expected snippet preview command to avoid catclip-tree pipe, got %q", command)
	}
}

func TestFzfContentMatchListCommandQuotesMultiwordQuery(t *testing.T) {
	command := discovery.FzfContentMatchListCommand([]string{".", "--exclude", "uninstall"}, "--snippet")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, discovery.ShellQuoteArg(self)+` --quiet --internal-content-match-list . --exclude uninstall --snippet {q}`) {
		t.Fatalf("expected content match list command to pass raw {q} placeholder, got %q", command)
	}
}

func TestFzfDiffFilePreviewCommandUsesFilePreviewRenderer(t *testing.T) {
	command := discovery.FzfDiffFilePreviewCommand([]string{"cmd", "--include", "Formula", "--changed-diff"})
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, discovery.ShellQuoteArg(self)+" --quiet --internal-file-preview --internal-file-path {3} cmd --include Formula --changed-diff --only {+2}") {
		t.Fatalf("expected diff file preview command to invoke internal file preview renderer with scope-narrowing --only, got %q", command)
	}
	if strings.Contains(command, "catclip-tree") || strings.Contains(command, "|") {
		t.Fatalf("expected diff file preview command to avoid catclip-tree pipe, got %q", command)
	}
}

func TestFzfFileSetPreviewCommandInheritsCurrentScope(t *testing.T) {
	command := discovery.FzfFileSetPreviewCommand([]string{"cmd", "--include", "Formula"}, "--only")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, discovery.ShellQuoteArg(self)+" --quiet --internal-tree-preview cmd --include Formula --only {+2} --internal-tree-target {3} --internal-tree-kind {4} --internal-tree-state {5}") {
		t.Fatalf("expected file-set preview to inherit current scope and refine by selected rows with hovered-row metadata, got %q", command)
	}
	if strings.Contains(command, "catclip-tree") || strings.Contains(command, "|") {
		t.Fatalf("expected file-set preview command to avoid catclip-tree pipe, got %q", command)
	}
}

func TestMultiSelectPickerBindingsIncludeRefreshPreview(t *testing.T) {
	bindings := discovery.MultiSelectPickerBindings()
	found := false
	for _, b := range bindings {
		if b == "multi:refresh-preview" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected multi:refresh-preview in picker bindings, got %v", bindings)
	}
}

func TestAllIgnoredTargetsIncludesGitignoreEntriesWithoutGitRepo(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":   "blocked/\n",
		"blocked/a.ts": "export const blocked = true\n",
		"src/main.ts":  "export const main = true\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "."})

	resolver := discovery.Resolver{
		Cfg:               invocationConfigFromParsedCommand(cfg),
		GitCtx:            git.Detect(project),
		AllowFileSymlinks: false,
	}

	targets, err := resolver.AllIgnoredTargets()
	if err != nil {
		t.Fatalf("allIgnoredTargets returned error: %v", err)
	}

	lookup := make(map[string]discovery.TargetMatch, len(targets))
	for _, target := range targets {
		lookup[target.Path] = target
	}

	if got, ok := lookup["blocked"]; !ok || !got.Ignored || got.IgnoreSource != ".gitignore" || got.Kind != "dir" {
		t.Fatalf("expected blocked dir to appear as ignored .gitignore entry outside git repo, got %#v (present=%v)", got, ok)
	}
	if got, ok := lookup["blocked/a.ts"]; !ok || !got.Ignored || got.IgnoreSource != ".gitignore" || got.Kind != "file" {
		t.Fatalf("expected blocked file to appear as ignored .gitignore entry outside git repo, got %#v (present=%v)", got, ok)
	}
}

func TestHasScopedIgnoredTargetsStreamingShallowGitignore(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":   "blocked/\n",
		"blocked/a.ts": "export const blocked = true\n",
		"src/main.ts":  "export const main = true\n",
	})
	hissPath := mustHissPath(t)

	got, err := search.HasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"."}, hissPath)
	if err != nil {
		t.Fatalf("hasScopedIgnoredTargetsStreaming returned error: %v", err)
	}
	if !got {
		t.Fatalf("expected true for shallow gitignored dir under root scope, got false")
	}
}

func TestHasScopedIgnoredTargetsStreamingNoIgnores(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":  "export const main = true\n",
		"src/lib/a.ts": "export const a = true\n",
	})
	hissPath := mustHissPath(t)

	got, err := search.HasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"."}, hissPath)
	if err != nil {
		t.Fatalf("hasScopedIgnoredTargetsStreaming returned error: %v", err)
	}
	if got {
		t.Fatalf("expected false when scope has no ignored entries, got true")
	}
}

func TestHasScopedIgnoredTargetsStreamingMidDepthIgnore(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":     "a/b/secret.txt\n",
		"a/b/visible.ts": "export const visible = true\n",
		"a/b/secret.txt": "secret\n",
	})
	hissPath := mustHissPath(t)

	got, err := search.HasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"."}, hissPath)
	if err != nil {
		t.Fatalf("hasScopedIgnoredTargetsStreaming returned error: %v", err)
	}
	if !got {
		t.Fatalf("expected true for ignored file at depth 3 from root scope, got false")
	}
}

func TestHasScopedIgnoredTargetsStreamingDeepIgnoreStillSurfaces(t *testing.T) {
	// Ignored entry deeper than the previously-rejected B = 3 bound.
	// Must still return true: false negatives at any depth would hide
	// `--include` from the modifier menu for menu-only users.
	project := setupTestProject(t, map[string]string{
		".gitignore":                   "packages/foo/bar/dist/\n",
		"packages/foo/bar/src/main.ts": "export const main = true\n",
		"packages/foo/bar/dist/out.js": "module.exports = {}\n",
	})
	hissPath := mustHissPath(t)

	got, err := search.HasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"."}, hissPath)
	if err != nil {
		t.Fatalf("hasScopedIgnoredTargetsStreaming returned error: %v", err)
	}
	if !got {
		t.Fatalf("expected true for deep ignored entry (no false negatives at any depth), got false")
	}
}

func TestHasScopedIgnoredTargetsStreamingDeepTargetWithIgnoredChild(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a/b/c/file.ts":      "export const a = true\n",
		"a/b/c/d/.gitignore": "blocked.ts\n",
		"a/b/c/d/blocked.ts": "blocked\n",
		"a/b/c/d/visible.ts": "visible\n",
	})
	hissPath := mustHissPath(t)

	got, err := search.HasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"a/b/c/d"}, hissPath)
	if err != nil {
		t.Fatalf("hasScopedIgnoredTargetsStreaming returned error: %v", err)
	}
	if !got {
		t.Fatalf("expected true for ignored child under a deep scope target, got false")
	}
}

func TestHasScopedIgnoredTargetsStreamingHardErrorPropagates(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "export const main = true\n",
	})
	hissPath := mustHissPath(t)

	got, err := search.HasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"does-not-exist"}, hissPath)
	if err == nil {
		t.Fatalf("expected error for non-existent scope target, got nil (got=%v)", got)
	}
	if got {
		t.Fatalf("expected (false, err) on hard rg error, got (true, %v)", err)
	}
}

// TestCollectChangedRepoPathsUnionEqualsAllThree pins the equality the
// modifier-menu Phase 2 dedupe relies on: `staged ∪ unstaged ∪ untracked`
// is the same set as the old `command.ExecutionScope{}` call (which spawned a
// fourth `git diff HEAD` subprocess). If anyone changes one of the
// individual collectors, this test fails before the menu silently
// disagrees with itself.
func TestCollectChangedRepoPathsUnionEqualsAllThree(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"committed.ts": "export const c = 1\n",
		"modified.ts":  "export const m = 1\n",
	})
	initGitRepo(t, project)

	writeProjectFile(t, project, "modified.ts", "export const m = 2\n")
	runGit(t, project, "add", "modified.ts")
	writeProjectFile(t, project, "modified.ts", "export const m = 3\n")
	writeProjectFile(t, project, "untracked.ts", "export const u = 1\n")

	gitCtx := git.Detect(project)
	if !gitCtx.Enabled {
		t.Fatal("expected git context to be enabled for the project")
	}

	staged, err := discovery.CollectChangedRepoPaths(gitCtx, command.ExecutionScope{Staged: true})
	if err != nil {
		t.Fatalf("staged: %v", err)
	}
	unstaged, err := discovery.CollectChangedRepoPaths(gitCtx, command.ExecutionScope{Unstaged: true})
	if err != nil {
		t.Fatalf("unstaged: %v", err)
	}
	untracked, err := discovery.CollectChangedRepoPaths(gitCtx, command.ExecutionScope{Untracked: true})
	if err != nil {
		t.Fatalf("untracked: %v", err)
	}
	all, err := discovery.CollectChangedRepoPaths(gitCtx, command.ExecutionScope{})
	if err != nil {
		t.Fatalf("all-three: %v", err)
	}

	want := map[string]struct{}{}
	for _, p := range staged {
		want[p] = struct{}{}
	}
	for _, p := range unstaged {
		want[p] = struct{}{}
	}
	for _, p := range untracked {
		want[p] = struct{}{}
	}
	got := map[string]struct{}{}
	for _, p := range all {
		got[p] = struct{}{}
	}

	if len(want) != len(got) {
		t.Fatalf("union mismatch: staged∪unstaged∪untracked=%v all-three=%v", staged, all)
	}
	for p := range want {
		if _, ok := got[p]; !ok {
			t.Fatalf("path %q in union but missing from all-three (staged=%v unstaged=%v untracked=%v all=%v)", p, staged, unstaged, untracked, all)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Fatalf("path %q in all-three but missing from union (staged=%v unstaged=%v untracked=%v all=%v)", p, staged, unstaged, untracked, all)
		}
	}
}

func mustHissPath(t *testing.T) string {
	t.Helper()
	path, err := discovery.ReadableHissPath()
	if err != nil {
		t.Fatalf("discovery.ReadableHissPath: %v", err)
	}
	return path
}

func TestRunScopedFzfFileSearchStaysWithinResolvedDirectory(t *testing.T) {
	installFakeFzf(t)

	project := setupTestProject(t, map[string]string{
		"src/auth/Button.tsx":   "export const AuthButton = true\n",
		"src/admin/Button.tsx":  "export const AdminButton = true\n",
		"src/shared/Input.tsx":  "export const Input = true\n",
		"src/shared/Button.tsx": "export const SharedButton = true\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "auth/btn"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/auth/Button.tsx">`) {
		t.Fatalf("expected scoped shorthand to stay inside auth/, got:\n%s", out)
	}
	for _, unwanted := range []string{
		`src/admin/Button.tsx`,
		`src/shared/Button.tsx`,
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("expected scoped fuzzy file search to exclude %s, got:\n%s", unwanted, out)
		}
	}
}

func TestRunMixedDirectoryAndFileTargetsStayIndependent(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"alpha/alpha.ts":         "export const alpha = true\n",
		"beta/beta.ts":           "export const beta = true\n",
		"shared/file.ts":         "export const shared = true\n",
		"nested/deeper/file2.ts": "export const second = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "alpha", "file.ts", "beta", "file2.ts"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		`<file path="alpha/alpha.ts">`,
		`<file path="beta/beta.ts">`,
		`<file path="shared/file.ts">`,
		`<file path="nested/deeper/file2.ts">`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %s, got:\n%s", want, out)
		}
	}
}

func TestRunChainedDirectoryFileTargetScopesFileSearch(t *testing.T) {
	installFakeFzf(t)

	project := setupTestProject(t, map[string]string{
		"features/auth/hooks/useLogin.ts":    "export const authHook = true\n",
		"features/auth/examples/useLogin.ts": "export const authExample = true\n",
		"features/profile/hooks/useLogin.ts": "export const profileHook = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "auth/useLogin.ts"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `features/auth/hooks/useLogin.ts`) || !strings.Contains(out, `features/auth/examples/useLogin.ts`) {
		t.Fatalf("expected scoped auth matches, got:\n%s", out)
	}
	if strings.Contains(out, `features/profile/hooks/useLogin.ts`) {
		t.Fatalf("expected chained search to stay within auth/, got:\n%s", out)
	}
}

func TestRunMissingFileTargetShowsDirectFilenameGuidance(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": "export const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "missing.ts"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected no-files-matched error")
	}
	if !strings.Contains(stderr.String(), "Direct file targets use exact basenames first. Non-exact file shorthand is resolved by fzf across safe directories.") {
		t.Fatalf("expected updated direct filename guidance, got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "use --include to allow that blocked file or directory first") {
		t.Fatalf("expected blocked-directory guidance, got:\n%s", stderr.String())
	}
}

func TestRunFuzzySearchRequiresFzf(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/components/App.tsx": "export const App = true\n",
	})

	t.Setenv("CATCLIP_FZF", filepath.Join(project, "missing-fzf"))
	cfg := parseInProject(t, project, []string{"--print", "cmpnts"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected fuzzy search to require fzf")
	}
	if !strings.Contains(err.Error(), "missing bundled fzf") {
		t.Fatalf("expected missing fzf guidance, got: %v", err)
	}
}

func TestRunExactBasenameSearchWorksWithoutFzf(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/Button.tsx": "export const Button = true\n",
	})

	t.Setenv("CATCLIP_FZF", filepath.Join(project, "missing-fzf"))
	cfg := parseInProject(t, project, []string{"--quiet", "--print", "Button.tsx"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("expected exact basename search to work without fzf, got: %v", err)
	}
	if !strings.Contains(stdout.String(), `<file path="src/Button.tsx">`) {
		t.Fatalf("expected exact basename payload, got:\n%s", stdout.String())
	}
}

func TestRunNoMatchShowsShellStyleFooter(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"node_modules/index.js": "ignored\n",
		"src/app.ts":            "export const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "include", "index.js"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected no-match exit")
	}

	errOut := stderr.String()
	for _, want := range []string{
		"No text files found matching your criteria.",
		"Possible causes:",
		"1. Directory is empty or contains only binary files",
		`Try: catclip --all-ignore-rules`,
		`catclip --include blocked-dir`,
		`catclip --hiss`,
	} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("expected stderr to contain %q, got:\n%s", want, errOut)
		}
	}
	if first := strings.Index(errOut, "Warning: No file or directory 'include' found"); first == -1 {
		t.Fatalf("expected missing-target warning, got:\n%s", errOut)
	} else if second := strings.Index(errOut, "1 match skipped by ignore rules"); second == -1 || first > second {
		t.Fatalf("expected diagnostics in target order, got:\n%s", errOut)
	}
}

func TestRunBroadDiscoverySkipsTextLikeImageAssets(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts":          "export const ok = true\n",
		"src/app.tsx.map":     "{\"version\":3}\n",
		"assets/logo.svg":     "<svg><text>logo</text></svg>\n",
		"assets/icon.xpm":     "/* XPM */\nstatic char * icon[] = {};\n",
		"assets/readme.txt":   "plain text\n",
		"assets/avatar.jpg":   "\xff\xd8\xff\xe0\x00\x10JFIF",
		"assets/custom.woff2": "wOF2\x00\x01\x00\x00",
	})

	cfg := parseInProject(t, project, []string{"--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "src/app.ts") || !strings.Contains(out, "assets/readme.txt") {
		t.Fatalf("expected regular text files in output, got:\n%s", out)
	}
	if strings.Contains(out, "src/app.tsx.map") || strings.Contains(out, "assets/logo.svg") || strings.Contains(out, "assets/icon.xpm") || strings.Contains(out, "assets/avatar.jpg") || strings.Contains(out, "assets/custom.woff2") {
		t.Fatalf("expected text-like image assets to be skipped, got:\n%s", out)
	}
}

func TestRunBroadDiscoverySkipsShellBlockedTextEncodedFormats(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts":                 "export const ok = true\n",
		"docs/setup.inf":             "[Version]\nSignature=\"$Windows NT$\"\n",
		"assets/logo.pbm":            "P1\n# comment\n2 2\n0 1\n1 0\n",
		"assets/logo.ppm":            "P3\n# comment\n1 1\n255\n0 0 0\n",
		"cfg/good.bconf":             "key = value\n",
		"cfg/bad-nonprintable.bconf": "bad\x00value\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "src/app.ts") {
		t.Fatalf("expected regular text file in output, got:\n%s", out)
	}
	if !strings.Contains(out, "cfg/good.bconf") {
		t.Fatalf("expected regular .bconf file to remain included, got:\n%s", out)
	}
	for _, blocked := range []string{"docs/setup.inf", "assets/logo.pbm", "assets/logo.ppm", "cfg/bad-nonprintable.bconf"} {
		if strings.Contains(out, blocked) {
			t.Fatalf("expected %s to be shell-blocked, got:\n%s", blocked, out)
		}
	}
}

func TestRunDirectTargetSkipsShellBlockedTextEncodedFormats(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"docs/setup.inf": "[Version]\nSignature=\"$Windows NT$\"\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "docs/setup.inf"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected no-match exit")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected blocked direct target to produce no preview output, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "No text files found matching your criteria.") {
		t.Fatalf("expected no-match footer, got:\n%s", stderr.String())
	}
}

func TestRunBlocksDefaultHissNoiseAndSurfacesUnderInclude(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"assets/icon.xpm": "/* XPM */\nstatic char * icon[] = {};\n",
		"src/app.ts":      "export const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "assets/icon.xpm") {
		t.Fatalf("expected default .hiss to block *.xpm, got:\n%s", out)
	}
	if !strings.Contains(out, "src/app.ts") {
		t.Fatalf("expected text files to still appear, got:\n%s", out)
	}

	cfg = parseInProject(t, project, []string{"--print", ".", "--include", "assets/icon.xpm"})
	stdout.Reset()
	stderr.Reset()
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	out = stdout.String()
	if !strings.Contains(out, `<file path="assets/icon.xpm">`) {
		t.Fatalf("expected --include to surface .hiss-blocked *.xpm, got:\n%s", out)
	}
}

func TestRunHeadlessAmbiguousTargetFailsWithGuidance(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/common/a.ts": "export const a = 1\n",
		"lib/common/b.ts": "export const b = 2\n",
	})

	cfg := parseInProject(t, project, []string{"--headless", "--quiet", "--print", "common"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected ambiguous target error")
	}
	if !strings.Contains(err.Error(), `Multiple directories match 'common'`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "Matches:") || !strings.Contains(err.Error(), "src/common") || !strings.Contains(err.Error(), "lib/common") {
		t.Fatalf("expected capped candidate list guidance, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Use a more specific path segment") {
		t.Fatalf("expected disambiguation guidance, got: %v", err)
	}
}

func TestRunHeadlessExactTargetEqualsIncludeWorks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"ignored/common.ts": "export const ok = true\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "ignored", "--include", "ignored"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("expected exact target==include run path to work, got: %v", err)
	}
	if !strings.Contains(stdout.String(), `<file path="ignored/common.ts">`) {
		t.Fatalf("expected include-authorized ignored output, got:\n%s", stdout.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected quiet headless run to keep stderr empty, got:\n%s", got)
	}
}

func TestRunIncludeDirectoryAuthorizesDescendantTarget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":                     "docs/\n",
		"docs/versions/v0.4.0/main.ts":   "export const ok = true\n",
		"docs/versions/v0.4.0/notes.txt": "notes\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "docs/versions/v0.4.0", "--include", "docs"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("expected included directory ancestor to authorize descendant target, got: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, `<file path="docs/versions/v0.4.0/main.ts">`) || !strings.Contains(out, `<file path="docs/versions/v0.4.0/notes.txt">`) {
		t.Fatalf("expected descendant target under included directory to emit files, got:\n%s", out)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected quiet run to keep stderr empty, got:\n%s", got)
	}
}

func TestRunIncludeAncestorAuthorizationDoesNotWidenExplicitDescendantTarget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":                "docs/\n",
		"docs/versions/v0.4.0/a.md": "v040\n",
		"docs/versions/v0.3.3/b.md": "v033\n",
		"docs/policy/c.md":          "policy\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "docs/versions/v0.4.0", "--include", "docs"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("expected descendant target under included ancestor to run, got: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="docs/versions/v0.4.0/a.md">`) {
		t.Fatalf("expected explicit descendant target output, got:\n%s", out)
	}
	if strings.Contains(out, "docs/versions/v0.3.3/b.md") || strings.Contains(out, "docs/policy/c.md") {
		t.Fatalf("expected ancestor include to authorize only the explicit descendant target, got:\n%s", out)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected quiet run to keep stderr empty, got:\n%s", got)
	}
}

// Deep-include rewrite: `catclip <target> --include <deep-file>` (the
// include value lives inside the target) must produce the same output
// as the manual `--include <target> --only <deep-file>` form, instead
// of the pre-v0.5.3 "your --include does not cover this target" error.
// See docs/versions/v0.5.3/reports/ACTIVE_BUG_include_ancestor_target_not_authorized.md.
func TestRunDeepIncludeFileRewritesToIncludeAncestorOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	files := map[string]string{
		".gitignore":          "docs/\n",
		"docs/a/keep.md":      "keep\n",
		"docs/a/other.md":     "other\n",
		"docs/b/elsewhere.md": "elsewhere\n",
	}
	project := setupTestProject(t, files)
	initGitRepo(t, project)

	deepCfg := parseInProject(t, project, []string{"--quiet", "--print", "docs", "--include", "docs/a/keep.md"})
	var deepOut, deepErr bytes.Buffer
	if err := run(deepCfg, &deepOut, &deepErr); err != nil {
		t.Fatalf("deep-include form returned error: %v", err)
	}

	project2 := setupTestProject(t, files)
	initGitRepo(t, project2)
	manualCfg := parseInProject(t, project2, []string{"--quiet", "--print", "docs", "--include", "docs", "--only", "docs/a/keep.md"})
	var manualOut, manualErr bytes.Buffer
	if err := run(manualCfg, &manualOut, &manualErr); err != nil {
		t.Fatalf("manual --include/--only form returned error: %v", err)
	}

	if deepOut.String() != manualOut.String() {
		t.Fatalf("deep-include output != manual form output\ndeep:\n%s\nmanual:\n%s", deepOut.String(), manualOut.String())
	}
	if !strings.Contains(deepOut.String(), `<file path="docs/a/keep.md">`) {
		t.Fatalf("expected docs/a/keep.md in output, got:\n%s", deepOut.String())
	}
	if strings.Contains(deepOut.String(), "other.md") || strings.Contains(deepOut.String(), "elsewhere.md") {
		t.Fatalf("deep-include should narrow to exactly docs/a/keep.md, got:\n%s", deepOut.String())
	}
}

// Deep-include of a directory narrows to that subtree, same as
// `--include <target> --only <deep-dir>`.
func TestRunDeepIncludeDirectoryNarrowsToSubtree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	project := setupTestProject(t, map[string]string{
		".gitignore":          "docs/\n",
		"docs/a/one.md":       "one\n",
		"docs/a/two.md":       "two\n",
		"docs/b/elsewhere.md": "elsewhere\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "docs", "--include", "docs/a"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("deep-include directory form returned error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, `<file path="docs/a/one.md">`) || !strings.Contains(out, `<file path="docs/a/two.md">`) {
		t.Fatalf("expected both files under docs/a, got:\n%s", out)
	}
	if strings.Contains(out, "elsewhere.md") {
		t.Fatalf("deep-include docs/a should not pull in docs/b, got:\n%s", out)
	}
}

// An include value that is NOT under any target must still produce the
// "include does not cover this target" error — the rewrite only fires
// when every include value is covered by a target.
func TestRunDeepIncludeUncoveredValueStillErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	project := setupTestProject(t, map[string]string{
		".gitignore":     "docs/\n",
		"docs/a/keep.md": "keep\n",
		"src/main.go":    "package main\n",
	})
	initGitRepo(t, project)

	// docs/a/keep.md is under the target, but src/main.go is not —
	// the mixed set must not rewrite.
	cfg := parseInProject(t, project, []string{"--quiet", "--print", "docs", "--include", "docs/a/keep.md", "src/main.go"})
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for mixed covered/uncovered include set, got output:\n%s", stdout.String())
	}
	// The ignored-target message is written to stderr; run() returns a
	// sentinel error. Assert against stderr, matching the other
	// blocked-target tests in this file.
	if !strings.Contains(stderr.String(), "is ignored by") {
		t.Fatalf("expected ignored-target message on stderr, got:\n%s", stderr.String())
	}
}

// rewriteDeepIncludeScope unit coverage: the pure transform itself.
func TestRewriteDeepIncludeScope(t *testing.T) {
	t.Run("deep file rewrites to include-ancestor plus only", func(t *testing.T) {
		in := command.ExecutionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"docs/a/keep.md"},
			Stages: []command.Stage{
				{Kind: command.StageInclude, Values: []string{"docs/a/keep.md"}},
			},
		}
		got := command.RewriteDeepIncludeScope(in)
		if len(got.IncludedTargets) != 1 || got.IncludedTargets[0] != "docs" {
			t.Fatalf("IncludedTargets = %v, want [docs]", got.IncludedTargets)
		}
		if len(got.Stages) != 2 {
			t.Fatalf("expected 2 stages (include + only), got %d: %#v", len(got.Stages), got.Stages)
		}
		if got.Stages[0].Kind != command.StageInclude || got.Stages[0].Values[0] != "docs" {
			t.Fatalf("stage 0 should be include[docs], got %#v", got.Stages[0])
		}
		if got.Stages[1].Kind != command.StageOnly || got.Stages[1].Values[0] != "docs/a/keep.md" {
			t.Fatalf("stage 1 should be only[docs/a/keep.md], got %#v", got.Stages[1])
		}
	})

	t.Run("plain include of target is untouched", func(t *testing.T) {
		in := command.ExecutionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"docs"},
			Stages:          []command.Stage{{Kind: command.StageInclude, Values: []string{"docs"}}},
		}
		got := command.RewriteDeepIncludeScope(in)
		if len(got.IncludedTargets) != 1 || got.IncludedTargets[0] != "docs" {
			t.Fatalf("plain include should be unchanged, got %v", got.IncludedTargets)
		}
		if len(got.Stages) != 1 {
			t.Fatalf("plain include should not gain an --only stage, got %#v", got.Stages)
		}
	})

	t.Run("uncovered include bails", func(t *testing.T) {
		in := command.ExecutionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"docs/a/keep.md", "src/main.go"},
			Stages:          []command.Stage{{Kind: command.StageInclude, Values: []string{"docs/a/keep.md", "src/main.go"}}},
		}
		got := command.RewriteDeepIncludeScope(in)
		if len(got.Stages) != 1 || got.Stages[0].Kind != command.StageInclude {
			t.Fatalf("uncovered include should leave stages untouched, got %#v", got.Stages)
		}
	})

	t.Run("wildcard include bails", func(t *testing.T) {
		in := command.ExecutionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"*"},
			Stages:          []command.Stage{{Kind: command.StageInclude, Values: []string{"*"}}},
		}
		got := command.RewriteDeepIncludeScope(in)
		if len(got.Stages) != 1 {
			t.Fatalf("wildcard include should be untouched, got %#v", got.Stages)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		in := command.ExecutionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"docs/a/keep.md"},
			Stages:          []command.Stage{{Kind: command.StageInclude, Values: []string{"docs/a/keep.md"}}},
		}
		once := command.RewriteDeepIncludeScope(in)
		twice := command.RewriteDeepIncludeScope(once)
		if len(twice.Stages) != len(once.Stages) || len(twice.IncludedTargets) != len(once.IncludedTargets) {
			t.Fatalf("rewrite not idempotent: once=%#v twice=%#v", once, twice)
		}
	})
}

func TestRunDotTargetWithIncludeStillWidensToIncludedIgnoredDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":                "docs/\n",
		"src/main.ts":               "export const ok = true\n",
		"docs/versions/v0.4.0/a.md": "v040\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--include", "docs"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("expected dot target with include to widen scope, got: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/main.ts">`) || !strings.Contains(out, `<file path="docs/versions/v0.4.0/a.md">`) {
		t.Fatalf("expected include stage to widen implicit root scope, got:\n%s", out)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected quiet run to keep stderr empty, got:\n%s", got)
	}
}

func TestRunIncludeDirectoryDoesNotAuthorizeSimilarPrefixDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":    "docs/\ndocs2/\n",
		"docs/file.ts":  "export const docs = true\n",
		"docs2/file.ts": "export const docs2 = true\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "docs2/file.ts", "--include", "docs"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected docs include not to authorize docs2 target")
	}
	if strings.Contains(stdout.String(), "docs2/file.ts") {
		t.Fatalf("expected docs2 target to stay blocked, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--include does not cover this target") {
		t.Fatalf("expected include guidance for blocked docs2 target, got:\n%s", stderr.String())
	}
}

func TestRunIncludeFileDoesNotAuthorizePrefixedSibling(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":         "ignored/\n",
		"ignored/secret.ts":  "export const a = true\n",
		"ignored/secret.tsx": "export const b = true\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "ignored/secret.tsx", "--include", "ignored/secret.ts"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected exact file include not to authorize prefixed sibling")
	}
	if strings.Contains(stdout.String(), "ignored/secret.tsx") {
		t.Fatalf("expected prefixed sibling to stay blocked, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--include does not cover this target") {
		t.Fatalf("expected include guidance for blocked sibling, got:\n%s", stderr.String())
	}
}

func TestRunInternalTreePreviewAmbiguousTargetFailsWithGuidance(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/common/a.ts": "export const a = 1\n",
		"lib/common/b.ts": "export const b = 2\n",
	})

	cfg := parseInProject(t, project, []string{"--internal-tree-preview", "common"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected ambiguous target error")
	}
	if !strings.Contains(err.Error(), `Multiple directories match 'common'`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "Use a more specific path segment") {
		t.Fatalf("expected disambiguation guidance, got: %v", err)
	}
}

func TestRunFuzzySearchUsesVisibleDirectoryIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":           "ignored/\n",
		"visible/common/ok.ts": "visible\n",
		"ignored/common/no.ts": "ignored\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "common"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("expected visible common/ to resolve without ambiguity, got: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="visible/common/ok.ts">`) {
		t.Fatalf("expected visible/common to be selected, got:\n%s", out)
	}
	if strings.Contains(out, "ignored/common/no.ts") {
		t.Fatalf("expected gitignored fuzzy candidate to stay hidden, got:\n%s", out)
	}
}

func TestRunGitIgnoredDirectoryRequiresInclude(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":           "ignored/\n",
		"ignored/common/ok.ts": "visible via include\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "ignored"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected gitignored directory without --include to fail")
	}
	if strings.Contains(stdout.String(), `ignored/common/ok.ts`) {
		t.Fatalf("expected gitignored directory to stay hidden, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Use --include to allow it for this run") {
		t.Fatalf("expected --include guidance, got:\n%s", stderr.String())
	}
}

func TestRunIncludeAllowsGitIgnoredDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":           "ignored/\n",
		"ignored/common/ok.ts": "visible via include\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{".", "--quiet", "--print", "--include", "ignored"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("expected --include to allow gitignored directory, got: %v", err)
	}

	if !strings.Contains(stdout.String(), `<file path="ignored/common/ok.ts">`) {
		t.Fatalf("expected gitignored directory output, got:\n%s", stdout.String())
	}
}

func TestRunSnippetEmitsBlankLineBoundedBlocks(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": strings.Join([]string{
			"const a = 1",
			"TODO: first",
			"const b = 2",
			"TODO: second",
			"",
			"const c = 3",
			"TODO: third",
			"const d = 4",
		}, "\n"),
		"src/skip.ts": "const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "src", "--snippet", "TODO"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "src/skip.ts") {
		t.Fatalf("expected non-matching file to stay out of snippet output, got:\n%s", out)
	}
	if got := strings.Count(out, `<file path="src/app.ts" lines="`); got != 2 {
		t.Fatalf("expected 2 snippet blocks, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "<file path=\"src/app.ts\" lines=\"1-4\">\nconst a = 1\nTODO: first\nconst b = 2\nTODO: second\n</file>\n\n") {
		t.Fatalf("expected first snippet block, got:\n%s", out)
	}
	if !strings.Contains(out, "<file path=\"src/app.ts\" lines=\"6-8\">\nconst c = 3\nTODO: third\nconst d = 4\n</file>\n\n") {
		t.Fatalf("expected second snippet block, got:\n%s", out)
	}
}

func TestRunPreviewRendersTableAndSummary(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts":              "const ok = true\n",
		"src/components/b.tsx":  "export const B = 1\n",
		"tests/ignored.test.ts": "ignored\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "src"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	// --preview now renders the file table: full relative paths, no tree glyphs.
	if !strings.HasPrefix(out, "# line 1: relative path\n") {
		t.Fatalf("expected preview header, got:\n%s", out)
	}
	if strings.Contains(out, "├──") {
		t.Fatalf("--preview must not render a tree, got:\n%s", out)
	}
	if !strings.Contains(out, "src/a.ts") || !strings.Contains(out, "src/components/b.tsx") {
		t.Fatalf("expected full relative paths in table, got:\n%s", out)
	}
	if !strings.Contains(out, "Count:") || !strings.Contains(out, "Size:") || !strings.Contains(out, "Tokens:") {
		t.Fatalf("expected preview summary in output, got:\n%s", out)
	}
	if strings.Contains(out, "ignored.test.ts") {
		t.Fatalf("expected default ignored tests/ directory to stay hidden, got:\n%s", out)
	}
}

func TestRunPreviewShowsResolvedNestedTargetPath(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".cache/babel-loader/abc.json": "ok\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "babel-loader"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	// The table shows the full resolved path directly, so the fuzzy target's
	// resolution is visible without a separate tree-style hint.
	out := stdout.String()
	if !strings.Contains(out, ".cache/babel-loader/abc.json") {
		t.Fatalf("expected resolved file path in preview table, got:\n%s", out)
	}
}

func TestRunPreviewDoesNotShowTargetPathHintForRootLevelTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "src"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if strings.Contains(stdout.String(), "(src/)") {
		t.Fatalf("expected no root-level target hint, got:\n%s", stdout.String())
	}
}

func TestRunPreviewNoTreeDoesNotSuppressTable(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "--no-tree", "src"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	// --no-tree governs only the confirmation-flow tree; it does not touch
	// --preview, which always renders its table.
	out := stdout.String()
	if !strings.Contains(out, "src/a.ts") {
		t.Fatalf("--no-tree must not suppress the --preview table, got:\n%s", out)
	}
	if strings.Contains(out, "├──") {
		t.Fatalf("--preview must not render a tree, got:\n%s", out)
	}
	if !strings.Contains(out, "Count:") || !strings.Contains(out, "Tokens:") {
		t.Fatalf("expected summary footer, got:\n%s", out)
	}
}

func TestRunPreviewShowsSnippetRangeTags(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": "const a = 1\nTODO: fix\nconst b = 2\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "src", "--snippet", "TODO"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	// The table's shape column carries the snippet range (no tree brackets).
	out := stdout.String()
	if !strings.Contains(out, "src/app.ts") || !strings.Contains(out, "snippet 1-3") {
		t.Fatalf("expected snippet range in the shape column, got:\n%s", out)
	}
}

func TestRunVerboseShowsPhaseTimings(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--verbose", "--print", "src"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	errOut := stderr.String()
	for _, want := range []string{
		"[verbose] scope 1:",
		"[verbose] report:",
		"[verbose] diagnostics:",
		"[verbose] output:",
		"[verbose] emit generate:",
		"[verbose] emit flush (stdout):",
		"[verbose] payload:",
		"[verbose] output throughput:",
		"[verbose] total:",
	} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("expected verbose timing %q, got:\n%s", want, errOut)
		}
	}
}

func TestRunVerboseShowsGitFileStateMetrics(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/clean.ts":    "export const clean = true\n",
		"src/modified.ts": "export const oldValue = true\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "src/modified.ts", "export const newValue = true\n")
	writeProjectFile(t, project, "src/untracked.ts", "export const untracked = true\n")

	cfg := parseInProject(t, project, []string{"--verbose", "--print", "src"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	errOut := stderr.String()
	if !strings.Contains(errOut, "[verbose] git file states: clean-tracked=1 modified/untracked=2") {
		t.Fatalf("expected git file state metrics, got:\n%s", errOut)
	}
}

func TestRunVerboseShowsUnavailableGitFileStateMetricsWhenRepoHasNoTrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/untracked.ts": "export const untracked = true\n",
	})
	runGit(t, project, "init")
	runGit(t, project, "config", "user.name", "catclip-tests")
	runGit(t, project, "config", "user.email", "catclip@example.com")

	cfg := parseInProject(t, project, []string{"--verbose", "--print", "src"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	errOut := stderr.String()
	if !strings.Contains(errOut, "[verbose] git file states: unavailable (git ls-files returned no tracked files)") {
		t.Fatalf("expected unavailable git file state metrics, got:\n%s", errOut)
	}
}

func TestBuildVisibleDirIndexDerivesDirsFromVisibleFiles(t *testing.T) {

	project := setupTestProject(t, map[string]string{
		"src/main.ts":           "export const main = true\n",
		"pkg/util/helpers.txt":  "helpers\n",
		"notes/README.md":       "# notes\n",
		"images/logo.png":       "\x89PNG\r\n\x1a\n\x00\x00\x00",
		"docs-empty/.keep.bin":  "\x00binary",
		"nested-empty/icon.png": "\x89PNG\r\n\x1a\n\x00\x00\x00",
	})
	if err := os.MkdirAll(filepath.Join(project, "truly-empty"), 0o755); err != nil {
		t.Fatalf("mkdir truly-empty: %v", err)
	}

	resolver := discovery.Resolver{
		Cfg: command.Invocation{WorkingDir: project},
	}

	if err := resolver.BuildVisibleDirIndex(); err != nil {
		t.Fatalf("BuildVisibleDirIndex: %v", err)
	}

	got := resolver.VisibleDirs.Dirs
	wantPresent := []string{"notes", "pkg", "pkg/util", "src"}
	for _, want := range wantPresent {
		if _, ok := resolver.VisibleDirs.Set[want]; !ok {
			t.Fatalf("expected visible dir %q in %#v", want, got)
		}
	}

	for _, blocked := range []string{"images", "docs-empty", "nested-empty", "truly-empty"} {
		if _, ok := resolver.VisibleDirs.Set[blocked]; ok {
			t.Fatalf("did not expect empty/non-text dir %q in %#v", blocked, got)
		}
	}
}

func TestRunGitIgnoreFiltersIgnoredFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"src/main.ts":       "console.log('ok')\n",
		"ignored/secret.ts": "console.log('ignored')\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "ignored/secret.ts") {
		t.Fatalf("expected gitignored file to be excluded, got:\n%s", out)
	}
	if !strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected tracked file in output, got:\n%s", out)
	}
}

func TestRunGitIgnoredFileRequiresInclude(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"src/main.ts":       "console.log('ok')\n",
		"ignored/secret.ts": "console.log('ignored')\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--print", "ignored/secret.ts"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected gitignored file without --include to fail")
	}
	if strings.Contains(stdout.String(), "ignored/secret.ts") {
		t.Fatalf("expected gitignored file to stay excluded, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Use --include to allow it for this run") {
		t.Fatalf("expected --include guidance, got:\n%s", stderr.String())
	}
}

func TestRunIncludeAllowsGitIgnoredFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"src/main.ts":       "console.log('ok')\n",
		"ignored/secret.ts": "console.log('ignored')\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{".", "--print", "--include", "ignored/secret.ts"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "ignored/secret.ts") {
		t.Fatalf("expected --include to restore gitignored file, got:\n%s", stdout.String())
	}
}

func TestRunIncludeFromStdinAllowsExactIgnoredTargets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"src/main.ts":       "console.log('ok')\n",
		"ignored/secret.ts": "console.log('ignored')\n",
	})
	initGitRepo(t, project)

	setTestPipeStdin(t, "ignored\\secret.ts\r\n")
	cfg := parseInProject(t, project, []string{".", "--quiet", "--print", "--include", "-"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "ignored/secret.ts") {
		t.Fatalf("expected stdin --include to restore gitignored file, got:\n%s", stdout.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected quiet stdin --include run to keep stderr empty, got:\n%s", got)
	}
}

func TestRunChangedSelectors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"clean.txt":    "clean\n",
		"staged.txt":   "one\n",
		"unstaged.txt": "one\n",
	})
	initGitRepo(t, project)

	writeProjectFile(t, project, "staged.txt", "two\n")
	runGit(t, project, "add", "staged.txt")
	writeProjectFile(t, project, "unstaged.txt", "two\n")
	writeProjectFile(t, project, "new.txt", "brand new\n")

	tests := []struct {
		name     string
		args     []string
		want     []string
		notWant  []string
		wantWarn string
	}{
		{
			name:    "changed",
			args:    []string{"--print", ".", "--changed"},
			want:    []string{"staged.txt", "unstaged.txt", "new.txt"},
			notWant: []string{"clean.txt"},
		},
		{
			name:    "staged",
			args:    []string{"--print", ".", "--staged"},
			want:    []string{"staged.txt"},
			notWant: []string{"unstaged.txt", "new.txt", "clean.txt"},
		},
		{
			name:    "unstaged",
			args:    []string{"--print", ".", "--unstaged"},
			want:    []string{"unstaged.txt"},
			notWant: []string{"staged.txt", "new.txt", "clean.txt"},
		},
		{
			name:    "untracked",
			args:    []string{"--print", ".", "--untracked"},
			want:    []string{"new.txt"},
			notWant: []string{"staged.txt", "unstaged.txt", "clean.txt"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parseInProject(t, project, tc.args)

			var stdout, stderr bytes.Buffer
			if err := run(cfg, &stdout, &stderr); err != nil {
				t.Fatalf("run returned error: %v", err)
			}

			out := stdout.String()
			for _, want := range tc.want {
				if !strings.Contains(out, `<file path="`+want+`">`) {
					t.Fatalf("expected %s in output, got:\n%s", want, out)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(out, `<file path="`+notWant+`">`) {
					t.Fatalf("did not expect %s in output, got:\n%s", notWant, out)
				}
			}
		})
	}
}

func TestRunChangedHardFailsOutsideGit(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})

	cfg := parseInProject(t, project, []string{"--print", ".", "--changed"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-zero exit when --changed is used outside a git repo")
	}
	var exitErr exitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitError, got %T: %v", err, err)
	}
	if exitErr.code != 2 {
		t.Fatalf("expected exit code 2 for single-scope unsatisfiable, got %d", exitErr.code)
	}
	if !strings.Contains(stderr.String(), "require a git repository") {
		t.Fatalf("expected git-required error on stderr, got:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "src/main.ts") {
		t.Fatalf("expected no file output when git selection is unsatisfiable, got:\n%s", stdout.String())
	}
}

func TestRunChangedDiffHardFailsOutsideGit(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})

	cfg := parseInProject(t, project, []string{"--print", ".", "--changed-diff"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-zero exit when --changed-diff is used outside a git repo")
	}
	var exitErr exitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitError, got %T: %v", err, err)
	}
	if exitErr.code != 2 {
		t.Fatalf("expected exit code 2 for single-scope unsatisfiable, got %d", exitErr.code)
	}
	if !strings.Contains(stderr.String(), "require a git repository") {
		t.Fatalf("expected git-required error on stderr, got:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "[diff only]") || strings.Contains(stdout.String(), "src/main.ts") {
		t.Fatalf("expected no file output when --changed-diff is unsatisfiable, got:\n%s", stdout.String())
	}
}

func TestRunChangedWithThenLetsSiblingScopeProceed(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":   "console.log('ok')\n",
		"docs/intro.md": "hello\n",
	})

	cfg := parseInProject(t, project, []string{
		"--quiet", "--print",
		"src", "--changed",
		"--then", "docs",
	})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-zero exit when one scope of --then chain is unsatisfiable")
	}
	var exitErr exitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitError, got %T: %v", err, err)
	}
	if exitErr.code != 1 {
		t.Fatalf("expected exit code 1 for mixed-success multi-scope, got %d", exitErr.code)
	}
	if !strings.Contains(stderr.String(), "require a git repository") {
		t.Fatalf("expected git-required error on stderr (printed even with -q), got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "docs/intro.md") {
		t.Fatalf("expected sibling scope (docs) to still produce output, got:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "src/main.ts") {
		t.Fatalf("did not expect unsatisfiable scope's files in output, got:\n%s", stdout.String())
	}
}

func TestRunPreviewShowsGitStatusMarkers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"staged.txt":    "one\n",
		"unstaged.txt":  "one\n",
		"bothstate.txt": "one\n",
	})
	initGitRepo(t, project)

	writeProjectFile(t, project, "staged.txt", "two\n")
	runGit(t, project, "add", "staged.txt")
	writeProjectFile(t, project, "unstaged.txt", "two\n")
	writeProjectFile(t, project, "bothstate.txt", "two\n")
	runGit(t, project, "add", "bothstate.txt")
	writeProjectFile(t, project, "bothstate.txt", "three\n")
	writeProjectFile(t, project, "new.txt", "brand new\n")

	cfg := parseInProject(t, project, []string{"--preview", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "staged.txt") || !strings.Contains(out, "[S]") {
		t.Fatalf("expected staged marker in preview, got:\n%s", out)
	}
	if !strings.Contains(out, "unstaged.txt") || !strings.Contains(out, "[M]") {
		t.Fatalf("expected unstaged marker in preview, got:\n%s", out)
	}
	if !strings.Contains(out, "bothstate.txt") || !strings.Contains(out, "[SM]") {
		t.Fatalf("expected staged+unstaged marker in preview, got:\n%s", out)
	}
	if !strings.Contains(out, "new.txt") || !strings.Contains(out, "[?]") {
		t.Fatalf("expected untracked marker in preview, got:\n%s", out)
	}
}

func TestPreviewGitStatusPathspecsPreferTargetRoots(t *testing.T) {
	gitCtx := git.Context{}
	units := []output.PreparedFileUnit{
		{Entry: discovery.Entry{RelPath: "src/a.ts", TargetRoot: "src"}},
		{Entry: discovery.Entry{RelPath: "src/b.ts", TargetRoot: "src"}},
		{Entry: discovery.Entry{RelPath: "docs/readme.md", TargetRoot: "docs"}},
		{Entry: discovery.Entry{RelPath: "scattered/file.txt"}},
	}

	got := output.BuildPlan(units).GitStatusPathspecs(gitCtx)
	want := []string{"docs", "scattered/file.txt", "src"}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("unexpected pathspecs: got %q want %q", got, want)
	}
}

func TestSelectionPathsForIgnoredTargetsDoesNotTreatDotAsCoveringIgnoredTargets(t *testing.T) {
	got := discovery.SelectionPathsForIgnoredTargets([]string{".", "src", "node_modules"})
	want := []string{"src", "node_modules"}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("unexpected ignored-target selection filter: got %q want %q", got, want)
	}
}

func TestRunGitDiscoverySkipsTrackedFileSymlinks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".github/copilot-instructions.md": "# instructions\n",
	})
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.Symlink("../.github/copilot-instructions.md", filepath.Join(project, ".claude", "CLAUDE.md")); err != nil {
		t.Fatalf("symlink failed: %v", err)
	}

	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if strings.Contains(stdout.String(), `<file path=".claude/CLAUDE.md">`) {
		t.Fatalf("did not expect tracked file symlink in output, got:\n%s", stdout.String())
	}
}

func TestRunPreviewSkipsTrackedFileSymlink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".github/copilot-instructions.md": "# instructions\n",
	})
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	linkTarget := "../.github/copilot-instructions.md"
	if err := os.Symlink(linkTarget, filepath.Join(project, ".claude", "CLAUDE.md")); err != nil {
		t.Fatalf("symlink failed: %v", err)
	}

	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--preview", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if strings.Contains(stdout.String(), "CLAUDE.md") {
		t.Fatalf("did not expect symlink in preview, got:\n%s", stdout.String())
	}
}

func TestFilterEntriesByContentWithRipgrep(t *testing.T) {

	project := setupTestProject(t, map[string]string{
		"src/todo.ts":  "const x = 'TODO';\n",
		"src/other.ts": "const x = 'done';\n",
	})

	entries := []discovery.Entry{
		{
			AbsPath: filepath.Join(project, "src", "todo.ts"),
			RelPath: "src/todo.ts",
		},
		{
			AbsPath: filepath.Join(project, "src", "other.ts"),
			RelPath: "src/other.ts",
		},
	}

	filtered, err := discovery.FilterEntriesByContent(entries, "TODO")
	if err != nil {
		t.Fatalf("discovery.FilterEntriesByContent returned error: %v", err)
	}
	if got := len(filtered); got != 1 {
		t.Fatalf("expected 1 matching entry, got %d", got)
	}
	if filtered[0].RelPath != "src/todo.ts" {
		t.Fatalf("expected todo.ts match, got %#v", filtered)
	}
}

func TestBuildVisibleFileListWithRipgrepSkipsDirSymlinkDescendants(t *testing.T) {

	project := setupTestProject(t, map[string]string{
		"real/file.ts": "export const value = 1\n",
		"plain.ts":     "export const plain = 1\n",
	})
	if err := os.Symlink("real", filepath.Join(project, "alias")); err != nil {
		t.Fatalf("symlink failed: %v", err)
	}

	resolver := discovery.Resolver{
		Cfg: command.Invocation{WorkingDir: project},
	}
	if err := resolver.BuildVisibleFileList(); err != nil {
		t.Fatalf("BuildVisibleFileList returned error: %v", err)
	}

	paths := make([]string, 0, len(resolver.VisibleFileList))
	for _, entry := range resolver.VisibleFileList {
		paths = append(paths, entry.RelPath)
	}
	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, "real/file.ts") {
		t.Fatalf("expected real file in visible list, got:\n%s", joined)
	}
	if !strings.Contains(joined, "plain.ts") {
		t.Fatalf("expected plain file in visible list, got:\n%s", joined)
	}
	if strings.Contains(joined, "alias/file.ts") {
		t.Fatalf("did not expect directory symlink descendant in visible list, got:\n%s", joined)
	}
}

func TestBuildVisibleFileListWithRipgrepSkipsFileSymlinks(t *testing.T) {

	project := setupTestProject(t, map[string]string{
		"real/file.ts": "export const value = 1\n",
	})
	if err := os.Symlink("real/file.ts", filepath.Join(project, "link.ts")); err != nil {
		t.Fatalf("symlink failed: %v", err)
	}

	resolver := discovery.Resolver{
		Cfg: command.Invocation{WorkingDir: project},
	}
	if err := resolver.BuildVisibleFileList(); err != nil {
		t.Fatalf("BuildVisibleFileList returned error: %v", err)
	}
	for _, entry := range resolver.VisibleFileList {
		if entry.RelPath == "link.ts" {
			t.Fatalf("did not expect file symlink in visible list: %#v", resolver.VisibleFileList)
		}
	}
}

func TestRunDiffOutputsPatchesAndUntrackedContent(t *testing.T) {
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

	cfg := parseInProject(t, project, []string{"--print", ".", "--changed-diff"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="staged.txt" type="diff">`) {
		t.Fatalf("expected staged diff block, got:\n%s", out)
	}
	if !strings.Contains(out, `<file path="unstaged.txt" type="diff">`) {
		t.Fatalf("expected unstaged diff block, got:\n%s", out)
	}
	if !strings.Contains(out, `<file path="new.txt" type="untracked">`) {
		t.Fatalf("expected untracked full-content block, got:\n%s", out)
	}
	if !strings.Contains(out, "@@") || !strings.Contains(out, "+two") {
		t.Fatalf("expected unified diff content, got:\n%s", out)
	}
	if !strings.Contains(out, "brand new") {
		t.Fatalf("expected untracked file content, got:\n%s", out)
	}
}

func TestRunDiffUsesSpecificDiffTypes(t *testing.T) {
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

	tests := []struct {
		name     string
		args     []string
		wantType string
		wantPath string
	}{
		{
			name:     "staged-diff",
			args:     []string{"--print", ".", "--staged-diff"},
			wantType: "staged-diff",
			wantPath: "staged.txt",
		},
		{
			name:     "unstaged-diff",
			args:     []string{"--print", ".", "--unstaged-diff"},
			wantType: "unstaged-diff",
			wantPath: "unstaged.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parseInProject(t, project, tc.args)

			var stdout, stderr bytes.Buffer
			if err := run(cfg, &stdout, &stderr); err != nil {
				t.Fatalf("run returned error: %v", err)
			}

			want := `<file path="` + tc.wantPath + `" type="` + tc.wantType + `">`
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("expected %s in output, got:\n%s", want, stdout.String())
			}
		})
	}
}

func TestRunPreviewShowsDiffOnlyTags(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"staged.txt": "one\n",
	})
	initGitRepo(t, project)

	writeProjectFile(t, project, "staged.txt", "two\n")
	runGit(t, project, "add", "staged.txt")
	writeProjectFile(t, project, "new.txt", "brand new\n")

	cfg := parseInProject(t, project, []string{"--preview", "--headless", ".", "--changed-diff"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	recs := parsePreviewRecordsRoot(t, stdout.String())
	staged, ok := recs["staged.txt"]
	if !ok || staged[4] != "diff only" {
		t.Fatalf("expected staged.txt shape=diff only, got %v in:\n%s", staged, stdout.String())
	}
	// Untracked new.txt has no diff, so it falls back to full content — not diff-only.
	if n, ok := recs["new.txt"]; ok {
		if n[2] != "[?]" {
			t.Errorf("new.txt git = %q, want [?]", n[2])
		}
		if n[4] != "full" {
			t.Errorf("new.txt shape = %q, want full (untracked has no diff)", n[4])
		}
	}
}

func TestRunHissCreatesAndOpensConfig(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})
	t.Setenv("EDITOR", "true")
	t.Setenv("VISUAL", "")

	cfg := parseInProject(t, project, []string{"--hiss"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	data, err := os.ReadFile(discovery.GlobalHissPath())
	if err != nil {
		t.Fatalf("expected hiss file to exist: %v", err)
	}
	if !strings.Contains(string(data), "catclip ignore config") {
		t.Fatalf("expected default hiss contents, got:\n%s", string(data))
	}
}

func TestRunHissResetRestoresDefaults(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})

	path := discovery.GlobalHissPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte("custom/\n"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	cfg := parseInProject(t, project, []string{"--hiss-reset", "--yes"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected hiss file to exist: %v", err)
	}
	if strings.Contains(string(data), "custom/") {
		t.Fatalf("expected hiss file to be reset, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "node_modules/") {
		t.Fatalf("expected default hiss contents after reset, got:\n%s", string(data))
	}
}

func TestHelpTextIncludesShellParitySections(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	hissDisplay := platform.DisplayPath(discovery.GlobalHissPath())
	help := cli.ShortHelpText("0.2.1", hissDisplay, platform.Palette{})
	full := cli.FullHelpText("0.2.1", hissDisplay, platform.Palette{})

	for _, want := range []string{"Quick Start:", "Interactive mode (build commands from menus):", "Filtering:", "Git Filters (requires a git repo):", "For agents and full flag reference: catclip --help-all"} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected short help to contain %q, got:\n%s", want, help)
		}
	}
	for _, want := range []string{"Agent Reference", "OPERATIONS", "TARGETING", "FILTERING", "PIPELINE MODEL", "AUTHORIZATION", "OUTPUT FORMAT", "CLIPBOARD DELIVERY", "EXIT CODES", "COMMON ERRORS", "MODIFIER REFERENCE", platform.DisplayPath(discovery.GlobalHissPath())} {
		if !strings.Contains(full, want) {
			t.Fatalf("expected full help to contain %q, got:\n%s", want, full)
		}
	}
}

func TestClipboardCommandDisplaylessRequiresWayland(t *testing.T) {
	skipUnlessLinux(t, "linux displayless clipboard error")

	t.Setenv("PATH", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "")
	t.Setenv("DISPLAY", "")

	_, err := output.ClipboardCommand("linux", platform.Palette{})
	if err == nil {
		t.Fatal("expected clipboard lookup error")
	}
	for _, want := range []string{"Clipboard output requires Wayland", "No Wayland session was detected", "--print", "--headless"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("displayless clipboard error missing %q, got: %v", want, err)
		}
	}
	legacyTool := "x" + "clip"
	if strings.Contains(err.Error(), legacyTool) {
		t.Fatalf("displayless clipboard error must not mention %s, got: %v", legacyTool, err)
	}
}

func TestClipboardCommandWaylandMissingWlCopyShowsInstallHint(t *testing.T) {
	skipUnlessLinux(t, "linux Wayland clipboard install hint")

	t.Setenv("PATH", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_SESSION_TYPE", "wayland")

	_, err := output.ClipboardCommand("linux", platform.Palette{})
	if err == nil {
		t.Fatal("expected clipboard lookup error")
	}
	for _, want := range []string{"Clipboard output requires wl-copy", "wl-clipboard", "Debian/Ubuntu", "Arch", "Fedora"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Wayland missing-tool error missing %q, got: %v", want, err)
		}
	}
	legacyTool := "x" + "clip"
	if strings.Contains(err.Error(), legacyTool) {
		t.Fatalf("Wayland install hint must not mention %s, got: %v", legacyTool, err)
	}
}

func TestClipboardInstallHintWaylandIncludesFedora(t *testing.T) {
	skipUnlessLinux(t, "wayland clipboard install hint")

	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	hint := output.ClipboardInstallHint("linux", platform.Palette{})
	for _, want := range []string{"wl-clipboard", "Debian/Ubuntu", "Arch", "Fedora"} {
		if !strings.Contains(hint, want) {
			t.Errorf("wayland install hint missing %q, got: %s", want, hint)
		}
	}
}

// TestClipboardInstallHintLinuxRecommendsWlClipboard supersedes the v0.5.3
// X11-specific install-hint test (deleted in v0.6.0). The Linux text and
// bundle clipboard contract is Wayland-only; the hint must recommend
// wl-clipboard with the same multi-distro framing the v0.5.2 install-hint
// cleanup standardized on.
func TestClipboardInstallHintLinuxRecommendsWlClipboard(t *testing.T) {
	skipUnlessLinux(t, "linux clipboard install hint")

	hint := output.ClipboardInstallHint("linux", platform.Palette{})
	for _, want := range []string{"wl-clipboard", "Debian/Ubuntu", "Arch", "Fedora"} {
		if !strings.Contains(hint, want) {
			t.Errorf("linux install hint missing %q, got: %s", want, hint)
		}
	}
	legacyTool := "x" + "clip"
	if strings.Contains(hint, legacyTool) {
		t.Errorf("linux install hint must not mention %s after v0.6.0, got: %s", legacyTool, hint)
	}
}

// EmitBundle still surfaces a unified install-hint for fileclip.ErrToolNotFound
// on Linux. v0.6.0 changed the hint shape to wl-clipboard only; the unified
// framing remains.
func TestEmitBundleSurfacesMultiDistroHintOnToolNotFound(t *testing.T) {
	skipUnlessLinux(t, "linux bundle clipboard install hint")

	originalCopy := output.FileclipCopy
	defer func() { output.FileclipCopy = originalCopy }()
	output.FileclipCopy = func(...string) error {
		return fmt.Errorf("%w: wl-copy not found", fileclip.ErrToolNotFound)
	}

	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	_, err := output.EmitBundle(output.EmitEnvironment{Platform: "linux", WorkingDir: dir}, []byte("bundle payload"), 0, platform.Palette{})
	if err == nil {
		t.Fatal("expected output.EmitBundle to surface tool-not-found error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "No clipboard tool found.") {
		t.Fatalf("expected unified 'No clipboard tool found.' header, got: %v", err)
	}
	for _, want := range []string{"wl-clipboard", "Debian/Ubuntu", "Arch", "Fedora"} {
		if !strings.Contains(msg, want) {
			t.Errorf("bundle install hint missing %q, got: %s", want, msg)
		}
	}
	if strings.Contains(msg, "clipboard command failed") {
		t.Fatalf("ErrToolNotFound should not fall back to the generic 'clipboard command failed' framing, got: %v", err)
	}
}

// TestEmitBundleSurfacesWaylandRequiredOnUnknownSession pins the v0.6.0
// branch for unknown/displayless Linux sessions that asked for clipboard
// delivery. (Detected X11 desktops are blocked at startup, so they never
// reach EmitBundle.) The temp bundle is removed in this branch — the
// remedy is stdout, not manual file copy.
func TestEmitBundleSurfacesWaylandRequiredOnUnknownSession(t *testing.T) {
	skipUnlessLinux(t, "linux unknown-session bundle path")

	originalCopy := output.FileclipCopy
	defer func() { output.FileclipCopy = originalCopy }()
	output.FileclipCopy = func(...string) error {
		return fileclip.ErrLinuxClipboardSessionUnsupported
	}

	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	_, err := output.EmitBundle(output.EmitEnvironment{Platform: "linux", WorkingDir: dir}, []byte("bundle payload"), 0, platform.Palette{})
	if err == nil {
		t.Fatal("expected output.EmitBundle to surface Wayland-required error")
	}
	msg := err.Error()
	for _, want := range []string{
		"Clipboard output requires Wayland",
		"No Wayland session was detected",
		"--print",
		"--headless",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Wayland-required message missing %q, got: %s", want, msg)
		}
	}
	if strings.Contains(msg, "bundle was saved to") {
		t.Errorf("unknown-session branch must not preserve the bundle file, got: %s", msg)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read bundle dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected temp bundle to be removed, got %d entries", len(entries))
	}
}

func TestEmitBundlePreservesBundleOnLegacyGNOMEUnsupported(t *testing.T) {
	originalCopy := output.FileclipCopy
	defer func() { output.FileclipCopy = originalCopy }()
	output.FileclipCopy = func(...string) error {
		return fileclip.ErrLegacyGNOMEUnsupported
	}

	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	_, err := output.EmitBundle(output.EmitEnvironment{Platform: "linux", WorkingDir: dir}, []byte("bundle payload"), 0, platform.Palette{})
	if err == nil {
		t.Fatalf("expected output.EmitBundle to surface GNOME below %d unsupported error", fileclip.MinimumGNOMEFileClipboardMajor)
	}
	msg := err.Error()
	for _, want := range []string{fmt.Sprintf("GNOME below %d file-reference clipboard is not supported", fileclip.MinimumGNOMEFileClipboardMajor), "Nothing was placed on your clipboard", "Your catclip bundle was saved to:", "Drag it into the target application", "--no-bundle", fmt.Sprintf("For one-step paste, upgrade to GNOME %d or newer.", fileclip.MinimumGNOMEFileClipboardMajor)} {
		if !strings.Contains(msg, want) {
			t.Errorf("GNOME below %d unsupported message missing %q, got: %s", fileclip.MinimumGNOMEFileClipboardMajor, want, msg)
		}
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read bundle dir: %v", readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("expected preserved bundle file, got %d entries", len(entries))
	}
}

// Non-ErrToolNotFound errors keep the generic "clipboard command failed"
// framing — those represent runtime failures from a present binary, not
// a missing-tool situation, and the install hint would be misleading.
func TestEmitBundleKeepsGenericFramingForToolFailures(t *testing.T) {
	originalCopy := output.FileclipCopy
	defer func() { output.FileclipCopy = originalCopy }()
	output.FileclipCopy = func(...string) error {
		return fmt.Errorf("%w: wl-copy: exit status 1", fileclip.ErrToolFailed)
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "")

	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	_, err := output.EmitBundle(output.EmitEnvironment{Platform: "linux", WorkingDir: dir}, []byte("bundle payload"), 0, platform.Palette{})
	if err == nil {
		t.Fatal("expected output.EmitBundle to surface tool-failed error")
	}
	if !strings.Contains(err.Error(), "clipboard command failed") {
		t.Fatalf("expected generic 'clipboard command failed' framing, got: %v", err)
	}
	if strings.Contains(err.Error(), "No clipboard tool found.") {
		t.Fatalf("tool-failed should not use the not-found hint, got: %v", err)
	}
}

func TestWithPayloadWriterDoesNotBlockOnResidentWaylandClipboard(t *testing.T) {
	skipUnlessLinux(t, "wayland clipboard handoff")

	dir := t.TempDir()
	wlCopy := filepath.Join(dir, "wl-copy")
	script := "#!/bin/sh\ncat >/dev/null\nsleep 2\n"
	if err := os.WriteFile(wlCopy, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake wl-copy: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("CATCLIP_CLIPBOARD_WAIT_MS", "10")

	cfg := output.EmitConfig{OutputMode: command.OutputModeClipboard}
	env := output.EmitEnvironment{Platform: "linux"}
	started := time.Now()
	stats, err := output.WithPayloadWriter(cfg, env, io.Discard, platform.Palette{}, func(w io.Writer) error {
		_, err := io.WriteString(w, "hello")
		return err
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("output.WithPayloadWriter returned error: %v", err)
	}
	if stats.SinkName != "wl-copy" {
		t.Fatalf("expected wl-copy sink, got %q", stats.SinkName)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected clipboard handoff to return quickly, took %s", elapsed)
	}
}

func installFakeFzf(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	state := filepath.Join(dir, "target-plus-state")
	backState := filepath.Join(dir, "ignored-back-state")
	script := `#!/bin/sh
filter=""
query=""
prompt=""
expect=""
print_query=0
state_file="` + state + `"
back_state_file="` + backState + `"
while [ "$#" -gt 0 ]; do
	printf 'ARG:%s\n' "$1" >&2
	case "$1" in
		--filter)
			filter="$2"
			shift 2
			;;
		--expect)
			expect="$2"
			shift 2
			;;
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

	case "$prompt" in
	"select> ")
		case "$query" in
			"")
				emit_query
				printf '%s\n' "$input" | grep -F "select all files"
				exit 0
				;;
			common)
				emit_query
				printf '%s\n' "$input" | grep -F "[dir] src/common"
				printf '%s\n' "$input" | grep -F "[dir] lib/common"
				printf '%s\n' "$input" | grep -F "[dir] shared/common"
				printf '%s\n' "$input" | grep -F "[file] src/common.ts"
				exit 0
				;;
			src/)
				emit_query
				printf '%s\n' "$input" | grep -F "[dir] src"
				printf '%s\n' "$input" | grep -F "[dir] docs/src"
				printf '%s\n' "$input" | grep -F "[dir] tools/src"
				exit 0
				;;
			src/vs/platform)
				emit_query
				printf '%s\n' "$input" | grep -F "[dir] src/vs/platform"
				printf '%s\n' "$input" | grep -F "[dir] tools/src/vs/platform"
				exit 0
				;;
		esac
		;;
	"then> ")
		emit_query
		printf '%s\n' "$input" | head -n 1
		exit 0
		;;
	esac

	case "$prompt" in
	"include> ")
		case "$query" in
			node)
				emit_query
				printf '%s\n' "$input" | grep -F "[ignored dir .hiss] node_modules" | head -n 1
				exit 0
				;;
		esac
		;;
	esac

value="$filter"
if [ -z "$value" ]; then
	value="$query"
fi

	case "$value" in
		cmpnts)
			emit_query
			printf '%s\n' "$input" | grep -F "src/components" | head -n 1
			exit 0
			;;
		platform)
			emit_query
			printf '%s\n' "$input" | grep -F "platform" | head -n 1
			exit 0
			;;
		src)
			emit_query
			printf '%s\n' "$input" | grep -F "src" | head -n 1
			exit 0
			;;
		common)
			emit_query
			printf '%s\n' "$input" | grep -F "src/common" | head -n 1
			exit 0
			;;
	auth)
		emit_query
		printf '%s\n' "$input" | grep -F "/auth" | head -n 1
		exit 0
		;;
	btn)
		emit_query
		printf '%s\n' "$input" | grep -F "Button.tsx" | head -n 1
		exit 0
		;;
	btn.tsx)
		emit_query
		printf '%s\n' "$input" | grep -F "Button.tsx" | head -n 1
		exit 0
		;;
	policy)
		emit_query
		printf '%s\n' "$input" | grep -F "policy.ts" | head -n 1
		exit 0
		;;
esac

emit_query
printf '%s\n' "$input"
`
	return installScriptFzf(t, script)
}

func installScriptFzf(t *testing.T, script string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake-fzf cannot run on Windows (no /bin/sh); revisit when Go-binary fake-fzf lands")
	}

	// Several fake-fzf scripts use `$'\t'` ANSI-C quoting to embed tabs in
	// grep patterns. macOS /bin/sh is bash 3.2 (which expands `$'...'`),
	// but Ubuntu /bin/sh is dash (which does not). Force bash so the same
	// scripts run identically on both runners.
	script = strings.Replace(script, "#!/bin/sh", "#!/bin/bash", 1)

	dir := t.TempDir()
	path := filepath.Join(dir, "fake-fzf")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake fzf: %v", err)
	}
	t.Setenv("CATCLIP_FZF", path)
	return path
}

func setupTestProject(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("HOME", root)

	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir failed for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write failed for %s: %v", rel, err)
		}
	}

	return root
}

func TestNormalizeInteractivePickerQueryTreatsBareStarAsEmpty(t *testing.T) {
	if got := discovery.NormalizeInteractivePickerQuery("*"); got != "" {
		t.Fatalf("discovery.NormalizeInteractivePickerQuery(*) = %q, want empty", got)
	}
	if got := discovery.NormalizeInteractivePickerQuery(" * "); got != "" {
		t.Fatalf("discovery.NormalizeInteractivePickerQuery(\" * \") = %q, want empty", got)
	}
	if got := discovery.NormalizeInteractivePickerQuery("src/*"); got != "src/*" {
		t.Fatalf("discovery.NormalizeInteractivePickerQuery(src/*) = %q, want src/*", got)
	}
}

func TestRunSkipsCoveredLaterTargetInScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/components/ui/Button.tsx":             "export function Button() {}\n",
		"src/features/profile/components/Card.tsx": "export function Card() {}\n",
		"src/shared/components/Badge.js":           "export function Badge() {}\n",
		"src/index.js":                             "console.log('ok')\n",
		"docs/components-guide.md":                 "# guide\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "components", "--only", "*.js"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/index.js">`) {
		t.Fatalf("expected src/index.js in output, got:\n%s", out)
	}
	if !strings.Contains(out, `<file path="src/shared/components/Badge.js">`) {
		t.Fatalf("expected Badge.js in output, got:\n%s", out)
	}
	if strings.Contains(out, "docs/components-guide.md") {
		t.Fatalf("expected covered later target not to widen scope outside src, got:\n%s", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected quiet run to keep stderr empty, got:\n%s", stderr.String())
	}
}

func TestFormatHeadlessCandidateListTruncatesAfterLimit(t *testing.T) {
	items := []string{
		"a", "b", "c", "d", "e",
		"f", "g", "h", "i", "j",
		"k", "l",
	}

	got := discovery.FormatHeadlessCandidateList(items)
	for _, item := range items[:10] {
		if !strings.Contains(got, item) {
			t.Fatalf("expected candidate list to include %q, got %q", item, got)
		}
	}
	if strings.Contains(got, "\n    - k") || strings.Contains(got, "\n    - l") {
		t.Fatalf("expected candidate list to truncate after %d entries, got %q", discovery.HeadlessCandidateListLimit, got)
	}
	if !strings.Contains(got, "... and 2 more") {
		t.Fatalf("expected overflow summary, got %q", got)
	}
}

func TestFormatResolvedStartupCommandShellQuotesArgs(t *testing.T) {
	got := cli.FormatResolvedStartupCommand([]string{"src", "--contains", "TODO items", "--only", "src/a test.ts"})
	// --contains is a regex modifier: always single-quoted. --only is a glob
	// pattern: conditionally quoted (double quotes only when it has spaces).
	want := `catclip src --contains 'TODO items' --only "src/a test.ts"`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestFormatResolvedStartupCommandPreservesLinesModifier(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "lines with start and end",
			args: []string{"src", "--recent", "5", "--lines", "1", "5"},
			want: "catclip src --recent 5 --lines 1 5",
		},
		{
			name: "lines with start only",
			args: []string{"src", "--lines", "10"},
			want: "catclip src --lines 10",
		},
		{
			name: "lines bare",
			args: []string{"src", "--lines"},
			want: "catclip src --lines",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cli.FormatResolvedStartupCommand(tc.args)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// TestCanonicalScopeArgsCoversAllStageKinds enforces the
// resolver-equals-executed invariant: every modifier flag listed in
// cli.ScopeModifierFlagSpecs must round-trip through canonicalScopeArgs.
// Without this, adding a new stage flag without updating the canonical
// builder silently drops it from the "Resolved command:" header — a
// trust-eroding mismatch (see RESOLVED_BUG_resolved_command_drops_lines).
func TestCanonicalScopeArgsCoversAllStageKinds(t *testing.T) {
	for _, spec := range cli.ScopeModifierFlagSpecs {
		t.Run(string(spec.StageKind), func(t *testing.T) {
			s := command.ExecutionScope{
				Stages: []command.Stage{{Kind: spec.StageKind}},
			}
			// Stages that read from sibling fields on command.ExecutionScope
			// rather than from stage.Values/stage.Limit need those
			// fields populated, otherwise the canonical builder
			// emits an incomplete (but still flagged) form. We just
			// need the flag itself to appear.
			switch spec.StageKind {
			case command.StageRecent, command.StageDepth:
				limit := 5
				s.Stages[0].Limit = &limit
			case command.StageSize:
				s.Stages[0].Nums = []int{1, 5}
			case command.StageInclude, command.StageOnly, command.StageExclude, command.StageContains, command.StageNotContains, command.StageSnippet:
				s.Stages[0].Values = []string{"x"}
			case command.StageLines:
				s.LinesStart = 1
				s.LinesEnd = 5
			}
			args := command.CanonicalScopeArgs(s)
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, spec.Flag) {
				t.Fatalf("canonicalScopeArgs missing case for %s (kind=%s); got %q", spec.Flag, spec.StageKind, joined)
			}
		})
	}
}

func TestWriteResolvedStartupCommandPrintsCanonicalCommand(t *testing.T) {
	var stderr bytes.Buffer
	if err := writeResolvedStartupCommand(&stderr, []string{"src", "--contains", "TODO", "--only", "src/a.ts"}); err != nil {
		t.Fatalf("writeResolvedStartupCommand returned error: %v", err)
	}

	want := "Resolved command:\n  catclip src --contains 'TODO' --only src/a.ts\n\n"
	if stderr.String() != want {
		t.Fatalf("expected stderr %q, got %q", want, stderr.String())
	}
}

func TestWriteResolvedStartupCommandShowsImplicitDotScope(t *testing.T) {
	var stderr bytes.Buffer
	if err := writeResolvedStartupCommand(&stderr, []string{"--only", "src/a.ts"}); err != nil {
		t.Fatalf("writeResolvedStartupCommand returned error: %v", err)
	}

	want := "Resolved command:\n  catclip . --only src/a.ts\n\n"
	if stderr.String() != want {
		t.Fatalf("expected stderr %q, got %q", want, stderr.String())
	}
}

func TestFormatResolvedStartupCommandHeadlessIsCanonical(t *testing.T) {
	got := cli.FormatResolvedStartupCommand([]string{"src", "--headless"})
	want := "catclip --headless src"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	if strings.Contains(got, "--quiet") || strings.Contains(got, "--print") {
		t.Fatalf("headless canonical command should not duplicate implied flags, got %q", got)
	}
}

func TestRunInternalContentMatchListOutputsCurrentScopeMatches(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "TODO: src\n",
		"src/util.ts":    "helper\n",
		"shared/util.ts": "TODO: shared\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-content-match-list", "src", "--contains", "TODO"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "[all current matches]") {
		t.Fatalf("expected all-matches row in internal content match list output, got %q", out)
	}
	if !strings.Contains(out, "\tsrc/main.ts\tfile\ttext") {
		t.Fatalf("expected src/main.ts in internal content match list output, got %q", out)
	}
	if strings.Contains(out, "\tshared/util.ts\tfile\ttext") {
		t.Fatalf("shared/util.ts leaked into src-only content match list output: %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunInternalContentMatchListSuppressesInvalidRegex(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: src\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-content-match-list", "src", "--contains", "["})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout for invalid regex, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr for invalid regex, got %q", stderr.String())
	}
}

// firstMatchLinePerFile returns the line of the first match per file.
// Files with no matches are absent. Cross-platform: parses rg's NUL-
// separated path-from-line stream so Windows drive-letter colons in
// the path don't confuse the split.
func TestFirstMatchLinePerFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	c := filepath.Join(dir, "c.txt")
	if err := os.WriteFile(a, []byte("aaa\nTODO: alpha\nmore aaa\n"), 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(b, []byte("xxx\nyyy\nTODO: beta\nzzz\nTODO: again\n"), 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}
	if err := os.WriteFile(c, []byte("no match here\n"), 0o600); err != nil {
		t.Fatalf("write c: %v", err)
	}

	got, err := search.FirstMatchLinePerFile("TODO", []string{a, b, c})
	if err != nil {
		t.Fatalf("firstMatchLinePerFile returned error: %v", err)
	}
	if got[a] != 2 {
		t.Errorf("a.txt first match line = %d, want 2", got[a])
	}
	if got[b] != 3 {
		t.Errorf("b.txt first match line = %d, want 3", got[b])
	}
	if _, present := got[c]; present {
		t.Errorf("c.txt should be absent (no matches), got %d", got[c])
	}
}

func TestFirstMatchLinePerFileEmptyInput(t *testing.T) {
	got, err := search.FirstMatchLinePerFile("TODO", nil)
	if err != nil {
		t.Fatalf("firstMatchLinePerFile returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

// discovery.ContentMatchPreviewWindow appends a +{6}-/2 offset for --contains so
// the preview pane centers on the first match. --snippet stays at the
// default window because snippet mode renders matched blocks already.
func TestContentMatchPreviewWindow(t *testing.T) {
	containsWindow := discovery.ContentMatchPreviewWindow("--contains")
	if !strings.HasSuffix(containsWindow, ":+{6}-/2") {
		t.Errorf("--contains preview window = %q, expected suffix :+{6}-/2", containsWindow)
	}
	if !strings.HasPrefix(containsWindow, picker.DefaultPreviewWindow) {
		t.Errorf("--contains preview window = %q, expected prefix %q", containsWindow, picker.DefaultPreviewWindow)
	}

	snippetWindow := discovery.ContentMatchPreviewWindow("--snippet")
	if snippetWindow != "" {
		t.Errorf("--snippet preview window should be empty (default applies), got %q", snippetWindow)
	}
}

// End-to-end check that the content match list emits first-match lines
// for --contains rows. Goes through ui.RunInternalContentMatchList rather
// than calling the helpers directly so we exercise the wire-up.
func TestRunInternalContentMatchListPopulatesFirstMatchLine(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.go": "package main\nfunc main() {}\nTODO: line three\n",
		"src/b.go": "package main\nTODO: line two\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-content-match-list", "src", "--contains", "TODO"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "\tsrc/a.go\tfile\ttext\t3\n") {
		t.Fatalf("expected a.go first-match line 3, got %q", out)
	}
	if !strings.Contains(out, "\tsrc/b.go\tfile\ttext\t2\n") {
		t.Fatalf("expected b.go first-match line 2, got %q", out)
	}
}

func TestRunInternalContentMatchListUsesSnippetPattern(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "TODO: src\n",
		"src/util.ts":    "helper\n",
		"shared/util.ts": "TODO: shared\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-content-match-list", "src", "--snippet", "TODO"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "[all current matches]") {
		t.Fatalf("expected all-matches row in internal content match list output, got %q", out)
	}
	if !strings.Contains(out, "\tsrc/main.ts\tfile\ttext") {
		t.Fatalf("expected src/main.ts in internal content match list output, got %q", out)
	}
	if strings.Contains(out, "\tshared/util.ts\tfile\ttext") {
		t.Fatalf("shared/util.ts leaked into src-only content match list output: %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunInternalContentMatchListAllowsEmptySnippetQuery(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: src\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-content-match-list", "src", "--snippet", ""})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout for empty snippet query, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr for empty snippet query, got %q", stderr.String())
	}
}

func TestRunInternalFilePreviewOutputsFilePayload(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "const a = 1\nconst b = 2\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "src/main.ts"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected file preview heading, got %q", out)
	}
	if !strings.Contains(out, "1 │ const a = 1") || !strings.Contains(out, "2 │ const b = 2") {
		t.Fatalf("expected cat-style file preview lines, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunInternalFilePreviewOutputsContainsPayloadBeyondOldPreviewByteLimit(t *testing.T) {
	largePrefix := strings.Repeat("x", 128*1024+1024)
	project := setupTestProject(t, map[string]string{
		"src/large.txt": largePrefix + "\nlate TODO match\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "src/large.txt", "--contains", "late TODO"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "late TODO match") {
		t.Fatalf("expected contains preview to include late match beyond old preview byte limit, got %q", out)
	}
	if !strings.Contains(out, largePrefix) {
		t.Fatalf("expected contains preview to keep full file content, got %q", out)
	}
	if strings.Contains(out, "truncated") {
		t.Fatalf("expected full contains preview, got truncated output %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunInternalFilePreviewOutputsSnippetPayload(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "const outside = 0\n\nconst a = 1\nTODO: first\nconst b = 2\n\nconst c = 3\nTODO: second\nconst d = 4\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "src/main.ts", "--snippet", "TODO"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected file preview heading, got %q", out)
	}
	if strings.Contains(out, "const outside = 0") {
		t.Fatalf("did not expect unrelated content in snippet preview, got %q", out)
	}
	if !strings.Contains(out, "TODO: first") || !strings.Contains(out, "TODO: second") {
		t.Fatalf("expected snippet preview to include both matching blocks, got %q", out)
	}
	if !strings.Contains(out, "[lines 3-5]") || !strings.Contains(out, "[lines 7-9]") {
		t.Fatalf("expected snippet preview to label snippet ranges, got %q", out)
	}
	if !strings.Contains(out, "\n5 │ \n") {
		t.Fatalf("expected snippet preview blocks to stay separated, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunInternalFilePreviewOutputsSnippetHintForEmptyRegex(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "const a = 1\nTODO: first\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "src/main.ts", "--snippet", ""})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "Snippet search") || !strings.Contains(out, "Smart-case") {
		t.Fatalf("expected snippet hint preview, got %q", out)
	}
	if strings.Contains(out, "src/main.ts") {
		t.Fatalf("did not expect focused file heading in snippet hint preview, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

// Empty --contains pattern with an empty focused file path is the
// initial state of the content picker (fzf's `{q}` is empty before the
// user types). The preview must render the contains-mode teaching hint
// — NOT the empty-tree "No previewable text files here" message.
func TestRunInternalFilePreviewOutputsContainsHintForEmptyRegex(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "const a = 1\nTODO: first\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "", "--contains", ""})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "Content search") || !strings.Contains(out, "Everyday searches") {
		t.Fatalf("expected contains hint preview, got %q", out)
	}
	if strings.Contains(out, "src/main.ts") {
		t.Fatalf("did not expect focused file heading in contains hint preview, got %q", out)
	}
	_ = project
}

// Same hint path as the contains case, but specifically when a file IS
// focused and the regex is still empty. The hint should fire regardless
// of the focused-row state (matches the picker's actual behavior — fzf
// substitutes both `{3}` and `{q}` per refresh, and the hint must win
// over the file preview while the query is empty).
func TestRunInternalFilePreviewContainsHintWinsOverFocusedFile(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "const a = 1\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "src/main.ts", "--contains", "   "})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "Content search") || strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected contains hint without focused file heading, got %q", out)
	}
	_ = project
}

// When the user typed a regex and focused [all current matches] (empty
// path), the preview should emit the full scope tree from the SCC
// checkpoint — same shape as --only/--exclude's [all files] preview.
// This replaces the misleading "No previewable text files" message.
func TestRunInternalFilePreviewEmitsTreeFromCheckpointForAllMatches(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "TODO: alpha\n",
		"src/b.ts": "TODO: beta\n",
	})

	// Build a checkpoint manually using the same path the picker would
	// take when chooseContentMatchesWithFzf runs.
	parentCfg := parseInProject(t, project, []string{"src"})
	gitCtx := git.Detect(parentCfg.WorkingDir)
	discovered, err := discovery.EvaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, platform.Palette{})
	if err != nil {
		t.Fatalf("discovery.EvaluateScope returned error: %v", err)
	}
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := discovery.WriteCheckpoint(checkpointPath, parentCfg.WorkingDir, discovery.CheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("discovery.WriteCheckpoint returned error: %v", err)
	}

	cfg := parseInProject(t, project, []string{
		"--internal-file-preview",
		"--internal-file-path", "",
		"--internal-prediscovered", checkpointPath,
		"src",
		"--contains", "TODO",
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "a.ts") || !strings.Contains(out, "b.ts") {
		t.Fatalf("expected tree preview with a.ts and b.ts, got %q", out)
	}
	if strings.Contains(out, "Content search") {
		t.Fatalf("expected tree preview, not hint preview, got %q", out)
	}
}

func TestRunInternalFilePreviewOutputsSnippetPayloadBeyondOldPreviewByteLimit(t *testing.T) {
	largePrefix := strings.Repeat("x", 128*1024+1024)
	project := setupTestProject(t, map[string]string{
		"src/large.txt": largePrefix + "\n\nmatch start\nTODO: late\nmatch end\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "src/large.txt", "--snippet", "TODO: late"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "TODO: late") {
		t.Fatalf("expected snippet preview to include late match beyond preview byte limit, got %q", out)
	}
	if strings.Contains(out, "truncated") {
		t.Fatalf("expected snippet preview to be built from extracted blocks, not marked truncated: %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunInternalFilePreviewOutputsDiffPayload(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"staged.txt": "one\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "staged.txt", "two\n")
	runGit(t, project, "add", "staged.txt")

	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "staged.txt", "--staged-diff"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "staged.txt") {
		t.Fatalf("expected diff preview heading, got %q", out)
	}
	if !strings.Contains(out, "diff --git") || !strings.Contains(out, "@@") {
		t.Fatalf("expected diff preview content, got %q", out)
	}
	if strings.Contains(out, `<file path="`) {
		t.Fatalf("did not expect wrapped emit payload in diff preview, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunInternalFilePreviewOutputsDiffPayloadBeyondOldPreviewByteLimit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"tracked.txt": "tracked\n",
	})
	initGitRepo(t, project)
	largePrefix := strings.Repeat("line in staged diff that makes it large enough\n", 4000)
	writeProjectFile(t, project, "staged.txt", largePrefix+"LATE DIFF MARKER\n")
	runGit(t, project, "add", "staged.txt")

	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "staged.txt", "--staged-diff"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "LATE DIFF MARKER") {
		t.Fatalf("expected diff preview to include late marker beyond old preview byte limit, got %q", out)
	}
	if strings.Contains(out, "truncated") {
		t.Fatalf("expected full diff preview, not marked truncated: %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunInternalFilePreviewChangedDiffFallsBackToFullFileForUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"tracked.txt": "tracked\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "new.txt", "brand new\n")

	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "new.txt", "--changed-diff"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "brand new") {
		t.Fatalf("expected full-file fallback content, got %q", out)
	}
	if strings.Contains(out, "diff --git") {
		t.Fatalf("did not expect synthetic diff for untracked preview, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func parseInProject(t *testing.T, project string, args []string) command.Parsed {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	cfg, err := cli.ParseArgs(args)
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	return cfg
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "catclip-tests")
	runGit(t, dir, "config", "user.email", "catclip@example.com")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=catclip-tests",
		"GIT_AUTHOR_EMAIL=catclip@example.com",
		"GIT_COMMITTER_NAME=catclip-tests",
		"GIT_COMMITTER_EMAIL=catclip@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func writeProjectFile(t *testing.T, root, rel, content string) {
	t.Helper()

	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir failed for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed for %s: %v", rel, err)
	}
}

// =============================================================================
// Plan A: Warn-and-continue for unresolvable targets
// =============================================================================

func TestRunUnresolvableTargetWarnsAndExitsNonZero(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "hello\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "zzzznothing"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-zero exit for all-unresolvable targets")
	}
	var exitErr exitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitError, got %T: %v", err, err)
	}
	if exitErr.code != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.code)
	}
	if !strings.Contains(stderr.String(), "Warning:") {
		t.Fatalf("expected warning on stderr even with -q, got:\n%s", stderr.String())
	}
}

func TestRunMixedResolvableAndUnresolvableTargetsOutputsAndWarns(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cli.go": "package main\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "cli.go", "zzzznothing"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-zero exit for mixed targets with unresolvable")
	}
	var exitErr exitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitError, got %T: %v", err, err)
	}
	if exitErr.code != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.code)
	}
	if !strings.Contains(stdout.String(), `<file path="cli.go">`) {
		t.Fatalf("expected resolvable target in output, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Warning:") {
		t.Fatalf("expected warning on stderr, got:\n%s", stderr.String())
	}
}

func TestRunAllResolvableTargetsExitsZero(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cli.go": "package main\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "cli.go"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected exit 0 for all-resolvable targets, got: %v", err)
	}
	if !strings.Contains(stdout.String(), `<file path="cli.go">`) {
		t.Fatalf("expected payload, got:\n%s", stdout.String())
	}
}

// =============================================================================
// Plan B: Glob patterns as targets
// =============================================================================

func TestRunGlobTargetMatchesFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go":   "package main\n",
		"cli.go":    "package main\n",
		"readme.md": "# readme\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "*.go", "--paths"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected glob target to succeed, got: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "cli.go") {
		t.Fatalf("expected .go files in output, got:\n%s", out)
	}
	if strings.Contains(out, "readme.md") {
		t.Fatalf("expected non-.go files excluded, got:\n%s", out)
	}
}

func TestRunMultipleGlobTargetsUnion(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go":   "package main\n",
		"readme.md": "# readme\n",
		"notes.txt": "notes\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "*.go", "*.md", "--paths"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected multiple glob targets to succeed, got: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "readme.md") {
		t.Fatalf("expected .go and .md files in output, got:\n%s", out)
	}
	if strings.Contains(out, "notes.txt") {
		t.Fatalf("expected non-matching files excluded, got:\n%s", out)
	}
}

func TestRunGlobTargetNoMatchWarnsAndExitsNonZero(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go": "package main\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "*.xyz", "--paths"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-zero exit for no-match glob")
	}
	var exitErr exitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitError, got %T: %v", err, err)
	}
	if !strings.Contains(stderr.String(), "Warning:") {
		t.Fatalf("expected warning on stderr for empty glob match, got:\n%s", stderr.String())
	}
}

func TestRunGlobAndPathTargetsUnion(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": "export const ok = true\n",
		"main.go":    "package main\n",
		"readme.md":  "# readme\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "*.go", "--paths"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected path+glob target union to succeed, got: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "src/app.ts") || !strings.Contains(out, "main.go") {
		t.Fatalf("expected src files and .go files in output, got:\n%s", out)
	}
	if strings.Contains(out, "readme.md") {
		t.Fatalf("expected non-matching files excluded, got:\n%s", out)
	}
}
