package catclip

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fixture: .hiss, root .gitignore, nested web/.gitignore, .git/info/exclude,
// with node_modules/ in BOTH .hiss and root .gitignore so the dedup case is
// exercised. Returns the temp working dir.
func setupIgnoreRulesFixture(t *testing.T) string {
	t.Helper()

	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	hissDir := filepath.Join(xdgDir, "catclip")
	if err := os.MkdirAll(hissDir, 0o755); err != nil {
		t.Fatalf("mkdir hiss: %v", err)
	}
	hiss := `# secrets section
.env
.env.local

# overlap with .gitignore
node_modules/

# duplicate within hiss (different lines)
*.log
*.log
`
	if err := os.WriteFile(filepath.Join(hissDir, ".hiss"), []byte(hiss), 0o644); err != nil {
		t.Fatalf("write .hiss: %v", err)
	}

	repo := t.TempDir()
	gitignore := `# project ignores
node_modules/
*.key
/dist
`
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	webDir := filepath.Join(repo, "web")
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatalf("mkdir web: %v", err)
	}
	if err := os.WriteFile(filepath.Join(webDir, ".gitignore"), []byte("*.local.js\n"), 0o644); err != nil {
		t.Fatalf("write web/.gitignore: %v", err)
	}

	// Fake .git/info/exclude (no real git init needed — detectGitContext
	// won't find a repo without `git rev-parse`, so info/exclude only lands in
	// the union when in a real repo. Tested separately in the in-repo case.)

	return repo
}

func runAndCapture(t *testing.T, repo string, targets []string) string {
	t.Helper()
	var buf bytes.Buffer
	cfg := listIgnoreRulesConfig{WorkingDir: repo, Targets: targets}
	if err := runListIgnoreRules(cfg, &buf, io.Discard); err != nil {
		t.Fatalf("runListIgnoreRules: %v", err)
	}
	return stripANSI(buf.String())
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiPattern.ReplaceAllString(s, "") }

func TestListIgnoreRulesDedupedUnion(t *testing.T) {
	repo := setupIgnoreRulesFixture(t)
	out := runAndCapture(t, repo, []string{"."})

	// Each unique pattern appears exactly once as a row prefix.
	for _, p := range []string{"node_modules/", ".env", ".env.local", "*.key", "/dist", "*.local.js", "*.log"} {
		if cnt := patternRowCount(out, p); cnt != 1 {
			t.Errorf("pattern %q: expected 1 row, got %d\n%s", p, cnt, out)
		}
	}

	// Shared pattern carries both tags, gitignore (higher precedence) first.
	nmRow := patternRow(out, "node_modules/")
	if nmRow == "" {
		t.Fatalf("no node_modules/ row in:\n%s", out)
	}
	gitIdx := strings.Index(nmRow, ".gitignore:")
	hissIdx := strings.Index(nmRow, ".hiss:")
	if gitIdx < 0 || hissIdx < 0 {
		t.Errorf("node_modules/ row missing dual tags: %q", nmRow)
	}
	if gitIdx > hissIdx {
		t.Errorf("tag order wrong (.gitignore should come before .hiss): %q", nmRow)
	}

	// Intra-source duplicate: *.log appears twice in .hiss → both lines under the same tag.
	logRow := patternRow(out, "*.log")
	hissTagCount := strings.Count(logRow, ".hiss:")
	if hissTagCount != 2 {
		t.Errorf("*.log row should list two .hiss lines, got %d in %q", hissTagCount, logRow)
	}

	// Comments/blank lines never leak.
	for _, leak := range []string{"# secrets", "# project ignores", "# overlap"} {
		if strings.Contains(out, leak) {
			t.Errorf("comment leaked into output: %q\n%s", leak, out)
		}
	}

	// Summary distinguishes unique patterns from total rules.
	// Fixture has: .env, .env.local, node_modules/, *.log (4 unique in .hiss) plus
	// *.log duplicate → 5 hiss rules. .gitignore: node_modules/, *.key, /dist → 3.
	// web/.gitignore: *.local.js → 1. Total = 9 rules, 7 unique patterns.
	expectSummary := "7 patterns (9 rules) · 3 sources"
	if !strings.Contains(out, expectSummary) {
		t.Errorf("summary missing %q\nactual tail:\n%s", expectSummary, tailLines(out, 4))
	}
}

func TestListIgnoreRulesLegendPrecedenceLine(t *testing.T) {
	repo := setupIgnoreRulesFixture(t)
	out := runAndCapture(t, repo, []string{"."})

	if !strings.Contains(out, "Sources (precedence high → low") {
		t.Errorf("legend header missing\n%s", out)
	}
	if !strings.Contains(out, ".gitignore overrides .hiss") {
		t.Errorf("precedence one-liner missing\n%s", out)
	}
	// Legend ordering: .gitignore line before .hiss line.
	gIdx := strings.Index(out, "  .gitignore ")
	hIdx := strings.Index(out, "  .hiss ")
	if gIdx < 0 || hIdx < 0 || gIdx >= hIdx {
		t.Errorf("legend rows out of order (.gitignore should precede .hiss): g=%d h=%d", gIdx, hIdx)
	}
}

