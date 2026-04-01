package catclip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if path, err := exec.LookPath("rg"); err == nil {
		_ = os.Setenv("CATCLIP_RG", path)
	}
	if path, err := exec.LookPath("fzf"); err == nil {
		_ = os.Setenv("CATCLIP_FZF", path)
	}
	os.Exit(m.Run())
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

func TestParseArgsDefaultsToCurrentDirectoryScope(t *testing.T) {
	cfg, err := parseArgs(nil)
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if len(cfg.Scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(cfg.Scopes))
	}
	if got := cfg.Scopes[0].Targets; len(got) != 1 || got[0] != "." {
		t.Fatalf("expected default target '.', got %#v", got)
	}
}

func TestParseArgsBuildsMultipleScopes(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--only", "*.ts", "--then", "tests", "--only", "*.test.ts"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if len(cfg.Scopes) != 2 {
		t.Fatalf("expected 2 scopes, got %d", len(cfg.Scopes))
	}

	if got := cfg.Scopes[0].Targets[0]; got != "src" {
		t.Fatalf("expected first scope target 'src', got %q", got)
	}
	if got := cfg.Scopes[0].Only[0]; got != "*.ts" {
		t.Fatalf("expected first scope only '*.ts', got %q", got)
	}
	if got := cfg.Scopes[1].Targets[0]; got != "tests" {
		t.Fatalf("expected second scope target 'tests', got %q", got)
	}
	if got := cfg.Scopes[1].Only[0]; got != "*.test.ts" {
		t.Fatalf("expected second scope only '*.test.ts', got %q", got)
	}
}

