package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestNormalizeSymbolicInteractiveFileSetValuesBypassesScopeInference(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.c": "int main(void) {}\n",
	})
	t.Chdir(project)

	got, ok, err := normalizeSymbolicInteractiveFileSetValues([]string{"*.c", "src/*.h", "*.c"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("all-symbolic selection did not take the normalization fast path")
	}
	if want := []string{"*.c", "src/*.h"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("symbolic values = %q, want %q", got, want)
	}
}

func TestNormalizeSymbolicInteractiveFileSetValuesRejectsLiteralWildcardFilename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain wildcard characters")
	}
	project := setupTestProject(t, nil)
	t.Chdir(project)
	literal := "literal*.go"
	if err := os.WriteFile(filepath.Join(project, literal), []byte("package literal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := normalizeSymbolicInteractiveFileSetValues([]string{literal}); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("literal wildcard filename was reinterpreted as a symbolic pattern")
	}
}

func TestResolveStartupScopeFileSetArgsOnlyDropsExactFilesCoveredBySelectedPattern(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go":             "package main\n",
		"cmd/catclip/main.go": "package main\n",
		"README.md":           "# readme\n",
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
	printf '%s\n' "$input" | grep -F $'\t*.go\t' | head -n 1
	printf '%s\n' "$input" | grep -F $'main.go\tmain.go\tmain.go\tfile\ttext\tfile' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"."}, "--only", "only> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), ".\n--only\n*.go"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestResolveStartupScopeFileSetArgsOnlyKeepsPatternRowsAtStableBottomOrder(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go":   "package main\n",
		"README.md": "# readme\n",
	})
	_ = parseInProject(t, project, []string{"."})
	installScriptFzf(t, `#!/bin/sh
prompt=""
nosort=0
while [ "$#" -gt 0 ]; do
	case "$1" in
		--prompt)
			prompt="$2"
			shift 2
			;;
		--no-sort)
			nosort=1
			shift
			;;
		*)
			shift
			;;
	esac
done

input="$(cat)"

if [ "$prompt" = "only> " ]; then
	[ "$nosort" -eq 1 ] || { echo "expected --no-sort for only> picker" >&2; exit 91; }
	printf '%s\n' "$input" | grep -F $'main.go\tmain.go\tmain.go\tfile\ttext\tfile' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"."}, "--only", "only> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), ".\n--only\nmain.go"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestStartupFileSetRowsOnlyPlacesPatternRowsBeforeFilesForBottomPromptLayout(t *testing.T) {
	rows := startupFileSetRows("--only", []string{"README.md", "main.go"})
	if len(rows) < 3 {
		t.Fatalf("expected pattern and file rows, got %#v", rows)
	}
	if rows[0].Kind != startupFileSetRowExtensionPattern {
		t.Fatalf("expected first row to be an extension pattern, got %#v", rows[0])
	}
	if rows[len(rows)-1].Kind != startupFileSetRowFile {
		t.Fatalf("expected last row to be a file row, got %#v", rows[len(rows)-1])
	}
}

func TestResolveStartupArgsExcludeDropsExactFilesCoveredBySelectedPattern(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main.go":     "package main\n",
		"pkg/util.go": "package pkg\n",
		"README.md":   "# readme\n",
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

if [ "$prompt" = "exclude> " ]; then
	[ "$query" = "go" ] || { echo "unexpected query: $query" >&2; exit 91; }
	printf '%s\n' "$input" | grep -F $'\t*.go\t' | head -n 1
	printf '%s\n' "$input" | grep -F $'main.go\tmain.go\tmain.go\tfile\ttext\tfile' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	resolver, err := newStartupPickerResolver()
	if err != nil {
		t.Fatalf("newStartupPickerResolver returned error: %v", err)
	}

	args, _, usedFzf, err := resolveStartupArgs(resolver, []string{".", "--exclude", "go"})
	if err != nil {
		t.Fatalf("resolveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected non-exact --exclude value to use fzf")
	}
	if got, want := strings.Join(args, "\n"), ".\n--exclude\n*.go"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func TestNormalizeInteractiveFileSetStageValuesDropsExactFilesCoveredBySelectedSubtree(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/main.ts":    "console.log('main')\n",
		"src/util.ts":    "console.log('util')\n",
		"docs/readme.md": "# readme\n",
	})
	_ = parseInProject(t, project, []string{"."})

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{"src/", "src/main.ts"})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"src/"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("expected normalized values %q, got %q", want, got)
	}
}

func TestDynamicPatternCandidatesForBasename(t *testing.T) {
	tests := []struct {
		name     string
		contains []string
	}{
		{name: "UserController.java", contains: []string{"User*", "*Controller.java", "*.java"}},
		{name: "auth_controller_test.go", contains: []string{"auth_controller*", "auth_controller_*", "*_controller_test.go", "*_test.go", "*.go"}},
		{name: "foo.spec.ts", contains: []string{"foo*", "foo.*", "*.spec.ts", "*.ts"}},
		{name: "user-card.test.tsx", contains: []string{"user*", "user-*", "*-card.test.tsx", "*.test.tsx", "*.tsx"}},
		{name: "routeV2.ts", contains: []string{"route*", "*V2.ts", "*.ts"}},
		{name: "editor_command_windows.go", contains: []string{"editor_command*", "editor_command_*", "*_windows.go", "*.go"}},
		{name: "command_spec_test.go", contains: []string{"command*", "command_*", "*_spec_test.go", "*_test.go", "*.go"}},
	}

	for _, tt := range tests {
		got := dynamicPatternCandidatesForBasename(tt.name)
		gotSet := make(map[string]struct{}, len(got))
		for _, candidate := range got {
			gotSet[candidate] = struct{}{}
		}
		for _, want := range tt.contains {
			if _, ok := gotSet[want]; !ok {
				t.Fatalf("dynamicPatternCandidatesForBasename(%q) = %q, missing %q", tt.name, got, want)
			}
		}
		for _, candidate := range got {
			if candidate == "*ller.java" || candidate == "*troller.java" {
				t.Fatalf("candidate generator produced raw token suffix %q for %q", candidate, tt.name)
			}
		}
	}
}

func TestInferDynamicFileSetPatternsCollapsesSharedPrefixFamily(t *testing.T) {
	selected := []string{
		"editor_command.go",
		"editor_command_test.go",
		"editor_command_windows.go",
		"editor_command_nonwindows.go",
	}
	inferred, remaining, err := inferDynamicFileSetPatterns(selected, selected, nil)
	if err != nil {
		t.Fatalf("inferDynamicFileSetPatterns returned error: %v", err)
	}
	if want := []string{"*.go"}; strings.Join(inferred, "\n") != strings.Join(want, "\n") {
		t.Fatalf("inferred = %q, want %q", inferred, want)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %q, want none", remaining)
	}
}

func TestInferDynamicFileSetPatternsKeepsDelimiterWhenItDefinesSelection(t *testing.T) {
	selected := []string{
		"editor_command_test.go",
		"editor_command_windows.go",
		"editor_command_nonwindows.go",
	}
	scope := append(append([]string(nil), selected...), "editor_command.go")
	inferred, remaining, err := inferDynamicFileSetPatterns(selected, scope, nil)
	if err != nil {
		t.Fatalf("inferDynamicFileSetPatterns returned error: %v", err)
	}
	if want := []string{"editor_command_*"}; strings.Join(inferred, "\n") != strings.Join(want, "\n") {
		t.Fatalf("inferred = %q, want %q", inferred, want)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %q, want none", remaining)
	}
}

func TestInferDynamicFileSetPatternsCompleteCoverage(t *testing.T) {
	selected := []string{"UserController.java", "AdminController.java", "AuthController.java"}
	inferred, remaining, err := inferDynamicFileSetPatterns(selected, selected, nil)
	if err != nil {
		t.Fatalf("inferDynamicFileSetPatterns returned error: %v", err)
	}
	if want := []string{"*.java"}; strings.Join(inferred, "\n") != strings.Join(want, "\n") {
		t.Fatalf("inferred = %q, want %q", inferred, want)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %q, want none", remaining)
	}
}

func TestInferDynamicFileSetPatternsRequiresCompleteCoverage(t *testing.T) {
	selected := []string{"UserController.java", "AdminController.java", "AuthController.java"}
	scope := append(append([]string(nil), selected...), "BillingController.java")
	inferred, remaining, err := inferDynamicFileSetPatterns(selected, scope, nil)
	if err != nil {
		t.Fatalf("inferDynamicFileSetPatterns returned error: %v", err)
	}
	if len(inferred) != 0 {
		t.Fatalf("inferred = %q, want none", inferred)
	}
	if strings.Join(remaining, "\n") != strings.Join(selected, "\n") {
		t.Fatalf("remaining = %q, want %q", remaining, selected)
	}
}

func TestNormalizeInteractiveFileSetStageValuesCollapsesControllers(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"UserController.java":  "class UserController {}\n",
		"AdminController.java": "class AdminController {}\n",
		"AuthController.java":  "class AuthController {}\n",
		"README.md":            "# readme\n",
	})
	_ = parseInProject(t, project, []string{"."})

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{
		"UserController.java",
		"AdminController.java",
		"AuthController.java",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"*.java"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestNormalizeInteractiveFileSetStageValuesKeepsIncompleteControllerSelectionExact(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"UserController.java":    "class UserController {}\n",
		"AdminController.java":   "class AdminController {}\n",
		"AuthController.java":    "class AuthController {}\n",
		"BillingController.java": "class BillingController {}\n",
	})
	_ = parseInProject(t, project, []string{"."})
	selected := []string{"UserController.java", "AdminController.java", "AuthController.java"}

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, selected)
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if strings.Join(got, "\n") != strings.Join(selected, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, selected)
	}
}

func TestNormalizeInteractiveFileSetStageValuesCollapsesDelimiterPatterns(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"auth_controller_test.go": "package auth\n",
		"user_controller_test.go": "package user\n",
		"main.go":                 "package main\n",
		"api.spec.ts":             "test('api')\n",
		"auth.spec.ts":            "test('auth')\n",
		"plain.ts":                "console.log('plain')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	goTests, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{
		"auth_controller_test.go",
		"user_controller_test.go",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"*_test.go"}; strings.Join(goTests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized Go test values = %q, want %q", goTests, want)
	}

	specs, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{
		"api.spec.ts",
		"auth.spec.ts",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"*.spec.ts"}; strings.Join(specs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized spec values = %q, want %q", specs, want)
	}
}

func TestNormalizeInteractiveFileSetStageValuesCollapsesCommandPrefixFamily(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"command_render.go":      "package catclip\n",
		"command_spec.go":        "package catclip\n",
		"command_spec_test.go":   "package catclip\n",
		"editor_command.go":      "package catclip\n",
		"editor_command_test.go": "package catclip\n",
	})
	_ = parseInProject(t, project, []string{"."})

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{
		"command_spec_test.go",
		"command_spec.go",
		"command_render.go",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"command*"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestNormalizeInteractiveFileSetStageValuesCollapsesEditorCommandPrefixFamily(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"editor_command.go":            "package catclip\n",
		"editor_command_nonwindows.go": "package catclip\n",
		"editor_command_test.go":       "package catclip\n",
		"editor_command_windows.go":    "package catclip\n",
		"command_spec.go":              "package catclip\n",
	})
	_ = parseInProject(t, project, []string{"."})

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{
		"editor_command_windows.go",
		"editor_command_test.go",
		"editor_command_nonwindows.go",
		"editor_command.go",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"editor*"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestNormalizeInteractiveFileSetStageValuesCollapsesAllTestFilesWithFalsePositive(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main_test.go":                            "package catclip\n",
		"recent_picker_test.go":                   "package catclip\n",
		"lines_picker_test.go":                    "package catclip\n",
		"startup_file_set_normalization_test.go":  "package catclip\n",
		"positional_glob_normalization_test.go":   "package catclip\n",
		"smart_case_test.go":                      "package catclip\n",
		"internal/render/smart_case_test.go":      "package render\n",
		"fileclip/clipboard_linux_test.go":        "package fileclip\n",
		"fileclip/clipboard_unsupported.go":       "package fileclip\n",
		"fileclip/clipboard_unsupported_testdata": "not a go file\n",
		"main.go": "package catclip\n",
	})
	_ = parseInProject(t, project, []string{"."})

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{
		"main_test.go",
		"recent_picker_test.go",
		"lines_picker_test.go",
		"startup_file_set_normalization_test.go",
		"positional_glob_normalization_test.go",
		"smart_case_test.go",
		"internal/render/smart_case_test.go",
		"fileclip/clipboard_linux_test.go",
		"fileclip/clipboard_unsupported.go",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"*_test.go", "fileclip/clipboard_unsupported.go"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestNormalizeInteractiveFileSetStageValuesPrunesPatternCoveredByBroaderInference(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"main_test.go":          "package catclip\n",
		"tree_test.go":          "package catclip\n",
		"recent_picker_test.go": "package catclip\n",
		"lines_picker_test.go":  "package catclip\n",
		"main.go":               "package catclip\n",
	})
	_ = parseInProject(t, project, []string{"."})

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{
		"*_picker_test.go",
		"main_test.go",
		"tree_test.go",
		"recent_picker_test.go",
		"lines_picker_test.go",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"*_test.go"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestInferDynamicFileSetPatternsPrefixCollisionKeepsDelimiterUntilSafe(t *testing.T) {
	selected := []string{
		"deploy_config.test.js",
		"deploy_script.test.js",
	}
	scope := append(append([]string(nil), selected...),
		"deployment.test.js",
		"build_script.test.js",
	)
	inferred, remaining, err := inferDynamicFileSetPatterns(selected, scope, nil)
	if err != nil {
		t.Fatalf("inferDynamicFileSetPatterns returned error: %v", err)
	}
	if want := []string{"deploy_*"}; strings.Join(inferred, "\n") != strings.Join(want, "\n") {
		t.Fatalf("inferred = %q, want %q", inferred, want)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %q, want none", remaining)
	}

	selected = append(append([]string(nil), selected...), "deployment.test.js")
	inferred, remaining, err = inferDynamicFileSetPatterns(selected, scope, nil)
	if err != nil {
		t.Fatalf("inferDynamicFileSetPatterns returned error: %v", err)
	}
	if want := []string{"deploy*"}; strings.Join(inferred, "\n") != strings.Join(want, "\n") {
		t.Fatalf("inferred = %q, want %q", inferred, want)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %q, want none", remaining)
	}
}

func TestInferDynamicFileSetPatternsDoesNotInventMiddleWildcard(t *testing.T) {
	selected := []string{
		"user.service.ts",
		"auth.service.js",
	}
	inferred, remaining, err := inferDynamicFileSetPatterns(selected, selected, nil)
	if err != nil {
		t.Fatalf("inferDynamicFileSetPatterns returned error: %v", err)
	}
	if len(inferred) != 0 {
		t.Fatalf("inferred = %q, want none", inferred)
	}
	if strings.Join(remaining, "\n") != strings.Join(selected, "\n") {
		t.Fatalf("remaining = %q, want %q", remaining, selected)
	}
}

func TestInferDynamicFileSetPatternsGreedyDecomposesOverlappingFamilies(t *testing.T) {
	selected := []string{
		"auth_controller.go",
		"auth_model.go",
		"auth_view.ts",
		"shared_model.go",
		"user_controller.go",
		"user_model.go",
		"user_view.ts",
	}
	inferred, remaining, err := inferDynamicFileSetPatterns(selected, selected, nil)
	if err != nil {
		t.Fatalf("inferDynamicFileSetPatterns returned error: %v", err)
	}
	if want := []string{"*.go", "auth_*", "user_*"}; strings.Join(inferred, "\n") != strings.Join(want, "\n") {
		t.Fatalf("inferred = %q, want %q", inferred, want)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %q, want none", remaining)
	}
}

func TestNormalizeInteractiveFileSetStageValuesKeepsExplicitPatternAndCollapsesRest(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"core_main.go":       "package main\n",
		"server.go":          "package main\n",
		"web_login_test.ts":  "test('web')\n",
		"api_logout_test.ts": "test('api')\n",
		"web_login.ts":       "console.log('web')\n",
	})
	_ = parseInProject(t, project, []string{"."})

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{
		"*.go",
		"core_main.go",
		"web_login_test.ts",
		"api_logout_test.ts",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"*.go", "*_test.ts"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestNormalizeInteractiveFileSetStageValuesDoesNotCollapseOneFile(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"UserController.java": "class UserController {}\n",
		"README.md":           "# readme\n",
	})
	_ = parseInProject(t, project, []string{"."})
	selected := []string{"UserController.java"}

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, selected)
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if strings.Join(got, "\n") != strings.Join(selected, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, selected)
	}
}

func TestNormalizeInteractiveFileSetStageValuesCollapsesCompleteDirectoryWithoutCrossingSiblings(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/UserController.java":    "class UserController {}\n",
		"src/AdminController.java":   "class AdminController {}\n",
		"legacy/AuthController.java": "class AuthController {}\n",
	})
	_ = parseInProject(t, project, []string{"."})
	selected := []string{"src/UserController.java", "src/AdminController.java"}

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, selected)
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"src/*"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestNormalizeInteractiveFileSetStageValuesCollapsesLargeRootSubtreePrecisely(t *testing.T) {
	files := map[string]string{
		"src/main.ts":                    "export const main = true\n",
		"other/node_modules/keep.js":     "module.exports = true\n",
		"node_modules/pkg-a/index.js":    "module.exports = 'a'\n",
		"node_modules/pkg-b/README.md":   "# package b\n",
		"node_modules/pkg-c/styles.css":  "body {}\n",
		"node_modules/pkg-c/schema.json": "{}\n",
	}
	project := setupTestProject(t, files)
	_ = parseInProject(t, project, []string{".", "--no-ignore"})

	selected := []string{
		"node_modules/pkg-a/index.js",
		"node_modules/pkg-b/README.md",
		"node_modules/pkg-c/styles.css",
		"node_modules/pkg-c/schema.json",
	}
	got, err := normalizeInteractiveFileSetStageValues([]string{".", "--no-ignore"}, selected)
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"node_modules/*"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}

	matcher, err := discovery.ClassifyStageValue(got[0])
	if err != nil {
		t.Fatalf("ClassifyStageValue returned error: %v", err)
	}
	if !discovery.MatchesStageValue("node_modules/pkg-c/schema.json", matcher) {
		t.Fatal("compacted selector did not retain a selected descendant")
	}
	if discovery.MatchesStageValue("other/node_modules/keep.js", matcher) {
		t.Fatal("compacted selector widened to a same-named nested directory")
	}
}

func TestNormalizeInteractiveFileSetStageValuesCollapsesAnchoredNestedSubtree(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"src/vendor/pkg-a/index.js":  "module.exports = 'a'\n",
		"src/vendor/pkg-b/README.md": "# package b\n",
		"src/main.ts":                "export const main = true\n",
		"other/vendor/keep.js":       "module.exports = true\n",
	})
	_ = parseInProject(t, project, []string{".", "--no-ignore"})

	got, err := normalizeInteractiveFileSetStageValues([]string{".", "--no-ignore"}, []string{
		"src/vendor/pkg-a/index.js",
		"src/vendor/pkg-b/README.md",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"src/vendor/"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestInferCompleteSelectedSubtreesDoesNotBroadenPartialDirectory(t *testing.T) {
	selected := []string{
		"node_modules/pkg-a/index.js",
		"node_modules/pkg-b/index.js",
	}
	scopeFiles := append(append([]string(nil), selected...), "node_modules/pkg-c/index.js")

	selectors, remaining, err := inferCompleteSelectedSubtrees(selected, scopeFiles)
	if err != nil {
		t.Fatalf("inferCompleteSelectedSubtrees returned error: %v", err)
	}
	if len(selectors) != 0 {
		t.Fatalf("selectors = %q, want none for a partial directory selection", selectors)
	}
	if got, want := strings.Join(remaining, "\n"), strings.Join(selected, "\n"); got != want {
		t.Fatalf("remaining exact paths = %q, want %q", got, want)
	}
}

func TestInferCompleteSelectedSubtreesScalesAcrossLargeNestedTree(t *testing.T) {
	const packageCount = 10000
	scopeFiles := make([]string, 0, packageCount+1)
	selected := make([]string, 0, packageCount)
	for i := 0; i < packageCount; i++ {
		relPath := fmt.Sprintf("node_modules/pkg-%05d/lib/index-%05d.js", i, i)
		scopeFiles = append(scopeFiles, relPath)
		selected = append(selected, relPath)
	}
	scopeFiles = append(scopeFiles, "src/main.ts")

	selectors, remaining, err := inferCompleteSelectedSubtrees(selected, scopeFiles)
	if err != nil {
		t.Fatalf("inferCompleteSelectedSubtrees returned error: %v", err)
	}
	if want := []string{"node_modules/*"}; strings.Join(selectors, "\n") != strings.Join(want, "\n") {
		t.Fatalf("selectors = %q, want %q", selectors, want)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining exact paths = %d, want 0", len(remaining))
	}
}

func TestNormalizeInteractiveFileSetStageValuesLargeTreeUsesOneSubtree(t *testing.T) {
	const packageCount = 10000
	scopeFiles := make([]string, 0, packageCount+1)
	selected := make([]string, 0, packageCount)
	for i := 0; i < packageCount; i++ {
		relPath := fmt.Sprintf("node_modules/pkg-%05d/lib/file-%05d.js", i, i)
		scopeFiles = append(scopeFiles, relPath)
		selected = append(selected, relPath)
	}
	scopeFiles = append(scopeFiles, "src/main.ts")

	got, err := normalizeInteractiveFileSetStageValuesForPaths(scopeFiles, selected)
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValuesForPaths returned error: %v", err)
	}
	if want := []string{"node_modules/*"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestInteractiveExcludeLargeDirectoryReachesOutputPreparationWithCompactStage(t *testing.T) {
	files := map[string]string{
		".gitignore":  "node_modules/\n",
		"src/main.ts": "export const main = true\n",
	}
	for i := 0; i < 512; i++ {
		relPath := fmt.Sprintf("node_modules/pkg-%04d/lib/file-%04d.%s", i, i, []string{"js", "json", "md", "css"}[i%4])
		files[relPath] = "fixture\n"
	}
	project := setupTestProject(t, files)
	_ = parseInProject(t, project, []string{".", "--no-ignore"})
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
	"filter> ")
		printf '%s\n' "$input" | grep -F $'\texclude\t' | head -n 1
		;;
	"exclude> ")
		printf '%s\n' "$input" | grep -F $'\tnode_modules/'
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
	args, _, usedFzf, err := resolveInteractiveStartupArgs(resolver, []string{".", "--no-ignore", "--"})
	if err != nil {
		t.Fatalf("resolveInteractiveStartupArgs returned error: %v", err)
	}
	if !usedFzf {
		t.Fatal("expected interactive filter and exclude pickers")
	}
	if got, want := strings.Join(args, "\n"), ".\n--no-ignore\n--exclude\nnode_modules/*"; got != want {
		t.Fatalf("resolved args = %q, want %q", got, want)
	}

	context, err := buildStartupSinkPickerContext(args)
	if err != nil {
		t.Fatalf("buildStartupSinkPickerContext returned error: %v", err)
	}
	paths := context.Plan.DistinctRelPaths()
	if got, want := strings.Join(paths, "\n"), ".gitignore\nsrc/main.ts"; got != want {
		t.Fatalf("prepared output paths = %q, want %q", got, want)
	}
}

func TestNormalizeInteractiveFileSetStageValuesOrderIsDeterministic(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"UserController.java":  "class UserController {}\n",
		"AdminController.java": "class AdminController {}\n",
		"README.md":            "# readme\n",
	})
	_ = parseInProject(t, project, []string{"."})

	got, err := normalizeInteractiveFileSetStageValues([]string{"."}, []string{
		"*.md",
		"UserController.java",
		"AdminController.java",
	})
	if err != nil {
		t.Fatalf("normalizeInteractiveFileSetStageValues returned error: %v", err)
	}
	if want := []string{"*.md", "*Controller.java"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, want)
	}
}

func TestResolveStartupScopeFileSetArgsExcludeDynamicallyCollapsesExactSelections(t *testing.T) {
	project := setupTestProject(t, map[string]string{
		"UserController.java":  "class UserController {}\n",
		"AdminController.java": "class AdminController {}\n",
		"README.md":            "# readme\n",
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
	printf '%s\n' "$input" | grep -F $'UserController.java\tUserController.java\tUserController.java\tfile\ttext\tfile' | head -n 1
	printf '%s\n' "$input" | grep -F $'AdminController.java\tAdminController.java\tAdminController.java\tfile\ttext\tfile' | head -n 1
	exit 0
fi

echo "unexpected prompt: $prompt" >&2
exit 91
`)

	args, _, err := resolveStartupScopeFileSetArgs([]string{"."}, "--exclude", "exclude> ")
	if err != nil {
		t.Fatalf("resolveStartupScopeFileSetArgs returned error: %v", err)
	}
	if got, want := strings.Join(args, "\n"), ".\n--exclude\n*.java"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}

func backslashPaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = strings.ReplaceAll(p, "/", `\`)
	}
	return out
}

// TestInferDynamicFileSetPatternsSeparatorAgnostic is the automated stand-in for
// the plan's "manual smoke on linux + windows". Inference is computed from
// path.Base(normalizeRelPath(...)), and normalizeRelPath converts "\" to "/"
// (discovery.go), so a Windows-style selection must infer exactly the same
// patterns as the POSIX form. Each case is run in both separator styles.
func TestInferDynamicFileSetPatternsSeparatorAgnostic(t *testing.T) {
	tests := []struct {
		name     string
		selected []string
		scope    []string
		want     []string // expected inferred patterns; remaining must be empty
	}{
		{
			// user_profile.go (a .go file) is unselected, so *.go is invalid;
			// the broadest valid full cover is the prefix auth* (fewest literal,
			// delimiter dropped per the chosen tie rule).
			name:     "prefix family across directories",
			selected: []string{"src/auth_login.go", "src/auth_logout.go"},
			scope:    []string{"src/auth_login.go", "src/auth_logout.go", "src/user_profile.go"},
			want:     []string{"auth*"},
		},
		{
			name:     "extension family",
			selected: []string{"pkg/a.go", "pkg/b.go"},
			scope:    []string{"pkg/a.go", "pkg/b.go", "pkg/notes.md"},
			want:     []string{"*.go"},
		},
		{
			name:     "camel prefix across extensions",
			selected: []string{"web/fooBar.js", "web/fooBaz.ts"},
			scope:    []string{"web/fooBar.js", "web/fooBaz.ts"},
			want:     []string{"foo*"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, sep := range []string{"posix", "windows"} {
				selected, scope := tt.selected, tt.scope
				if sep == "windows" {
					selected, scope = backslashPaths(tt.selected), backslashPaths(tt.scope)
				}
				inferred, remaining, err := inferDynamicFileSetPatterns(selected, scope, nil)
				if err != nil {
					t.Fatalf("[%s] returned error: %v", sep, err)
				}
				if got := strings.Join(inferred, "\n"); got != strings.Join(tt.want, "\n") {
					t.Fatalf("[%s] inferred = %q, want %q", sep, inferred, tt.want)
				}
				if len(remaining) != 0 {
					t.Fatalf("[%s] remaining = %q, want none", sep, remaining)
				}
			}
		})
	}
}

// TestInferDynamicFileSetPatternsBackslashCrossDirectoryStaysExact mirrors the
// POSIX cross-directory guard with Windows separators. Patterns are
// basename-based and cannot distinguish directories, so an unselected sibling
// sharing the basename family (legacy\AuthController.java) makes the only
// 2-match candidate (*Controller.java) over-match and be rejected.
func TestInferDynamicFileSetPatternsBackslashCrossDirectoryStaysExact(t *testing.T) {
	selected := backslashPaths([]string{"src/UserController.java", "src/AdminController.java"})
	scope := backslashPaths([]string{
		"src/UserController.java",
		"src/AdminController.java",
		"legacy/AuthController.java",
	})
	inferred, remaining, err := inferDynamicFileSetPatterns(selected, scope, nil)
	if err != nil {
		t.Fatalf("returned error: %v", err)
	}
	if len(inferred) != 0 {
		t.Fatalf("inferred = %q, want none", inferred)
	}
	if strings.Join(remaining, "\n") != strings.Join(selected, "\n") {
		t.Fatalf("remaining = %q, want %q", remaining, selected)
	}
}

// TestInferDynamicFileSetPatternsNicheBoundaryCases exercises the niche
// boundary/normalization paths: camelCase false positives (goal 3 — minified or
// obfuscated names must not over-collapse), digit boundaries, and "./"/".\"
// prefix + mixed-separator normalization.
func TestInferDynamicFileSetPatternsNicheBoundaryCases(t *testing.T) {
	tests := []struct {
		name          string
		selected      []string
		scope         []string
		wantInferred  []string
		wantRemaining []string
	}{
		{
			name:          "camel false positive stays exact",
			selected:      []string{"fooBar.js", "fooBaz.js"},
			scope:         []string{"fooBar.js", "fooBaz.js", "fooQux.js"},
			wantInferred:  nil,
			wantRemaining: []string{"fooBar.js", "fooBaz.js"},
		},
		{
			name:          "digit boundary prefix isolates numbered family",
			selected:      []string{"img1.png", "img2.png"},
			scope:         []string{"img1.png", "img2.png", "icon.png"},
			wantInferred:  []string{"img*"},
			wantRemaining: nil,
		},
		{
			name:          "dot-slash and mixed separators normalize",
			selected:      []string{"./a.go", `.\b.go`},
			scope:         []string{"./a.go", `.\b.go`, "c.txt"},
			wantInferred:  []string{"*.go"},
			wantRemaining: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inferred, remaining, err := inferDynamicFileSetPatterns(tt.selected, tt.scope, nil)
			if err != nil {
				t.Fatalf("returned error: %v", err)
			}
			if strings.Join(inferred, "\n") != strings.Join(tt.wantInferred, "\n") {
				t.Fatalf("inferred = %q, want %q", inferred, tt.wantInferred)
			}
			if strings.Join(remaining, "\n") != strings.Join(tt.wantRemaining, "\n") {
				t.Fatalf("remaining = %q, want %q", remaining, tt.wantRemaining)
			}
		})
	}
}

// TestInferDynamicFileSetPatternsLargeSelectionStillCollapses locks in the fix
// to the old candidate-count cap: a large selection must still collapse into a
// glob. Previously, once candidate enumeration exceeded the 512 cap, inference
// bailed to the exact file list (so selecting 200 .go files across a big repo
// emitted 200 paths instead of *.go). Now there is no cap, and 200 .go files in
// a .go-only scope collapse to the broadest clean glob, *.go.
func TestInferDynamicFileSetPatternsLargeSelectionStillCollapses(t *testing.T) {
	const n = 200
	selected := make([]string, 0, n)
	for i := range n {
		selected = append(selected, fmt.Sprintf("pkg%03d/file%03d_mod.go", i, i))
	}
	inferred, remaining, err := inferDynamicFileSetPatterns(selected, selected, nil)
	if err != nil {
		t.Fatalf("returned error: %v", err)
	}
	if want := []string{"*.go"}; strings.Join(inferred, "\n") != strings.Join(want, "\n") {
		t.Fatalf("inferred = %q, want %q (large selection must collapse, not bail to exact)", inferred, want)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %q, want none", remaining)
	}
}

// benchInferenceScope builds a synthetic repo file set: directory-nested paths
// across a spread of extensions. No filesystem involved — inference is pure over
// these strings, so a 50k+ "repo" is just a slice.
func benchInferenceScope(n int) []string {
	exts := [...]string{"go", "ts", "js", "md", "txt", "json"}
	scope := make([]string, n)
	for i := range n {
		scope[i] = fmt.Sprintf("pkg%03d/module%05d.%s", i%256, i, exts[i%len(exts)])
	}
	return scope
}

// benchInferSink prevents the compiler from eliminating the benchmarked call.
var benchInferSink int

// BenchmarkInferDynamicFileSetPatterns is the automated stand-in for the plan's
// ">50k file" performance check (goal 1) — no real repo needed. After the cap
// removal + fast prefix/suffix matching, cost is O(scope × basename length),
// independent of candidate count. These confirm that even large/diverse
// selections at 50k scope collapse into globs in the millisecond range (the old
// regex path took ~10 s on the diverse case).
func BenchmarkInferDynamicFileSetPatterns(b *testing.B) {
	// Family selection: few candidates, scope grows 1k → 50k.
	familySelected := []string{
		"feature/auth_login.go",
		"feature/auth_logout.go",
		"feature/auth_refresh.go",
		"feature/auth_session.go",
	}
	for _, n := range []int{1000, 10000, 50000} {
		scope := append(benchInferenceScope(n), familySelected...)
		b.Run(fmt.Sprintf("family_select/scope=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				inferred, _, _ := inferDynamicFileSetPatterns(familySelected, scope, nil)
				benchInferSink += len(inferred)
			}
		})
	}

	// Large selection: 5k files selected in a 55k scope. Used to exceed the old
	// candidate cap and bail to exact; now it collapses to a glob and must stay
	// fast despite the huge candidate set.
	bigScope := benchInferenceScope(50000)
	bigSelected := make([]string, 0, 5000)
	for i := range 5000 {
		bigSelected = append(bigSelected, fmt.Sprintf("diverse/item%05d_part.go", i))
	}
	bigScope = append(bigScope, bigSelected...)
	b.Run("large_select/select=5000/scope=50000", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			inferred, _, _ := inferDynamicFileSetPatterns(bigSelected, bigScope, nil)
			benchInferSink += len(inferred)
		}
	})

	// Diverse selection: ~90 files with distinct multi-boundary basenames (~450
	// candidates) at 50k scope. This was the old worst case (~10 s under the
	// regex path, just under the cap so it did not bail); it must now be fast.
	diverseSelected := make([]string, 0, 90)
	for i := range 90 {
		diverseSelected = append(diverseSelected, fmt.Sprintf("near/worst%02d_mod.go", i))
	}
	diverseScope := append(benchInferenceScope(50000), diverseSelected...)
	b.Run("diverse_select/select=90/scope=50000", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			inferred, _, _ := inferDynamicFileSetPatterns(diverseSelected, diverseScope, nil)
			benchInferSink += len(inferred)
		}
	})
}