func TestListIgnoreRulesTargetScoping(t *testing.T) {
	repo := setupIgnoreRulesFixture(t)
	// Also add an unrelated nested .gitignore that should NOT appear when target=web/.
	otherDir := filepath.Join(repo, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, ".gitignore"), []byte("SHOULD_NOT_APPEAR\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runAndCapture(t, repo, []string{"web"})

	if strings.Contains(out, "SHOULD_NOT_APPEAR") {
		t.Errorf("target=web/ leaked an unrelated other/.gitignore rule:\n%s", out)
	}
	if !strings.Contains(out, "*.local.js") {
		t.Errorf("target=web/ missing the web-nested rule:\n%s", out)
	}
}

func TestListIgnoreRulesNoGitignoreOnlyHiss(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	if err := os.MkdirAll(filepath.Join(xdgDir, "catclip"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdgDir, "catclip", ".hiss"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir() // no .gitignore, not a git repo

	out := runAndCapture(t, repo, []string{"."})

	if !strings.Contains(out, ".env") {
		t.Errorf("expected .hiss rule in output:\n%s", out)
	}
	if strings.Contains(out, ".gitignore") {
		t.Errorf("non-git fixture should not show .gitignore source:\n%s", out)
	}
	// Precedence one-liner only when both contribute; here only hiss does.
	if strings.Contains(out, ".gitignore overrides .hiss") {
		t.Errorf("precedence line should be omitted when only .hiss contributes:\n%s", out)
	}
}

func TestListIgnoreRulesHeadlessStripsColor(t *testing.T) {
	repo := setupIgnoreRulesFixture(t)
	// Use a non-TTY writer (bytes.Buffer) so activeColorPaletteForWriter
	// returns the empty palette — same as agents see under --headless.
	var buf bytes.Buffer
	cfg := listIgnoreRulesConfig{WorkingDir: repo, Targets: []string{"."}}
	if err := runListIgnoreRules(cfg, &buf, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	raw := buf.String()
	if strings.ContainsRune(raw, '\x1b') {
		t.Errorf("non-TTY output contains ANSI escapes; expected stripped palette")
	}
}

func TestListIgnoreRulesEmptyResult(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	// XDG points at an empty dir — no .hiss file there. Not a git repo. So
	// nothing contributes.
	repo := t.TempDir()
	out := runAndCapture(t, repo, []string{"."})
	if !strings.Contains(out, "No ignore rules in effect") {
		t.Errorf("empty fixture should produce friendly empty message, got:\n%s", out)
	}
}

// patternRow returns the full line of the row whose first column is exactly
// `pattern` (the column is padded with spaces, then `[`).
func patternRow(s, pattern string) string {
	for _, line := range strings.Split(s, "\n") {
		// rows are "<pattern padded>  [...]"
		if strings.HasPrefix(strings.TrimRight(line, " "), pattern) {
			rest := strings.TrimPrefix(strings.TrimRight(line, " "), pattern)
			if rest == "" || strings.HasPrefix(strings.TrimLeft(rest, " "), "[") {
				return line
			}
		}
	}
	return ""
}

func patternRowCount(s, pattern string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimRight(line, " "), pattern) {
			rest := strings.TrimPrefix(strings.TrimRight(line, " "), pattern)
			if rest == "" || strings.HasPrefix(strings.TrimLeft(rest, " "), "[") {
				n++
			}
		}
	}
	return n
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// ignoreRemovalHint branches: --hiss only edits .hiss, so for any other source
// the hint must point at --all-ignore-rules instead of --hiss.
func TestIgnoreRemovalHintIsSourceAware(t *testing.T) {
	cases := []struct {
		source      string
		wantContain string
		wantAbsent  string
	}{
		{".hiss", "catclip --hiss", "--all-ignore-rules"},
		{".gitignore", "catclip --all-ignore-rules", "catclip --hiss"},
		{".git/info/exclude", "catclip --all-ignore-rules", "catclip --hiss"},
		{"(global)", "catclip --all-ignore-rules", "catclip --hiss"},
	}
	for _, tc := range cases {
		got := stripANSI(ignoreRemovalHint(tc.source, colorPalette{}))
		if !strings.Contains(got, tc.wantContain) {
			t.Errorf("source=%q: missing %q in %q", tc.source, tc.wantContain, got)
		}
		if strings.Contains(got, tc.wantAbsent) {
			t.Errorf("source=%q: should not mention %q, got %q", tc.source, tc.wantAbsent, got)
		}
	}
}
