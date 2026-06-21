package ui

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
	printf '%s\n' "$preview" | grep -F -- "--internal-tree-preview" >/dev/null || {
		echo "preview command missing internal tree preview invocation" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "catclip-tree" >/dev/null && {
		echo "preview command must not invoke catclip-tree" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "--internal-prediscovered" >/dev/null || {
		echo "preview command missing --internal-prediscovered checkpoint flag" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "{4}" >/dev/null || {
		echo "preview command missing {4} per-row depth-tail placeholder" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "--input-dir" >/dev/null && {
		echo "lazy preview must not use the old --input-dir/--input-stem per-bucket file shape" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- "--internal-tree-payload" >/dev/null && {
		echo "preview command should no longer invoke catclip --internal-tree-payload" >&2
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
	# {4} must live OUTSIDE any double-quoted region — fzf substitution
	# preserves shell-escaped chars literally when inside double quotes,
	# which would break the multi-token --depth tail substitution.
	if printf '%s\n' "$preview" | grep -E '"[^"]*\{4\}[^"]*"' >/dev/null; then
		echo "preview command quotes {4} inside double quotes: $preview" >&2
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

// TestStartupDepthPickerWritesSingleCheckpoint pins the Item 5 lazy redesign:
// the depth picker writes one shared checkpoint, not N per-bucket payload
// files. fzf substitutes a `--depth N` tail from each line's hidden column 4
// into the per-focus preview command, and the child applies the depth filter
// against the checkpoint in-memory.
func TestStartupDepthPickerWritesSingleCheckpoint(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"a.ts":          "a\n",
		"src/b.ts":      "b\n",
		"src/deep/c.ts": "c\n",
		"x/y/z/leaf.ts": "leaf\n",
	})
	_ = parseInProject(t, project, []string{"."})

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

# Pull the checkpoint path out of the preview command. Pattern looks like:
#   "<catclip>" --quiet --internal-tree-preview --internal-prediscovered "<ckpt>" {4}
ckpt_token="$(printf '%s\n' "$preview" | sed -n 's/.*--internal-prediscovered "\{0,1\}\([^" ]*\)"\{0,1\}.*/\1/p')"
if [ -z "$ckpt_token" ]; then
	echo "could not extract checkpoint path from preview command: $preview" >&2
	exit 91
fi

tmpdir_token="$(dirname "$ckpt_token")"

# Snapshot the tmpdir's contents while the picker is open, so the Go test can
# verify exactly one checkpoint file exists (not N per-bucket files).
ls "$tmpdir_token" >"$CATCLIP_DEPTH_TEST_PROBE/files.txt" 2>/dev/null || true
printf '%s\n' "$tmpdir_token" >"$CATCLIP_DEPTH_TEST_PROBE/tmpdir.txt"
printf '%s\n' "$ckpt_token" >"$CATCLIP_DEPTH_TEST_PROBE/ckpt.txt"

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

	ckptBytes, err := os.ReadFile(filepath.Join(probeDir, "ckpt.txt"))
	if err != nil {
		t.Fatalf("read probe ckpt.txt: %v", err)
	}
	ckpt := strings.TrimSpace(string(ckptBytes))
	if filepath.Base(ckpt) != "scope.json" {
		t.Fatalf("expected checkpoint named scope.json, got %q", filepath.Base(ckpt))
	}

	filesBytes, err := os.ReadFile(filepath.Join(probeDir, "files.txt"))
	if err != nil {
		t.Fatalf("read probe files.txt: %v", err)
	}
	files := strings.Fields(string(filesBytes))
	// Item 5 lazy redesign: exactly ONE file in the tmpdir — the shared
	// checkpoint — regardless of bucket count. Previously this would have
	// been 4 files (1.json through 4.json for depths 1-4).
	if len(files) != 1 || files[0] != "scope.json" {
		t.Fatalf("expected tmpdir to contain exactly one shared checkpoint named scope.json; got %v", files)
	}

	// Tmpdir is cleaned up after the picker returns.
	if _, err := os.Stat(tmpdir); !os.IsNotExist(err) {
		t.Fatalf("expected tmpdir %q to be removed after picker returned, stat err: %v", tmpdir, err)
	}
}

func TestStartupDepthPickerPreviewCommandUsesInternalTreePreview(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":              "main\n",
		"src/components/Button.ts": "button\n",
	})
	_ = parseInProject(t, project, []string{"."})

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

# The preview command uses catclip's in-process tree renderer over precomputed payload files.
if ! printf '%s\n' "$preview" | grep -F -- "$CATCLIP_DEPTH_TEST_SELF" >/dev/null; then
	echo "preview command must invoke the parent catclip binary: $preview" >&2
	exit 91
fi
if ! printf '%s\n' "$preview" | grep -F -- "--internal-tree-preview" >/dev/null; then
	echo "preview command missing --internal-tree-preview: $preview" >&2
	exit 91
fi
if printf '%s\n' "$preview" | grep -F -- "--internal-tree-payload" >/dev/null; then
	echo "preview command must not invoke --internal-tree-payload: $preview" >&2
	exit 91
fi
if printf '%s\n' "$preview" | grep -F -- "catclip-tree" >/dev/null; then
	echo "preview command must not invoke catclip-tree: $preview" >&2
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
