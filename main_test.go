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
	"github.com/tigreau/catclip/internal/picker"
)

func TestMain(m *testing.M) {
	if os.Getenv("CATCLIP_TEST_RUN_MAIN") == "1" {
		Main()
		os.Exit(0)
	}
	if _, ok := ripgrepBinary(); !ok {
		fmt.Fprintln(os.Stderr, "FATAL: rg not found. Run 'make dev' to set up dev tools.")
		os.Exit(1)
	}
	if _, ok := fzfBinary(); !ok {
		fmt.Fprintln(os.Stderr, "FATAL: fzf not found. Run 'make dev' to set up dev tools.")
		os.Exit(1)
	}
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

func parsedExecutionScopes(t *testing.T, cfg parsedCommand) []executionScope {
	t.Helper()
	return executionScopesFromCommandSpec(cfg.Command)
}

func parsedExecutionScope(t *testing.T, cfg parsedCommand) executionScope {
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
	_, err := parseArgs(nil)
	if err == nil {
		t.Fatal("expected bare parseArgs to error (no implicit '.' target)")
	}
	if !strings.Contains(err.Error(), "no target specified") {
		t.Fatalf("expected no-target error, got: %v", err)
	}
}

func TestParseArgsBuildsMultipleScopes(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--only", "*.ts", "--then", "tests", "--only", "*.test.ts"})
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
	_, err := parseArgs([]string{"--changed-diff"})
	if err == nil {
		t.Fatal("expected modifier-only invocation to error (no implicit '.' target)")
	}
	if !strings.Contains(err.Error(), "no target specified") {
		t.Fatalf("expected no-target error, got: %v", err)
	}
}

func TestParseArgsInternalTreePayloadRequiresExplicitTarget(t *testing.T) {
	_, err := parseArgs([]string{"--internal-tree-payload"})
	if err == nil {
		t.Fatal("expected error for bare --internal-tree-payload")
	}
	if !strings.Contains(err.Error(), "--internal-tree-payload requires an explicit target") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsConsumesMultiValueExcludeStage(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--exclude", "*.snap", "build/"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if got, want := len(scope.Exclude), 2; got != want {
		t.Fatalf("expected %d exclude patterns, got %d", want, got)
	}
}

func TestParseArgsAcceptsBareRecentStage(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--recent"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if got, want := len(scope.Stages), 1; got != want {
		t.Fatalf("expected %d stage, got %d", want, got)
	}
	if scope.Stages[0].Kind != scopeStageRecent {
		t.Fatalf("expected recent stage, got %q", scope.Stages[0].Kind)
	}
	if scope.Stages[0].Limit != nil {
		t.Fatalf("expected bare --recent to have no limit, got %v", *scope.Stages[0].Limit)
	}
}

func TestParseArgsAcceptsRecentLimitAndKeepsStageBoundaries(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--only", "*.ts", "--recent", "5"})
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
	if scope.Stages[0].Kind != scopeStageOnly {
		t.Fatalf("expected first stage to be only, got %q", scope.Stages[0].Kind)
	}
	if scope.Stages[1].Kind != scopeStageRecent {
		t.Fatalf("expected second stage to be recent, got %q", scope.Stages[1].Kind)
	}
	if scope.Stages[1].Limit == nil || *scope.Stages[1].Limit != 5 {
		t.Fatalf("expected recent limit 5, got %+v", scope.Stages[1].Limit)
	}
}

func TestParseArgsRejectsInvalidRecentValue(t *testing.T) {
	_, err := parseArgs([]string{"src", "--recent", "later"})
	if err == nil {
		t.Fatal("expected error for invalid --recent value")
	}
	if !strings.Contains(err.Error(), "--recent takes an optional positive integer") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsRecentEqualsForm(t *testing.T) {
	_, err := parseArgs([]string{"src", "--recent=5"})
	if err == nil {
		t.Fatal("expected error for --recent=5")
	}
	if !strings.Contains(err.Error(), "--recent requires a space before the value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsAcceptsDepthStage(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--depth", "2"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if got, want := len(scope.Stages), 1; got != want {
		t.Fatalf("expected %d stage, got %d", want, got)
	}
	if scope.Stages[0].Kind != scopeStageDepth {
		t.Fatalf("expected depth stage, got %q", scope.Stages[0].Kind)
	}
	if scope.Stages[0].Limit == nil || *scope.Stages[0].Limit != 2 {
		t.Fatalf("expected depth limit 2, got %+v", scope.Stages[0].Limit)
	}
}

func TestParseArgsRejectsInvalidDepthValue(t *testing.T) {
	_, err := parseArgs([]string{"src", "--depth", "0"})
	if err == nil {
		t.Fatal("expected error for invalid --depth value")
	}
	if !strings.Contains(err.Error(), "--depth takes a positive integer") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsDepthEqualsForm(t *testing.T) {
	_, err := parseArgs([]string{"src", "--depth=2"})
	if err == nil {
		t.Fatal("expected error for --depth=2")
	}
	if !strings.Contains(err.Error(), "--depth requires a space before the value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsAcceptsPathsStage(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--paths"})
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
	if scope.Stages[0].Kind != scopeStagePaths {
		t.Fatalf("expected paths stage, got %q", scope.Stages[0].Kind)
	}
}

func TestParseArgsAcceptsRawFlag(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "-r"})
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
		cfg, err := parseArgs(args)
		if err != nil {
			t.Fatalf("parseArgs(%v) returned error: %v", args, err)
		}
		if !cfg.Preview {
			t.Fatalf("parseArgs(%v): expected Preview=true, got %+v", args, cfg)
		}
		if cfg.OutputMode != outputModeStdout {
			t.Fatalf("parseArgs(%v): expected OutputMode=stdout, got %q", args, cfg.OutputMode)
		}
	}
}

