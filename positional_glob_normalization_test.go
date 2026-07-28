package catclip

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizePositionalGlobArgsPassesEveryGlobThroughUnchanged(t *testing.T) {
	for _, args := range [][]string{
		{"*file1*"},
		{"*layout/Footer*"},
		{"*/utils/*"},
		{"util*"},
		{"*util"},
		{"*.go"},
		{"**auth**"},
		{"*file1*", "--quiet"},
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

func TestNormalizePositionalGlobArgsGlobPatternPassesThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*.js"})
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"*.js"}) {
		t.Fatalf("expected glob pattern to pass through, got %#v", result.Args)
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

func TestNormalizePositionalGlobArgsCrossScopeGlobPassesThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*.js", "--then", "*.ts"})
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"*.js", "--then", "*.ts"}) {
		t.Fatalf("expected cross-scope globs to pass through, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsWrapperStarAndPatternPassesThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*Button*", "*.js"})
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"*Button*", "*.js"}) {
		t.Fatalf("expected mixed wrapper-star/pattern to pass through, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsDegenerateStarPassesThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"*"})
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"*"}) {
		t.Fatalf("expected degenerate star to pass through, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsInterleavedGlobsPassThrough(t *testing.T) {
	result, err := normalizePositionalGlobArgs([]string{"src", "*.js", "components", "*.tsx"})
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, []string{"src", "*.js", "components", "*.tsx"}) {
		t.Fatalf("expected interleaved globs to pass through, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsLeavesModifierValuesUntouched(t *testing.T) {
	args := []string{"src", "--only", "*.js"}
	result, err := normalizePositionalGlobArgs(args)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, args) {
		t.Fatalf("expected modifier values untouched, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsLeavesDotfileTargetsUntouched(t *testing.T) {
	args := []string{".gitignore"}
	result, err := normalizePositionalGlobArgs(args)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, args) {
		t.Fatalf("expected dotfile target untouched, got %#v", result.Args)
	}
}

func TestNormalizePositionalGlobArgsSkipsConsumedThenLikeModifierValue(t *testing.T) {
	args := []string{"src", "--contains", "--then"}
	result, err := normalizePositionalGlobArgs(args)
	if err != nil {
		t.Fatalf("normalizePositionalGlobArgs returned error: %v", err)
	}
	if !reflect.DeepEqual(result.Args, args) {
		t.Fatalf("expected consumed modifier value untouched, got %#v", result.Args)
	}
}
