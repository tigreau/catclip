package catclip

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizePositionalGlobArgsPassesValidArgsThroughUnchanged(t *testing.T) {
	for _, args := range [][]string{
		{"*file1*"},
		{"*layout/Footer*"},
		{"*/utils/*"},
		{"util*"},
		{"*util"},
		{"*.go"},
		{"**auth**"},
		{"*file1*", "--quiet"},
		{"*.js"},
		{"*.js", "--then", "*.ts"},
		{"*Button*", "*.js"},
		{"*"},
		{"src", "*.js", "components", "*.tsx"},
		{"src", "--only", "*.js"},
		{".gitignore"},
		{"src", "--contains", "--then"},
	} {
		result, err := normalizePositionalGlobArgs(args)
		if err != nil {
			t.Fatalf("normalizePositionalGlobArgs(%q) returned error: %v", args, err)
		}
		if !reflect.DeepEqual(result.Args, args) {
			t.Fatalf("normalizePositionalGlobArgs(%q) = %#v, want byte-for-byte passthrough", args, result.Args)
		}
	}
}

func TestNormalizePositionalGlobArgsBareExtensionFixIt(t *testing.T) {
	_, err := normalizePositionalGlobArgs([]string{"src", ".tsx", "--changed"})
	if err == nil {
		t.Fatal("expected bare-extension fix-it error")
	}
	if !strings.Contains(err.Error(), "Error: '.tsx' is a bare extension, not a target.") {
		t.Fatalf("expected bare-extension fix-it, got:\n%s", err)
	}
	onlySuggestion := `catclip src --only "*.tsx" --changed`
	targetSuggestion := `catclip src "*.tsx" --changed`
	if runtime.GOOS == "windows" {
		onlySuggestion = `catclip src --only '*.tsx' --changed`
		targetSuggestion = `catclip src '*.tsx' --changed`
	}
	if !strings.Contains(err.Error(), onlySuggestion) {
		t.Fatalf("expected --only filter suggestion, got:\n%s", err)
	}
	if !strings.Contains(err.Error(), targetSuggestion) {
		t.Fatalf("expected glob-as-target suggestion, got:\n%s", err)
	}
}