func TestParseArgsRejectsContainsAfterPaths(t *testing.T) {
	_, err := parseArgs([]string{"src", "--paths", "--contains", "TODO"})
	if err == nil {
		t.Fatal("expected terminal boundary error after --paths")
	}
	if !strings.Contains(err.Error(), "--paths finalizes the current scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsTreatsIncludeAsAllowedTargetSelection(t *testing.T) {
	cfg, err := parseArgs([]string{".", "--include", "node_modules", "coverage"})
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
	cfg, err := parseArgs([]string{".", "--include", "node_modules", "coverage"})
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
	_, err := parseArgs([]string{"-"})
	if err == nil {
		t.Fatal("expected bare '-' target to fail")
	}
	if !strings.Contains(err.Error(), "'-' is not a valid target path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsStdinModifierWithoutPipe(t *testing.T) {
	if !isTerminalFile(os.Stdin) {
		t.Skip("terminal stdin not available")
	}

	_, err := parseArgs([]string{"src", "--exclude", "-"})
	if err == nil {
		t.Fatal("expected --exclude - without a pipe to fail")
	}
	if !strings.Contains(err.Error(), "--exclude - reads paths from stdin, but no input is being piped") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsEmptyStdinPathList(t *testing.T) {
	setTestPipeStdin(t, "")

	_, err := parseArgs([]string{"src", "--only", "-"})
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
			_, err := parseArgs([]string{".", "--include", tc.value})
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
	cfg, err := parseArgs([]string{".", "--include", "*"})
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
			_, err := parseArgs([]string{".", "--include", "-"})
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
	cfg, err := parseArgs([]string{"src", "--staged"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if !scope.Staged || !scope.Changed {
		t.Fatalf("expected staged to imply changed, got %+v", scope)
	}
}

func TestParseArgsSnippetRequiresPattern(t *testing.T) {
	_, err := parseArgs([]string{"src", "--snippet"})
	if err == nil {
		t.Fatal("expected error for --snippet without a regex pattern")
	}
	if !strings.Contains(err.Error(), "--snippet requires a regex pattern") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsSnippetWithDiff(t *testing.T) {
	_, err := parseArgs([]string{"src", "--snippet", "TODO", "--changed-diff"})
	if err == nil {
		t.Fatal("expected error for --snippet with --diff")
	}
	if !strings.Contains(err.Error(), "--snippet and --diff cannot be combined") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsContainsAfterSnippet(t *testing.T) {
	_, err := parseArgs([]string{"src", "--snippet", "TODO", "--contains", "FIXME"})
	if err == nil {
		t.Fatal("expected error for --contains after --snippet")
	}
	if !strings.Contains(err.Error(), "--contains must come before --snippet in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsRepeatedSnippet(t *testing.T) {
	_, err := parseArgs([]string{"src", "--snippet", "TODO", "--snippet", "FIXME"})
	if err == nil {
		t.Fatal("expected error for repeated --snippet")
	}
	if !strings.Contains(err.Error(), "--snippet cannot be repeated in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsAllowsSnippetAfterEarlierFilters(t *testing.T) {
	cfg, err := parseArgs([]string{".", "--contains", "keep", "--only", "README.md", "--snippet", "show"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scope := parsedExecutionScope(t, cfg)
	if !scope.Snippet || scope.Contains != "keep" || scope.SnippetPattern != "show" {
		t.Fatalf("unexpected parsed scope: %+v", scope)
	}
}

func TestParseArgsAllowsContainsBeforeChangedDiff(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--contains", "TODO", "--changed-diff"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if !scope.Diff || !scope.Changed || scope.Contains != "TODO" {
		t.Fatalf("unexpected parsed scope: %+v", scope)
	}
}

func TestParseArgsRejectsContainsAfterDiff(t *testing.T) {
	_, err := parseArgs([]string{"src", "--changed-diff", "--contains", "TODO"})
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
	cfg, err := parseArgs([]string{"src", "--contains", "--snippet"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scope := parsedExecutionScope(t, cfg)
	if got := scope.Contains; got != "--snippet" {
		t.Fatalf("scope.Contains = %q, want --snippet", got)
	}
}

func TestParseArgsAllowsModifierLikeSnippetPattern(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--snippet", "--contains"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	scope := parsedExecutionScope(t, cfg)
	if got := scope.SnippetPattern; got != "--contains" {
		t.Fatalf("scope.SnippetPattern = %q, want --contains", got)
	}
}

func TestParseArgsAllowsDoubleDashRegexPatterns(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--contains", "--", "--snippet", "--"})
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

func TestParseStartupInputTokensAllowsModifierLikeRegexValues(t *testing.T) {
	parsed, err := parseStartupInputTokens([]string{".", "--contains", "--snippet", "--snippet", "--contains"})
	if err != nil {
		t.Fatalf("parseStartupInputTokens returned error: %v", err)
	}
	if got, want := strings.Join(parsed.modifiers, "\n"), "--contains\n--snippet\n--snippet\n--contains"; got != want {
		t.Fatalf("parsed.modifiers = %q, want %q", got, want)
	}
}

func TestParseStartupInputTokensKeepsSnippetContext(t *testing.T) {
	parsed, err := parseStartupInputTokens([]string{"src", "--snippet", "TODO", "3"})
	if err != nil {
		t.Fatalf("parseStartupInputTokens returned error: %v", err)
	}
	if got, want := strings.Join(parsed.modifiers, "\n"), "--snippet\nTODO\n3"; got != want {
		t.Fatalf("parsed.modifiers = %q, want %q", got, want)
	}
}

func TestParseStartupInputTokensRejectsInvalidIncludeValue(t *testing.T) {
	_, err := parseStartupInputTokens([]string{"--include", "src/../vendor"})
	if err == nil {
		t.Fatal("expected invalid startup include value to fail")
	}
	if !strings.Contains(err.Error(), "--include cannot traverse above the current target scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsGitFilterAfterDiff(t *testing.T) {
	_, err := parseArgs([]string{"src", "--changed-diff", "--staged"})
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
	cfg, err := parseArgs([]string{"src", "--changed-diff", "--recent", "5"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := parsedExecutionScope(t, cfg)
	if !scope.Diff || !scope.Changed {
		t.Fatalf("unexpected parsed scope: %+v", scope)
	}
	if len(scope.Stages) == 0 || scope.Stages[len(scope.Stages)-1].Kind != scopeStageRecent {
		t.Fatalf("expected trailing recent stage, got %+v", scope.Stages)
	}
}

func TestParseArgsRejectsDiffWithoutChangeSelector(t *testing.T) {
	_, err := parseArgs([]string{"src", "--diff"})
	if err == nil {
		t.Fatal("expected error for --diff without change selector")
	}
	if !strings.Contains(err.Error(), "--diff is no longer a standalone modifier") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsUntrackedDiffAlone(t *testing.T) {
	_, err := parseArgs([]string{"src", "--untracked", "--changed-diff"})
	if err == nil {
		t.Fatal("expected error for --untracked --diff")
	}
	if !strings.Contains(err.Error(), "--untracked-diff doesn't make sense") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsContainsEqualsForm(t *testing.T) {
	_, err := parseArgs([]string{"src", "--contains=TODO"})
	if err == nil {
		t.Fatal("expected error for --contains=TODO")
	}
	if !strings.Contains(err.Error(), "--contains requires a space") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsWarnsAboutGlobLikeContainsPattern(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--contains", "use*Context"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if len(cfg.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(cfg.Warnings))
	}
}

func TestParseArgsRejectsExtraContainsValueAfterModifierMode(t *testing.T) {
	_, err := parseArgs([]string{"src", "--contains", "TODO", "extra"})
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
	_, err := parseArgs([]string{"src", "--changed", "extra"})
	if err == nil {
		t.Fatal("expected error for plain token after zero-arg modifier")
	}
	if !strings.Contains(err.Error(), "--changed takes no value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsBareDoubleDashAsPositionalDelimiter(t *testing.T) {
	_, err := parseArgs([]string{"src", "--", "other"})
	if err == nil {
		t.Fatal("expected bare -- delimiter usage to fail")
	}
	if !strings.Contains(err.Error(), "bare -- can only be followed by another bare -- in the same scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsTrailingBareDoubleDashOutsideInteractive(t *testing.T) {
	_, err := parseArgs([]string{"src", "--"})
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
	_, err := parseArgs([]string{".", "--headless", "--"})
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
	cfg, err := parseArgs([]string{".", "--headless"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !cfg.Headless {
		t.Fatal("expected cfg.Headless to be true")
	}
	if cfg.OutputMode != outputModeStdout {
		t.Fatalf("expected OutputMode=stdout, got %q", cfg.OutputMode)
	}
	if !cfg.Quiet {
		t.Fatal("expected cfg.Quiet to be true")
	}
}

func TestParseArgsHeadlessRequiresExplicitTargets(t *testing.T) {
	_, err := parseArgs([]string{"--headless"})
	if err == nil {
		t.Fatal("expected --headless without targets to fail")
	}
	if !strings.Contains(err.Error(), "--headless requires explicit targets") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := parseArgs([]string{".", "--headless"}); err != nil {
		t.Fatalf("--headless with explicit target should succeed, got: %v", err)
	}
}

func TestParseArgsHeadlessPreviewRequiresTargets(t *testing.T) {
	_, err := parseArgs([]string{"--preview", "--headless"})
	if err == nil {
		t.Fatal("expected --preview --headless without targets to fail")
	}
	if !strings.Contains(err.Error(), "--headless requires explicit targets") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := parseArgs([]string{".", "--preview", "--headless"}); err != nil {
		t.Fatalf("--preview --headless with explicit target should succeed, got: %v", err)
	}
}

func TestRunHeadlessRejectsBareDoubleDash(t *testing.T) {
	_, err := parseArgs([]string{".", "--headless", "--"})
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
	if !canPromptInteractively() {
		t.Skip("interactive terminal not available; bare -- test requires TTY for picker")
	}
	_, err := parseArgs([]string{".", "-p", "-q", "--"})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "headless mode") {
		t.Fatalf("-p -q should not produce a headless error, got: %v", err)
	}
}

func TestInternalCommandTargetMessageNotShadowed(t *testing.T) {
	_, err := parseArgs([]string{"--internal-tree-payload"})
	if err == nil {
		t.Fatal("expected --internal-tree-payload without targets to fail")
	}
	if !strings.Contains(err.Error(), "--internal-tree-payload requires an explicit target") {
		t.Fatalf("expected internal-specific error, got: %v", err)
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
	cfg, err := parseArgs([]string{"--version", "src", "--changed"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if cfg.Action != actionVersion {
		t.Fatalf("expected version action, got %q", cfg.Action)
	}
	if got := len(parsedExecutionScopes(t, cfg)); got != 0 {
		t.Fatalf("expected no scopes for immediate action, got %d", got)
	}
}

func TestParseArgsHissResetStillParsesGlobalFlags(t *testing.T) {
	cfg, err := parseArgs([]string{"--hiss-reset", "--yes", "--quiet"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if cfg.Action != actionResetHiss {
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
	if cfg.OutputMode != outputModeClipboard {
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
// The lines picker preview path inherits this via emitOutputPlan.
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
	_, err := parseArgs([]string{"src", "--lines", "0"})
	if err == nil {
		t.Fatal("expected error for --lines 0")
	}
	if !strings.Contains(err.Error(), "--lines start must be >= 1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsEndLessThanStart(t *testing.T) {
	_, err := parseArgs([]string{"src", "--lines", "10", "5"})
	if err == nil {
		t.Fatal("expected error for --lines 10 5")
	}
	if !strings.Contains(err.Error(), "--lines end (5) must be >= start (10)") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsNonIntegerStart(t *testing.T) {
	_, err := parseArgs([]string{"src", "--lines", "abc"})
	if err == nil {
		t.Fatal("expected error for --lines abc")
	}
	if !strings.Contains(err.Error(), "--lines expects line numbers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsNonIntegerEnd(t *testing.T) {
	_, err := parseArgs([]string{"src", "--lines", "10", "abc"})
	if err == nil {
		t.Fatal("expected error for --lines 10 abc")
	}
	if !strings.Contains(err.Error(), "--lines expects line numbers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsWithSnippet(t *testing.T) {
	_, err := parseArgs([]string{"src", "--lines", "--snippet", "a"})
	if err == nil {
		t.Fatal("expected error for --lines --snippet")
	}
	if !strings.Contains(err.Error(), "--lines finalizes the current scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsLinesRejectsWithPaths(t *testing.T) {
	_, err := parseArgs([]string{"src", "--lines", "--paths"})
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
	recs := parsePreviewRecords(t, stdout.String())
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
	if err := emitWrappedReader(&out, "plain.txt", "", strings.NewReader("hello")); err != nil {
		t.Fatalf("emitWrappedReader returned error: %v", err)
	}

	want := "<file path=\"plain.txt\">\nhello\n</file>\n\n"
	if out.String() != want {
		t.Fatalf("unexpected streamed output:\n%s", out.String())
	}
}

func TestEmitWrappedReaderReturnsPathAwareStreamError(t *testing.T) {
	var out bytes.Buffer
	err := emitWrappedReader(&out, "broken.txt", "", &errAfterReader{
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

	entries := []fileEntry{
		{RelPath: "a.txt", AbsPath: filepath.Join(project, "a.txt")},
		{RelPath: "b.txt", AbsPath: filepath.Join(project, "b.txt")},
		{RelPath: "c.txt", AbsPath: filepath.Join(project, "c.txt")},
	}
	units, err := prepareFileUnits(gitContext{}, entries)
	if err != nil {
		t.Fatalf("prepareFileUnits returned error: %v", err)
	}

	cfg := emitConfig{OutputMode: outputModeStdout}

	var sequential bytes.Buffer
	if _, err := emitFullOutput(cfg, emitEnvironment{}, units, &sequential, colorPalette{}); err != nil {
		t.Fatalf("sequential emitFullOutput returned error: %v", err)
	}

	t.Setenv("CATCLIP_READ_WORKERS", "2")
	t.Setenv("CATCLIP_PREFETCH_FILE_KIB", "64")

	var prefetched bytes.Buffer
	if _, err := emitFullOutput(cfg, emitEnvironment{}, units, &prefetched, colorPalette{}); err != nil {
		t.Fatalf("prefetch emitFullOutput returned error: %v", err)
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

	_, err := parseArgs([]string{"--quiet", "--print", ".", "--include", "*.js"})
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

	_, err := parseArgs([]string{"--quiet", "--print", "--with-binaries", ".", "--contains", "pattern"})
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

	_, err := parseArgs([]string{"--quiet", "--print", "--with-binaries", ".", "--snippet", "pattern"})
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

	_, err := parseArgs([]string{"--quiet", "--print", "--with-binaries", ".", "--changed-diff"})
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
	candidates := []targetMatch{
		{Path: "src", Kind: "dir"},
		{Path: "src/vs", Kind: "dir"},
		{Path: "src/vs/platform", Kind: "dir"},
		{Path: "src/index.ts", Kind: "file"},
		{Path: "docs/src", Kind: "dir"},
	}

	got := filterRedundantTargetMatches(candidates, []string{"src"})
	want := []targetMatch{{Path: "docs/src", Kind: "dir"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected filtered matches %v, got %v", want, got)
	}
}

func TestSafeTargetPickerHeaderOmitsCtrlO(t *testing.T) {
	header := safeTargetPickerHeader()
	if strings.Contains(header, "[Ctrl-O]") || strings.Contains(header, "ignored ones") {
		t.Fatalf("expected safe picker header to stay visible-target-only, got %q", header)
	}
	if !strings.Contains(header, "Pick files and folders to include.") || !strings.Contains(header, "[Up/Down] move  [Enter] confirm  [Tab] mark  [Esc] exit") {
		t.Fatalf("expected safe picker header to guide first-time fzf users, got %q", header)
	}
}

func TestPickerHeadersUseFourLinesMax60Chars(t *testing.T) {
	headers := map[string]string{
		"safe":         safeTargetPickerHeader(),
		"ignored":      ignoredTargetPickerHeader(),
		"contains":     contentMatchPickerHeader("--contains"),
		"snippet":      contentMatchPickerHeader("--snippet"),
		"snippet-mode": snippetBoundaryPickerHeader(),
		"modifier":     startupModifierPickerHeader(),
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
		"target":         targetPickerHeaderWithEscHint("select> ", "undo"),
		"ignored":        ignoredTargetPickerHeaderWithEscHint("undo"),
		"contains":       contentMatchPickerHeaderWithEscHint("--contains", "undo"),
		"snippet-mode":   snippetBoundaryPickerHeaderWithEscHint("undo"),
		"modifier":       startupModifierPickerHeaderWithEscHint("undo"),
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

func TestTargetMatchLabelsMapsPlainCopyAllSelection(t *testing.T) {
	labels, index := targetMatchLabels([]targetMatch{{Path: ".", Kind: "all"}})
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

func TestTargetMatchLabelsShowIgnoredSourceTemporarily(t *testing.T) {
	labels, index := targetMatchLabels([]targetMatch{
		{Path: "src/components", Kind: "dir", State: treeTargetStateOK},
		{Path: "node_modules", Kind: "dir", State: treeTargetStateNoTextChildren, Ignored: true, IgnoreSource: ".hiss"},
		{Path: "coverage-final.json", Kind: "file", State: treeTargetStateText, Ignored: true, IgnoreSource: ".gitignore"},
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

func TestFzfPreviewCommandUsesCatclipTreeRenderer(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfPreviewCommand(false)
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+" --quiet --internal-tree-payload --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4} {+2}") {
		t.Fatalf("expected preview command to invoke catclip payload producer, got %q", command)
	}
	if !strings.Contains(command, "| "+shellQuoteArg(treeBin)+" --preview-theme "+fzfTreePreviewTheme+" --color always") {
		t.Fatalf("expected preview command to pipe into themed catclip-tree preview renderer, got %q", command)
	}
}

func TestFzfPreviewCommandIncludesIgnoredTargetAuthorization(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfPreviewCommand(true)
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}
	if !strings.Contains(command, shellQuoteArg(self)+" --quiet {+2} --internal-tree-payload --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4} --include {+2}") {
		t.Fatalf("expected ignored target preview to allow the hovered path, got %q", command)
	}
}

func TestFzfContentPreviewCommandUsesFilePreviewPayload(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfContentPreviewCommand("--contains", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+` --quiet --internal-file-preview --internal-file-path {3} --contains {q}`) {
		t.Fatalf("expected contains preview to invoke file preview payload producer, got %q", command)
	}
	if !strings.Contains(command, "| "+shellQuoteArg(treeBin)+" --shape-tags --git-badges --entry-sizes --summary-footer --preview-theme "+fzfTreePreviewTheme+" --color always") {
		t.Fatalf("expected contains preview command to pipe into themed catclip-tree filter renderer, got %q", command)
	}
}

func TestFzfContentSnippetPreviewCommandUsesSnippetFlag(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfContentPreviewCommand("--snippet", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+` --quiet --internal-file-preview --internal-file-path {3} --snippet {q}`) {
		t.Fatalf("expected snippet contains preview to forward --snippet, got %q", command)
	}
}

func TestFzfContentMatchListCommandQuotesMultiwordQuery(t *testing.T) {
	command := fzfContentMatchListCommand([]string{".", "--exclude", "uninstall"}, "--snippet")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+` --quiet --internal-content-match-list . --exclude uninstall --snippet {q}`) {
		t.Fatalf("expected content match list command to pass raw {q} placeholder, got %q", command)
	}
}

func TestFzfDiffFilePreviewCommandUsesFilePreviewPayload(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfDiffFilePreviewCommand([]string{"cmd", "--include", "Formula", "--changed-diff"})
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+" --quiet --internal-file-preview --internal-file-path {3} cmd --include Formula --changed-diff --only {+2}") {
		t.Fatalf("expected diff file preview command to invoke internal file preview payload producer with scope-narrowing --only, got %q", command)
	}
	if !strings.Contains(command, "| "+shellQuoteArg(treeBin)+" --preview-theme "+fzfTreePreviewTheme+" --color always") {
		t.Fatalf("expected diff file preview command to pipe into themed catclip-tree preview renderer, got %q", command)
	}
}

func TestFzfFileSetPreviewCommandInheritsCurrentScope(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfFileSetPreviewCommand([]string{"cmd", "--include", "Formula"}, "--only")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+" --quiet --internal-tree-payload cmd --include Formula --only {+2} --internal-tree-target {3} --internal-tree-kind {4} --internal-tree-state {5}") {
		t.Fatalf("expected file-set preview to inherit current scope and refine by selected rows with hovered-row metadata, got %q", command)
	}
	if !strings.Contains(command, "| "+shellQuoteArg(treeBin)+" --shape-tags --git-badges --entry-sizes --summary-footer --preview-theme "+fzfTreePreviewTheme+" --color always") {
		t.Fatalf("expected file-set preview command to pipe into themed catclip-tree filter renderer, got %q", command)
	}
}

func TestResolveStartupOnlyUsesCheckpointPreviewCommand(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"src/skip.test":  "test\n",
		"shared/util.ts": "console.log('shared')\n",
	})
	_ = parseInProject(t, project, []string{"src", "shared"})
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)
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
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)
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
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)
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

func TestMultiSelectPickerBindingsIncludeRefreshPreview(t *testing.T) {
	bindings := multiSelectPickerBindings()
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

func TestStartupFileSetPreviewCommandKeepsDiffPreviewAfterDiffModeChosen(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := startupFileSetPreviewCommand([]string{"cmd", "--include", "Formula", "--changed-diff"}, "--only", false)
	if !strings.Contains(command, "--internal-file-preview") {
		t.Fatalf("expected --only after diff mode to keep diff preview, got %q", command)
	}
	if !strings.Contains(command, "--changed-diff") {
		t.Fatalf("expected --only after diff mode to inherit current diff scope, got %q", command)
	}
}

func TestStartupFileSetPreviewCommandUsesOnlyRefinementForGitSelectors(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

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
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)
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

func TestStartupModifierCurrentScopePreviewCommandUsesCurrentScopeTree(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	limit := 5
	command := startupModifierCurrentScopePreviewCommand(startupCurrentScopeState{
		Known: true,
		Scopes: []executionScope{
			{
				Targets: []string{"src"},
				Stages:  []scopeStage{{Kind: scopeStageOnly, Values: []string{"*.ts"}}},
			},
			{
				Targets: []string{"docs"},
				Stages:  []scopeStage{{Kind: scopeStageRecent, Limit: &limit}},
			},
		},
	})

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+" --quiet --internal-tree-payload docs --recent 5") {
		t.Fatalf("expected modifier preview to render the current scope tree, got %q", command)
	}
	if strings.Contains(command, " src --only '*.ts'") {
		t.Fatalf("expected modifier preview to exclude earlier scopes, got %q", command)
	}
	if !strings.Contains(command, "| "+shellQuoteArg(treeBin)+" --shape-tags --git-badges --entry-sizes --summary-footer --preview-theme "+fzfTreePreviewTheme+" --color always") {
		t.Fatalf("expected modifier preview command to pipe into themed catclip-tree filter renderer, got %q", command)
	}
}

func TestAllIgnoredTargetsIncludesIgnoredEntries(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts":                  "export const app = true\n",
		"node_modules/react/index.js": "module.exports = {}\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "."})

	resolver := scopeResolver{
		cfg:               invocationConfigFromParsedCommand(cfg),
		allowFileSymlinks: false,
	}

	targets, err := resolver.allIgnoredTargets()
	if err != nil {
		t.Fatalf("allIgnoredTargets returned error: %v", err)
	}

	lookup := make(map[string]targetMatch, len(targets))
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
	if got := lookup["node_modules/react/index.js"].State; got != treeTargetStateText {
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

	resolver := scopeResolver{
		cfg:               invocationConfigFromParsedCommand(cfg),
		gitCtx:            detectGitContext(project),
		allowFileSymlinks: false,
	}

	targets, err := resolver.allIgnoredTargets()
	if err != nil {
		t.Fatalf("allIgnoredTargets returned error: %v", err)
	}

	lookup := make(map[string]targetMatch, len(targets))
	for _, target := range targets {
		lookup[target.Path] = target
	}

	if got, ok := lookup["blocked-empty"]; ok {
		t.Fatalf("expected truly empty directory to be invisible (rg emits no files for it), got %#v", got)
	}
	if got, ok := lookup["blocked-binary"]; !ok || got.State != treeTargetStateNoTextChildren {
		t.Fatalf("expected blocked-binary to be marked no_text_children, got %#v (present=%v)", got, ok)
	}
}

func TestAllIgnoredTargetsIncludesGitignoreEntriesWithoutGitRepo(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":   "blocked/\n",
		"blocked/a.ts": "export const blocked = true\n",
		"src/main.ts":  "export const main = true\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "."})

	resolver := scopeResolver{
		cfg:               invocationConfigFromParsedCommand(cfg),
		gitCtx:            detectGitContext(project),
		allowFileSymlinks: false,
	}

	targets, err := resolver.allIgnoredTargets()
	if err != nil {
		t.Fatalf("allIgnoredTargets returned error: %v", err)
	}

	lookup := make(map[string]targetMatch, len(targets))
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

	got, err := hasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"."}, hissPath)
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

	got, err := hasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"."}, hissPath)
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

	got, err := hasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"."}, hissPath)
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

	got, err := hasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"."}, hissPath)
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

	got, err := hasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"a/b/c/d"}, hissPath)
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

	got, err := hasScopedIgnoredTargetsStreaming(context.Background(), project, []string{"does-not-exist"}, hissPath)
	if err == nil {
		t.Fatalf("expected error for non-existent scope target, got nil (got=%v)", got)
	}
	if got {
		t.Fatalf("expected (false, err) on hard rg error, got (true, %v)", err)
	}
}

// TestCollectChangedRepoPathsUnionEqualsAllThree pins the equality the
// modifier-menu Phase 2 dedupe relies on: `staged ∪ unstaged ∪ untracked`
// is the same set as the old `executionScope{}` call (which spawned a
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

	gitCtx := detectGitContext(project)
	if !gitCtx.Enabled {
		t.Fatal("expected git context to be enabled for the project")
	}

	staged, err := collectChangedRepoPaths(gitCtx, executionScope{Staged: true})
	if err != nil {
		t.Fatalf("staged: %v", err)
	}
	unstaged, err := collectChangedRepoPaths(gitCtx, executionScope{Unstaged: true})
	if err != nil {
		t.Fatalf("unstaged: %v", err)
	}
	untracked, err := collectChangedRepoPaths(gitCtx, executionScope{Untracked: true})
	if err != nil {
		t.Fatalf("untracked: %v", err)
	}
	all, err := collectChangedRepoPaths(gitCtx, executionScope{})
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
	path, err := readableHissPath()
	if err != nil {
		t.Fatalf("readableHissPath: %v", err)
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
		in := executionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"docs/a/keep.md"},
			Stages: []scopeStage{
				{Kind: scopeStageInclude, Values: []string{"docs/a/keep.md"}},
			},
		}
		got := rewriteDeepIncludeScope(in)
		if len(got.IncludedTargets) != 1 || got.IncludedTargets[0] != "docs" {
			t.Fatalf("IncludedTargets = %v, want [docs]", got.IncludedTargets)
		}
		if len(got.Stages) != 2 {
			t.Fatalf("expected 2 stages (include + only), got %d: %#v", len(got.Stages), got.Stages)
		}
		if got.Stages[0].Kind != scopeStageInclude || got.Stages[0].Values[0] != "docs" {
			t.Fatalf("stage 0 should be include[docs], got %#v", got.Stages[0])
		}
		if got.Stages[1].Kind != scopeStageOnly || got.Stages[1].Values[0] != "docs/a/keep.md" {
			t.Fatalf("stage 1 should be only[docs/a/keep.md], got %#v", got.Stages[1])
		}
	})

	t.Run("plain include of target is untouched", func(t *testing.T) {
		in := executionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"docs"},
			Stages:          []scopeStage{{Kind: scopeStageInclude, Values: []string{"docs"}}},
		}
		got := rewriteDeepIncludeScope(in)
		if len(got.IncludedTargets) != 1 || got.IncludedTargets[0] != "docs" {
			t.Fatalf("plain include should be unchanged, got %v", got.IncludedTargets)
		}
		if len(got.Stages) != 1 {
			t.Fatalf("plain include should not gain an --only stage, got %#v", got.Stages)
		}
	})

	t.Run("uncovered include bails", func(t *testing.T) {
		in := executionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"docs/a/keep.md", "src/main.go"},
			Stages:          []scopeStage{{Kind: scopeStageInclude, Values: []string{"docs/a/keep.md", "src/main.go"}}},
		}
		got := rewriteDeepIncludeScope(in)
		if len(got.Stages) != 1 || got.Stages[0].Kind != scopeStageInclude {
			t.Fatalf("uncovered include should leave stages untouched, got %#v", got.Stages)
		}
	})

	t.Run("wildcard include bails", func(t *testing.T) {
		in := executionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"*"},
			Stages:          []scopeStage{{Kind: scopeStageInclude, Values: []string{"*"}}},
		}
		got := rewriteDeepIncludeScope(in)
		if len(got.Stages) != 1 {
			t.Fatalf("wildcard include should be untouched, got %#v", got.Stages)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		in := executionScope{
			Targets:         []string{"docs"},
			IncludedTargets: []string{"docs/a/keep.md"},
			Stages:          []scopeStage{{Kind: scopeStageInclude, Values: []string{"docs/a/keep.md"}}},
		}
		once := rewriteDeepIncludeScope(in)
		twice := rewriteDeepIncludeScope(once)
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

func TestRunInternalTreePayloadAmbiguousTargetFailsWithGuidance(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/common/a.ts": "export const a = 1\n",
		"lib/common/b.ts": "export const b = 2\n",
	})

	cfg := parseInProject(t, project, []string{"--internal-tree-payload", "common"})

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
	if !strings.HasPrefix(out, previewTableHeaderLines[0]+"\n") {
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

	resolver := scopeResolver{
		cfg: invocationConfig{WorkingDir: project},
	}

	if err := resolver.buildVisibleDirIndex(); err != nil {
		t.Fatalf("buildVisibleDirIndex: %v", err)
	}

	got := resolver.visibleDirs.dirs
	wantPresent := []string{"notes", "pkg", "pkg/util", "src"}
	for _, want := range wantPresent {
		if _, ok := resolver.visibleDirs.set[want]; !ok {
			t.Fatalf("expected visible dir %q in %#v", want, got)
		}
	}

	for _, blocked := range []string{"images", "docs-empty", "nested-empty", "truly-empty"} {
		if _, ok := resolver.visibleDirs.set[blocked]; ok {
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
	gitCtx := gitContext{}
	units := []preparedFileUnit{
		{Entry: fileEntry{RelPath: "src/a.ts", TargetRoot: "src"}},
		{Entry: fileEntry{RelPath: "src/b.ts", TargetRoot: "src"}},
		{Entry: fileEntry{RelPath: "docs/readme.md", TargetRoot: "docs"}},
		{Entry: fileEntry{RelPath: "scattered/file.txt"}},
	}

	got := buildOutputPlan(units).GitStatusPathspecs(gitCtx)
	want := []string{"docs", "scattered/file.txt", "src"}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("unexpected pathspecs: got %q want %q", got, want)
	}
}

func TestAllowedByIncludeDirectoryLabelColorsEntireIncludedSubtree(t *testing.T) {
	entry := fileEntry{
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

func TestSelectionPathsForIgnoredTargetsDoesNotTreatDotAsCoveringIgnoredTargets(t *testing.T) {
	got := selectionPathsForIgnoredTargets([]string{".", "src", "node_modules"})
	want := []string{"src", "node_modules"}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("unexpected ignored-target selection filter: got %q want %q", got, want)
	}
}

func TestTargetMatchArgsBatchesIgnoredOnlySelection(t *testing.T) {
	got := targetMatchArgs([]targetMatch{
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

	entries := []fileEntry{
		{
			AbsPath: filepath.Join(project, "src", "todo.ts"),
			RelPath: "src/todo.ts",
		},
		{
			AbsPath: filepath.Join(project, "src", "other.ts"),
			RelPath: "src/other.ts",
		},
	}

	filtered, err := filterEntriesByContent(entries, "TODO")
	if err != nil {
		t.Fatalf("filterEntriesByContent returned error: %v", err)
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

	resolver := scopeResolver{
		cfg: invocationConfig{WorkingDir: project},
	}
	if err := resolver.buildVisibleFileList(); err != nil {
		t.Fatalf("buildVisibleFileList returned error: %v", err)
	}

	paths := make([]string, 0, len(resolver.visibleFileList))
	for _, entry := range resolver.visibleFileList {
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

	resolver := scopeResolver{
		cfg: invocationConfig{WorkingDir: project},
	}
	if err := resolver.buildVisibleFileList(); err != nil {
		t.Fatalf("buildVisibleFileList returned error: %v", err)
	}
	for _, entry := range resolver.visibleFileList {
		if entry.RelPath == "link.ts" {
			t.Fatalf("did not expect file symlink in visible list: %#v", resolver.visibleFileList)
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

	recs := parsePreviewRecords(t, stdout.String())
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

	data, err := os.ReadFile(globalHissPath())
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

	path := globalHissPath()
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

	help := shortHelpText("0.2.1", colorPalette{})
	full := fullHelpText("0.2.1", colorPalette{})

	for _, want := range []string{"Quick Start:", "Interactive mode (build commands from menus):", "Filtering:", "Git Filters (requires a git repo):", "For agents and full flag reference: catclip --help-all"} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected short help to contain %q, got:\n%s", want, help)
		}
	}
	for _, want := range []string{"Agent Reference", "OPERATIONS", "TARGETING", "FILTERING", "PIPELINE MODEL", "AUTHORIZATION", "OUTPUT FORMAT", "CLIPBOARD DELIVERY", "EXIT CODES", "COMMON ERRORS", "MODIFIER REFERENCE", displayPath(globalHissPath())} {
		if !strings.Contains(full, want) {
			t.Fatalf("expected full help to contain %q, got:\n%s", want, full)
		}
	}
}

func TestClipboardCommandShowsInstallHint(t *testing.T) {
	skipUnlessLinux(t, "linux clipboard install hint")

	t.Setenv("PATH", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "")

	_, err := clipboardCommand("linux", colorPalette{})
	if err == nil {
		t.Fatal("expected clipboard lookup error")
	}
	// xsel was previously suggested as a fallback. Removed because it
	// can't reliably serve the bundle path's file-reference MIME targets;
	// see emit.go::clipboardCommand for the rationale.
	if !strings.Contains(err.Error(), "Install xclip") {
		t.Fatalf("expected xclip install hint, got: %v", err)
	}
	if strings.Contains(err.Error(), "xsel") {
		t.Fatalf("install hint should no longer mention xsel, got: %v", err)
	}
}

func TestClipboardInstallHintWaylandIncludesFedora(t *testing.T) {
	skipUnlessLinux(t, "wayland clipboard install hint")

	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	hint := clipboardInstallHint("linux", colorPalette{})
	for _, want := range []string{"wl-clipboard", "Debian/Ubuntu", "Arch", "Fedora"} {
		if !strings.Contains(hint, want) {
			t.Errorf("wayland install hint missing %q, got: %s", want, hint)
		}
	}
}

func TestClipboardInstallHintX11IncludesFedora(t *testing.T) {
	skipUnlessLinux(t, "x11 clipboard install hint")

	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "")
	hint := clipboardInstallHint("linux", colorPalette{})
	for _, want := range []string{"xclip", "Debian/Ubuntu", "Arch", "Fedora"} {
		if !strings.Contains(hint, want) {
			t.Errorf("x11 install hint missing %q, got: %s", want, hint)
		}
	}
}

// The bundle path (large clipboard payloads -> file-ref clipboard via
// fileclip) used to surface fileclip's raw single-line "xclip not found
// (install: sudo apt install xclip)" error, asymmetric to the text path's
// multi-distro hint. emitBundle now intercepts fileclip.ErrToolNotFound
// and emits the same install-hint shape so both sinks teach the user
// identically.
func TestEmitBundleSurfacesMultiDistroHintOnToolNotFound(t *testing.T) {
	skipUnlessLinux(t, "linux bundle clipboard install hint")

	originalCopy := fileclipCopy
	defer func() { fileclipCopy = originalCopy }()
	fileclipCopy = func(...string) error {
		return fmt.Errorf("%w: xclip not found", fileclip.ErrToolNotFound)
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "")

	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	_, err := emitBundle(emitEnvironment{Platform: "linux", WorkingDir: dir}, []byte("bundle payload"), 0, colorPalette{})
	if err == nil {
		t.Fatal("expected emitBundle to surface tool-not-found error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "No clipboard tool found.") {
		t.Fatalf("expected unified 'No clipboard tool found.' header, got: %v", err)
	}
	for _, want := range []string{"xclip", "Debian/Ubuntu", "Arch", "Fedora"} {
		if !strings.Contains(msg, want) {
			t.Errorf("bundle install hint missing %q, got: %s", want, msg)
		}
	}
	if strings.Contains(msg, "clipboard command failed") {
		t.Fatalf("ErrToolNotFound should not fall back to the generic 'clipboard command failed' framing, got: %v", err)
	}
}

func TestEmitBundlePreservesBundleOnX11Unsupported(t *testing.T) {
	originalCopy := fileclipCopy
	defer func() { fileclipCopy = originalCopy }()
	fileclipCopy = func(...string) error {
		return fileclip.ErrX11Unsupported
	}

	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	_, err := emitBundle(emitEnvironment{Platform: "linux", WorkingDir: dir}, []byte("bundle payload"), 0, colorPalette{})
	if err == nil {
		t.Fatal("expected emitBundle to surface X11 unsupported error")
	}
	msg := err.Error()
	for _, want := range []string{"X11 file-reference clipboard is not supported", "Nothing was placed on your clipboard", "Your catclip bundle was saved to:", "Drag it into the target application", "--no-bundle", "For one-step paste, log into a Wayland session."} {
		if !strings.Contains(msg, want) {
			t.Errorf("X11 unsupported message missing %q, got: %s", want, msg)
		}
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read bundle dir: %v", readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("expected preserved bundle file, got %d entries", len(entries))
	}
	body, readErr := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if readErr != nil {
		t.Fatalf("read preserved bundle: %v", readErr)
	}
	if string(body) != "bundle payload" {
		t.Fatalf("preserved bundle body = %q", body)
	}
}

func TestEmitBundlePreservesBundleOnLegacyGNOMEUnsupported(t *testing.T) {
	originalCopy := fileclipCopy
	defer func() { fileclipCopy = originalCopy }()
	fileclipCopy = func(...string) error {
		return fileclip.ErrLegacyGNOMEUnsupported
	}

	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	_, err := emitBundle(emitEnvironment{Platform: "linux", WorkingDir: dir}, []byte("bundle payload"), 0, colorPalette{})
	if err == nil {
		t.Fatalf("expected emitBundle to surface GNOME below %d unsupported error", fileclip.MinimumGNOMEFileClipboardMajor)
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
	originalCopy := fileclipCopy
	defer func() { fileclipCopy = originalCopy }()
	fileclipCopy = func(...string) error {
		return fmt.Errorf("%w: xclip: exit status 1", fileclip.ErrToolFailed)
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "")

	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	_, err := emitBundle(emitEnvironment{Platform: "linux", WorkingDir: dir}, []byte("bundle payload"), 0, colorPalette{})
	if err == nil {
		t.Fatal("expected emitBundle to surface tool-failed error")
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

	cfg := emitConfig{OutputMode: outputModeClipboard}
	env := emitEnvironment{Platform: "linux"}
	started := time.Now()
	stats, err := withPayloadWriter(cfg, env, io.Discard, colorPalette{}, func(w io.Writer) error {
		_, err := io.WriteString(w, "hello")
		return err
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("withPayloadWriter returned error: %v", err)
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
	if got := normalizeInteractivePickerQuery("*"); got != "" {
		t.Fatalf("normalizeInteractivePickerQuery(*) = %q, want empty", got)
	}
	if got := normalizeInteractivePickerQuery(" * "); got != "" {
		t.Fatalf("normalizeInteractivePickerQuery(\" * \") = %q, want empty", got)
	}
	if got := normalizeInteractivePickerQuery("src/*"); got != "src/*" {
		t.Fatalf("normalizeInteractivePickerQuery(src/*) = %q, want src/*", got)
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
	if !strings.Contains(err.Error(), "--include cannot traverse above the current target scope") {
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

func TestResolveStartupScopeInputsBareIncludeOpensIgnoredPicker(t *testing.T) {
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
		t.Fatal("expected bare --include to use the ignored picker")
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
	var noScoped errNoScopedIgnoredTargets
	if !errors.As(err, &noScoped) {
		t.Fatalf("expected errNoScopedIgnoredTargets, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cmd") {
		t.Fatalf("expected error to mention scope target, got: %v", err)
	}
}

func TestResolveBareStartupModifierArgsIncludeReusesIgnoredPicker(t *testing.T) {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !errors.Is(err, errSelectionCancelled) {
		t.Fatalf("expected first-window Esc to cancel invocation, got %v", err)
	}
}

func startupModifierChoiceKeysContain(choices []startupModifierChoice, want string) bool {
	for _, choice := range choices {
		if choice.Key == want {
			return true
		}
	}
	return false
}

func startupModifierChoiceKeys(choices []startupModifierChoice) []string {
	keys := make([]string, 0, len(choices))
	for _, choice := range choices {
		keys = append(keys, choice.Key)
	}
	return keys
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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

func TestFormatHeadlessCandidateListTruncatesAfterLimit(t *testing.T) {
	items := []string{
		"a", "b", "c", "d", "e",
		"f", "g", "h", "i", "j",
		"k", "l",
	}

	got := formatHeadlessCandidateList(items)
	for _, item := range items[:10] {
		if !strings.Contains(got, item) {
			t.Fatalf("expected candidate list to include %q, got %q", item, got)
		}
	}
	if strings.Contains(got, "\n    - k") || strings.Contains(got, "\n    - l") {
		t.Fatalf("expected candidate list to truncate after %d entries, got %q", headlessCandidateListLimit, got)
	}
	if !strings.Contains(got, "... and 2 more") {
		t.Fatalf("expected overflow summary, got %q", got)
	}
}

func TestPromptYesNoErrorsWhenHeadlessPromptGuardActive(t *testing.T) {
	restore := pushHeadlessPromptGuard(true)
	defer restore()

	answer, err := promptYesNo("Are you sure? [y/N]", false, &bytes.Buffer{})
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	if !canPromptInteractively() {
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
	expectedBinding := multiSelectToggleAllBinding()
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

	args, _, err := resolveStartupTrailingActionArgs(resolver, nil, startupTrailingActionExclude)
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
		"discovery.go":     "package catclip\n",
		"cmd/main.go":      "package main\n",
		"content.go":       "package catclip\n",
		"contains_list.go": "package catclip\n",
		"internal/tree/a":  "tree\n",
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
		"--exclude", "internal/tree",
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
	if got, want := strings.Join(args, "\n"), "--only\ndiscovery.go\n--only\ncmd/\n--exclude\ninternal/tree\n--only\ncontent.go\n--exclude\ncontent.go\n--only\ncontains_list.go"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
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
	expectedBinding := multiSelectToggleAllBinding()
	expectedKey := multiSelectToggleAllKey()
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
	printf '%%s\n' "$bindings" | grep -F -- "start:reload:" >/dev/null || {
		echo "missing start reload binding" >&2
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
	expectedBinding := multiSelectToggleAllBinding()
	expectedKey := multiSelectToggleAllKey()
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
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)
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
	printf '%s\n' "$preview" | grep -F -- '--internal-file-preview --internal-file-path {3}' >/dev/null || {
		echo "missing file preview command: $preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- '--snippet {q}' >/dev/null || {
		echo "missing snippet flag/query: $preview" >&2
		exit 91
	}
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

func TestBuildSnippetBoundaryPreviewForScopeStreamsSnippetOutput(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "before main\nTODO: one\nafter main\n",
		"src/util.ts": "before util\nTODO: two\nafter util\n",
	})
	_ = parseInProject(t, project, []string{"."})
	view, err := resolvedCurrentScopeViewForArgs([]string{"src"})
	if err != nil {
		t.Fatalf("resolvedCurrentScopeViewForArgs returned error: %v", err)
	}

	matched, err := snippetBoundaryPreviewMatchedEntries(view, "TODO", nil)
	if err != nil {
		t.Fatalf("snippetBoundaryPreviewMatchedEntries: %v", err)
	}
	cmd, tmpdir := buildSnippetBoundaryPreviewForScope(view, "TODO", matched, nil)
	if cmd == "" || tmpdir == "" {
		t.Fatal("expected snippet boundary preview command and tmpdir")
	}
	defer os.RemoveAll(tmpdir)

	// The preview streams raw snippet output through the internal handler — there
	// is no catclip-tree pipe, so it works even when catclip-tree is absent.
	if strings.Contains(cmd, "catclip-tree") {
		t.Fatalf("streamed boundary preview must not pipe to catclip-tree, got %q", cmd)
	}
	for _, want := range []string{"--internal-snippet-boundary-preview", "--internal-boundary-source", "--internal-boundary-key {2}"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("preview command missing %q, got %q", want, cmd)
		}
	}

	sourcePath := filepath.Join(tmpdir, "source.json")

	// The handler syntax-highlights bodies, so strip the ANSI sequences before
	// substring checks (the <file> wrappers are not highlighted, but body text
	// like "TODO: one" is split by color escapes).
	stripANSI := func(b []byte) string { return string(ansiEscape.ReplaceAll(b, nil)) }

	// Block boundary: the full blank-line-delimited block around each match.
	var blockBuf bytes.Buffer
	if err := runInternalSnippetBoundaryPreview(sourcePath, "block", &blockBuf); err != nil {
		t.Fatalf("stream block boundary: %v", err)
	}
	blockContent := stripANSI(blockBuf.Bytes())
	for _, want := range []string{`<file path="src/main.ts"`, `TODO: one`, `before main`, `after main`, `<file path="src/util.ts"`, `TODO: two`, `before util`, `after util`} {
		if !strings.Contains(blockContent, want) {
			t.Fatalf("block preview missing %q in:\n%s", want, blockContent)
		}
	}

	// Zero-context boundary: only the matching lines, no neighbors.
	var zeroBuf bytes.Buffer
	if err := runInternalSnippetBoundaryPreview(sourcePath, "0", &zeroBuf); err != nil {
		t.Fatalf("stream zero-context boundary: %v", err)
	}
	zeroContent := stripANSI(zeroBuf.Bytes())
	if !strings.Contains(zeroContent, "TODO: one") || !strings.Contains(zeroContent, "TODO: two") {
		t.Fatalf("zero-context preview should include both matching lines, got %q", zeroContent)
	}
	if strings.Contains(zeroContent, "before main") || strings.Contains(zeroContent, "after main") ||
		strings.Contains(zeroContent, "before util") || strings.Contains(zeroContent, "after util") {
		t.Fatalf("zero-context preview should not include neighboring lines, got %q", zeroContent)
	}
}

func TestBuildSnippetBoundaryPreviewForScopePreservesRecentOrder(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a_old.go":    "package main\n\nfunc oldMatch() {}\n",
		"src/z_recent.go": "package main\n\nfunc recentMatch() {}\n",
	})
	now := time.Date(2026, time.May, 25, 12, 0, 0, 0, time.UTC)
	setProjectModTime(t, project, "src/a_old.go", now.Add(-1*time.Hour))
	setProjectModTime(t, project, "src/z_recent.go", now)
	_ = parseInProject(t, project, []string{"."})

	view, err := resolvedCurrentScopeViewForArgs([]string{"src", "--only", "*.go", "--recent", "2"})
	if err != nil {
		t.Fatalf("resolvedCurrentScopeViewForArgs returned error: %v", err)
	}
	matched, err := snippetBoundaryPreviewMatchedEntries(view, "func", nil)
	if err != nil {
		t.Fatalf("snippetBoundaryPreviewMatchedEntries: %v", err)
	}
	cmd, tmpdir := buildSnippetBoundaryPreviewForScope(view, "func", matched, nil)
	if cmd == "" || tmpdir == "" {
		t.Fatal("expected snippet boundary preview command and tmpdir")
	}
	defer os.RemoveAll(tmpdir)

	// Lazy preview: the picker open serializes a source; the per-focus handler
	// streams one boundary. Stream the zero-context ("0") boundary the way the
	// handler would and verify --recent order survives into it.
	var zeroBuf bytes.Buffer
	if err := runInternalSnippetBoundaryPreview(filepath.Join(tmpdir, "source.json"), "0", &zeroBuf); err != nil {
		t.Fatalf("stream zero-context boundary: %v", err)
	}
	content := zeroBuf.String()
	recentIndex := strings.Index(content, `path="src/z_recent.go"`)
	oldIndex := strings.Index(content, `path="src/a_old.go"`)
	if recentIndex < 0 || oldIndex < 0 {
		t.Fatalf("preview missing expected files:\n%s", content)
	}
	if recentIndex > oldIndex {
		t.Fatalf("preview should preserve --recent order, got:\n%s", content)
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
	first_label="$(printf '%s\n' "$first" | cut -f1)"
	first_key="$(printf '%s\n' "$first" | cut -f2)"
	last_key="$(printf '%s\n' "$last" | cut -f2)"
	last_label="$(printf '%s\n' "$last" | cut -f1)"
	last_desc="$(printf '%s\n' "$last" | cut -f3)"
	[ "$first_key" = "only" ] || {
		echo "unexpected first modifier row: $first" >&2
		exit 91
	}
	[ "$last_key" = "then" ] || {
		echo "unexpected last modifier row: $last" >&2
		exit 91
	}
	printf '%s\n' "$last_label" | grep -F -- "--then" >/dev/null || {
		echo "missing --then label in last row: $last_label" >&2
		exit 91
	}
	printf '%s\n' "$last_label" | grep -F -- "Chain a new scope with its own targets and filters" >/dev/null && {
		echo "label column should not contain description text: $last_label" >&2
		exit 91
	}
	printf '%s\n' "$last_desc" | grep -F -- "Chain a new scope with its own targets and filters" >/dev/null || {
		echo "missing --then description in description column: $last_desc" >&2
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

	args, _, err := resolveStartupTrailingActionArgs(resolver, []string{"src"}, startupTrailingActionContains)
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

	_, _, err = resolveStartupTrailingActionArgs(resolver, []string{"src", "--changed-diff"}, startupTrailingActionContains)
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

	_, _, err = resolveStartupTrailingActionArgs(resolver, []string{"src", "--snippet", "TODO"}, startupTrailingActionContains)
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

	args, _, err := resolveStartupTrailingActionArgs(resolver, []string{"src"}, startupTrailingActionSnippet)
	if err != nil {
		t.Fatalf("resolveStartupTrailingActionArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--snippet\nTODO"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestFormatResolvedStartupCommandShellQuotesArgs(t *testing.T) {
	got := formatResolvedStartupCommand([]string{"src", "--contains", "TODO items", "--only", "src/a test.ts"})
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
			got := formatResolvedStartupCommand(tc.args)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// TestCanonicalScopeArgsCoversAllStageKinds enforces the
// resolver-equals-executed invariant: every modifier flag listed in
// scopeModifierFlagSpecs must round-trip through canonicalScopeArgs.
// Without this, adding a new stage flag without updating the canonical
// builder silently drops it from the "Resolved command:" header — a
// trust-eroding mismatch (see RESOLVED_BUG_resolved_command_drops_lines).
func TestCanonicalScopeArgsCoversAllStageKinds(t *testing.T) {
	for _, spec := range scopeModifierFlagSpecs {
		t.Run(string(spec.StageKind), func(t *testing.T) {
			s := executionScope{
				Stages: []scopeStage{{Kind: spec.StageKind}},
			}
			// Stages that read from sibling fields on executionScope
			// rather than from stage.Values/stage.Limit need those
			// fields populated, otherwise the canonical builder
			// emits an incomplete (but still flagged) form. We just
			// need the flag itself to appear.
			switch spec.StageKind {
			case scopeStageRecent, scopeStageDepth:
				limit := 5
				s.Stages[0].Limit = &limit
			case scopeStageInclude, scopeStageOnly, scopeStageExclude, scopeStageContains, scopeStageSnippet:
				s.Stages[0].Values = []string{"x"}
			case scopeStageLines:
				s.LinesStart = 1
				s.LinesEnd = 5
			}
			args := canonicalScopeArgs(s)
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
	got := formatResolvedStartupCommand([]string{"src", "--headless"})
	want := "catclip --headless src"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	if strings.Contains(got, "--quiet") || strings.Contains(got, "--print") {
		t.Fatalf("headless canonical command should not duplicate implied flags, got %q", got)
	}
}

func TestShouldWriteResolvedStartupCommandHonorsPickerSelectedHeadless(t *testing.T) {
	cfg, err := parseArgs([]string{".", "--headless"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !cfg.Quiet {
		t.Fatal("test setup expected --headless to imply quiet")
	}

	if !shouldWriteResolvedStartupCommand(startupPickerResult{UsedFzf: true, ForceResolvedCommand: true}, cfg.Quiet) {
		t.Fatal("expected picker-selected --headless to still print the resolved command")
	}
	if shouldWriteResolvedStartupCommand(startupPickerResult{UsedFzf: true}, cfg.Quiet) {
		t.Fatal("expected typed quiet/headless command without force to stay quiet")
	}
	if shouldWriteResolvedStartupCommand(startupPickerResult{ForceResolvedCommand: true}, cfg.Quiet) {
		t.Fatal("expected no resolved command when fzf was not used")
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
	if !strings.Contains(out, contentMatchAllMatchesLabel) {
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

func TestRunInternalContentMatchListUsesSingleLabelForRootFiles(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cli.go": "TODO: root\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-content-match-list", ".", "--contains", "TODO"})

	var stdout bytes.Buffer
	if err := runInternalContentMatchList(contentMatchListConfigFromParsedCommand(cfg), &stdout); err != nil {
		t.Fatalf("runInternalContentMatchList returned error: %v", err)
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

	got, err := firstMatchLinePerFile("TODO", []string{a, b, c})
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
	got, err := firstMatchLinePerFile("TODO", nil)
	if err != nil {
		t.Fatalf("firstMatchLinePerFile returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
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

// contentMatchPreviewWindow appends a +{6}-/2 offset for --contains so
// the preview pane centers on the first match. --snippet stays at the
// default window because snippet mode renders matched blocks already.
func TestContentMatchPreviewWindow(t *testing.T) {
	containsWindow := contentMatchPreviewWindow("--contains")
	if !strings.HasSuffix(containsWindow, ":+{6}-/2") {
		t.Errorf("--contains preview window = %q, expected suffix :+{6}-/2", containsWindow)
	}
	if !strings.HasPrefix(containsWindow, picker.DefaultPreviewWindow) {
		t.Errorf("--contains preview window = %q, expected prefix %q", containsWindow, picker.DefaultPreviewWindow)
	}

	snippetWindow := contentMatchPreviewWindow("--snippet")
	if snippetWindow != "" {
		t.Errorf("--snippet preview window should be empty (default applies), got %q", snippetWindow)
	}
}

// End-to-end check that the content match list emits first-match lines
// for --contains rows. Goes through runInternalContentMatchList rather
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
	if !strings.Contains(out, contentMatchAllMatchesLabel) {
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

	var rendered bytes.Buffer
	if err := RunTreeCLI(nil, &stdout, &rendered, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunTreeCLI returned error: %v", err)
	}

	out := rendered.String()
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

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File == nil {
		t.Fatal("expected file preview payload")
	}
	if !strings.Contains(doc.File.Content, "late TODO match") {
		t.Fatalf("expected contains preview to include late match beyond old preview byte limit, got %q", doc.File.Content)
	}
	if got := doc.File.MatchPattern; got != "late TODO" {
		t.Fatalf("doc.File.MatchPattern = %q, want %q", got, "late TODO")
	}
	if !strings.Contains(doc.File.Content, largePrefix) {
		t.Fatalf("expected contains preview payload to keep full file content, got %q", doc.File.Content)
	}
	if doc.File.Truncated {
		t.Fatalf("expected full contains preview payload, not marked truncated")
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

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File == nil {
		t.Fatal("expected file preview payload")
	}
	if got := doc.File.Path; got != "src/main.ts" {
		t.Fatalf("doc.File.Path = %q, want src/main.ts", got)
	}
	if got := doc.File.MatchPattern; got != "TODO" {
		t.Fatalf("doc.File.MatchPattern = %q, want \"TODO\" for snippet block preview", got)
	}
	if got, want := doc.File.FocusLines, []int{2, 3, 4, 7, 8, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("doc.File.FocusLines = %v, want %v", got, want)
	}
	if strings.Contains(doc.File.Content, "const outside = 0") {
		t.Fatalf("did not expect unrelated content in snippet preview, got %q", doc.File.Content)
	}
	if !strings.Contains(doc.File.Content, "TODO: first") || !strings.Contains(doc.File.Content, "TODO: second") {
		t.Fatalf("expected snippet preview to include both matching blocks, got %q", doc.File.Content)
	}
	if !strings.Contains(doc.File.Content, "[lines 3-5]") || !strings.Contains(doc.File.Content, "[lines 7-9]") {
		t.Fatalf("expected snippet preview to label snippet ranges, got %q", doc.File.Content)
	}
	if !strings.Contains(doc.File.Content, "\n\n") {
		t.Fatalf("expected snippet preview blocks to stay separated, got %q", doc.File.Content)
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

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File == nil {
		t.Fatal("expected file preview payload")
	}
	if got := doc.File.Path; got != "" {
		t.Fatalf("doc.File.Path = %q, want empty for snippet hint preview", got)
	}
	if doc.File.MatchPattern != "" {
		t.Fatalf("doc.File.MatchPattern = %q, want empty", doc.File.MatchPattern)
	}
	if len(doc.File.FocusLines) != 0 {
		t.Fatalf("doc.File.FocusLines = %v, want empty", doc.File.FocusLines)
	}
	if got := doc.File.Content; got != internalSnippetPreviewEmptyHint {
		t.Fatalf("doc.File.Content = %q, want %q", got, internalSnippetPreviewEmptyHint)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

// Empty --contains pattern with an empty focused file path is the
// initial state of the content picker (fzf's `{q}` is empty before the
// user types). The preview must render the contains-mode teaching hint
// — NOT the "No previewable text files here" message that catclip-tree
// emits for empty payloads.
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

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File == nil {
		t.Fatal("expected file preview payload for contains hint")
	}
	if doc.File.Path != "" {
		t.Fatalf("doc.File.Path = %q, want empty for hint preview", doc.File.Path)
	}
	if doc.File.Content != internalContainsPreviewEmptyHint {
		t.Fatalf("doc.File.Content = %q, want %q", doc.File.Content, internalContainsPreviewEmptyHint)
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
	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File == nil || doc.File.Content != internalContainsPreviewEmptyHint {
		t.Fatalf("expected contains hint, got %#v", doc.File)
	}
	_ = project
}

// internalPreviewPatternIsEmpty fires only in content-picker context
// (snippet stage OR contains stage). A scope with no content stage
// should not be treated as "empty pattern" — that would short-circuit
// non-content pickers' preview into the hint.
func TestInternalPreviewPatternIsEmptyOnlyContentMode(t *testing.T) {
	cases := []struct {
		name string
		s    executionScope
		want bool
	}{
		{
			name: "contains-empty",
			s: executionScope{
				Contains: "",
				Stages:   []scopeStage{{Kind: scopeStageContains}},
			},
			want: true,
		},
		{
			name: "contains-non-empty",
			s: executionScope{
				Contains: "TODO",
				Stages:   []scopeStage{{Kind: scopeStageContains}},
			},
			want: false,
		},
		{
			name: "snippet-empty",
			s: executionScope{
				Snippet:        true,
				SnippetPattern: "  ",
				Stages:         []scopeStage{{Kind: scopeStageSnippet}},
			},
			want: true,
		},
		{
			name: "no-content-stage",
			s: executionScope{
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
	snippetDoc := buildInternalContentHintDocument(executionScope{
		Snippet: true,
		Stages:  []scopeStage{{Kind: scopeStageSnippet}},
	})
	if snippetDoc.File == nil || snippetDoc.File.Content != internalSnippetPreviewEmptyHint {
		t.Fatalf("snippet hint mismatch: %#v", snippetDoc.File)
	}

	containsDoc := buildInternalContentHintDocument(executionScope{
		Stages: []scopeStage{{Kind: scopeStageContains}},
	})
	if containsDoc.File == nil || containsDoc.File.Content != internalContainsPreviewEmptyHint {
		t.Fatalf("contains hint mismatch: %#v", containsDoc.File)
	}
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
	gitCtx := detectGitContext(parentCfg.WorkingDir)
	discovered, err := evaluateScope(invocationConfigFromParsedCommand(parentCfg), gitCtx, 0, parsedExecutionScope(t, parentCfg), io.Discard, colorPalette{})
	if err != nil {
		t.Fatalf("evaluateScope returned error: %v", err)
	}
	checkpointPath := filepath.Join(t.TempDir(), "scope.json")
	if err := writePrediscoveredCheckpoint(checkpointPath, parentCfg.WorkingDir, prediscoveredCheckpointData{
		GitContext: gitCtx,
		GitStatus:  map[string]string{},
		Entries:    discovered.Entries,
	}); err != nil {
		t.Fatalf("writePrediscoveredCheckpoint returned error: %v", err)
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

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File != nil {
		t.Fatalf("expected tree payload, not file preview, got file=%#v", doc.File)
	}
	if len(doc.Entries) < 2 {
		t.Fatalf("expected at least 2 tree entries (a.ts + b.ts), got %d", len(doc.Entries))
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

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File == nil {
		t.Fatal("expected file preview payload")
	}
	if !strings.Contains(doc.File.Content, "TODO: late") {
		t.Fatalf("expected snippet preview to include late match beyond preview byte limit, got %q", doc.File.Content)
	}
	if doc.File.Truncated {
		t.Fatalf("expected snippet preview payload to be built from extracted blocks, not marked truncated")
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

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File == nil {
		t.Fatal("expected file preview payload")
	}
	if got := doc.File.HighlightPath; got != internalDiffHighlightPath {
		t.Fatalf("doc.File.HighlightPath = %q, want %q", got, internalDiffHighlightPath)
	}
	if !strings.Contains(doc.File.Content, "diff --git") || !strings.Contains(doc.File.Content, "@@") {
		t.Fatalf("expected diff preview content, got %q", doc.File.Content)
	}
	if strings.Contains(doc.File.Content, `<file path="`) {
		t.Fatalf("did not expect wrapped emit payload in diff preview, got %q", doc.File.Content)
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

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File == nil {
		t.Fatal("expected file preview payload")
	}
	if !strings.Contains(doc.File.Content, "LATE DIFF MARKER") {
		t.Fatalf("expected diff preview to include late marker beyond old preview byte limit, got %q", doc.File.Content)
	}
	if got := doc.File.HighlightPath; got != internalDiffHighlightPath {
		t.Fatalf("doc.File.HighlightPath = %q, want %q", got, internalDiffHighlightPath)
	}
	if doc.File.Truncated {
		t.Fatalf("expected full diff preview payload, not marked truncated")
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

	doc, err := decodeTreePayload(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("decodeTreePayload returned error: %v", err)
	}
	if doc.File == nil {
		t.Fatal("expected file preview payload")
	}
	if got := doc.File.HighlightPath; got != "" {
		t.Fatalf("doc.File.HighlightPath = %q, want empty for full-file fallback", got)
	}
	if !strings.Contains(doc.File.Content, "brand new") {
		t.Fatalf("expected full-file fallback content, got %q", doc.File.Content)
	}
	if strings.Contains(doc.File.Content, "diff --git") {
		t.Fatalf("did not expect synthetic diff for untracked preview, got %q", doc.File.Content)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func parseInProject(t *testing.T, project string, args []string) parsedCommand {
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

	cfg, err := parseArgs(args)
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
