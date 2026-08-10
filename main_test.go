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

func TestInternalBenchCommandKindDistinguishesTreePreviewRoutes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "target", args: []string{"--internal-tree-preview", "--internal-tree-target", "src"}, want: "internal-tree-preview-target"},
		{name: "checkpoint", args: []string{"--internal-tree-preview", "--internal-prediscovered", "scope.json", "--internal-tree-target", "src"}, want: "internal-tree-preview-checkpoint"},
		{name: "payload", args: []string{"--internal-tree-preview", "--input-dir", "payload"}, want: "internal-tree-preview-payload"},
		{name: "generic", args: []string{"--internal-tree-preview"}, want: "internal-tree-preview"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := internalBenchCommandKind(test.args); got != test.want {
				t.Fatalf("internalBenchCommandKind(%#v) = %q, want %q", test.args, got, test.want)
			}
		})
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

func requireCLIValidationFailure(t *testing.T, err error, wantReason cli.Reason) cli.ValidationFailure {
	t.Helper()
	if err == nil {
		t.Fatalf("expected CLI validation failure %q", wantReason)
	}
	var failure cli.ValidationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("expected cli.ValidationFailure reason %q, got %T: %v", wantReason, err, err)
	}
	if failure.Reason != wantReason {
		t.Fatalf("validation reason = %q, want %q", failure.Reason, wantReason)
	}
	return failure
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

