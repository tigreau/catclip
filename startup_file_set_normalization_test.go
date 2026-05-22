package catclip

import (
	"strings"
	"testing"
)

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
	if want := []string{"editor_command*"}; strings.Join(inferred, "\n") != strings.Join(want, "\n") {
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
	if want := []string{"*Controller.java"}; strings.Join(inferred, "\n") != strings.Join(want, "\n") {
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
	if want := []string{"*Controller.java"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
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
	if want := []string{"*_controller_test.go"}; strings.Join(goTests, "\n") != strings.Join(want, "\n") {
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
	if want := []string{"command_*"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
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
	if want := []string{"editor_command*"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
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
		"internal/tree/smart_case_test.go":        "package tree\n",
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
		"internal/tree/smart_case_test.go",
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

func TestNormalizeInteractiveFileSetStageValuesDoesNotCollapseAcrossUnselectedDirectories(t *testing.T) {
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
	if strings.Join(got, "\n") != strings.Join(selected, "\n") {
		t.Fatalf("normalized values = %q, want %q", got, selected)
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
	if got, want := strings.Join(args, "\n"), ".\n--exclude\n*Controller.java"; got != want {
		t.Fatalf("expected resolved args %q, got %q", want, got)
	}
}
