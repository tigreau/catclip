package catclip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStartupDepthArgsUsesPicker(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":              "main\n",
		"src/components/Button.ts": "button\n",
		"src/features/view.ts":     "view\n",
	})
	_ = parseInProject(t, project, []string{"."})

	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

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

if [ "$prompt" = "depth> " ]; then
	printf '%s\n' "$header" | grep -F "Pick maximum path depth." >/dev/null || {
		echo "missing depth header" >&2
		exit 91
	}
	printf '%s\n' "$header" | grep -F "Depth counts path segments from the working directory root." >/dev/null || {
		echo "missing depth explanation" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "catclip-tree" >/dev/null || {
		echo "preview command missing catclip-tree invocation" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "--input-dir" >/dev/null || {
		echo "preview command missing --input-dir" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "--input-stem {2}" >/dev/null || {
		echo "preview command missing per-bucket stem placeholder" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "--internal-tree-payload" >/dev/null && {
		echo "preview command should no longer invoke catclip --internal-tree-payload" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "--depth {2}" >/dev/null && {
		echo "preview command should no longer pipe --depth {2} into catclip" >&2
		exit 91
	}
	case "$preview" in
		*'|'*)
			echo "preview command must not contain a shell pipe" >&2
			exit 91
			;;
		*'<'*)
			echo "preview command must not contain a shell redirect" >&2
			exit 91
			;;
	esac
	# {2} must live OUTSIDE any double-quoted region. If fzf substitutes
	# the placeholder while it's wrapped in quotes, the shell-escaped
	# replacement ('2') is preserved literally in the resulting path.
	if printf '%s\n' "$preview" | grep -E '"[^"]*\{2\}[^"]*"' >/dev/null; then
		echo "preview command quotes {2} inside double quotes: $preview" >&2
		exit 91
	fi
	printf '%s\n' "$input" | grep -F '1	1	keep files at depth <= 1' >/dev/null && {
		echo "depth 1 should not appear (no files at depth 1)" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F '2	2	keep files at depth <= 2 (1 files)' >/dev/null || {
		echo "missing depth row 2" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F '3	3	keep files at depth <= 3 (3 files)' >/dev/null || {
		echo "missing depth row 3" >&2
		exit 91
	}
	printf '%s\n' "$input" | grep -F '2	2	keep files at depth <= 2' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, usedFzf, err := resolveStartupDepthArgs([]string{"src"})
	if err != nil {
		t.Fatalf("resolveStartupDepthArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected depth picker to use fzf")
	}
	if got, want := strings.Join(args, "\n"), "src\n--depth\n2"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestStartupDepthPickerPrerendersAllBuckets(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.ts":          "a\n",
		"src/b.ts":      "b\n",
		"src/deep/c.ts": "c\n",
		"x/y/z/leaf.ts": "leaf\n",
	})
	_ = parseInProject(t, project, []string{"."})

	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	probeDir := t.TempDir()
	t.Setenv("CATCLIP_DEPTH_TEST_PROBE", probeDir)

	installScriptFzf(t, `#!/bin/sh
prompt=""
preview=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt) prompt="$2"; shift 2 ;;
		--header) shift 2 ;;
		--preview) preview="$2"; shift 2 ;;
		*) shift ;;
	esac
done

input="$(cat)"

if [ "$prompt" != "depth> " ]; then
	echo "unexpected prompt: $prompt" >&2
	exit 91
fi

# Pull the tmpdir out of the preview command. Pattern looks like:
#   "<treeBin>" --shape-tags --git-badges ... --input-dir "<tmpdir>" --input-stem {2}
tmpdir_token="$(printf '%s\n' "$preview" | sed -n 's/.*--input-dir "\{0,1\}\([^" ]*\)"\{0,1\} --input-stem.*/\1/p')"
if [ -z "$tmpdir_token" ]; then
	echo "could not extract tmpdir from preview command: $preview" >&2
	exit 91
fi

# Snapshot the tmpdir's contents so the Go test can verify pre-rendered files
# existed while the picker was open.
ls "$tmpdir_token" >"$CATCLIP_DEPTH_TEST_PROBE/files.txt" 2>/dev/null || true
printf '%s\n' "$tmpdir_token" >"$CATCLIP_DEPTH_TEST_PROBE/tmpdir.txt"

# Select the deepest bucket so the picker exits cleanly.
printf '%s\n' "$input" | tail -n 1
exit 0
`)

	_, _, err := resolveStartupDepthArgs([]string{"."})
	if err != nil {
		t.Fatalf("resolveStartupDepthArgs returned error: %v", err)
	}

	tmpdirBytes, err := os.ReadFile(filepath.Join(probeDir, "tmpdir.txt"))
	if err != nil {
		t.Fatalf("read probe tmpdir.txt: %v", err)
	}
	tmpdir := strings.TrimSpace(string(tmpdirBytes))
	if tmpdir == "" {
		t.Fatal("expected non-empty tmpdir captured during picker run")
	}
	if !strings.Contains(filepath.Base(tmpdir), "catclip-depth-") {
		t.Fatalf("expected tmpdir name to carry catclip-depth- prefix, got %q", tmpdir)
	}

	filesBytes, err := os.ReadFile(filepath.Join(probeDir, "files.txt"))
	if err != nil {
		t.Fatalf("read probe files.txt: %v", err)
	}
	files := strings.Fields(string(filesBytes))
	// Project max depth is 4 (x/y/z/leaf.ts); every depth 1..4 has at least one
	// file, so we expect 1.json through 4.json. Buckets only exist where
	// counts[d] > 0, which matches every depth in this fixture.
	wantFiles := []string{"1.json", "2.json", "3.json", "4.json"}
	got := map[string]bool{}
	for _, f := range files {
		got[f] = true
	}
	for _, w := range wantFiles {
		if !got[w] {
			t.Fatalf("expected pre-rendered bucket file %q to exist in tmpdir; got %v", w, files)
		}
		raw, err := os.ReadFile(filepath.Join(tmpdir, w))
		if err == nil {
			// Files are deleted via defer after the picker returns; capture earlier.
			if len(raw) == 0 {
				t.Fatalf("bucket file %q is empty during picker run", w)
			}
		}
	}

	// Tmpdir is cleaned up after the picker returns.
	if _, err := os.Stat(tmpdir); !os.IsNotExist(err) {
		t.Fatalf("expected tmpdir %q to be removed after picker returned, stat err: %v", tmpdir, err)
	}
}

func TestStartupDepthPickerPreviewCommandDoesNotCallCatclip(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":              "main\n",
		"src/components/Button.ts": "button\n",
	})
	_ = parseInProject(t, project, []string{"."})

	treeBin := filepath.Join(t.TempDir(), "catclip-tree")
	if err := os.WriteFile(treeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake catclip-tree: %v", err)
	}
	t.Setenv("CATCLIP_TREE", treeBin)

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("CATCLIP_DEPTH_TEST_SELF", self)

	installScriptFzf(t, `#!/bin/sh
prompt=""
preview=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt) prompt="$2"; shift 2 ;;
		--header) shift 2 ;;
		--preview) preview="$2"; shift 2 ;;
		*) shift ;;
	esac
done

input="$(cat)"

if [ "$prompt" != "depth> " ]; then
	echo "unexpected prompt: $prompt" >&2
	exit 91
fi

# The preview command must not invoke the parent catclip binary in any form.
if printf '%s\n' "$preview" | grep -F -- "$CATCLIP_DEPTH_TEST_SELF" >/dev/null; then
	echo "preview command must not invoke the parent catclip binary: $preview" >&2
	exit 91
fi
if printf '%s\n' "$preview" | grep -F -- "--internal-tree-payload" >/dev/null; then
	echo "preview command must not invoke --internal-tree-payload: $preview" >&2
	exit 91
fi

printf '%s\n' "$input" | head -n 1
exit 0
`)

	if _, _, err := resolveStartupDepthArgs([]string{"."}); err != nil {
		t.Fatalf("resolveStartupDepthArgs returned error: %v", err)
	}
}

func TestValidateStartupDepthValueRejectsOutOfRangeChoice(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"README.md":   "readme\n",
		"src/main.ts": "main\n",
	})
	_ = parseInProject(t, project, []string{"."})

	_, err := validateStartupDepthValue([]string{".", "--only", "README.md", "src/main.ts"}, "3")
	if err == nil {
		t.Fatal("expected out-of-range depth error")
	}
	if !strings.Contains(err.Error(), "current scope max depth 2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartupAvailableModifierChoicesShowDepthForShallowScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"README.md": "readme\n",
	})
	_ = parseInProject(t, project, []string{"."})

	choices := startupAvailableModifierChoices([]string{".", "--only", "README.md"})
	if !startupModifierChoiceKeysContain(choices, "depth") {
		t.Fatalf("depth should remain available regardless of scope max depth: %#v", startupModifierChoiceKeys(choices))
	}
}