func TestParseArgsAcceptedCommandShapes(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		check func(t *testing.T, cfg command.Parsed)
	}{
		{
			name: "multiple scopes",
			args: []string{"src", "--only", "*.ts", "--then", "tests", "--only", "*.test.ts"},
			check: func(t *testing.T, cfg command.Parsed) {
				scopes := parsedExecutionScopes(t, cfg)
				if len(scopes) != 2 || !reflect.DeepEqual(scopes[0].Targets, []string{"src"}) || !reflect.DeepEqual(scopes[0].Only, []string{"*.ts"}) || !reflect.DeepEqual(scopes[1].Targets, []string{"tests"}) || !reflect.DeepEqual(scopes[1].Only, []string{"*.test.ts"}) {
					t.Fatalf("unexpected scopes: %#v", scopes)
				}
			},
		},
		{name: "multi-value exclude", args: []string{"src", "--exclude", "*.snap", "build/"}, check: func(t *testing.T, cfg command.Parsed) {
			if got := parsedExecutionScope(t, cfg).Exclude; !reflect.DeepEqual(got, []string{"*.snap", "build/"}) {
				t.Fatalf("exclude = %v", got)
			}
		}},
		{name: "bare recent", args: []string{"src", "--recent"}, check: func(t *testing.T, cfg command.Parsed) {
			stages := parsedExecutionScope(t, cfg).Stages
			if len(stages) != 1 || stages[0].Kind != command.StageRecent || stages[0].Limit != nil {
				t.Fatalf("unexpected stages: %#v", stages)
			}
		}},
		{name: "recent limit keeps boundaries", args: []string{"src", "--only", "*.ts", "--recent", "5"}, check: func(t *testing.T, cfg command.Parsed) {
			scope := parsedExecutionScope(t, cfg)
			if len(scope.Stages) != 2 || scope.Stages[0].Kind != command.StageOnly || scope.Stages[1].Kind != command.StageRecent || scope.Stages[1].Limit == nil || *scope.Stages[1].Limit != 5 {
				t.Fatalf("unexpected stages: %#v", scope.Stages)
			}
		}},
		{name: "depth", args: []string{"src", "--depth", "2"}, check: func(t *testing.T, cfg command.Parsed) {
			stages := parsedExecutionScope(t, cfg).Stages
			if len(stages) != 1 || stages[0].Kind != command.StageDepth || stages[0].Limit == nil || *stages[0].Limit != 2 {
				t.Fatalf("unexpected stages: %#v", stages)
			}
		}},
		{name: "paths", args: []string{"src", "--paths"}, check: func(t *testing.T, cfg command.Parsed) {
			scope := parsedExecutionScope(t, cfg)
			if !scope.Paths || len(scope.Stages) != 1 || scope.Stages[0].Kind != command.StagePaths {
				t.Fatalf("unexpected scope: %#v", scope)
			}
		}},
		{name: "raw", args: []string{"src", "-r"}, check: func(t *testing.T, cfg command.Parsed) {
			if !cfg.Raw {
				t.Fatal("Raw = false")
			}
		}},
		{name: "preview then print", args: []string{"src", "--preview", "--print"}, check: assertPreviewStdoutParsed},
		{name: "print then preview", args: []string{"src", "--print", "--preview"}, check: assertPreviewStdoutParsed},
		{name: "no ignore", args: []string{".", "--no-ignore"}, check: func(t *testing.T, cfg command.Parsed) {
			if scope := parsedExecutionScope(t, cfg); !scope.NoIgnore {
				t.Fatalf("unexpected scope: %#v", scope)
			}
		}},
		{name: "staged implies changed", args: []string{"src", "--staged"}, check: func(t *testing.T, cfg command.Parsed) {
			scope := parsedExecutionScope(t, cfg)
			if !scope.Staged || !scope.Changed {
				t.Fatalf("unexpected scope: %#v", scope)
			}
		}},
		{name: "snippet after filters", args: []string{".", "--contains", "keep", "--only", "README.md", "--snippet", "show"}, check: func(t *testing.T, cfg command.Parsed) {
			scope := parsedExecutionScope(t, cfg)
			if !scope.Snippet || scope.Contains != "keep" || scope.SnippetPattern != "show" {
				t.Fatalf("unexpected scope: %#v", scope)
			}
		}},
		{name: "contains before diff", args: []string{"src", "--contains", "TODO", "--changed-diff"}, check: func(t *testing.T, cfg command.Parsed) {
			scope := parsedExecutionScope(t, cfg)
			if !scope.Diff || !scope.Changed || scope.Contains != "TODO" {
				t.Fatalf("unexpected scope: %#v", scope)
			}
		}},
		{name: "modifier-like contains pattern", args: []string{"src", "--contains", "--snippet"}, check: func(t *testing.T, cfg command.Parsed) {
			if got := parsedExecutionScope(t, cfg).Contains; got != "--snippet" {
				t.Fatalf("Contains = %q", got)
			}
		}},
		{name: "modifier-like snippet pattern", args: []string{"src", "--snippet", "--contains"}, check: func(t *testing.T, cfg command.Parsed) {
			if got := parsedExecutionScope(t, cfg).SnippetPattern; got != "--contains" {
				t.Fatalf("SnippetPattern = %q", got)
			}
		}},
		{name: "double-dash regex patterns", args: []string{"src", "--contains", "--", "--snippet", "--"}, check: func(t *testing.T, cfg command.Parsed) {
			scope := parsedExecutionScope(t, cfg)
			if scope.Contains != "--" || scope.SnippetPattern != "--" {
				t.Fatalf("unexpected scope: %#v", scope)
			}
		}},
		{name: "recent after diff", args: []string{"src", "--changed-diff", "--recent", "5"}, check: func(t *testing.T, cfg command.Parsed) {
			scope := parsedExecutionScope(t, cfg)
			if !scope.Diff || !scope.Changed || len(scope.Stages) == 0 || scope.Stages[len(scope.Stages)-1].Kind != command.StageRecent {
				t.Fatalf("unexpected scope: %#v", scope)
			}
		}},
		{name: "glob-like contains warning", args: []string{"src", "--contains", "use*Context"}, check: func(t *testing.T, cfg command.Parsed) {
			if len(cfg.Warnings) != 1 {
				t.Fatalf("warnings = %v", cfg.Warnings)
			}
		}},
		{name: "headless implies stdout and quiet", args: []string{".", "--headless"}, check: func(t *testing.T, cfg command.Parsed) {
			if !cfg.Headless || !cfg.Quiet || cfg.OutputMode != command.OutputModeStdout {
				t.Fatalf("unexpected config: %#v", cfg)
			}
		}},
		{name: "headless preview with target", args: []string{".", "--preview", "--headless"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := cli.ParseArgs(tt.args)
			if err != nil {
				t.Fatalf("ParseArgs() error = %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func assertPreviewStdoutParsed(t *testing.T, cfg command.Parsed) {
	t.Helper()
	if !cfg.Preview || cfg.OutputMode != command.OutputModeStdout {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestParseArgsRejectedCommandShapes(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantText string
	}{
		{name: "bare invocation", wantText: "no target specified"},
		{name: "modifier without target", args: []string{"--changed-diff"}, wantText: "no target specified"},
		{name: "retired internal tree payload", args: []string{"--internal-tree-payload"}, wantText: "Unknown option '--internal-tree-payload'"},
		{name: "recent non-integer", args: []string{"src", "--recent", "later"}, wantText: "--recent takes an optional positive integer"},
		{name: "recent equals", args: []string{"src", "--recent=5"}, wantText: "--recent requires a space before the value"},
		{name: "size non-integer", args: []string{"src", "--size", "large"}, wantText: "--size expects integer KiB values"},
		{name: "size negative", args: []string{"src", "--size", "-1"}, wantText: "--size expects integer KiB values"},
		{name: "size zero max", args: []string{"src", "--size", "0", "0"}, wantText: "--size max must be >= 1 KiB"},
		{name: "size max before min", args: []string{"src", "--size", "100", "10"}, wantText: "--size max (10) must be >= min (100)"},
		{name: "size too many", args: []string{"src", "--size", "1", "2", "3"}, wantText: "--size takes at most two values"},
		{name: "size equals", args: []string{"src", "--size=10"}, wantText: "--size requires a space before the value"},
		{name: "size unknown flag boundary", args: []string{"src", "--size", "--foo"}, wantText: "Unknown option '--foo'"},
		{name: "depth zero", args: []string{"src", "--depth", "0"}, wantText: "--depth takes a positive integer"},
		{name: "depth equals", args: []string{"src", "--depth=2"}, wantText: "--depth requires a space before the value"},
		{name: "bare dash target", args: []string{"-"}, wantText: "'-' is not a valid target path"},
		{name: "removed include", args: []string{".", "--include", "src/generated"}, wantText: "--include is not a supported option"},
		{name: "removed include equals", args: []string{".", "--include=src/generated"}, wantText: "--include is not a supported option"},
		{name: "contains equals", args: []string{"src", "--contains=TODO"}, wantText: "--contains requires a space"},
		{name: "extra contains value", args: []string{"src", "--contains", "TODO", "extra"}, wantText: "--contains 'TODO extra'"},
		{name: "headless without target", args: []string{"--headless"}, wantText: "--headless requires explicit targets"},
		{name: "headless preview without target", args: []string{"--preview", "--headless"}, wantText: "--headless requires explicit targets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cli.ParseArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("ParseArgs() error = %v, want text %q", err, tt.wantText)
			}
		})
	}
}

func TestParseArgsStructuredFailures(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantReason   cli.Reason
		wantFlag     string
		wantBoundary string
		wantNext     string
	}{
		{name: "contains after paths", args: []string{"src", "--paths", "--contains", "TODO"}, wantReason: cli.ReasonTerminalBoundaryOrder, wantBoundary: "--paths", wantNext: "--contains"},
		{name: "snippet missing pattern", args: []string{"src", "--snippet"}, wantReason: cli.ReasonRequiredValue, wantFlag: "--snippet"},
		{name: "snippet with diff", args: []string{"src", "--snippet", "TODO", "--changed-diff"}, wantReason: cli.ReasonDiffSnippetConflict},
		{name: "contains after snippet", args: []string{"src", "--snippet", "TODO", "--contains", "FIXME"}, wantReason: cli.ReasonSnippetContentFilterOrder, wantFlag: "--contains"},
		{name: "repeated snippet", args: []string{"src", "--snippet", "TODO", "--snippet", "FIXME"}, wantReason: cli.ReasonRepeatedOutputMode, wantFlag: "--snippet"},
		{name: "contains after diff", args: []string{"src", "--changed-diff", "--contains", "TODO"}, wantReason: cli.ReasonDiffContentFilterOrder, wantFlag: "--contains", wantBoundary: "--changed-diff"},
		{name: "git filter after diff", args: []string{"src", "--changed-diff", "--staged"}, wantReason: cli.ReasonDiffGitFilterOrder, wantFlag: "--staged", wantBoundary: "--changed-diff"},
		{name: "standalone diff", args: []string{"src", "--diff"}, wantReason: cli.ReasonDiffStandalone},
		{name: "untracked diff", args: []string{"src", "--untracked", "--changed-diff"}, wantReason: cli.ReasonUntrackedDiff},
		{name: "plain token after no-value modifier", args: []string{"src", "--changed", "extra"}, wantReason: cli.ReasonNoValueModifier, wantFlag: "--changed"},
		{name: "bare double dash order", args: []string{"src", "--", "other"}, wantReason: cli.ReasonBarePlaceholderOrder},
		{name: "bare double dash outside interactive", args: []string{"src", "--"}, wantReason: cli.ReasonBarePlaceholderInteractiveOnly},
		{name: "bare double dash in headless", args: []string{".", "--headless", "--"}, wantReason: cli.ReasonBarePlaceholderHeadlessMode},
		{name: "no ignore without target", args: []string{"--no-ignore", "--headless"}, wantReason: cli.ReasonNoIgnoreMissingPositionalTarget, wantFlag: "--no-ignore"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cli.ParseArgs(tt.args)
			failure := requireCLIValidationFailure(t, err, tt.wantReason)
			if failure.Flag != tt.wantFlag || failure.BoundaryFlag != tt.wantBoundary || failure.NextFlag != tt.wantNext {
				t.Fatalf("validation failure = %#v, want flag=%q boundary=%q next=%q", failure, tt.wantFlag, tt.wantBoundary, tt.wantNext)
			}
		})
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

func TestParseArgsRejectsInvalidStdinValues(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		stdin   string
		wantErr string
	}{
		{name: "empty only list", args: []string{"src", "--only", "-"}, wantErr: "--only - received no paths from stdin"},
		{name: "removed include stdin form", args: []string{".", "--include", "-"}, stdin: "src/generated\n", wantErr: "--include is not a supported option"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setTestPipeStdin(t, tc.stdin)
			_, err := cli.ParseArgs(tc.args)
			if err == nil {
				t.Fatalf("expected stdin %q to fail", tc.stdin)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestParseStartupInputTokens* tests moved to internal/ui/startup_input_parse_test.go
// during the v0.6.0 internal/ui extraction — they assert against the
// private startupInputParse.modifiers field which can't be read from
// root.

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

func TestRunSizeOrdersMultipleTargetsAsOneScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/tiny.txt":   strings.Repeat("a", 100),
		"src/medium.txt": strings.Repeat("b", 300),
		"docs/small.md":  strings.Repeat("c", 200),
		"docs/large.md":  strings.Repeat("d", 400),
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "docs", "--size", "--paths"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if got, want := strings.TrimSpace(stdout.String()), strings.Join([]string{
		"docs/large.md",
		"src/medium.txt",
		"docs/small.md",
		"src/tiny.txt",
	}, "\n"); got != want {
		t.Fatalf("multi-target size order = %q, want one scope-wide order %q", got, want)
	}
}

func TestRunStageOrderChangesRecentThenOnlyResult(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/newest.ts":  "newest\n",
		"src/newer.go":   "package src\n",
		"src/older.go":   "package src\n",
		"src/oldest.txt": "oldest\n",
	})

	now := time.Now()
	for relPath, modTime := range map[string]time.Time{
		"src/newest.ts":  now.Add(-1 * time.Hour),
		"src/newer.go":   now.Add(-2 * time.Hour),
		"src/older.go":   now.Add(-3 * time.Hour),
		"src/oldest.txt": now.Add(-4 * time.Hour),
	} {
		if err := os.Chtimes(filepath.Join(project, filepath.FromSlash(relPath)), modTime, modTime); err != nil {
			t.Fatalf("chtimes %s failed: %v", relPath, err)
		}
	}

	runPaths := func(args ...string) string {
		t.Helper()
		cfg := parseInProject(t, project, append([]string{"--quiet", "--print"}, args...))
		var stdout, stderr bytes.Buffer
		if err := run(cfg, &stdout, &stderr); err != nil {
			t.Fatalf("run(%v) returned error: %v\nstderr:\n%s", args, err, stderr.String())
		}
		return strings.TrimSpace(stdout.String())
	}

	if got, want := runPaths("src", "--recent", "2", "--only", "*.go", "--paths"), "src/newer.go"; got != want {
		t.Fatalf("recent-then-only paths = %q, want %q", got, want)
	}
	if got, want := runPaths("src", "--only", "*.go", "--recent", "2", "--paths"), "src/newer.go\nsrc/older.go"; got != want {
		t.Fatalf("only-then-recent paths = %q, want %q", got, want)
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

func TestParseArgsImmediateActionsReturnEarly(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want command.Action
	}{
		{name: "version", args: []string{"--version", "src", "--changed"}, want: command.ActionVersion},
		{name: "short help", args: []string{"--help", "src", "--changed"}, want: command.ActionHelp},
		{name: "full help", args: []string{"--help-all", "src", "--changed"}, want: command.ActionHelpAll},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := cli.ParseArgs(test.args)
			if err != nil {
				t.Fatalf("parseArgs returned error: %v", err)
			}
			if cfg.Action != test.want {
				t.Fatalf("expected %q action, got %q", test.want, cfg.Action)
			}
			if got := len(parsedExecutionScopes(t, cfg)); got != 0 {
				t.Fatalf("expected no scopes for immediate action, got %d", got)
			}
		})
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

// v0.6.6: --depth anchors at each target. `catclip src --depth 1` keeps the
// direct children of src (src/main.ts) and drops deeper files. Under the old
// project-root anchoring src/main.ts was project-depth 2, so --depth 1 would
// have returned nothing; this test is a discriminator for the re-anchor.
func TestRunDepthAnchorsToTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"README.md":                 "readme\n",
		"src/main.ts":               "main\n",
		"src/components/Button.tsx": "button\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--depth", "1"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path="src/main.ts">`) {
		t.Fatalf("expected direct child src/main.ts at anchored depth 1, got:\n%s", out)
	}
	if strings.Contains(out, "src/components/Button.tsx") {
		t.Fatalf("expected deeper src/components/Button.tsx to be filtered, got:\n%s", out)
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

func TestParseArgsLinesRejectsInvalidBounds(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantText string
	}{
		{name: "zero start", args: []string{"src", "--lines", "0"}, wantText: "--lines start must be >= 1"},
		{name: "end before start", args: []string{"src", "--lines", "10", "5"}, wantText: "--lines end (5) must be >= start (10)"},
		{name: "non-integer start", args: []string{"src", "--lines", "abc"}, wantText: "--lines expects line numbers"},
		{name: "non-integer end", args: []string{"src", "--lines", "10", "abc"}, wantText: "--lines expects line numbers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cli.ParseArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("ParseArgs() error = %v, want text %q", err, tt.wantText)
			}
		})
	}
}

func TestParseArgsLinesRejectsFollowingOutputStage(t *testing.T) {
	for _, next := range []string{"--snippet", "--paths"} {
		t.Run(next, func(t *testing.T) {
			args := []string{"src", "--lines", next}
			if next == "--snippet" {
				args = append(args, "a")
			}
			_, err := cli.ParseArgs(args)
			failure := requireCLIValidationFailure(t, err, cli.ReasonTerminalBoundaryOrder)
			if failure.BoundaryFlag != "--lines" || failure.NextFlag != next {
				t.Fatalf("unexpected validation fields: %#v", failure)
			}
		})
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

func TestRunNoIgnoreBypassesAllIgnoreRules(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":             "console.log('ok')\n",
		"tests/main.ts":           "console.log('test')\n",
		"node_modules/lib/idx.js": "module.exports = {}\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--no-ignore", "--paths"})

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

func TestRunNoIgnoreKeepsBinaryPolicySeparate(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":         "ignored/\n",
		"ignored/readme.txt": "ignored text\n",
		"ignored/data.bin":   "\x00\x01ignored binary",
	})

	withoutBinary := parseInProject(t, project, []string{"--quiet", "--print", ".", "--no-ignore", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(withoutBinary, &stdout, &stderr); err != nil {
		t.Fatalf("run without binaries: %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "ignored/readme.txt") || strings.Contains(out, "ignored/data.bin") {
		t.Fatalf("no-ignore must retain text classification, got:\n%s", out)
	}

	withBinary := parseInProject(t, project, []string{"--quiet", "--print", ".", "--no-ignore", "--with-binaries", "--paths"})
	stdout.Reset()
	stderr.Reset()
	if err := run(withBinary, &stdout, &stderr); err != nil {
		t.Fatalf("run with binaries: %v", err)
	}
	if out := stdout.String(); !strings.Contains(out, "ignored/readme.txt") || !strings.Contains(out, "ignored/data.bin") {
		t.Fatalf("no-ignore plus with-binaries should include both files, got:\n%s", out)
	}
}

func TestRunNoIgnoreDoesNotFollowFileSymlinks(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":    "ignored/\n",
		"real/value.ts": "export const value = true\n",
	})
	if err := os.MkdirAll(filepath.Join(project, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../real/value.ts", filepath.Join(project, "ignored", "value.ts")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	cfg := parseInProject(t, project, []string{"--quiet", "--print", ".", "--no-ignore", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if out := stdout.String(); strings.Contains(out, "ignored/value.ts") || !strings.Contains(out, "real/value.ts") {
		t.Fatalf("no-ignore must retain non-followed symlink policy, got:\n%s", out)
	}
}

func TestRunNoIgnoreScopedToTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":            "console.log('ok')\n",
		"src/tests/test.ts":      "test('ok', () => {})\n",
		"vendor/lodash/index.js": "module.exports = {}\n",
	})

	// --no-ignore with target "src" should only bypass ignore rules under src/.
	cfg := parseInProject(t, project, []string{"--quiet", "--print", "src", "--no-ignore", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "src/main.ts") {
		t.Fatalf("expected --no-ignore to include src files, got:\n%s", out)
	}
	if !strings.Contains(out, "src/tests/test.ts") {
		t.Fatalf("expected --no-ignore to bypass ignore rules under src/, got:\n%s", out)
	}
	if strings.Contains(out, "vendor/lodash") {
		t.Fatalf("expected --no-ignore scoped to src to NOT include root vendor/, got:\n%s", out)
	}
}

func TestRunNoIgnoreUsesCombinedTargetUniverse(t *testing.T) {
	t.Run("visible and ignored exact basenames remain ambiguous", func(t *testing.T) {
		project := setupTestProject(t, map[string]string{
			".gitignore":          "generated/\n",
			"src/config.ts":       "export const visible = true\n",
			"generated/config.ts": "export const hidden = true\n",
		})

		cfg := parseInProject(t, project, []string{"config.ts", "--no-ignore", "--headless", "--paths"})
		var stdout, stderr bytes.Buffer
		err := run(cfg, &stdout, &stderr)
		if err == nil {
			t.Fatalf("expected combined visible/ignored ambiguity, got stdout:\n%s", stdout.String())
		}
		message := err.Error()
		for _, want := range []string{"Multiple files match 'config.ts'", "src/config.ts", "generated/config.ts"} {
			if !strings.Contains(message, want) {
				t.Fatalf("combined ambiguity missing %q:\n%s", want, message)
			}
		}
	})

	t.Run("visible and ignored exact directory basenames remain ambiguous", func(t *testing.T) {
		project := setupTestProject(t, map[string]string{
			".gitignore":                  "app/src/generated/\nignored/\n",
			"app/src/main.ts":             "export const main = true\n",
			"app/src/generated/hidden.ts": "export const hidden = true\n",
			"ignored/src/wrong.ts":        "export const wrong = true\n",
		})

		cfg := parseInProject(t, project, []string{"src", "--no-ignore", "--headless", "--paths"})
		var stdout, stderr bytes.Buffer
		err := run(cfg, &stdout, &stderr)
		if err == nil {
			t.Fatalf("expected combined visible/ignored ambiguity, got stdout:\n%s", stdout.String())
		}
		message := err.Error()
		for _, want := range []string{"Multiple directories match 'src'", "app/src", "ignored/src"} {
			if !strings.Contains(message, want) {
				t.Fatalf("combined ambiguity missing %q:\n%s", want, message)
			}
		}
	})

	t.Run("ignored exact basename beats visible fuzzy file", func(t *testing.T) {
		project := setupTestProject(t, map[string]string{
			".gitignore":          "generated/\n",
			"generated/nested.ts": "export const exact = true\n",
			"src/nested_test.ts":  "export const fuzzy = true\n",
		})

		cfg := parseInProject(t, project, []string{"--quiet", "--print", "nested.ts", "--no-ignore", "--paths"})
		var stdout, stderr bytes.Buffer
		if err := run(cfg, &stdout, &stderr); err != nil {
			t.Fatalf("run: %v\nstderr:\n%s", err, stderr.String())
		}
		if got, want := strings.TrimSpace(stdout.String()), "generated/nested.ts"; got != want {
			t.Fatalf("ignored exact basename priority = %q, want %q", got, want)
		}
	})

	t.Run("unique ignored fuzzy target resolves", func(t *testing.T) {
		project := setupTestProject(t, map[string]string{
			".gitignore":          "generated/\n",
			"generated/Login.tsx": "export const hidden = true\n",
		})

		cfg := parseInProject(t, project, []string{"--quiet", "--print", "lgn", "--no-ignore", "--paths"})
		var stdout, stderr bytes.Buffer
		if err := run(cfg, &stdout, &stderr); err != nil {
			t.Fatalf("run: %v\nstderr:\n%s", err, stderr.String())
		}
		if got, want := strings.TrimSpace(stdout.String()), "generated/Login.tsx"; got != want {
			t.Fatalf("unique ignored fuzzy target = %q, want %q", got, want)
		}
	})
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
		"foo/bar.go":        "package foo\n",
		"foo/bar.go.bak.ts": "export const backup = true\n",
		"foo/space name.go": "package foo\n",
		"foo/windows.go":    "package foo\n",
		"foo/other.go":      "package foo\n",
	})

	setTestPipeStdin(t, "./foo//bar.go\r\nx/../foo/space name.go\r\nfoo\\windows.go\r\n../escape\r\n")
	cfg := parseInProject(t, project, []string{"--quiet", "--print", "foo", "--only", "-"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"foo/bar.go", "foo/space name.go", "foo/windows.go"} {
		if !strings.Contains(out, `<file path="`+want+`">`) {
			t.Fatalf("expected normalized stdin path %s in output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "foo/bar.go.bak") || strings.Contains(out, "foo/other.go") || strings.Contains(out, "../escape") {
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
	if !strings.Contains(errOut, `For ignored paths, name the complete relative path or add --no-ignore.`) {
		t.Fatalf("expected ignored-path guidance, got:\n%s", errOut)
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

func TestFzfPreviewCommandIncludesBinaryPolicy(t *testing.T) {
	command := discovery.FzfPreviewCommand(true, true)
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}
	if !strings.Contains(command, discovery.ShellQuoteArg(self)+" --quiet --with-binaries --internal-tree-preview") {
		t.Fatalf("expected target preview to preserve --with-binaries, got %q", command)
	}
}

func TestFzfContentPreviewCommandUsesFilePreviewRenderer(t *testing.T) {
	command := discovery.FzfContentPreviewCommand("--contains", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, discovery.ShellQuoteArg(self)+` --quiet --internal-file-preview --internal-searching-preview --internal-file-path {3} --internal-tree-target {1} --contains {q}`) {
		t.Fatalf("expected contains preview to invoke file preview renderer, got %q", command)
	}
	if strings.Contains(command, "catclip-tree") || strings.Contains(command, "|") {
		t.Fatalf("expected contains preview command to avoid catclip-tree pipe, got %q", command)
	}
}

func TestFzfContentSearchingPreviewCommandUsesForcedSearchingRenderer(t *testing.T) {
	command := discovery.FzfContentSearchingPreviewCommand("--contains")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	want := discovery.ShellQuoteArg(self) + ` --quiet --internal-file-preview --internal-searching-preview --internal-file-path "" --contains {q}`
	if !strings.Contains(command, want) {
		t.Fatalf("expected searching preview to invoke forced file preview renderer, got %q", command)
	}
	if strings.Contains(command, "catclip-tree") || strings.Contains(command, "|") {
		t.Fatalf("expected searching preview command to avoid catclip-tree pipe, got %q", command)
	}
}

func TestFzfContentSnippetPreviewCommandUsesSnippetFlag(t *testing.T) {
	command := discovery.FzfContentPreviewCommand("--snippet", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, discovery.ShellQuoteArg(self)+` --quiet --internal-file-preview --internal-searching-preview --internal-file-path {3} --internal-tree-target {1} --snippet {q}`) {
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
	command := discovery.FzfDiffFilePreviewCommand([]string{"cmd", "--no-ignore", "--changed-diff"})
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, discovery.ShellQuoteArg(self)+" --quiet --internal-file-preview --internal-file-path {3} cmd --no-ignore --changed-diff --only {+2}") {
		t.Fatalf("expected diff file preview command to invoke internal file preview renderer with scope-narrowing --only, got %q", command)
	}
	if strings.Contains(command, "catclip-tree") || strings.Contains(command, "|") {
		t.Fatalf("expected diff file preview command to avoid catclip-tree pipe, got %q", command)
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

	targets, err := resolver.AllIgnoredTargets(nil)
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
	// Must still return true: the modifier menu uses this probe to decide
	// whether broad no-ignore discovery is relevant to the current targets.
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

// TestRunCaseInsensitiveTargetResolvesOnCaseFoldFS asserts the
// v0.6.4 fix: on case-insensitive filesystems (APFS, NTFS) a user-
// typed target like `Cli.go` no longer surfaces the wrong-attribution
// "ignored by .gitignore" error. Matches rg's behavior — path
// arguments work regardless of casing on case-insensitive FS.
func TestRunCaseInsensitiveTargetResolvesOnCaseFoldFS(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skipf("case-insensitive FS test: skipping on %s (assumed case-sensitive)", runtime.GOOS)
	}
	project := setupTestProject(t, map[string]string{
		"cli.go": "package main\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "Cli.go"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected case-fold path to resolve, got err=%v stderr=%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "is ignored by .gitignore") {
		t.Fatalf("case-fold path must not be misattributed as gitignored, stderr was:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "package main") {
		t.Fatalf("expected file content in stdout, got:\n%s", stdout.String())
	}
}

// TestRunCaseSensitiveTargetMissingOnCaseSensitiveFS guards the
// Linux/ext4 behavior: a typed-wrong-case target should still fail
// (file truly doesn't exist), unchanged by the case-fold fix.
func TestRunCaseSensitiveTargetMissingOnCaseSensitiveFS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("case-sensitive FS test: skipping on %s", runtime.GOOS)
	}
	project := setupTestProject(t, map[string]string{
		"cli.go": "package main\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "Cli.go"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected wrong-case target to fail on case-sensitive FS, stdout=%s", stdout.String())
	}
	_ = stderr
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
	if !strings.Contains(stderr.String(), "Direct file targets use exact basenames first. Non-exact file shorthand is resolved by fzf across visible directories.") {
		t.Fatalf("expected updated direct filename guidance, got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "For ignored files, name the complete relative path or add --no-ignore.") {
		t.Fatalf("expected blocked-directory guidance, got:\n%s", stderr.String())
	}
}

// TestRunGlobZeroMatchVisibleParent pins executable recursive guidance. Target
// `**` has no recursive semantics, so recovery must use exact directory
// traversal followed by the cwd-relative legacy filter.
func TestRunGlobZeroMatchVisibleParent(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cmd/sub/main.go": "package main\n",
		"src/app.ts":      "export const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "cmd/*.go"})

	var stdout, stderr bytes.Buffer
	_ = run(cfg, &stdout, &stderr)
	stderrText := stderr.String()
	if strings.Contains(stderrText, "If the parent directory is ignored, use --include to allow it first") {
		t.Fatalf("visible-parent glob should NOT show the misleading --include hint, got:\n%s", stderrText)
	}
	if !strings.Contains(stderrText, "target '*' does not cross folders") {
		t.Fatalf("expected anchoring explanation, got:\n%s", stderrText)
	}
	if strings.Contains(stderrText, "**") {
		t.Fatalf("must not advertise unsupported target doublestar, got:\n%s", stderrText)
	}
	if !strings.Contains(stderrText, `catclip cmd --only "*.go"`) {
		t.Fatalf("expected exact-directory plus filter suggestion, got:\n%s", stderrText)
	}

	// Execute the suggested argv through the normal parser/resolver pipeline.
	suggested := parseInProject(t, project, []string{"--quiet", "--print", "cmd", "--only", "*.go", "--paths"})
	stdout.Reset()
	stderr.Reset()
	if err := run(suggested, &stdout, &stderr); err != nil {
		t.Fatalf("suggested command failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if got, want := stdout.String(), "cmd/sub/main.go\n"; got != want {
		t.Fatalf("suggested command selected wrong files\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestRunGlobZeroMatchIgnoredParent pins direct ignored-directory recovery.
func TestRunGlobZeroMatchIgnoredParent(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":     "docs/\n",
		"docs/readme.md": "# Docs\n",
		"src/app.ts":     "export const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "docs/*.md"})

	var stdout, stderr bytes.Buffer
	_ = run(cfg, &stdout, &stderr)
	stderrText := stderr.String()
	if !strings.Contains(stderrText, "is ignored by .gitignore") {
		t.Fatalf("expected ignored-parent source attribution, got:\n%s", stderrText)
	}
	if strings.Contains(stderrText, "**") {
		t.Fatalf("must not advertise unsupported target doublestar, got:\n%s", stderrText)
	}
	if !strings.Contains(stderrText, `catclip docs --only "*.md"`) {
		t.Fatalf("expected executable direct-target plus filter suggestion, got:\n%s", stderrText)
	}

	suggested := parseInProject(t, project, []string{"--quiet", "--print", "docs", "--only", "*.md", "--paths"})
	stdout.Reset()
	stderr.Reset()
	if err := run(suggested, &stdout, &stderr); err != nil {
		t.Fatalf("suggested ignored-directory command failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if got, want := stdout.String(), "docs/readme.md\n"; got != want {
		t.Fatalf("suggested ignored-directory command selected wrong files\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunDirectoryShapedGlobZeroMatchSuggestsExactDirectory(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"internal/cli/main.go":      "package cli\n",
		"internal/output/emit.go":   "package output\n",
		"internal/root.go":          "package internal\n",
		"outside/unrelated/main.go": "package main\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "internal/*/"})
	var stdout, stderr bytes.Buffer
	_ = run(cfg, &stdout, &stderr)
	message := stderr.String()
	if !strings.Contains(message, "Target globs match files, not directory names") {
		t.Fatalf("expected file-vs-directory explanation, got:\n%s", message)
	}
	if !strings.Contains(message, "catclip internal") {
		t.Fatalf("expected exact-directory suggestion, got:\n%s", message)
	}
	if strings.Contains(message, "**") || strings.Contains(message, "--only '*/'") || strings.Contains(message, `--only "*/"`) {
		t.Fatalf("must not print an impossible directory glob suggestion, got:\n%s", message)
	}
	if strings.Contains(message, "Possible causes:") || strings.Contains(message, "No text files found matching your criteria") {
		t.Fatalf("conclusive glob explanation must suppress the generic no-files footer, got:\n%s", message)
	}

	suggested := parseInProject(t, project, []string{"--quiet", "--print", "internal", "--paths"})
	stdout.Reset()
	stderr.Reset()
	if err := run(suggested, &stdout, &stderr); err != nil {
		t.Fatalf("suggested exact-directory command failed: %v\nstderr:\n%s", err, stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{"internal/cli/main.go\n", "internal/output/emit.go\n", "internal/root.go\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("suggested command missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "outside/") {
		t.Fatalf("suggested command escaped the literal prefix:\n%s", got)
	}
}

func TestRunIgnoredDirectoryShapedGlobSuggestsAuthorizedDirectory(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":                "generated/\n",
		"generated/client/types.go": "package client\n",
		"src/main.go":               "package main\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "generated/*/"})
	var stdout, stderr bytes.Buffer
	_ = run(cfg, &stdout, &stderr)
	message := stderr.String()
	if !strings.Contains(message, "is ignored by .gitignore") || !strings.Contains(message, "Target globs match files, not directory names") {
		t.Fatalf("expected ignored directory-glob explanation, got:\n%s", message)
	}
	if !strings.Contains(message, "catclip generated") {
		t.Fatalf("expected exact-directory suggestion, got:\n%s", message)
	}
	if strings.Contains(message, "**") || strings.Contains(message, "--only") {
		t.Fatalf("must not print a glob filter for a directory-shaped target, got:\n%s", message)
	}

	suggested := parseInProject(t, project, []string{"--quiet", "--print", "generated", "--paths"})
	stdout.Reset()
	stderr.Reset()
	if err := run(suggested, &stdout, &stderr); err != nil {
		t.Fatalf("suggested authorized-directory command failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if got, want := stdout.String(), "generated/client/types.go\n"; got != want {
		t.Fatalf("suggested authorized-directory command selected wrong files\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunGlobZeroMatchSuggestionQuotesLiteralPrefix(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"source files/nested/main.go": "package main\n",
		"src/app.ts":                  "export const app = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "source files/*.go"})
	var stdout, stderr bytes.Buffer
	_ = run(cfg, &stdout, &stderr)
	message := stderr.String()
	if !strings.Contains(message, `catclip "source files" --only "*.go"`) {
		t.Fatalf("expected shell-safe literal-prefix suggestion, got:\n%s", message)
	}

	suggested := parseInProject(t, project, []string{"--quiet", "--print", "source files", "--only", "*.go", "--paths"})
	stdout.Reset()
	stderr.Reset()
	if err := run(suggested, &stdout, &stderr); err != nil {
		t.Fatalf("suggested spaced-prefix command failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if got, want := stdout.String(), "source files/nested/main.go\n"; got != want {
		t.Fatalf("suggested spaced-prefix command selected wrong files\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunGlobZeroMatchMissingPrefixUsesFuzzyTargetGuidanceWithoutInclude(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/vs/base/common/root.ts":        "export const root = true\n",
		"src/vs/base/common/deep/value.ts":  "export const deep = true\n",
		"src/vs/editor/common/editor.ts":    "export const editor = true\n",
		"src/vs/workbench/common/worker.ts": "export const worker = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "common/*"})
	var stdout, stderr bytes.Buffer
	_ = run(cfg, &stdout, &stderr)
	message := stderr.String()

	if !strings.Contains(message, "target globs do not fuzzy-match directory segments") {
		t.Fatalf("expected cwd-relative glob explanation, got:\n%s", message)
	}
	if !strings.Contains(message, "catclip common\n") || !strings.Contains(message, "catclip common --depth 1") {
		t.Fatalf("expected navigation-first recovery commands, got:\n%s", message)
	}
	if strings.Contains(message, "catclip common --include common") {
		t.Fatalf("missing glob prefix must not be misdiagnosed as ignored, got:\n%s", message)
	}
	if strings.Contains(message, "Possible causes:") || strings.Contains(message, "No text files found matching your criteria") {
		t.Fatalf("conclusive missing-prefix explanation must suppress the generic no-files footer, got:\n%s", message)
	}

	cfg = parseInProject(t, project, []string{"--print", "common/*.ts"})
	stdout.Reset()
	stderr.Reset()
	_ = run(cfg, &stdout, &stderr)
	message = stderr.String()
	if !strings.Contains(message, `catclip common --only "*.ts"`) ||
		!strings.Contains(message, `catclip common --depth 1 --only "*.ts"`) {
		t.Fatalf("expected typed navigation-first recovery commands, got:\n%s", message)
	}
	if strings.Contains(message, "catclip common --include common") {
		t.Fatalf("typed missing glob prefix must not be misdiagnosed as ignored, got:\n%s", message)
	}
}

func TestRunRejectsPositionalTargetDoublestar(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cmd/root.go":         "package cmd\n",
		"cmd/one/one.go":      "package one\n",
		"cmd/two/deep/two.go": "package deep\n",
	})

	for _, tc := range []struct {
		target string
		hint   string
		forbid string
	}{
		{target: "cmd/**/*.go", hint: `catclip cmd --only '*.go'`},
		{target: "cmd/**", hint: "catclip cmd", forbid: "--only"},
		{target: "**", hint: "catclip .", forbid: "--only"},
		{target: "common/**.ts", hint: `catclip common --only '*.ts'`},
	} {
		err := parseErrorInProject(t, project, []string{"--print", tc.target})
		if err == nil {
			t.Fatalf("expected positional target %q to reject doublestar", tc.target)
		}
		if !strings.Contains(err.Error(), "Positional target patterns do not support '**'") {
			t.Fatalf("expected positional doublestar error for %q, got: %v", tc.target, err)
		}
		if !strings.Contains(err.Error(), tc.hint) {
			t.Fatalf("expected recovery %q for %q, got: %v", tc.hint, tc.target, err)
		}
		if tc.forbid != "" && strings.Contains(err.Error(), tc.forbid) {
			t.Fatalf("recovery for %q contains redundant %q: %v", tc.target, tc.forbid, err)
		}
	}
}

func TestParseRejectsFilterDoublestar(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"cmd/root.go":    "package cmd\n",
		"cmd/one/one.go": "package one\n",
	})

	tests := []struct {
		args    []string
		flag    string
		pattern string
	}{
		{args: []string{"cmd", "--only", "**"}, flag: "--only", pattern: "**"},
		{args: []string{"cmd", "--only", "src/**"}, flag: "--only", pattern: "src/**"},
		{args: []string{"cmd", "--only", "*.go", "src/**/*.go"}, flag: "--only", pattern: "src/**/*.go"},
		{args: []string{"cmd", "--exclude", "**/*.map"}, flag: "--exclude", pattern: "**/*.map"},
		{args: []string{"cmd", "--exclude", "***"}, flag: "--exclude", pattern: "***"},
		{args: []string{"cmd", "--only", "*.go", "--then", ".", "--exclude", "build/**"}, flag: "--exclude", pattern: "build/**"},
	}
	for _, tt := range tests {
		err := parseErrorInProject(t, project, tt.args)
		if err == nil {
			t.Fatalf("expected %v to reject doublestar", tt.args)
		}
		for _, want := range []string{
			tt.flag + " patterns do not support '**'",
			"'" + tt.pattern + "'",
			"'*' already matches across folders",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%v error missing %q:\n%s", tt.args, want, err)
			}
		}
	}
}

func TestRunAllStarTargetRecoveryUsesDirectoryWithoutRedundantFilter(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"internal/output/emit.go": "package output\n",
		"internal/cli/main.go":    "package cli\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "internal/*"})
	var stdout, stderr bytes.Buffer
	_ = run(cfg, &stdout, &stderr)
	message := stderr.String()
	if !strings.Contains(message, "catclip internal") {
		t.Fatalf("expected exact-directory recovery, got:\n%s", message)
	}
	if strings.Contains(message, "--only") {
		t.Fatalf("all-star recovery must not add a redundant filter, got:\n%s", message)
	}
	if strings.Contains(message, "Possible causes:") {
		t.Fatalf("conclusive all-star explanation must suppress the generic footer, got:\n%s", message)
	}
}

func TestRunIgnoredAllStarTargetRecoveryUsesDirectDirectoryWithoutFilter(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":                "generated/\n",
		"generated/client/types.go": "package client\n",
		"src/app.go":                "package src\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "generated/*"})
	var stdout, stderr bytes.Buffer
	_ = run(cfg, &stdout, &stderr)
	message := stderr.String()
	if !strings.Contains(message, "catclip generated") {
		t.Fatalf("expected direct directory recovery, got:\n%s", message)
	}
	if strings.Contains(message, "--only") {
		t.Fatalf("ignored all-star recovery must not add a redundant filter, got:\n%s", message)
	}
	if strings.Contains(message, "Possible causes:") {
		t.Fatalf("conclusive ignored all-star explanation must suppress the generic footer, got:\n%s", message)
	}
}

func TestRunWarnsForDeadTrailingSlashFilterGlobs(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"internal/output/emit.go": "package output\n",
		"internal/cli/main.go":    "package cli\n",
	})

	onlyCfg := parseInProject(t, project, []string{"--print", "internal", "--only", "*/", "--paths"})
	var stdout, stderr bytes.Buffer
	err := run(onlyCfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected dead --only glob to produce an empty result")
	}
	message := stderr.String()
	if !strings.Contains(message, "--only pattern '*/' cannot match file paths") || !strings.Contains(message, "--only 'output/'") {
		t.Fatalf("expected dead --only glob guidance, got:\n%s", message)
	}
	if strings.Contains(message, "Possible causes:") || strings.Contains(message, "No text files found matching your criteria") {
		t.Fatalf("dead --only glob must own the empty result, got:\n%s", message)
	}

	excludeCfg := parseInProject(t, project, []string{"--print", "internal", "--exclude", "*/", "--paths"})
	stdout.Reset()
	stderr.Reset()
	if err := run(excludeCfg, &stdout, &stderr); err != nil {
		t.Fatalf("dead --exclude glob must leave the file set unchanged: %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--exclude pattern '*/' cannot match file paths") || !strings.Contains(stderr.String(), "--exclude 'output/'") {
		t.Fatalf("expected dead --exclude glob guidance, got:\n%s", stderr.String())
	}
	for _, want := range []string{"internal/output/emit.go\n", "internal/cli/main.go\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("dead --exclude glob changed selection; missing %q:\n%s", want, stdout.String())
		}
	}

	literalCfg := parseInProject(t, project, []string{"--print", "internal", "--only", "output/", "--paths"})
	stdout.Reset()
	stderr.Reset()
	if err := run(literalCfg, &stdout, &stderr); err != nil {
		t.Fatalf("literal trailing-slash subtree must remain valid: %v\nstderr:\n%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "cannot match file paths") {
		t.Fatalf("literal subtree selector must not trigger glob warning:\n%s", stderr.String())
	}
	if got, want := stdout.String(), "internal/output/emit.go\n"; got != want {
		t.Fatalf("literal subtree selection changed\nwant: %q\ngot:  %q", want, got)
	}

	unreachedCfg := parseInProject(t, project, []string{"--print", "internal", "--only", "*.rs", "--exclude", "*/", "--paths"})
	stdout.Reset()
	stderr.Reset()
	_ = run(unreachedCfg, &stdout, &stderr)
	if strings.Contains(stderr.String(), "--exclude pattern '*/'") {
		t.Fatalf("a stage after an empty result was not reached and must not warn:\n%s", stderr.String())
	}
}

func TestRunHeadlessDeadOnlyGlobExplainsFailure(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"internal/output/emit.go": "package output\n",
	})

	cfg := parseInProject(t, project, []string{"--headless", "internal", "--only", "*/", "--paths"})
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected dead --only glob to produce an empty result")
	}
	if stdout.Len() != 0 {
		t.Fatalf("headless failure polluted stdout:\n%s", stdout.String())
	}
	message := stderr.String()
	if !strings.Contains(message, "--only pattern '*/' cannot match file paths") {
		t.Fatalf("headless failure missing precise warning:\n%s", message)
	}
	if strings.Contains(message, "Possible causes:") {
		t.Fatalf("precise warning must suppress generic footer:\n%s", message)
	}
}

func TestRunQuietDeadOnlyGlobStillExplainsFailure(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"internal/output/emit.go": "package output\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "internal", "--only", "*/", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err == nil {
		t.Fatal("expected dead --only glob to produce an empty result")
	}
	if !strings.Contains(stderr.String(), "--only pattern '*/' cannot match file paths") {
		t.Fatalf("quiet failure missing precise warning:\n%s", stderr.String())
	}
}

func TestRunHeadlessDeadExcludeGlobWarnsWithoutPollutingStdout(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"internal/output/emit.go": "package output\n",
	})

	cfg := parseInProject(t, project, []string{"--headless", "internal", "--exclude", "*/", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("dead --exclude glob changed successful selection: %v", err)
	}
	if got, want := stdout.String(), "internal/output/emit.go\n"; got != want {
		t.Fatalf("stdout mismatch\nwant: %q\ngot:  %q", want, got)
	}
	if !strings.Contains(stderr.String(), "--exclude pattern '*/' cannot match file paths") {
		t.Fatalf("headless success missing ineffective-filter warning:\n%s", stderr.String())
	}
}

func TestRunHeadlessParserWarningDoesNotPolluteStdout(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": "export const useContext = true\n",
	})

	cfg := parseInProject(t, project, []string{"--headless", "src", "--contains", "use*Context", "--paths"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("glob-shaped regex command failed: %v", err)
	}
	if got, want := stdout.String(), "src/app.ts\n"; got != want {
		t.Fatalf("stdout mismatch\nwant: %q\ngot:  %q", want, got)
	}
	if !strings.Contains(stderr.String(), "--contains uses regex, not globs") {
		t.Fatalf("headless parser warning missing:\n%s", stderr.String())
	}
}

func TestRunSuppressesGenericFooterOnlyWhenEveryEmptyScopeIsExplained(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.go": "package src\n",
	})

	allExplained := parseInProject(t, project, []string{"--print", "missing/*.go", "--then", "absent/*.ts", "--paths"})
	var stdout, stderr bytes.Buffer
	_ = run(allExplained, &stdout, &stderr)
	if strings.Contains(stderr.String(), "Possible causes:") {
		t.Fatalf("all conclusively explained scopes must suppress the generic footer:\n%s", stderr.String())
	}
	for _, want := range []string{"(scope 1)", "(scope 2)"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected retained scope numbering %q:\n%s", want, stderr.String())
		}
	}

	mixed := parseInProject(t, project, []string{"--print", "missing/*.go", "--then", ".", "--only", "*.rs", "--paths"})
	stdout.Reset()
	stderr.Reset()
	_ = run(mixed, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "Possible causes:") {
		t.Fatalf("an unexplained empty scope must retain the generic footer:\n%s", stderr.String())
	}

	mixedTargets := parseInProject(t, project, []string{"--print", "missing/*.go", "src", "--only", "*.rs", "--paths"})
	stdout.Reset()
	stderr.Reset()
	_ = run(mixedTargets, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "Possible causes:") {
		t.Fatalf("a glob warning must not explain a sibling target emptied by a later stage:\n%s", stderr.String())
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

func TestRunNoMatchSuppressesGenericFooterWhenEveryTargetIsExplained(t *testing.T) {
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
	if strings.Contains(errOut, "No text files found matching your criteria.") || strings.Contains(errOut, "Possible causes:") {
		t.Fatalf("fully explained targets must suppress the generic footer:\n%s", errOut)
	}
	if first := strings.Index(errOut, "Warning: No file or directory 'include' found"); first == -1 {
		t.Fatalf("expected missing-target warning, got:\n%s", errOut)
	} else if second := strings.Index(errOut, "'index.js' is hidden by an ignored ancestor"); second == -1 || first > second {
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

func TestRunDirectTargetOverridesDefaultHissForTextFile(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"docs/setup.inf": "[Version]\nSignature=\"$Windows NT$\"\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "docs/setup.inf"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("exact ignored text target should succeed: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "docs/setup.inf") {
		t.Fatalf("expected exact ignored file in preview:\n%s", stdout.String())
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
	if !strings.Contains(err.Error(), `Multiple files and directories match 'common'`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "Matches:") || !strings.Contains(err.Error(), "src/common") || !strings.Contains(err.Error(), "lib/common") {
		t.Fatalf("expected capped candidate list guidance, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Use a more specific name or path") {
		t.Fatalf("expected disambiguation guidance, got: %v", err)
	}
}

// TestNoIgnoreBypassesEffect4Check verifies that broad no-ignore discovery
// admits ignored descendants beneath a visible target.
func TestNoIgnoreBypassesEffect4Check(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	project := setupTestProject(t, map[string]string{
		".gitignore":            "cmd/build/\n",
		"cmd/main.go":           "package main\n",
		"cmd/build/artifact.js": "artifact\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "cmd", "--no-ignore"})
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("no-ignore on visible-target scope should succeed, got err: %v\nstderr:\n%s", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `<file path="cmd/main.go">`) {
		t.Fatalf("expected cmd/main.go:\n%s", out)
	}
	if !strings.Contains(out, `<file path="cmd/build/artifact.js">`) {
		t.Fatalf("expected cmd/build/artifact.js (--no-ignore authorizes ignored descendants):\n%s", out)
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
	if !strings.Contains(err.Error(), `Multiple files and directories match 'common'`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "Use a more specific name or path") {
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

func TestRunNestedRepositoryTargetUsesGitContextFromWorkingDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"README.md": "parent repository\n",
	})
	initGitRepo(t, project)

	nested := filepath.Join(project, "nested")
	writeProjectFile(t, project, "nested/inner.txt", "committed in nested repository\n")
	initGitRepo(t, nested)
	writeProjectFile(t, project, "nested/inner.txt", "modified only in nested repository\n")

	// The nested repository sees an unstaged tracked change. The parent sees the
	// nested tree as untracked, so a parent-owned --unstaged selection is empty.
	// This difference makes the repository owner observable without inspecting
	// internal git.Context fields.
	if output, err := git.Capture(nested, "diff", "--name-only"); err != nil || !strings.Contains(output, "inner.txt") {
		t.Fatalf("nested repository fixture missing unstaged change: output=%q err=%v", output, err)
	}

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "nested", "--unstaged", "--paths"})
	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected parent-repository --unstaged selection to be empty")
	}
	if strings.Contains(stdout.String(), "nested/inner.txt") {
		t.Fatalf("nested target incorrectly switched to nested repository context:\n%s", stdout.String())
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
	if strings.Contains(stderr.String(), "working tree may be clean") || strings.Contains(stderr.String(), "No --changed files found") {
		t.Fatalf("precise git-required error must suppress clean-tree guidance:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "src/main.ts") {
		t.Fatalf("expected no file output when git selection is unsatisfiable, got:\n%s", stdout.String())
	}
}

func TestRunRepeatedNoGitDiagnosticsDedupe(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})

	cfg := parseInProject(t, project, []string{
		"--quiet", "--print", ".", "--changed",
		"--then", ".", "--changed",
	})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-zero exit when every scope requires git")
	}
	var exitErr exitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitError, got %T: %v", err, err)
	}
	if exitErr.code != 2 {
		t.Fatalf("expected exit code 2 when every scope is unsatisfiable, got %d", exitErr.code)
	}
	message := stderr.String()
	if got := strings.Count(message, "require a git repository"); got != 1 {
		t.Fatalf("expected one deduplicated git-required error, got %d:\n%s", got, message)
	}
	if strings.Contains(message, "working tree may be clean") || strings.Contains(message, "No --changed files found") {
		t.Fatalf("precise git-required errors must suppress clean-tree guidance:\n%s", message)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unsatisfiable scopes polluted stdout:\n%s", stdout.String())
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

// The wl-copy liveness failure (started but exited before serving the
// offer) mirrors the text sink's wl-copy error so both sinks teach the
// user identically. The temp bundle is removed; the remedy is stdout.
func TestEmitBundleSurfacesWlCopyExitedError(t *testing.T) {
	originalCopy := output.FileclipCopy
	defer func() { output.FileclipCopy = originalCopy }()
	output.FileclipCopy = func(...string) error {
		return fmt.Errorf("%w: %w", fileclip.ErrToolFailed, fileclip.ErrWaylandOfferNotServed)
	}

	dir := t.TempDir()
	t.Setenv("CATCLIP_BUNDLE_DIR", dir)

	_, err := output.EmitBundle(output.EmitEnvironment{Platform: "linux", WorkingDir: dir}, []byte("bundle payload"), 0, platform.Palette{})
	if err == nil {
		t.Fatal("expected output.EmitBundle to surface wl-copy exited error")
	}
	msg := err.Error()
	for _, want := range []string{
		"wl-copy failed.",
		"wl-copy exited before the clipboard offer was served",
		"Wayland compositor/session",
		"--print",
		"--headless",
		"Details: fileclip: clipboard tool failed: wl-copy exited before serving the clipboard offer",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("wl-copy exited message missing %q, got: %s", want, msg)
		}
	}
	if strings.Contains(msg, "clipboard command failed") {
		t.Errorf("wl-copy exited must not use the generic framing, got: %s", msg)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read bundle dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected temp bundle to be removed, got %d entries", len(entries))
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
				printf '%s\n' "$input" | awk -F '\t' '$2 == "src/common"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "lib/common"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "shared/common"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "src/common.ts"'
				exit 0
				;;
			src/)
				emit_query
				printf '%s\n' "$input" | awk -F '\t' '$2 == "src"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "docs/src"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "tools/src"'
				exit 0
				;;
			src/vs/platform)
				emit_query
				printf '%s\n' "$input" | awk -F '\t' '$2 == "src/vs/platform"'
				printf '%s\n' "$input" | awk -F '\t' '$2 == "tools/src/vs/platform"'
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
				printf '%s\n' "$input" | awk -F '\t' '$2 == "node_modules"' | head -n 1
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
	// --contains is a regex modifier and stays single-quoted. Spaced ordinary
	// values use the documented user-shell spelling.
	want := `catclip src --contains 'TODO items' --only "src/a test.ts"`
	if runtime.GOOS == "windows" {
		want = `catclip src --contains 'TODO items' --only 'src/a test.ts'`
	}
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestFormatResolvedStartupCommandRoundTripsEveryUserStageShape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell replay test")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	args := []string{
		"--verbose", "--no-tree", "--no-bundle",
		".",
		"--only", "*.go", "*.md",
		"--exclude", "*_test.go",
		"--recent", "5",
		"--size", "1", "200",
		"--depth", "3",
		"--contains", `func \$name`,
		"--not-contains", "generated",
		"--lines", "1", "5",
		"--then", "docs",
		"--only", "*.md",
		"--snippet", `can't TODO`, "3",
		"--then", ".",
		"--changed-diff",
		"--then", ".",
		"--paths",
	}
	before, err := cli.ParseArgs(args)
	if err != nil {
		t.Fatalf("initial parse: %v", err)
	}

	rendered := cli.FormatResolvedStartupCommand(args)
	script := "set -- " + strings.TrimPrefix(rendered, "catclip ") + `; printf '%s\0' "$@"`
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("shell replay: %v\ncommand: %s", err, rendered)
	}
	raw := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	replayedArgs := make([]string, len(raw))
	for i := range raw {
		replayedArgs[i] = string(raw[i])
	}
	after, err := cli.ParseArgs(replayedArgs)
	if err != nil {
		t.Fatalf("replayed command did not parse: %v\ncommand: %s\nargv: %q", err, rendered, replayedArgs)
	}

	if got, want := command.ExecutionScopesFromSpec(after.Command), command.ExecutionScopesFromSpec(before.Command); !reflect.DeepEqual(got, want) {
		t.Fatalf("scope model changed across replay\ncommand: %s\n got: %#v\nwant: %#v", rendered, got, want)
	}
	if after.Verbose != before.Verbose || after.NoTree != before.NoTree || after.NoBundle != before.NoBundle {
		t.Fatalf("global flags changed across replay\ncommand: %s\nbefore: %#v\nafter: %#v", rendered, before, after)
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
			case command.StageOnly, command.StageExclude, command.StageContains, command.StageNotContains, command.StageSnippet:
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
		"--internal-searching-preview",
		"--internal-file-path", "",
		"--internal-tree-target", "[all current matches]",
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
	if strings.Contains(out, "no matches for this pattern yet") {
		t.Fatalf("expected sentinel row to bypass searching preview, got %q", out)
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

func parseErrorInProject(t *testing.T, project string, args []string) error {
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

	_, err = cli.ParseArgs(args)
	return err
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
	if strings.Contains(stderr.String(), "Possible causes:") {
		t.Fatalf("precise missing-target warning must suppress generic footer:\n%s", stderr.String())
	}
}

func TestRunMixedMissingAndUnexplainedTargetKeepsGenericFooter(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": "export const app = true\n",
	})
	if err := os.Mkdir(filepath.Join(project, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty target: %v", err)
	}

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "zzzznothing", "empty"})

	var stdout, stderr bytes.Buffer
	err := run(cfg, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected non-zero exit for empty targets")
	}
	message := stderr.String()
	if !strings.Contains(message, "zzzznothing") {
		t.Fatalf("expected precise warning for missing target:\n%s", message)
	}
	if !strings.Contains(message, "Possible causes:") {
		t.Fatalf("unexplained empty directory must retain generic footer:\n%s", message)
	}
	if stdout.Len() != 0 {
		t.Fatalf("empty result polluted stdout:\n%s", stdout.String())
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

// Non-empty pattern + forced searching-preview mode renders the searching
// hint. This is the state the content picker explicitly installs through
// start/change preview bindings while a keystroke's search is still running
// or has not produced rows yet. See RunInternalFilePreview state dispatch.
func TestRunInternalFilePreviewOutputsSearchingHintWhenForcedByPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "const a = 1\nTODO: first\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-searching-preview", "--internal-file-path", "", "--contains", "todo"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "no matches for this pattern yet") {
		t.Fatalf("expected forced searching hint, got %q", out)
	}
}

// Non-empty pattern + empty focused path + fzf reporting a zero-row list
// (FZF_MATCH_COUNT=0) still renders the searching hint as a compatibility
// fallback. The picker does not rely on this undocumented env var anymore.
func TestRunInternalFilePreviewOutputsSearchingHintWhileZeroRows(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "const a = 1\nTODO: first\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "", "--contains", "todo"})

	t.Setenv("FZF_MATCH_COUNT", "0")
	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	out := rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if !strings.Contains(out, "no matches for this pattern yet") {
		t.Fatalf("expected searching hint with FZF_MATCH_COUNT=0, got %q", out)
	}

	t.Setenv("FZF_MATCH_COUNT", "3")
	stdout.Reset()
	stderr.Reset()
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	out = rootAnsiEscape.ReplaceAllString(stdout.String(), "")
	if strings.Contains(out, "no matches for this pattern yet") {
		t.Fatalf("searching hint must not render when rows exist (sentinel row focus), got %q", out)
	}
}

// TestRunRawWithThenScope closes output_pipeline_followups Item 4 (decision
// 2026-05-12; carried since v0.5.4): raw semantics across --then scope
// boundaries. Three pinned cases — plain concat spans scopes, an
// incompatible output mode in ANY scope rejects the whole raw run, and
// per-scope --lines slices concat without numbering. (The original note
// wrote `--lines 1,3`; current syntax is space-separated START END.)
func TestRunRawWithThenScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.txt": "alpha1\nalpha2\nalpha3\n",
		"b.txt": "beta1\nbeta2\nbeta3\n",
	})

	t.Run("both scopes concat raw", func(t *testing.T) {
		cfg := parseInProject(t, project, []string{"--quiet", "--print", "a.txt", "--then", "b.txt", "--raw"})
		var stdout, stderr bytes.Buffer
		if err := run(cfg, &stdout, &stderr); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
		want := "alpha1\nalpha2\nalpha3\nbeta1\nbeta2\nbeta3\n"
		if got := stdout.String(); got != want {
			t.Fatalf("unexpected raw concat across --then\nwant:\n%s\ngot:\n%s", want, got)
		}
		if strings.Contains(stdout.String(), "<file path=") {
			t.Fatalf("did not expect wrapped output, got:\n%s", stdout.String())
		}
	})

	t.Run("paths in second scope rejects raw", func(t *testing.T) {
		cfg := parseInProject(t, project, []string{"--quiet", "--print", "a.txt", "--then", "b.txt", "--paths", "--raw"})
		var stdout, stderr bytes.Buffer
		err := run(cfg, &stdout, &stderr)
		if err == nil {
			t.Fatal("expected raw mode to reject --paths in the second scope")
		}
		if !strings.Contains(err.Error(), "--raw cannot be combined with --paths") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("lines slices concat raw without numbers", func(t *testing.T) {
		cfg := parseInProject(t, project, []string{"--quiet", "--print", "a.txt", "--lines", "1", "2", "--then", "b.txt", "--lines", "2", "3", "--raw"})
		var stdout, stderr bytes.Buffer
		if err := run(cfg, &stdout, &stderr); err != nil {
			t.Fatalf("run returned error: %v", err)
		}
		want := "alpha1\nalpha2\nbeta2\nbeta3\n"
		if got := stdout.String(); got != want {
			t.Fatalf("unexpected raw --lines concat across --then\nwant:\n%s\ngot:\n%s", want, got)
		}
		if strings.Contains(stdout.String(), "   1") || strings.Contains(stdout.String(), "1 |") {
			t.Fatalf("raw lines output must not carry line numbers, got:\n%s", stdout.String())
		}
	})
}