func TestParseArgsAppliesModifierOnlyScopeToDot(t *testing.T) {
	cfg, err := parseArgs([]string{"--changed", "--diff"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if len(cfg.Scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(cfg.Scopes))
	}
	scope := cfg.Scopes[0]
	if len(scope.Targets) != 1 || scope.Targets[0] != "." {
		t.Fatalf("expected modifier-only scope to default to '.', got %#v", scope.Targets)
	}
	if !scope.Changed || !scope.Diff {
		t.Fatalf("expected changed+diff scope, got %+v", scope)
	}
}

func TestParseArgsConsumesMultiValueExcludeStage(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--exclude", "*.snap", "build/"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := cfg.Scopes[0]
	if got, want := len(scope.Exclude), 2; got != want {
		t.Fatalf("expected %d exclude patterns, got %d", want, got)
	}
}

func TestParseArgsTreatsIncludeAsAuthorizedTargetSelection(t *testing.T) {
	cfg, err := parseArgs([]string{".", "--include", "node_modules", "coverage"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if got := cfg.Scopes[0].Targets; strings.Join(got, "\n") != "." {
		t.Fatalf("expected include to preserve the explicit base target only, got %#v", got)
	}
	if got := cfg.Scopes[0].IncludedTargets; strings.Join(got, "\n") != "node_modules\ncoverage" {
		t.Fatalf("expected included target metadata, got %#v", got)
	}
}

func TestParseArgsTreatsBareIncludeAsAugmentingDotScope(t *testing.T) {
	cfg, err := parseArgs([]string{"--include", "node_modules", "coverage"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if got := cfg.Scopes[0].Targets; strings.Join(got, "\n") != "." {
		t.Fatalf("expected bare include to preserve implicit dot scope, got %#v", got)
	}
	if got := cfg.Scopes[0].IncludedTargets; strings.Join(got, "\n") != "node_modules\ncoverage" {
		t.Fatalf("expected included target metadata, got %#v", got)
	}
}

func TestKnownTextLikeFileClassifiesCommonNamesAndExtensions(t *testing.T) {
	if !knownTextLikeFile("src/app.ts") {
		t.Fatal("expected .ts extension to classify as known text")
	}
	if !knownTextLikeFile("Makefile") {
		t.Fatal("expected Makefile basename to classify as known text")
	}
	if knownTextLikeFile("image.unknownbin") {
		t.Fatal("did not expect unknown extension to classify as known text")
	}
}

func TestIsLikelyTextFileUsesKnownTextFastPath(t *testing.T) {
	project := setupTestProject(t, map[string]string{})

	binaryByName := filepath.Join(project, "fake.ts")
	if err := os.WriteFile(binaryByName, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	text, err := isLikelyTextFile("fake.ts", binaryByName)
	if err != nil {
		t.Fatalf("isLikelyTextFile returned error: %v", err)
	}
	if !text {
		t.Fatal("expected known text extension to bypass sniffing")
	}

	unknown := filepath.Join(project, "fake.unknownbin")
	if err := os.WriteFile(unknown, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	text, err = isLikelyTextFile("fake.unknownbin", unknown)
	if err != nil {
		t.Fatalf("isLikelyTextFile returned error: %v", err)
	}
	if text {
		t.Fatal("expected unknown binary-like file to still use sniff fallback")
	}
}

func TestParseArgsStagedImpliesChanged(t *testing.T) {
	cfg, err := parseArgs([]string{"src", "--staged"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := cfg.Scopes[0]
	if !scope.Staged || !scope.Changed {
		t.Fatalf("expected staged to imply changed, got %+v", scope)
	}
}

func TestParseArgsSnippetRequiresContains(t *testing.T) {
	_, err := parseArgs([]string{"src", "--snippet"})
	if err == nil {
		t.Fatal("expected error for --snippet without --contains")
	}
	if !strings.Contains(err.Error(), "--snippet requires --contains") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsSnippetWithDiff(t *testing.T) {
	_, err := parseArgs([]string{"src", "--contains", "TODO", "--snippet", "--changed", "--diff"})
	if err == nil {
		t.Fatal("expected error for --snippet with --diff")
	}
	if !strings.Contains(err.Error(), "--snippet and --diff cannot be combined") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsDiffWithoutChangeSelector(t *testing.T) {
	_, err := parseArgs([]string{"src", "--diff"})
	if err == nil {
		t.Fatal("expected error for --diff without change selector")
	}
	if !strings.Contains(err.Error(), "--diff requires --changed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsUntrackedDiffAlone(t *testing.T) {
	_, err := parseArgs([]string{"src", "--untracked", "--diff"})
	if err == nil {
		t.Fatal("expected error for --untracked --diff")
	}
	if !strings.Contains(err.Error(), "--untracked --diff doesn't make sense") {
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
	if !strings.Contains(err.Error(), "positional targets must come before modifiers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsPlainTokenAfterZeroArgModifier(t *testing.T) {
	_, err := parseArgs([]string{"src", "--changed", "extra"})
	if err == nil {
		t.Fatal("expected error for plain token after zero-arg modifier")
	}
	if !strings.Contains(err.Error(), "positional targets must come before modifiers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsStopsOptionParsingAfterDoubleDash(t *testing.T) {
	cfg, err := parseArgs([]string{"--", "--changed", "src"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	scope := cfg.Scopes[0]
	if len(scope.Targets) != 2 || scope.Targets[0] != "--changed" || scope.Targets[1] != "src" {
		t.Fatalf("expected post -- tokens to be targets, got %#v", scope.Targets)
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

func TestRunIncludeSupportsGitignoreOutsideGitRepo(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".gitignore":   "blocked/\n",
		"blocked/a.ts": "export const blocked = true\n",
		"src/main.ts":  "export const main = true\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "--include", "blocked", "--only", "blocked/a.ts"})

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
	if len(cfg.Scopes) != 0 {
		t.Fatalf("expected no scopes for immediate action, got %#v", cfg.Scopes)
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

	cfg := runConfig{OutputMode: outputModeStdout}

	var sequential bytes.Buffer
	if _, err := emitFullOutput(cfg, gitContext{}, entries, &sequential, colorPalette{}); err != nil {
		t.Fatalf("sequential emitFullOutput returned error: %v", err)
	}

	t.Setenv("CATCLIP_READ_WORKERS", "2")
	t.Setenv("CATCLIP_PREFETCH_FILE_KIB", "64")

	var prefetched bytes.Buffer
	if _, err := emitFullOutput(cfg, gitContext{}, entries, &prefetched, colorPalette{}); err != nil {
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
	if !strings.Contains(stderr.String(), "Use --include to authorize it for this run") {
		t.Fatalf("expected --include guidance, got:\n%s", stderr.String())
	}
}

func TestRunIncludeAuthorizesBlockedDirectory(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":   "console.log('ok')\n",
		"tests/main.ts": "console.log('test')\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "--include", "tests"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "tests/main.ts") {
		t.Fatalf("expected --include to authorize tests/, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "bypassing ignore rule 'tests/'") {
		t.Fatalf("expected bypass warning, got:\n%s", stderr.String())
	}
}

func TestRunMatchesMultiSegmentDirectoryRulesAsContiguousPathFragments(t *testing.T) {
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
	if strings.Contains(out, "foo/bar/hidden.ts") || strings.Contains(out, "qux/foo/bar/deep.ts") {
		t.Fatalf("expected foo/bar/ to match as a contiguous directory fragment, got:\n%s", out)
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

func TestSafeTargetPickerHeaderMentionsCtrlOInclude(t *testing.T) {
	header := safeTargetPickerHeader()
	if !strings.Contains(header, "Ctrl-O") || !strings.Contains(header, "ignored targets") {
		t.Fatalf("expected safe picker header to mention Ctrl-O include handoff, got %q", header)
	}
	if !strings.Contains(header, "Type part of a directory or file name") || !strings.Contains(header, "arrow keys") {
		t.Fatalf("expected safe picker header to guide first-time fzf users, got %q", header)
	}
}

func TestPickerHeadersUseFourLines(t *testing.T) {
	headers := map[string]string{
		"safe":     safeTargetPickerHeader(),
		"ignored":  ignoredTargetPickerHeader(),
		"contains": containsPickerHeader(),
		"modifier": startupModifierPickerHeader(),
		"only":     startupFileSetPickerHeader("--only"),
		"exclude":  startupFileSetPickerHeader("--exclude"),
	}
	for name, header := range headers {
		if got, want := len(strings.Split(header, "\n")), 4; got != want {
			t.Fatalf("expected %s header to use %d lines, got %d: %q", name, want, got, header)
		}
	}
}

func TestTargetMatchLabelsMapsPlainCopyAllSelection(t *testing.T) {
	labels, index := targetMatchLabels([]targetMatch{{Path: ".", Kind: "all"}})
	if len(labels) != 1 {
		t.Fatalf("expected one label, got %d", len(labels))
	}
	if labels[0] != "\x1b[1m[copy all files]\x1b[0m\t.\tdir\tok" {
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

	if !strings.Contains(command, shellQuoteArg(self)+" --quiet --internal-tree-payload --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4} {2}") {
		t.Fatalf("expected preview command to invoke catclip payload producer, got %q", command)
	}
	if !strings.Contains(command, "| "+shellQuoteArg(treeBin)+" --bare --color always") {
		t.Fatalf("expected preview command to pipe into catclip-tree --bare --color always, got %q", command)
	}
}

func TestFzfPreviewCommandIncludesIgnoredTargetAuthorization(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfPreviewCommand(true)
	if !strings.Contains(command, "--include {2} --internal-tree-target {2} --internal-tree-kind {3} --internal-tree-state {4} {2}") {
		t.Fatalf("expected ignored target preview to authorize the hovered path, got %q", command)
	}
}

func TestFzfContainsPreviewCommandUsesFilePreviewPayload(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfContainsPreviewCommand(false)
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+" --quiet --internal-file-preview --internal-file-path {2} --contains {q}") {
		t.Fatalf("expected contains preview to invoke file preview payload producer, got %q", command)
	}
	if !strings.Contains(command, "| "+shellQuoteArg(treeBin)+" --bare --color always") {
		t.Fatalf("expected contains preview command to pipe into catclip-tree --bare --color always, got %q", command)
	}
}

func TestFzfContainsSnippetPreviewCommandUsesSnippetFlag(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfContainsPreviewCommand(true)
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+" --quiet --internal-file-preview --internal-file-path {2} --snippet --contains {q}") {
		t.Fatalf("expected snippet contains preview to forward --snippet, got %q", command)
	}
}

func TestFzfDiffFilePreviewCommandUsesFilePreviewPayload(t *testing.T) {
	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	command := fzfDiffFilePreviewCommand("--changed")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}

	if !strings.Contains(command, shellQuoteArg(self)+" --quiet --internal-file-preview --internal-file-path \"$preview_target\" --changed --diff") {
		t.Fatalf("expected diff file preview command to invoke internal file preview payload producer, got %q", command)
	}
	if !strings.Contains(command, "| "+shellQuoteArg(treeBin)+" --bare --color always") {
		t.Fatalf("expected diff file preview command to pipe into catclip-tree --bare --color always, got %q", command)
	}
}

func TestAllIgnoredTargetsIncludesIgnoredEntries(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts":                  "export const app = true\n",
		"node_modules/react/index.js": "module.exports = {}\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "."})
	baseRules, err := loadIgnoreRules()
	if err != nil {
		t.Fatalf("loadIgnoreRules returned error: %v", err)
	}
	matcher, err := buildScopeMatcher(baseRules, scope{})
	if err != nil {
		t.Fatalf("buildScopeMatcher returned error: %v", err)
	}

	resolver := scopeResolver{
		cfg:               cfg,
		matcher:           matcher,
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

func TestAllIgnoredTargetsTracksEmptyAndNoTextDirectoryState(t *testing.T) {
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
	baseRules, err := loadIgnoreRules()
	if err != nil {
		t.Fatalf("loadIgnoreRules returned error: %v", err)
	}
	matcher, err := buildScopeMatcher(baseRules, scope{})
	if err != nil {
		t.Fatalf("buildScopeMatcher returned error: %v", err)
	}

	resolver := scopeResolver{
		cfg:               cfg,
		gitCtx:            detectGitContext(project),
		matcher:           matcher,
		allowFileSymlinks: false,
		useGitIgnore:      true,
	}

	targets, err := resolver.allIgnoredTargets()
	if err != nil {
		t.Fatalf("allIgnoredTargets returned error: %v", err)
	}

	lookup := make(map[string]targetMatch, len(targets))
	for _, target := range targets {
		lookup[target.Path] = target
	}

	if got, ok := lookup["blocked-empty"]; !ok || got.State != treeTargetStateEmpty {
		t.Fatalf("expected blocked-empty to be marked empty, got %#v (present=%v)", got, ok)
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
	baseRules, err := loadIgnoreRules()
	if err != nil {
		t.Fatalf("loadIgnoreRules returned error: %v", err)
	}
	matcher, err := buildScopeMatcher(baseRules, scope{})
	if err != nil {
		t.Fatalf("buildScopeMatcher returned error: %v", err)
	}

	resolver := scopeResolver{
		cfg:               cfg,
		gitCtx:            detectGitContext(project),
		matcher:           matcher,
		allowFileSymlinks: false,
		useGitIgnore:      false,
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
	if !strings.Contains(stderr.String(), "use --include to authorize the blocked file or directory first") {
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
		`Try: catclip --hiss`,
		`catclip --include blocked-dir`,
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
		"assets/avatar.jpg":   "not really a jpg\n",
		"assets/custom.woff2": "not really a font\n",
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
		"cfg/bad-nonprintable.bconf": "bad\x01value\n",
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

func TestRunTreatsSingleDotDotfilesAsExtensionlessForParity(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".eslintcache": "{}",
		"src/app.ts":   "export const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--print", "."})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, `<file path=".eslintcache">`) {
		t.Fatalf("expected .eslintcache to match shell parity, got:\n%s", out)
	}
}

func TestRunHeadlessAmbiguousTargetFailsWithGuidance(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/common/a.ts": "export const a = 1\n",
		"lib/common/b.ts": "export const b = 2\n",
	})

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "common"})

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
	if !strings.Contains(stderr.String(), "Use --include to authorize it for this run") {
		t.Fatalf("expected --include guidance, got:\n%s", stderr.String())
	}
}

func TestRunIncludeAuthorizesGitIgnoredDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":           "ignored/\n",
		"ignored/common/ok.ts": "visible via include\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--quiet", "--print", "--include", "ignored"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("expected --include to authorize gitignored directory, got: %v", err)
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

	cfg := parseInProject(t, project, []string{"--print", "src", "--contains", "TODO", "--snippet"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "src/skip.ts") {
		t.Fatalf("expected non-matching file to stay out of snippet output, got:\n%s", out)
	}
	if got := strings.Count(out, `<file path="src/app.ts" snippet="`); got != 2 {
		t.Fatalf("expected 2 snippet blocks, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "<file path=\"src/app.ts\" snippet=\"1-4\">\nconst a = 1\nTODO: first\nconst b = 2\nTODO: second\n</file>\n\n") {
		t.Fatalf("expected first snippet block, got:\n%s", out)
	}
	if !strings.Contains(out, "<file path=\"src/app.ts\" snippet=\"6-8\">\nconst c = 3\nTODO: third\nconst d = 4\n</file>\n\n") {
		t.Fatalf("expected second snippet block, got:\n%s", out)
	}
}

func TestRunPreviewRendersTreeAndSummary(t *testing.T) {
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
	if !strings.Contains(out, "src/") || !strings.Contains(out, "├── a.ts") || !strings.Contains(out, "components/") {
		t.Fatalf("expected preview tree in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Count:") || !strings.Contains(out, "Size:") || !strings.Contains(out, "Tokens:") {
		t.Fatalf("expected preview summary in output, got:\n%s", out)
	}
	if strings.Contains(out, "ignored.test.ts") {
		t.Fatalf("expected default ignored tests/ directory to stay hidden, got:\n%s", out)
	}
}

func TestRunPreviewShowsTargetPathHintForNestedResolvedTarget(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		".cache/babel-loader/abc.json": "ok\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "babel-loader"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "babel-loader/ (.cache/babel-loader/)") {
		t.Fatalf("expected nested target path hint in preview, got:\n%s", out)
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

func TestRunPreviewNoTreeShowsSummaryOnly(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/a.ts": "const ok = true\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "--no-tree", "src"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "├──") || strings.Contains(out, "a.ts") {
		t.Fatalf("expected no tree output with --no-tree, got:\n%s", out)
	}
	if !strings.Contains(out, "Count:") || !strings.Contains(out, "Tokens:") {
		t.Fatalf("expected summary output, got:\n%s", out)
	}
}

func TestRunPreviewShowsSnippetOnlyTags(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/app.ts": "const a = 1\nTODO: fix\nconst b = 2\n",
	})

	cfg := parseInProject(t, project, []string{"--preview", "src", "--contains", "TODO", "--snippet"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "app.ts") || !strings.Contains(out, "[snippet only]") {
		t.Fatalf("expected snippet-only preview tag, got:\n%s", out)
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

func TestFilterGitIgnoredEntriesSkipsGitVisibleEntries(t *testing.T) {
	entries := []fileEntry{{
		RelPath:    "src/main.ts",
		GitVisible: true,
	}}

	out, err := filterGitIgnoredEntries(gitContext{
		Enabled: true,
		Root:    "/definitely/missing/repo",
	}, entries)
	if err != nil {
		t.Fatalf("expected git-visible entries to bypass git check-ignore, got error: %v", err)
	}
	if len(out) != 1 || out[0].RelPath != "src/main.ts" {
		t.Fatalf("unexpected filtered entries: %#v", out)
	}
}

func TestBuildVisibleDirIndexDerivesDirsFromVisibleFiles(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not available")
	}

	project := setupTestProject(t, map[string]string{
		"src/main.ts":           "export const main = true\n",
		"pkg/util/helpers.txt":  "helpers\n",
		"notes/README.md":       "# notes\n",
		"images/logo.png":       "not really png\n",
		"docs-empty/.keep.bin":  "binary",
		"nested-empty/icon.png": "not really png\n",
	})
	if err := os.MkdirAll(filepath.Join(project, "truly-empty"), 0o755); err != nil {
		t.Fatalf("mkdir truly-empty: %v", err)
	}

	matcher, err := buildScopeMatcher(nil, scope{})
	if err != nil {
		t.Fatalf("buildScopeMatcher: %v", err)
	}

	resolver := scopeResolver{
		cfg: runConfig{
			WorkingDir: project,
		},
		matcher: matcher,
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
	if !strings.Contains(stderr.String(), "Use --include to authorize it for this run") {
		t.Fatalf("expected --include guidance, got:\n%s", stderr.String())
	}
}

func TestRunIncludeAuthorizesGitIgnoredFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		".gitignore":        "ignored/\n",
		"src/main.ts":       "console.log('ok')\n",
		"ignored/secret.ts": "console.log('ignored')\n",
	})
	initGitRepo(t, project)

	cfg := parseInProject(t, project, []string{"--print", "--include", "ignored/secret.ts"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "ignored/secret.ts") {
		t.Fatalf("expected --include to restore gitignored file, got:\n%s", stdout.String())
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

func TestRunChangedWarnsOutsideGit(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "console.log('ok')\n",
	})

	cfg := parseInProject(t, project, []string{"--print", ".", "--changed"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if !strings.Contains(stderr.String(), "--changed/--staged/--unstaged/--untracked require a git repo") {
		t.Fatalf("expected git warning, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "src/main.ts") {
		t.Fatalf("expected file to remain in output outside git repo, got:\n%s", stdout.String())
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
	entries := []fileEntry{
		{RelPath: "src/a.ts", TargetRoot: "src"},
		{RelPath: "src/b.ts", TargetRoot: "src"},
		{RelPath: "docs/readme.md", TargetRoot: "docs"},
		{RelPath: "scattered/file.txt"},
	}

	got := previewGitStatusPathspecs(gitCtx, entries)
	want := []string{"docs", "scattered/file.txt", "src"}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("unexpected pathspecs: got %q want %q", got, want)
	}
}

func TestBypassesDirectoryLabelColorsEntireIncludedSubtree(t *testing.T) {
	entry := fileEntry{
		RelPath:    "node_modules/.cache/babel-loader/abc123.json",
		TargetRoot: "node_modules",
		Bypassed:   true,
		BlockRule:  "node_modules/",
	}

	for _, relDir := range []string{
		"node_modules",
		"node_modules/.cache",
		"node_modules/.cache/babel-loader",
	} {
		if !bypassesDirectoryLabel(entry, relDir) {
			t.Fatalf("expected %q to inherit bypass coloring", relDir)
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
	if _, ok := ripgrepBinary(); !ok {
		t.Skip("rg not available")
	}

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
	if _, ok := ripgrepBinary(); !ok {
		t.Skip("rg not available")
	}

	project := setupTestProject(t, map[string]string{
		"real/file.ts": "export const value = 1\n",
		"plain.ts":     "export const plain = 1\n",
	})
	if err := os.Symlink("real", filepath.Join(project, "alias")); err != nil {
		t.Fatalf("symlink failed: %v", err)
	}

	resolver := scopeResolver{
		cfg: runConfig{WorkingDir: project},
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
	if _, ok := ripgrepBinary(); !ok {
		t.Skip("rg not available")
	}

	project := setupTestProject(t, map[string]string{
		"real/file.ts": "export const value = 1\n",
	})
	if err := os.Symlink("real/file.ts", filepath.Join(project, "link.ts")); err != nil {
		t.Fatalf("symlink failed: %v", err)
	}

	resolver := scopeResolver{
		cfg: runConfig{WorkingDir: project},
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

	cfg := parseInProject(t, project, []string{"--print", ".", "--changed", "--diff"})

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
			args:     []string{"--print", ".", "--staged", "--diff"},
			wantType: "staged-diff",
			wantPath: "staged.txt",
		},
		{
			name:     "unstaged-diff",
			args:     []string{"--print", ".", "--unstaged", "--diff"},
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

	cfg := parseInProject(t, project, []string{"--preview", ".", "--changed", "--diff"})

	var stdout, stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "staged.txt") || !strings.Contains(out, "[diff only]") {
		t.Fatalf("expected diff-only preview tag, got:\n%s", out)
	}
	if strings.Contains(out, "new.txt") && strings.Contains(out, "new.txt") && strings.Contains(out, "[?] [diff only]") {
		t.Fatalf("did not expect untracked diff-only tag, got:\n%s", out)
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

	for _, want := range []string{"Machine mode:", "Scope Modifiers:", "Patterns use shell glob syntax", "Run 'catclip --help-all' for the full manual."} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected short help to contain %q, got:\n%s", want, help)
		}
	}
	for _, want := range []string{"━━━ Full Manual ━━━", "Scope System:", "Evaluation Order (per scope):", displayPath(globalHissPath())} {
		if !strings.Contains(full, want) {
			t.Fatalf("expected full help to contain %q, got:\n%s", want, full)
		}
	}
}

func TestClipboardCommandShowsInstallHint(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "")

	_, err := clipboardCommand("linux", colorPalette{})
	if err == nil {
		t.Fatal("expected clipboard lookup error")
	}
	if !strings.Contains(err.Error(), "Install xclip or xsel") {
		t.Fatalf("expected install hint, got: %v", err)
	}
}

func TestWithPayloadWriterDoesNotBlockOnResidentWaylandClipboard(t *testing.T) {
	dir := t.TempDir()
	wlCopy := filepath.Join(dir, "wl-copy")
	script := "#!/bin/sh\ncat >/dev/null\nsleep 2\n"
	if err := os.WriteFile(wlCopy, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake wl-copy: %v", err)
	}

	t.Setenv("PATH", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	t.Setenv("CATCLIP_CLIPBOARD_WAIT_MS", "10")

	cfg := runConfig{OutputMode: outputModeClipboard, Platform: "linux"}
	started := time.Now()
	stats, err := withPayloadWriter(cfg, io.Discard, colorPalette{}, func(w io.Writer) error {
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
	"pick> ")
		if [ "$expect" = "ctrl-o" ] && [ "$query" = "node" ]; then
			emit_query
			printf '%s\n' "ctrl-o"
			exit 0
		fi
		if [ "$expect" = "ctrl-o" ] && [ "$query" = "src-back" ]; then
			if [ ! -f "$back_state_file" ]; then
				: > "$back_state_file"
				emit_query
				printf '%s\n' "ctrl-o"
				exit 0
			fi
			emit_query
			printf '%s\n' "$input" | grep -F "[dir] src" | head -n 1
			exit 0
		fi
		case "$query" in
			"")
				emit_query
				printf '%s\n' "$input" | grep -F "copy all files"
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
	"scope> ")
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
			src-back)
				exit 130
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

	args, targets, usedPicker, err := resolveStartupScopeInputs(resolver, nil, nil, nil)
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
	printf '%s\n' "$header" | grep -F "authorize ignored paths from .gitignore or .hiss" >/dev/null || {
		echo "missing include header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Enter continues with the current selection as --include." >/dev/null || {
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

	args, targets, usedPicker, err := resolveStartupScopeInputs(resolver, nil, []string{""}, nil)
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

if [ "$prompt" = "pick> " ] && [ "$query" = "sr" ]; then
	emit_query
	printf '%s\n' "$input" | grep -F "[dir] src" | head -n 1
	exit 0
fi

if [ "$prompt" = "pick> " ] && [ "$query" = "s" ]; then
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

	args, targets, usedPicker, err := resolveStartupScopeInputs(resolver, []string{"sr", "s"}, nil, nil)
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

if [ "$prompt" = "modifier> " ]; then
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

if [ "$prompt" = "modifier> " ]; then
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

if [ "$prompt" = "modifier> " ]; then
	printf '%s\n' "$input" | grep -F -- "--changed --diff" | head -n 1
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
	if got, want := strings.Join(args, "\n"), "--changed\n--diff"; got != want {
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

if [ "$prompt" = "modifier> " ]; then
	printf '%s\n' "$input" | grep -F $'\tchanged' | head -n 1
	exit 0
fi

if [ "$prompt" = "changed> " ]; then
	printf '%s\n' "$header" | grep -F "Start from changed files in the current scope" >/dev/null || {
		echo "missing changed header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Selecting every file keeps plain --changed." >/dev/null || {
		echo "missing changed all-files help" >&2
		exit 91
	}
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

if [ "$prompt" = "modifier> " ]; then
	printf '%s\n' "$input" | grep -F $'\tchanged-diff' | head -n 1
	exit 0
fi

if [ "$prompt" = "changed> " ]; then
	printf '%s\n' "$header" | grep -F "Start from changed files in the current scope" >/dev/null || {
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
	if got, want := strings.Join(args, "\n"), "--changed\n--only\nsrc/main.ts\n--diff"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestMaybeResolveStartupPickerArgsTrailingModifierMenuAfterResolvedTargets(t *testing.T) {
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

if [ "$prompt" = "modifier> " ]; then
	printf '%s\n' "$input" | grep -F $'\tchanged' | head -n 1
	exit 0
fi

	echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, err := resolveStartupModifierArgs(resolver, []string{"src", "shared"}, []string{"src", "shared"})
	if err != nil {
		t.Fatalf("resolveStartupModifierArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\nshared\n--changed"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
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

	_, _, _, err = resolveStartupArgs(resolver, []string{"--untracked", "--diff"})
	if err == nil {
		t.Fatal("expected startup resolution error for --untracked --diff")
	}
	if !strings.Contains(err.Error(), "--untracked --diff doesn't make sense") {
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

if [ "$prompt" = "modifier> " ]; then
	printf '%s\n' "$input" | grep -F $'\tchanged' | head -n 1
	exit 0
fi

if [ "$prompt" = "changed> " ]; then
	printf '%s\n' "$header" | grep -F "Start from changed files in the current scope" >/dev/null || {
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

	args, _, err := resolveStartupModifierArgs(resolver, []string{"src"}, []string{"src"})
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

if [ "$prompt" = "only> " ]; then
	printf '%s\n' "$bindings" | grep -F -- "alt-a:toggle-all" >/dev/null || {
		echo "missing alt-a toggle-all binding" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "keep only those files in the current scope" >/dev/null || {
		echo "missing only header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Enter continues with the current selection as --only." >/dev/null || {
		echo "missing only enter help" >&2
		exit 91
	}
	if ! printf '%s\n' "$input" | grep -F "src/main.ts" >/dev/null; then
		echo "expected src/main.ts in only picker" >&2
		exit 91
	fi
	if ! printf '%s\n' "$input" | grep -F "shared/util.ts" >/dev/null; then
		echo "expected shared/util.ts in only picker" >&2
		exit 91
	fi
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
	printf '%s\n' "$header" | grep -F "remove files from the current scope" >/dev/null || {
		echo "missing exclude header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Enter continues with the current selection as --exclude." >/dev/null || {
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

func TestResolveStartupTrailingActionArgsConsumesBareModifierSentinel(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('src')\n",
		"shared/util.ts": "console.log('shared')\n",
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

if [ "$prompt" = "only> " ]; then
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

	args, _, err := resolveStartupTrailingActionArgs(resolver, []string{".", "--"}, startupTrailingActionOnly)
	if err != nil {
		t.Fatalf("resolveStartupTrailingActionArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), ".\n--only\nsrc/main.ts"; got != want {
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

if [ "$prompt" = "pick> " ] && [ "$query" = "sr" ]; then
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

func TestResolveStartupArgsKeepsOnlyValueLiteralInCurrentScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"src/util.ts":      "console.log('util')\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not run for explicit --only values" >&2
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
	if usedFzf {
		t.Fatal("expected explicit --only value to stay literal without fzf")
	}
	if got, want := strings.Join(args, "\n"), "src\n--only\nuti"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestResolveStartupArgsKeepsExcludeValueLiteralInDefaultScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":      "console.log('src')\n",
		"src/util.ts":      "console.log('util')\n",
		"shared/util.ts":   "console.log('shared')\n",
		"scripts/build.ts": "console.log('scripts')\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
echo "fzf should not run for explicit --exclude values" >&2
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
	if usedFzf {
		t.Fatal("expected explicit --exclude value to stay literal without fzf")
	}
	if got, want := strings.Join(args, "\n"), "--exclude\nmai"; got != want {
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

if [ "$prompt" = "pick> " ] && [ "$query" = "Button.tsx" ]; then
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

if [ "$prompt" = "pick> " ]; then
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
}

func TestResolveBareStartupModifierArgsContainsOpensLiveRegexPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/todo.ts": "TODO: wire this up\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
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

if [ "$prompt" = "modifier> " ]; then
	printf '%s\n' "$input" | grep -F -- "--contains" | head -n 1
	exit 0
fi

if [ "$prompt" = "regex> " ]; then
	[ "$disabled" -eq 1 ] || { echo "contains picker must use --disabled" >&2; exit 91; }
	[ -z "$expect" ] || { echo "unexpected --expect: $expect" >&2; exit 91; }
	printf '%s\n' "$header" | grep -F "Regex matches file contents, not file names." >/dev/null || {
		echo "missing regex header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Enter continues with the current selection." >/dev/null || {
		echo "missing Enter header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Alt-A toggles all current matches." >/dev/null || {
		echo "missing Alt-A header" >&2
		exit 91
	}
	printf '%s\n' "$bindings" | grep -F -- "start:reload:" >/dev/null || {
		echo "missing start reload binding" >&2
		exit 91
	}
	printf '%s\n' "$bindings" | grep -F -- "alt-a:toggle-all" >/dev/null || {
		echo "missing alt-a toggle-all binding" >&2
		exit 91
	}
	printf '%s\n' "$bindings" | grep -F -- "--internal-contains-list" >/dev/null || {
		echo "missing internal contains list command" >&2
		exit 91
	}
	printf 'TODO\n'
	printf 'todo.ts\tsrc/todo.ts\tfile\ttext\n'
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
	if got, want := strings.Join(args, "\n"), "--contains\nTODO\n--only\nsrc/todo.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestResolveBareStartupModifierArgsContainsSnippetAppendsSnippetFlag(t *testing.T) {
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

if [ "$prompt" = "modifier> " ]; then
	printf '%s\n' 'selected	contains-snippet'
	exit 0
fi

if [ "$prompt" = "regex> " ]; then
	printf 'TODO\n'
	printf 'todo.ts\tsrc/todo.ts\tfile\ttext\n'
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
	if got, want := strings.Join(args, "\n"), "--contains\nTODO\n--only\nsrc/todo.ts\n--snippet"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestResolveBareStartupModifierArgsContainsSnippetUsesSnippetPreviewCommand(t *testing.T) {
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

if [ "$prompt" = "modifier> " ]; then
	printf '%s\n' 'selected	contains-snippet'
	exit 0
fi

if [ "$prompt" = "regex> " ]; then
	printf '%s\n' "$preview" | grep -F -- "--internal-file-preview --internal-file-path {2} --snippet --contains {q}" >/dev/null || {
		echo "missing snippet preview command: $preview" >&2
		exit 91
	}
	printf 'TODO\n'
	printf 'todo.ts\tsrc/todo.ts\tfile\ttext\n'
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	if _, _, err := resolveBareStartupModifierArgs(resolver); err != nil {
		t.Fatalf("resolveBareStartupModifierArgs returned error: %v", err)
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
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "modifier> " ]; then
	[ "$(printf '%s\n' "$header" | wc -l | tr -d ' ')" = "4" ] || {
		echo "expected 4-line modifier header" >&2
		exit 91
	}
	[ "$no_sort" -eq 1 ] || {
		echo "expected modifier picker to disable sorting" >&2
		exit 91
	}
	[ -z "$bindings" ] || {
		echo "unexpected modifier bindings: $bindings" >&2
		exit 91
	}
	first="$(printf '%s\n' "$input" | head -n 1)"
	last="$(printf '%s\n' "$input" | tail -n 1)"
	first_key="$(printf '%s\n' "$first" | cut -f2)"
	last_key="$(printf '%s\n' "$last" | cut -f2)"
	[ "$first_key" = "only" ] || {
		echo "unexpected first modifier row: $first" >&2
		exit 91
	}
	[ "$last_key" = "unstaged-diff" ] || {
		echo "unexpected last modifier row: $last" >&2
		exit 91
	}
	printf '%s\n' "$first" | grep -F -- "--only" >/dev/null || {
		echo "missing --only label in first row: $first" >&2
		exit 91
	}
	printf '%s\n' "$first" | grep -F -- "Keep only matching files from the current scope" >/dev/null || {
		echo "missing --only description in first row: $first" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F $'\tcontains-snippet' >/dev/null || {
		echo "missing contains-snippet modifier row" >&2
		exit 91
	}
	printf '%s\n' "$first"
	exit 0
fi

if [ "$prompt" = "only> " ]; then
	printf '%s\n' "$input" | grep -F "src/todo.ts" | head -n 1
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

if [ "$prompt" = "regex> " ]; then
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

	args, _, err := resolveStartupContainsArgs([]string{"src"}, false)
	if err != nil {
		t.Fatalf("resolveStartupContainsArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), "src\n--contains\nTODO\n--only\nsrc/main.ts\nsrc/util.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
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

if [ "$prompt" = "modifier> " ]; then
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
	if got, want := strings.Join(args, "\n"), "--only\nsrc/main.ts\nsrc/util.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestResolveStartupArgsPlaceholderOnlyIncludeOnlyKeepsDotScope(t *testing.T) {
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

if [ "$prompt" = "modifier> " ]; then
	count=0
	if [ -f %q ]; then
		count="$(cat %q)"
	fi
	count=$((count + 1))
	printf '%%s' "$count" > %q
	case "$count" in
		1)
			printf '%%s\n' 'selected	only'
			;;
		2)
			printf '%%s\n' 'selected	include'
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
		printf '%%s\n' "$input" | grep -F "node_modules/pkg/index.js" | head -n 1
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
	if got, want := strings.Join(args, "\n"), "--only\nsrc/main.ts\n--include\nnode_modules\n--only\nnode_modules/pkg/index.js"; got != want {
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

if [ "$prompt" = "modifier> " ]; then
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

if [ "$prompt" = "modifier> " ]; then
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

if [ "$prompt" = "regex> " ]; then
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
	if got, want := strings.Join(args, "\n"), "src\n--contains\nTODO\n--only\nsrc/main.ts"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestFormatResolvedStartupCommandShellQuotesArgs(t *testing.T) {
	got := formatResolvedStartupCommand([]string{"src", "--contains", "TODO items", "--only", "src/a test.ts"})
	want := `catclip src --contains "TODO items" --only "src/a test.ts"`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestWriteResolvedStartupCommandPrintsCanonicalCommand(t *testing.T) {
	var stderr bytes.Buffer
	if err := writeResolvedStartupCommand(&stderr, []string{"src", "--contains", "TODO", "--only", "src/a.ts"}); err != nil {
		t.Fatalf("writeResolvedStartupCommand returned error: %v", err)
	}

	want := "Resolved command:\n  catclip src --contains TODO --only src/a.ts\n"
	if stderr.String() != want {
		t.Fatalf("expected stderr %q, got %q", want, stderr.String())
	}
}

func TestRunInternalContainsListOutputsCurrentScopeMatches(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "TODO: src\n",
		"src/util.ts":    "helper\n",
		"shared/util.ts": "TODO: shared\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-contains-list", "src", "--contains", "TODO"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(cfg, &stdout, &stderr); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "\tsrc/main.ts\tfile\ttext") {
		t.Fatalf("expected src/main.ts in internal contains list output, got %q", out)
	}
	if strings.Contains(out, "\tshared/util.ts\tfile\ttext") {
		t.Fatalf("shared/util.ts leaked into src-only contains list output: %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr, got %q", stderr.String())
	}
}

func TestRunInternalContainsListSuppressesInvalidRegex(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "TODO: src\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-contains-list", "src", "--contains", "["})

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

func TestRunInternalFilePreviewOutputsSnippetPayload(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts": "const outside = 0\n\nconst a = 1\nTODO: first\nconst b = 2\n\nconst c = 3\nTODO: second\nconst d = 4\n",
	})
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "src/main.ts", "--contains", "TODO", "--snippet"})

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
	if got := doc.File.MatchPattern; got != "" {
		t.Fatalf("doc.File.MatchPattern = %q, want empty for snippet block preview", got)
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
	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "src/main.ts", "--contains", "", "--snippet"})

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

	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "staged.txt", "--staged", "--diff"})

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

func TestRunInternalFilePreviewChangedDiffFallsBackToFullFileForUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	project := setupTestProject(t, map[string]string{
		"tracked.txt": "tracked\n",
	})
	initGitRepo(t, project)
	writeProjectFile(t, project, "new.txt", "brand new\n")

	cfg := parseInProject(t, project, []string{"--internal-file-preview", "--internal-file-path", "new.txt", "--changed", "--diff"})

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

func parseInProject(t *testing.T, project string, args []string) runConfig {
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
