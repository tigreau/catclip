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
	printf '%s\n' "$preview" | grep -F -- "--internal-tree-payload" >/dev/null || {
		echo "missing internal tree payload preview" >&2
		exit 91
	}
	printf '%s\n' "$preview" | grep -F -- '--depth {2}' >/dev/null || {
		echo "missing dynamic depth preview" >&2
		exit 91
	}
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

func TestStartupAvailableModifierChoicesHideDepthForShallowScope(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"README.md": "readme\n",
	})
	_ = parseInProject(t, project, []string{"."})

	choices := startupAvailableModifierChoices([]string{".", "--only", "README.md"})
	if startupModifierChoiceKeysContain(choices, "depth") {
		t.Fatalf("depth should be hidden for a scope whose max depth is already 1: %#v", startupModifierChoiceKeys(choices))
	}
}
